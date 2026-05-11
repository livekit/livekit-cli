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
	"io"
	"math/rand"
	"os"
	"time"

	"github.com/mattn/go-isatty"
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

const (
	agentRegisterTimeout   = 20 * time.Second
	simulationPollInterval = 1 * time.Second
	simulationAPITimeout   = 10 * time.Second
)

var simulateCommand = &cli.Command{
	Name:      "simulate",
	Usage:     "Run agent simulations against LiveKit Cloud",
	ArgsUsage: "[entrypoint]",
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

// simulateConfig holds all parameters needed to run a simulation in either TUI or CI mode.
type simulateConfig struct {
	ctx             context.Context
	client          *lksdk.AgentSimulationClient
	pc              *config.ProjectConfig
	numSimulations  int32
	mode            simulateMode
	description     string
	agentName       string
	projectDir      string
	projectType     agentfs.ProjectType
	entrypoint      string
	cfg             *simulationConfig
	scenarioGroupID string
}

// simulateMode represents how scenarios are sourced.
type simulateMode int

const (
	modeInlineScenarios simulateMode = iota
	modeScenarioGroup
	modeGenerateFromDescription
	modeGenerateFromSource
)

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

	projectDir, projectType, err := agentfs.DetectProjectRoot(".")
	if err != nil {
		return err
	}
	if !projectType.IsPython() {
		return fmt.Errorf("simulate currently only supports Python agents (detected: %s)", projectType)
	}

	entrypoint, err := findEntrypoint(projectDir, cmd.Args().First(), projectType)
	if err != nil {
		return err
	}

	simClient := lksdk.NewAgentSimulationClient(serverURL, pc.APIKey, pc.APISecret)

	simCfg := &simulateConfig{
		ctx:             ctx,
		client:          simClient,
		pc:              pc,
		numSimulations:  numSimulations,
		mode:            mode,
		description:     description,
		agentName:       agentName,
		projectDir:      projectDir,
		projectType:     projectType,
		entrypoint:      entrypoint,
		cfg:             cfg,
		scenarioGroupID: scenarioGroupID,
	}

	if !isInteractive() {
		return runSimulateCI(ctx, simCfg)
	}
	return runSimulateTUI(simCfg)
}

func isInteractive() bool {
	if os.Getenv("CI") != "" {
		return false
	}
	return isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
}

// --- Shared lifecycle functions used by both TUI and CI modes ---

func startSimulationAgent(c *simulateConfig, forwardOutput io.Writer) (*AgentProcess, error) {
	return startAgent(AgentStartConfig{
		Dir:         c.projectDir,
		Entrypoint:  c.entrypoint,
		ProjectType: c.projectType,
		CLIArgs: []string{
			"start",
			"--url", c.pc.URL,
			"--api-key", c.pc.APIKey,
			"--api-secret", c.pc.APISecret,
			"--log-level", "DEBUG",
			"--log-format", "colored",
		},
		Env: []string{
			"LIVEKIT_AGENT_NAME=" + c.agentName,
			"LIVEKIT_URL=" + c.pc.URL,
			"LIVEKIT_API_KEY=" + c.pc.APIKey,
			"LIVEKIT_API_SECRET=" + c.pc.APISecret,
		},
		ReadySignal:   "registered worker",
		ForwardOutput: forwardOutput,
	})
}

func createSimulationRun(ctx context.Context, c *simulateConfig) (string, *livekit.PresignedPostRequest, error) {
	req := &livekit.SimulationRun_Create_Request{
		AgentName:        c.agentName,
		AgentDescription: c.description,
		NumSimulations:   c.numSimulations,
	}
	switch c.mode {
	case modeInlineScenarios:
		scenarios := make([]*livekit.SimulationRun_Create_Scenario, 0, len(c.cfg.Scenarios))
		for _, sc := range c.cfg.Scenarios {
			scenarios = append(scenarios, &livekit.SimulationRun_Create_Scenario{
				Label:             sc.Label,
				Instructions:      sc.Instructions,
				AgentExpectations: sc.AgentExpectations,
				Metadata:          sc.Metadata,
			})
		}
		req.Source = &livekit.SimulationRun_Create_Request_Scenarios{
			Scenarios: &livekit.SimulationRun_Create_Scenarios{
				Scenarios: scenarios,
			},
		}
	case modeScenarioGroup:
		req.Source = &livekit.SimulationRun_Create_Request_GroupId{
			GroupId: c.scenarioGroupID,
		}
	}

	resp, err := c.client.CreateSimulationRun(ctx, req)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create simulation: %w", err)
	}
	return resp.SimulationRunId, resp.PresignedPostRequest, nil
}

func uploadSource(ctx context.Context, client *lksdk.AgentSimulationClient, runID string, presigned *livekit.PresignedPostRequest, projectDir, entrypoint string) error {
	if presigned == nil {
		return fmt.Errorf("server did not return upload URL")
	}
	var buf bytes.Buffer
	if err := cloudagents.CreateSourceTarball(os.DirFS(projectDir), nil, &buf); err != nil {
		return fmt.Errorf("failed to create source archive: %w", err)
	}
	if err := cloudagents.MultipartUpload(presigned.Url, presigned.Values, &buf); err != nil {
		return fmt.Errorf("failed to upload source: %w", err)
	}
	if _, err := client.ConfirmSimulationSourceUpload(ctx, &livekit.SimulationRun_ConfirmSourceUpload_Request{
		SimulationRunId:  runID,
		CodeEntrypoint: entrypoint,
	}); err != nil {
		return fmt.Errorf("failed to confirm upload: %w", err)
	}
	return nil
}

func getSimulationRun(ctx context.Context, client *lksdk.AgentSimulationClient, runID string) (*livekit.SimulationRun, error) {
	resp, err := client.GetSimulationRun(ctx, &livekit.SimulationRun_Get_Request{
		SimulationRunId: runID,
	})
	if err != nil {
		return nil, err
	}
	return resp.Run, nil
}

func isTerminalRunStatus(status livekit.SimulationRun_Status) bool {
	return status == livekit.SimulationRun_STATUS_COMPLETED ||
		status == livekit.SimulationRun_STATUS_FAILED ||
		status == livekit.SimulationRun_STATUS_CANCELLED
}

func simulationDashboardURL(projectID, runID string) string {
	if projectID == "" || runID == "" {
		return ""
	}
	return fmt.Sprintf("%s/projects/%s/agents/simulations/%s", dashboardURL, projectID, runID)
}

func cancelSimulationRun(client *lksdk.AgentSimulationClient, runID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.CancelSimulationRun(ctx, &livekit.SimulationRun_Cancel_Request{
		SimulationRunId: runID,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to cancel run: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "Run cancelled\n")
	}
}

func simulationJobCounts(run *livekit.SimulationRun) (total, done, passed, failed int) {
	if run == nil {
		return
	}
	total = len(run.Jobs)
	for _, j := range run.Jobs {
		switch j.Status {
		case livekit.SimulationRun_Job_STATUS_COMPLETED:
			done++
			passed++
		case livekit.SimulationRun_Job_STATUS_FAILED:
			done++
			failed++
		}
	}
	return
}
