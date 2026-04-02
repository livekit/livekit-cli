// Copyright 2025 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

var (
	simulateProjectConfig *config.ProjectConfig
)

var simulateCommand = &cli.Command{
	Name:  "simulate",
	Usage: "Run agent simulations against LiveKit Cloud",
	Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
		pc, err := loadProjectDetails(cmd)
		if err != nil {
			return nil, err
		}
		simulateProjectConfig = pc
		return nil, nil
	},
	Action: runSimulate,
	Flags: []cli.Flag{
		&cli.IntFlag{
			Name:    "num-simulations",
			Aliases: []string{"n"},
			Usage:   "Number of scenarios to generate",
			Value:   5,
		},
		&cli.StringFlag{
			Name:  "description",
			Usage: "Agent description for scenario generation",
		},
		&cli.StringFlag{
			Name:  "scenario-group-id",
			Usage: "Use a pre-configured scenario group",
		},
		&cli.StringFlag{
			Name:  "config",
			Usage: "Path to simulation config `FILE`",
		},
		&cli.StringFlag{
			Name:  "entrypoint",
			Usage: "Agent entrypoint `FILE` (default: agent.py)",
		},
	},
}

// simulationConfig represents the simulation.json config file.
type simulationConfig struct {
	AgentDescription string           `json:"agent_description"`
	Scenarios        []scenarioConfig `json:"scenarios"`
}

type scenarioConfig struct {
	Label             string            `json:"label"`
	Instructions      string            `json:"instructions"`
	AgentExpectations string            `json:"agent_expectations"`
	Metadata          map[string]string `json:"metadata"`
}

func loadSimulationConfig(path string) (*simulationConfig, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}
	var cfg simulationConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	return &cfg, nil
}

func generateAgentName() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return "simulation-" + string(b)
}

// simulateMode represents how scenarios are sourced.
type simulateMode int

const (
	modeInlineScenarios simulateMode = iota
	modeScenarioGroup
	modeGenerateFromDescription
	modeGenerateFromSource
)

func runSimulate(ctx context.Context, cmd *cli.Command) error {
	pc := simulateProjectConfig

	configPath := cmd.String("config")
	cfg, err := loadSimulationConfig(configPath)
	if err != nil {
		return err
	}

	description := cmd.String("description")
	if description == "" && cfg != nil {
		description = cfg.AgentDescription
	}

	numSimulations := int32(cmd.Int("num-simulations"))
	scenarioGroupID := cmd.String("scenario-group-id")
	agentName := generateAgentName()

	// Mode detection (checked in priority order)
	var mode simulateMode
	switch {
	case cfg != nil && len(cfg.Scenarios) > 0:
		mode = modeInlineScenarios
	case scenarioGroupID != "":
		mode = modeScenarioGroup
	case description != "":
		mode = modeGenerateFromDescription
	default:
		mode = modeGenerateFromSource
	}

	// Detect project type, walking up parent directories if needed
	projectDir, projectType, err := agentfs.DetectProjectRoot(".")
	if err != nil {
		return err
	}
	if !projectType.IsPython() {
		return fmt.Errorf("simulate currently only supports Python agents (detected: %s)", projectType)
	}

	// Resolve entrypoint
	entrypoint, err := findEntrypoint(projectDir, cmd.String("entrypoint"), projectType)
	if err != nil {
		return err
	}

	simClient := lksdk.NewAgentSimulationClient(serverURL, pc.APIKey, pc.APISecret)

	m := newSimulateModel(&simulateConfig{
		ctx:            ctx,
		client:         simClient,
		pc:             pc,
		numSimulations: numSimulations,
		mode:           mode,
		description:    description,
		agentName:      agentName,
		projectDir:     projectDir,
		projectType:    projectType,
		entrypoint:     entrypoint,
		cfg:            cfg,
		scenarioGroupID: scenarioGroupID,
	})

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	if m.agent != nil {
		m.agent.Kill()
		if m.agent.LogPath != "" {
			fmt.Fprintf(os.Stderr, "Agent logs: %s\n", m.agent.LogPath)
		}
	}

	if url := m.getDashboardURL(); url != "" {
		fmt.Fprintf(os.Stderr, "Dashboard:  %s\n", url)
	}

	// Cancel the run — server will no-op if already terminal
	if m.runID != "" && !m.runFinished {
		cancelCtx, cancelFn := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelFn()
		if _, err := simClient.CancelSimulationRun(cancelCtx, &livekit.SimulationRun_Cancel_Request{
			SimulationRunId: m.runID,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to cancel run: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Run cancelled\n")
		}
	}

	if m.err != nil && m.err != context.Canceled {
		return m.err
	}
	return nil
}


