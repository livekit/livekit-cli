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
	"bytes"
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
	"github.com/livekit/server-sdk-go/v2/pkg/cloudagents"
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
		fmt.Printf("Mode: running %d inline scenarios from %s\n", len(cfg.Scenarios), configPath)
	case scenarioGroupID != "":
		mode = modeScenarioGroup
		fmt.Printf("Mode: running scenario group %s\n", scenarioGroupID)
	case description != "":
		mode = modeGenerateFromDescription
		fmt.Printf("Mode: generating %d scenarios from description\n", numSimulations)
	default:
		mode = modeGenerateFromSource
		fmt.Printf("Mode: generating %d scenarios from agent source code (no description provided)\n", numSimulations)
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

	// Launch agent subprocess
	agent, err := startAgent(AgentStartConfig{
		Dir:         projectDir,
		Entrypoint:  entrypoint,
		ProjectType: projectType,
		CLIArgs: []string{
			"start",
			"--url", pc.URL,
			"--api-key", pc.APIKey,
			"--api-secret", pc.APISecret,
		},
		Env: []string{
			"LIVEKIT_AGENT_NAME=" + agentName,
		},
		ReadySignal: "registered worker",
	})
	if err != nil {
		return err
	}
	defer agent.Kill()

	// Create API client
	simClient := lksdk.NewAgentSimulationClient(serverURL, pc.APIKey, pc.APISecret)

	// Build the create request
	req := &livekit.CreateSimulationRunRequest{
		AgentName:        agentName,
		AgentDescription: description,
		NumSimulations:   numSimulations,
	}
	switch mode {
	case modeInlineScenarios:
		scenarios := make([]*livekit.CreateSimulationRunRequest_Scenario, 0, len(cfg.Scenarios))
		for _, sc := range cfg.Scenarios {
			scenarios = append(scenarios, &livekit.CreateSimulationRunRequest_Scenario{
				Instructions:      sc.Instructions,
				AgentExpectations: sc.AgentExpectations,
				Metadata:          sc.Metadata,
			})
		}
		req.Source = &livekit.CreateSimulationRunRequest_Scenarios_{
			Scenarios: &livekit.CreateSimulationRunRequest_Scenarios{
				Scenarios: scenarios,
			},
		}
	case modeScenarioGroup:
		req.Source = &livekit.CreateSimulationRunRequest_GroupId{
			GroupId: scenarioGroupID,
		}
	}

	// Wait for worker registration or subprocess exit
	fmt.Println("Starting agent...")
	select {
	case <-agent.Ready():
		// Worker registered
	case err := <-agent.Done():
		logs := agent.RecentLogs(20)
		for _, l := range logs {
			fmt.Fprintln(os.Stderr, l)
		}
		if err != nil {
			return fmt.Errorf("agent exited before registering: %w", err)
		}
		return fmt.Errorf("agent exited before registering")
	case <-time.After(60 * time.Second):
		return fmt.Errorf("timed out waiting for agent to register")
	case <-ctx.Done():
		return ctx.Err()
	}

	// Create the simulation run
	fmt.Println("Creating simulation run...")
	resp, err := simClient.CreateSimulationRun(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to create simulation run: %w", err)
	}
	runID := resp.SimulationRunId

	// Source upload flow: zip project, upload, confirm
	if mode == modeGenerateFromSource {
		presigned := resp.PresignedPostRequest
		if presigned == nil {
			return fmt.Errorf("server did not return a presigned upload URL for source upload mode")
		}

		fmt.Println("Uploading agent source code...")
		sourceDir, _ := os.Getwd()
		var buf bytes.Buffer
		if err := cloudagents.CreateSourceZip(os.DirFS(sourceDir), nil, &buf); err != nil {
			return fmt.Errorf("failed to create source zip: %w", err)
		}
		if err := cloudagents.MultipartUpload(presigned.Url, presigned.Values, &buf); err != nil {
			return fmt.Errorf("failed to upload source: %w", err)
		}

		fmt.Println("Analyzing source code and generating scenarios...")
		if _, err := simClient.ConfirmSimulationSourceUpload(ctx, &livekit.ConfirmSimulationSourceUploadRequest{
			SimulationRunId: runID,
		}); err != nil {
			return fmt.Errorf("failed to confirm source upload: %w", err)
		}
	}

	// Run the TUI
	model := newSimulateModel(simClient, runID, numSimulations, agent)
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}


