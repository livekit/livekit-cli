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
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/util"
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
		&cli.IntFlag{
			Name:  "concurrency",
			Usage: "Max simulations running in parallel (default: server-side limit)",
		},
		&cli.StringFlag{
			Name:  "scenarios",
			Usage: "Path to a scenarios `FILE` (yaml). If omitted, scenarios are generated from the agent's source",
		},
		&cli.BoolFlag{
			Name:    "yes",
			Aliases: []string{"y"},
			Usage:   "Skip the source-upload confirmation prompt (required for non-interactive runs that generate from source)",
		},
	},
}

// writeGeneratedScenariosTemp writes a generated run's scenarios to a temp
// scenarios.yaml and returns its path, so the simulate command can print where
// they landed (mirroring how it prints the agent log path). Returns "" when the
// run carries no generated scenarios.
func writeGeneratedScenariosTemp(run *livekit.SimulationRun) (string, error) {
	group := run.GetScenarioGroup()
	if group == nil || len(group.GetScenarios()) == 0 {
		return "", nil
	}
	out, err := scenarioGroupToYAML(group)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "scenarios-*.yaml")
	if err != nil {
		return "", err
	}
	_, werr := f.Write(out)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return "", werr
	}
	return f.Name(), nil
}

// scenarioGroupToYAML renders a ScenarioGroup as a scenarios.yaml document — the
// inverse of loadScenarioGroup, decoding each scenario's JSON userdata string
// back into a nested mapping.
func scenarioGroupToYAML(group *livekit.ScenarioGroup) ([]byte, error) {
	f := scenariosFile{Name: group.GetName()}
	for _, s := range group.GetScenarios() {
		ys := yamlScenario{
			Label:             s.GetLabel(),
			Instructions:      s.GetInstructions(),
			AgentExpectations: s.GetAgentExpectations(),
			Tags:              s.GetTags(),
		}
		if s.GetUserdata() != "" {
			var ud map[string]any
			if err := json.Unmarshal([]byte(s.GetUserdata()), &ud); err != nil {
				return nil, fmt.Errorf("failed to decode userdata for scenario %q: %w", s.GetLabel(), err)
			}
			ys.Userdata = ud
		}
		f.Scenarios = append(f.Scenarios, ys)
	}
	return yaml.Marshal(f)
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
	concurrency    int32
	mode           simulateMode
	agentName      string
	projectDir     string
	projectType    agentfs.ProjectType
	entrypoint     string
	scenarioGroup  *livekit.ScenarioGroup
	scenariosPath  string // path to the --scenarios file (empty when generating from source)
}

// simulateMode represents how scenarios are sourced.
type simulateMode int

const (
	modeScenarios simulateMode = iota
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

	numSimulations := int32(cmd.Int("num-simulations"))
	concurrency := int32(cmd.Int("concurrency"))
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

	// The scenarios file must be specified explicitly via --scenarios; we never
	// auto-discover one. When provided, those scenarios are the source of truth;
	// otherwise scenarios are generated from the agent's source.
	scenariosPath := cmd.String("scenarios")

	var scenarioGroup *livekit.ScenarioGroup
	if scenariosPath != "" {
		scenarioGroup, err = loadScenarioGroup(scenariosPath)
		if err != nil {
			return err
		}
	}

	var mode simulateMode
	if scenarioGroup != nil && len(scenarioGroup.Scenarios) > 0 {
		mode = modeScenarios
	} else {
		mode = modeGenerateFromSource
	}

	// Generating from source uploads the agent's code to LiveKit Cloud, so make
	// the user agree to it explicitly before anything is sent.
	if mode == modeGenerateFromSource {
		if err := confirmSourceUpload(cmd, projectDir); err != nil {
			return err
		}
	}

	simClient := lksdk.NewAgentSimulationClient(serverURL, pc.APIKey, pc.APISecret)

	simCfg := &simulateConfig{
		ctx:            ctx,
		client:         simClient,
		pc:             pc,
		numSimulations: numSimulations,
		concurrency:    concurrency,
		mode:           mode,
		agentName:      agentName,
		projectDir:     projectDir,
		projectType:    projectType,
		entrypoint:     entrypoint,
		scenarioGroup:  scenarioGroup,
		scenariosPath:  scenariosPath,
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

// confirmSourceUpload makes the user explicitly agree that their agent's source
// code will be uploaded to LiveKit Cloud before generating scenarios from it.
func confirmSourceUpload(cmd *cli.Command, projectDir string) error {
	if cmd.Bool("yes") {
		return nil
	}
	if !isInteractive() {
		return fmt.Errorf(
			"generating scenarios from source uploads your agent's code (%s) to LiveKit Cloud; re-run with --yes to confirm",
			projectDir,
		)
	}
	confirmed := false
	err := huh.NewForm(huh.NewGroup(huh.NewConfirm().
		Title("Upload source to LiveKit Cloud?").
		Description(fmt.Sprintf(
			"No --scenarios file was provided, so test scenarios will be generated\n"+
				"from your agent's code. This uploads %s to LiveKit Cloud.",
			util.Accented(projectDir),
		)).
		Affirmative("Upload").
		Negative("Cancel").
		Value(&confirmed))).
		WithTheme(util.Theme).
		Run()
	if err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf("aborted: source upload was not confirmed")
	}
	return nil
}

// --- Shared lifecycle functions used by both TUI and CI modes ---

// agentLauncher owns the agent subprocess lifecycle around the TUI, which only
// observes the start via Wait. Stop kills the worker even when the TUI quits
// mid-start; a leaked worker keeps its port bound and breaks the next run.
type agentLauncher struct {
	done chan struct{}
	proc *AgentProcess
	err  error
}

func launchSimulationAgent(c *simulateConfig) *agentLauncher {
	l := &agentLauncher{done: make(chan struct{})}
	go func() {
		l.proc, l.err = startSimulationAgent(c, nil)
		close(l.done)
	}()
	return l
}

// Wait blocks until the start attempt finishes and returns its result.
func (l *agentLauncher) Wait() (*AgentProcess, error) {
	<-l.done
	return l.proc, l.err
}

// Stop kills the agent once the start attempt finishes (bounded so a stuck
// start can't hang the exit path) and returns it for post-exit reporting.
func (l *agentLauncher) Stop() *AgentProcess {
	select {
	case <-l.done:
	case <-time.After(10 * time.Second):
		return nil
	}
	if l.proc != nil {
		l.proc.Kill()
	}
	return l.proc
}

// ForceStop kills the agent immediately, without the SIGINT grace.
func (l *agentLauncher) ForceStop() {
	<-l.done
	if l.proc != nil {
		l.proc.ForceKill()
	}
}

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
			// disable the worker load limit so the run can saturate the agent
			"--simulation",
		},
		Env: []string{
			// force the agent to register under the dispatch name regardless of any
			// agent_name hardcoded in the user's code (see LIVEKIT_AGENT_NAME_OVERRIDE
			// precedence in livekit-agents worker.py).
			"LIVEKIT_AGENT_NAME_OVERRIDE=" + c.agentName,
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
		AgentName:      c.agentName,
		NumSimulations: c.numSimulations,
	}
	if c.concurrency > 0 {
		req.Concurrency = &c.concurrency
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
		SimulationRunId: runID,
		CodeEntrypoint:  entrypoint,
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
		out.Warnf("Warning: failed to cancel run: %v", err)
	} else {
		out.Status("Run cancelled")
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
