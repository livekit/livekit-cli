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

	"github.com/charmbracelet/huh"
	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"

	"github.com/klauspost/compress/zstd"
	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/livekit/server-sdk-go/v2/pkg/cloudagents"
	"google.golang.org/protobuf/proto"
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
	// Hide the implicit `help` subcommand so shell completion falls back to
	// native filename completion for the entrypoint arg (see startCommand).
	HideHelpCommand: true,
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
			Name:  "audio",
			Usage: "Simulate speech-to-speech interactions using the agent's full audio pipeline. By default, simulations run in text-only mode.",
		},
		&cli.BoolFlag{
			Name:    "yes",
			Aliases: []string{"y"},
			Usage:   "Skip the source-upload confirmation prompt (required for non-interactive runs that generate from source)",
		},
		&cli.StringFlag{
			Name:  "view",
			Usage: "Open a pre-existing simulation",
		},
		&cli.StringFlag{
			Name:  "agent-name",
			Usage: "Run against an already-running agent instead of spawning one locally. Pass the registered `NAME`, or \"\" to target the project's default agent (the one that auto-joins every room). Requires --scenarios.",
		},
	},
}

// writeGeneratedScenariosTemp writes a generated run's scenarios to a temp
// scenarios.yaml; "" when the run carries none.
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

// scenarioGroupToYAML renders a ScenarioGroup as a scenarios.yaml document, the
// inverse of loadScenarioGroup.
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

// scenariosFile mirrors a scenarios.yaml; `userdata` is a nested mapping here
// and JSON-encoded into the proto's string field.
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

type simulateConfig struct {
	ctx            context.Context
	client         *lksdk.AgentSimulationClient
	pc             *config.ProjectConfig
	numSimulations int32
	concurrency    int32
	mode           simulateMode
	simulationMode livekit.SimulationMode
	agentName      string
	projectDir     string
	projectType    agentfs.ProjectType
	entrypoint     string
	scenarioGroup  *livekit.ScenarioGroup
	scenariosPath  string // path to the --scenarios file (empty when generating from source)
	viewModeRunID  string // non-empty when --view opens a pre-existing run
	liveAgent      bool   // --agent-name: run against an already-running agent, don't spawn one
	// TODO (steveyoon): add agent deployment support
	// agentDeployment string
}

type simulateMode int

const (
	modeScenarios simulateMode = iota
	modeGenerateFromSource
	modeView
)

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

type PackageJSON struct {
	Scripts map[string]string `json:"scripts"`
}

// buildTaskExists reports whether package.json defines a "build" script. Such a
// task usually means the entrypoint path is nontrivial (todo: check dist/main.js).
func buildTaskExists(projectDir string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(projectDir, "package.json"))
	if err != nil {
		return false, err
	}

	var pkg PackageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false, err
	}

	_, ok := pkg.Scripts["build"]
	return ok, nil
}

func runSimulate(ctx context.Context, cmd *cli.Command) error {
	pc := simulateProjectConfig

	numSimulations := int32(cmd.Int("num-simulations"))
	concurrency := int32(cmd.Int("concurrency"))
	runID := cmd.String("view")
	liveAgentName := cmd.String("agent-name")

	// never auto-discovered: an explicit --scenarios file is the source of
	// truth, otherwise scenarios are generated from the agent's source
	scenariosPath := cmd.String("scenarios")

	var (
		agentName   string
		projectDir  string
		projectType agentfs.ProjectType
		entrypoint  string
		liveAgent   bool
		err         error
	)

	// --agent-name (even empty) means: run against an already-running agent,
	// don't spawn one. https://docs.livekit.io/agents/server/agent-dispatch/#automatic
	if cmd.IsSet("agent-name") {
		// nothing is spawned, so there's no source to generate scenarios from.
		if scenariosPath == "" {
			return fmt.Errorf("--agent-name requires --scenarios (no source to generate scenarios from when running against a live agent)")
		}
		liveAgent = true
		agentName = liveAgentName
	} else {
		agentName = generateAgentName()
		projectDir, projectType, err = agentfs.DetectProjectRoot(".")
		if err != nil {
			return err
		}

		entrypointArg := cmd.Args().First()

		// check if a script called "build" exists in the package.json, if so, refuse to discover the
		// entrypoint: build tasks usually mean the entrypoint path is nontrivial (e.g. dist/main.js)
		if projectType.IsNode() && entrypointArg == "" {
			buildTaskDoesExist, err := buildTaskExists(projectDir)
			if err != nil {
				return err
			} else if buildTaskDoesExist {
				return fmt.Errorf("you currently have a build task in your package.json, but no entrypoint was explicitly given; so you must add an entrypoint to the simulate cli")
			}
		}

		entrypoint, err = findEntrypoint(projectDir, entrypointArg, projectType)
		if err != nil {
			return err
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
	case runID != "":
		mode = modeView
	case scenarioGroup != nil && len(scenarioGroup.Scenarios) > 0:
		mode = modeScenarios
	default:
		mode = modeGenerateFromSource
	}

	if mode == modeGenerateFromSource {
		if err := confirmSourceUpload(cmd, projectDir); err != nil {
			return err
		}
	}

	simClient := lksdk.NewAgentSimulationClient(serverURL, pc.APIKey, pc.APISecret)

	simulationMode := livekit.SimulationMode_SIMULATION_MODE_TEXT
	if cmd.Bool("audio") {
		simulationMode = livekit.SimulationMode_SIMULATION_MODE_AUDIO
	}

	simCfg := &simulateConfig{
		ctx:            ctx,
		client:         simClient,
		pc:             pc,
		numSimulations: numSimulations,
		concurrency:    concurrency,
		mode:           mode,
		simulationMode: simulationMode,
		agentName:      agentName,
		projectDir:     projectDir,
		projectType:    projectType,
		entrypoint:     entrypoint,
		scenarioGroup:  scenarioGroup,
		scenariosPath:  scenariosPath,
		viewModeRunID:  runID,
		liveAgent:      liveAgent,
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

// confirmSourceUpload makes the user agree before their agent's source is
// uploaded to LiveKit Cloud.
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

// agentLauncher owns the agent subprocess lifecycle around the TUI. Stop kills
// the worker even when the TUI quits mid-start; a leaked worker keeps its port
// bound and breaks the next run.
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

func (l *agentLauncher) Wait() (*AgentProcess, error) {
	<-l.done
	return l.proc, l.err
}

// Stop kills the agent once the start attempt finishes (bounded wait) and
// returns it for post-exit reporting.
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
	args := []string{
		"start",
		"--url", c.pc.URL,
		"--api-key", c.pc.APIKey,
		"--api-secret", c.pc.APISecret,
		"--log-level", normalizeLogLevel(c.projectType, "DEBUG"),
		// disable the worker load limit so the run can saturate the agent
		"--simulation",
	}

	// --log-format is a Python-only flag; the Node CLI doesn't accept it.
	if c.projectType.IsPython() {
		args = append(args, "--log-format", "colored")
	}

	return startAgent(AgentStartConfig{
		Dir:         c.projectDir,
		Entrypoint:  c.entrypoint,
		ProjectType: c.projectType,
		CLIArgs:     args,
		Env: []string{
			// register under the dispatch name regardless of any agent_name
			// hardcoded in the user's code
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
		Mode:           c.simulationMode,
	}
	if c.concurrency > 0 {
		req.Concurrency = &c.concurrency
	}
	if c.mode == modeScenarios {
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

func decodeRunSummary(run *livekit.SimulationRun) *livekit.SimulationRunSummary {
	if run == nil || len(run.SummaryZstd) == 0 {
		return nil
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil
	}
	defer dec.Close()
	raw, err := dec.DecodeAll(run.SummaryZstd, nil)
	if err != nil {
		return nil
	}
	summary := &livekit.SimulationRunSummary{}
	if err := proto.Unmarshal(raw, summary); err != nil {
		return nil
	}
	return summary
}
