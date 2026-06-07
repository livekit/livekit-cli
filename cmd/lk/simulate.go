//go:build console

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
	"path/filepath"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/livekit/server-sdk-go/v2/pkg/cloudagents"
)

func init() {
	AgentCommands[0].Commands = append(AgentCommands[0].Commands, simulateCommand)
}

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
			Name:  "scenarios",
			Usage: "Path to a scenarios `FILE` (yaml). Defaults to scenarios.yaml next to the entrypoint",
		},
	},
}

// scenariosFile mirrors a scenarios.yaml (the source of truth for scenarios).
// It maps field-for-field onto livekit.ScenarioGroup; `userdata` is written as a
// nested mapping here and JSON-encoded into the proto's string field.
type scenariosFile struct {
	Name      string         `yaml:"name"`
	Scenarios []yamlScenario `yaml:"scenarios"`
}

type yamlScenario struct {
	Label             string            `yaml:"label"`
	Instructions      string            `yaml:"instructions"`
	AgentExpectations string            `yaml:"agent_expectations"`
	Tags              map[string]string `yaml:"tags"`
	Userdata          map[string]any    `yaml:"userdata"`
}

// simulateConfig holds all parameters needed to run a simulation in either TUI or CI mode.
type simulateConfig struct {
	ctx            context.Context
	client         *lksdk.AgentSimulationClient
	pc             *config.ProjectConfig
	numSimulations int32
	mode           simulateMode
	description    string
	agentName      string
	projectDir     string
	projectType    agentfs.ProjectType
	entrypoint     string
	scenarioGroup  *livekit.ScenarioGroup
}

// simulateMode represents how scenarios are sourced.
type simulateMode int

const (
	modeScenarios simulateMode = iota
	modeGenerateFromDescription
	modeGenerateFromSource
)

// loadScenarioGroup reads a scenarios.yaml into a livekit.ScenarioGroup, JSON-encoding
// each scenario's nested `userdata` mapping into the proto's string field.
func loadScenarioGroup(path string) (*livekit.ScenarioGroup, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read scenarios file: %w", err)
	}
	var f scenariosFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("failed to parse scenarios file: %w", err)
	}

	group := &livekit.ScenarioGroup{Name: f.Name}
	for _, s := range f.Scenarios {
		var userdata string
		if len(s.Userdata) > 0 {
			b, err := json.Marshal(s.Userdata)
			if err != nil {
				return nil, fmt.Errorf("failed to encode userdata for scenario %q: %w", s.Label, err)
			}
			userdata = string(b)
		}
		group.Scenarios = append(group.Scenarios, &livekit.Scenario{
			Label:             s.Label,
			Instructions:      s.Instructions,
			AgentExpectations: s.AgentExpectations,
			Tags:              s.Tags,
			Userdata:          userdata,
		})
	}
	return group, nil
}

// defaultScenariosPath returns the path to a scenarios.yaml sitting next to the
// entrypoint (or in the project root), or "" if none exists.
func defaultScenariosPath(projectDir, entrypoint string) string {
	candidates := []string{
		filepath.Join(filepath.Dir(entrypoint), "scenarios.yaml"),
		filepath.Join(projectDir, "scenarios.yaml"),
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
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

	description := cmd.String("description")
	numSimulations := int32(cmd.Int("num-simulations"))
	agentName := generateAgentName()

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

	// Resolve the scenarios file: explicit --scenarios, else scenarios.yaml next to
	// the entrypoint. When present, those scenarios are the source of truth.
	scenariosPath := cmd.String("scenarios")
	if scenariosPath == "" {
		if def := defaultScenariosPath(projectDir, entrypoint); def != "" {
			scenariosPath = def
		}
	}

	var scenarioGroup *livekit.ScenarioGroup
	if scenariosPath != "" {
		scenarioGroup, err = loadScenarioGroup(scenariosPath)
		if err != nil {
			return err
		}
	}

	var mode simulateMode
	switch {
	case scenarioGroup != nil && len(scenarioGroup.Scenarios) > 0:
		mode = modeScenarios
	case description != "":
		mode = modeGenerateFromDescription
	default:
		mode = modeGenerateFromSource
	}

	simClient := lksdk.NewAgentSimulationClient(serverURL, pc.APIKey, pc.APISecret)

	simCfg := &simulateConfig{
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
		scenarioGroup:  scenarioGroup,
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
	if c.mode == modeScenarios {
		// Run the scenarios from the yaml. When unset, the server generates
		// num_simulations scenarios from the uploaded source.
		req.ScenarioGroup = c.scenarioGroup
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

// dashboardBaseURL returns the cloud dashboard URL, derived from the API
// server URL so that --server-url (e.g. staging) is respected without a
// separate flag. The cloud API and dashboard hosts differ only by "-api":
//
//	https://cloud-api.livekit.io          -> https://cloud.livekit.io
//	https://cloud-api.staging.livekit.io  -> https://cloud.staging.livekit.io
func dashboardBaseURL() string {
	if base := strings.Replace(serverURL, "cloud-api", "cloud", 1); base != serverURL {
		return base
	}
	return dashboardURL
}

func simulationDashboardURL(projectID, runID string) string {
	if projectID == "" || runID == "" {
		return ""
	}
	return fmt.Sprintf("%s/projects/%s/simulations/runs/%s", dashboardBaseURL(), projectID, runID)
}

func simulationJobDashboardURL(projectID, runID, jobID string) string {
	base := simulationDashboardURL(projectID, runID)
	if base == "" || jobID == "" {
		return ""
	}
	return fmt.Sprintf("%s?job=%s", base, jobID)
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
