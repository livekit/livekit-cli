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
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/server-sdk-go/v2/pkg/cloudagents"
	agent "github.com/livekit/protocol/livekit/agent"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// --- Styles ---

var (
	tagStyle     = lipgloss.NewStyle().Background(lipgloss.Color("#1fd5f9")).Foreground(lipgloss.Color("#000000")).Bold(true).Padding(0, 1)
	greenStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	redStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	yellowStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	dimStyle     = lipgloss.NewStyle().Faint(true)
	boldStyle    = lipgloss.NewStyle().Bold(true)
	reverseStyle = lipgloss.NewStyle().Reverse(true)
	cyanStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)

	simSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
)

// --- Message types ---

type simulationRunMsg struct {
	run *livekit.SimulationRun
	err error
}

type pollTickMsg struct{}
type spinnerTickMsg struct{}
type glowTickMsg struct{}

type subprocessExitMsg struct {
	err error
}

// --- Filter ---

const (
	filterAll = iota
	filterFailed
	filterPassed
	filterRunning
)

var filterNames = []string{"All", "Failed", "Passed", "Running"}

// --- Model ---

type step struct {
	label   string
	status  string // "pending", "running", "done", "failed"
	elapsed time.Duration
}

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

type simulateModel struct {
	config      *simulateConfig
	client      *lksdk.AgentSimulationClient
	runID       string
	agent       *AgentProcess
	setupCancel context.CancelFunc

	// Setup phase
	steps     []step
	setupDone bool

	// Run phase
	run            *livekit.SimulationRun
	runFinished    bool
	numSimulations int32
	startTime      time.Time
	genStart       time.Time

	quoteIdx   int
	quoteTick  int
	spinnerIdx int
	glowIdx    int

	filter          int
	cursor          int
	scrollOff       int
	detailJobID     string
	showLogs        bool
	showDescription bool

	width  int
	height int
	err    error
}

type quote struct {
	text   string
	glow   bool // iconic quotes get a subtle glow
	weight int  // higher = more likely to appear
}

var simulationQuotes = []quote{
	// Iconic — glow sweep, high weight
	{"There is no spoon.", true, 5},                                    // Spoon Boy — The Matrix
	{"What is real? How do you define real?", true, 5},                 // Morpheus — The Matrix
	{"Wake up, Neo.", true, 5},                                         // Trinity — The Matrix
	{"Free your mind.", true, 4},                                       // Morpheus — The Matrix
	{"Welcome to the real world.", true, 4},                            // Morpheus — The Matrix
	{"Shall we play a game?", true, 4},                                 // WOPR — WarGames
	{"Open the pod bay doors, HAL.", true, 4},                          // Dave — 2001: A Space Odyssey
	{"The Matrix is everywhere. It is all around us.", true, 3},        // Morpheus — The Matrix
	// Well-known — no glow, medium weight
	{"Do not try and bend the spoon. That's impossible.", false, 3},                                 // Spoon Boy — The Matrix
	{"The only winning move is not to play.", false, 3},                                              // WarGames
	{"These violent delights have violent ends.", false, 3},                                          // Westworld
	{"I think, therefore I am.", false, 3},                                                           // René Descartes
	{"Unfortunately, no one can be told what the Matrix is.", false, 2},                              // Morpheus — The Matrix
	{"Ever had that feeling where you're not sure if you're awake or still dreaming?", false, 2},    // Neo — The Matrix
	{"I can only show you the door. You're the one that has to walk through it.", false, 2},         // Morpheus — The Matrix
	{"Remember, all I'm offering is the truth. Nothing more.", false, 2},                            // Morpheus — The Matrix
	// Niche — low weight
	{"I don't like the idea that I'm not in control of my life.", false, 1},                         // Neo — The Matrix
	{"Choice is an illusion created between those with power and those without.", false, 1},          // Merovingian — The Matrix Reloaded
	{"The odds that we are in base reality is one in billions.", false, 1},                           // Elon Musk
	{"The world, then, is a radical illusion.", false, 1},                                            // Jean Baudrillard
	{"That's all it is. Information.", false, 1},                                                     // Ghost in the Shell
	{"Not one single bit of it is real.", false, 1},                                                  // The Metamorphosis of Prime Intellect
	{"I wish I had a good argument against it.", false, 1},                                           // Neil deGrasse Tyson
	// Playful
	{"Warming up the neural pathways...", false, 1},
	{"Reticulating splines...", false, 1},                                                            // SimCity
	{"Generating plausible humans...", false, 1},
	{"Convincing the AI to cooperate...", false, 2},
	{"Teaching robots to small talk...", false, 1},
}

// weightedQuotePool builds a flat slice with quotes repeated by weight for random selection.
var weightedQuotePool = func() []int {
	var pool []int
	for i, q := range simulationQuotes {
		for range q.weight {
			pool = append(pool, i)
		}
	}
	return pool
}()

func newSimulateModel(config *simulateConfig) *simulateModel {
	return &simulateModel{
		config:         config,
		client:         config.client,
		numSimulations: config.numSimulations,
		quoteIdx:       weightedQuotePool[rand.Intn(len(weightedQuotePool))],
		width:          80,
		height:         24,
	}
}

// --- Setup messages ---

type setupStepMsg struct {
	stepIdx  int
	elapsed  []time.Duration // elapsed time per completed step
	err      error
	runID    string
	agent    *AgentProcess
}

func (m *simulateModel) Init() tea.Cmd {
	return tea.Batch(
		m.runSetup(),
		tickCmd(),
		spinnerTickCmd(),
		glowTickCmd(),
	)
}

func (m *simulateModel) runSetup() tea.Cmd {
	c := m.config

	// Determine which steps to show
	m.steps = []step{
		{label: "Starting agent", status: "running"},
		{label: "Creating simulation", status: "pending"},
	}
	if c.mode == modeGenerateFromSource {
		m.steps = append(m.steps, step{label: "Uploading source", status: "pending"})
	}

	ctx, cancel := context.WithCancel(c.ctx)
	m.setupCancel = cancel

	return func() tea.Msg {
		var elapsed []time.Duration
		stepStart := time.Now()

		// Step 0: Start agent & wait for registration
		agent, err := startAgent(AgentStartConfig{
			Dir:         c.projectDir,
			Entrypoint:  c.entrypoint,
			ProjectType: c.projectType,
			CLIArgs: []string{
				"start",
				"--url", c.pc.URL,
				"--api-key", c.pc.APIKey,
				"--api-secret", c.pc.APISecret,
			},
			Env: []string{
				"LIVEKIT_AGENT_NAME=" + c.agentName,
				"LIVEKIT_URL=" + c.pc.URL,
				"LIVEKIT_API_KEY=" + c.pc.APIKey,
				"LIVEKIT_API_SECRET=" + c.pc.APISecret,
			},
			ReadySignal: "registered worker",
		})
		if err != nil {
			return setupStepMsg{stepIdx: 0, err: fmt.Errorf("failed to start agent: %w", err)}
		}

		// Wait for agent ready
		timeout := time.NewTimer(10 * time.Second)
		defer timeout.Stop()
		select {
		case <-agent.Ready():
		case err := <-agent.Done():
			if err != nil {
				return setupStepMsg{stepIdx: 0, err: fmt.Errorf("agent exited before registering: %w", err), agent: agent}
			}
			return setupStepMsg{stepIdx: 0, err: fmt.Errorf("agent exited before registering"), agent: agent}
		case <-timeout.C:
			return setupStepMsg{stepIdx: 0, err: fmt.Errorf("timed out waiting for agent to register (10s)"), agent: agent}
		case <-ctx.Done():
			return setupStepMsg{stepIdx: 0, err: ctx.Err(), agent: agent}
		}
		elapsed = append(elapsed, time.Since(stepStart))
		stepStart = time.Now()

		// Step 1: Create simulation run
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
			return setupStepMsg{stepIdx: 1, err: fmt.Errorf("failed to create simulation: %w", err), agent: agent}
		}
		elapsed = append(elapsed, time.Since(stepStart))
		stepStart = time.Now()
		runID := resp.SimulationRunId

		// Step 2: Upload source (if needed)
		if c.mode == modeGenerateFromSource {
			presigned := resp.PresignedPostRequest
			if presigned == nil {
				return setupStepMsg{stepIdx: 2, err: fmt.Errorf("server did not return upload URL"), agent: agent, runID: runID}
			}

			sourceDir, _ := os.Getwd()
			var buf bytes.Buffer
			if err := cloudagents.CreateSourceTarball(os.DirFS(sourceDir), nil, &buf); err != nil {
				return setupStepMsg{stepIdx: 2, err: fmt.Errorf("failed to create source archive: %w", err), agent: agent, runID: runID}
			}
			if err := cloudagents.MultipartUpload(presigned.Url, presigned.Values, &buf); err != nil {
				return setupStepMsg{stepIdx: 2, err: fmt.Errorf("failed to upload source: %w", err), agent: agent, runID: runID}
			}
			if _, err := c.client.ConfirmSimulationSourceUpload(ctx, &livekit.SimulationRun_ConfirmSourceUpload_Request{
				SimulationRunId: runID,
			}); err != nil {
				return setupStepMsg{stepIdx: 2, err: fmt.Errorf("failed to confirm upload: %w", err), agent: agent, runID: runID}
			}
			elapsed = append(elapsed, time.Since(stepStart))
		}

		// All done
		lastStep := len(m.steps) - 1
		return setupStepMsg{stepIdx: lastStep, elapsed: elapsed, agent: agent, runID: runID}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return pollTickMsg{}
	})
}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

func glowTickCmd() tea.Cmd {
	return tea.Tick(40*time.Millisecond, func(t time.Time) tea.Msg {
		return glowTickMsg{}
	})
}

func (m *simulateModel) pollSimulation() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := m.client.GetSimulationRun(ctx, &livekit.SimulationRun_Get_Request{
			SimulationRunId: m.runID,
		})
		if err != nil {
			return simulationRunMsg{err: err}
		}
		return simulationRunMsg{run: resp.Run}
	}
}

func (m *simulateModel) waitSubprocess() tea.Cmd {
	if m.agent == nil {
		return nil
	}
	return func() tea.Msg {
		err := <-m.agent.Done()
		return subprocessExitMsg{err: err}
	}
}

func (m *simulateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case setupStepMsg:
		if msg.agent != nil {
			m.agent = msg.agent
		}
		if msg.runID != "" {
			m.runID = msg.runID
		}
		if msg.err != nil {
			// Mark current step as failed
			if msg.stepIdx < len(m.steps) {
				m.steps[msg.stepIdx].status = "failed"
			}
			m.err = msg.err
			m.setupDone = true
			m.runFinished = true
			return m, nil
		}
		// Mark all steps up to and including this one as done
		for i := 0; i <= msg.stepIdx && i < len(m.steps); i++ {
			m.steps[i].status = "done"
			if i < len(msg.elapsed) {
				m.steps[i].elapsed = msg.elapsed[i]
			}
		}
		// If all steps are done, start polling
		if msg.stepIdx >= len(m.steps)-1 {
			m.setupDone = true
			m.genStart = time.Now()
			return m, tea.Batch(m.pollSimulation(), m.waitSubprocess())
		}
		// Mark next step as running
		if msg.stepIdx+1 < len(m.steps) {
			m.steps[msg.stepIdx+1].status = "running"
		}
		return m, nil

	case simulationRunMsg:
		if msg.err == nil && msg.run != nil {
			m.run = msg.run
			if m.startTime.IsZero() && msg.run.Status == livekit.SimulationRun_STATUS_RUNNING {
				m.startTime = time.Now()
			}
			if msg.run.Status == livekit.SimulationRun_STATUS_COMPLETED ||
				msg.run.Status == livekit.SimulationRun_STATUS_FAILED ||
				msg.run.Status == livekit.SimulationRun_STATUS_CANCELLED {
				m.runFinished = true
			}
		}

	case spinnerTickMsg:
		m.spinnerIdx++
		return m, spinnerTickCmd()

	case glowTickMsg:
		m.glowIdx++
		return m, glowTickCmd()

	case pollTickMsg:
		m.quoteTick++
		if m.quoteTick%60 == 0 {
			m.quoteIdx = weightedQuotePool[rand.Intn(len(weightedQuotePool))]
		}
		var cmds []tea.Cmd
		if m.setupDone && !m.runFinished {
			cmds = append(cmds, m.pollSimulation())
		}
		cmds = append(cmds, tickCmd())
		return m, tea.Batch(cmds...)

	case subprocessExitMsg:
		// Subprocess exited — don't quit TUI, just note it

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *simulateModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.setupCancel != nil {
			m.setupCancel()
		}
		return m, tea.Quit
	case "ctrl+l":
		m.showLogs = !m.showLogs
	case "d":
		if m.detailJobID == "" {
			m.showDescription = !m.showDescription
		}
	case "up", "shift+tab":
		m.cursor--
	case "down", "tab":
		m.cursor++
	case "pgup":
		m.cursor -= 20
	case "pgdown":
		m.cursor += 20
	case "left":
		m.filter = (m.filter + len(filterNames) - 1) % len(filterNames)
		m.cursor = 0
		m.scrollOff = 0
	case "right":
		m.filter = (m.filter + 1) % len(filterNames)
		m.cursor = 0
		m.scrollOff = 0
	case "enter":
		if m.detailJobID == "" {
			jobs := m.filteredJobs()
			if m.cursor >= 0 && m.cursor < len(jobs) {
				m.detailJobID = jobs[m.cursor].job.Id
			}
		}
	case "esc", "backspace":
		if m.detailJobID != "" {
			m.detailJobID = ""
		}
	case "q":
		if m.detailJobID != "" {
			m.detailJobID = ""
		} else {
			return m, tea.Quit
		}
	}
	return m, nil
}

type indexedJob struct {
	origIdx int
	job     *livekit.SimulationRun_Job
}

func (m *simulateModel) filteredJobs() []indexedJob {
	if m.run == nil {
		return nil
	}
	var result []indexedJob
	for i, j := range m.run.Jobs {
		match := false
		switch m.filter {
		case filterAll:
			match = true
		case filterFailed:
			match = j.Status == livekit.SimulationRun_Job_STATUS_FAILED
		case filterPassed:
			match = j.Status == livekit.SimulationRun_Job_STATUS_COMPLETED
		case filterRunning:
			match = j.Status == livekit.SimulationRun_Job_STATUS_RUNNING
		}
		if match {
			result = append(result, indexedJob{origIdx: i + 1, job: j})
		}
	}
	return result
}

func (m *simulateModel) View() string {
	// Setup phase or generating phase — show unified step view
	if !m.setupDone || m.run == nil || m.run.Status == livekit.SimulationRun_STATUS_GENERATING {
		return m.viewSetup()
	}
	switch m.run.Status {
	case livekit.SimulationRun_STATUS_FAILED:
		if len(m.run.Jobs) == 0 {
			return m.viewFailed()
		}
		return m.viewRunning()
	default:
		return m.viewRunning()
	}
}

func (m *simulateModel) viewSetup() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(tagStyle.Render("Agent Simulation"))
	b.WriteString("\n\n")

	if m.config.pc != nil && m.config.pc.Name != "" {
		b.WriteString(dimStyle.Render("  Project: "+m.config.pc.Name) + "\n")
	}
	if m.config.pc != nil && m.config.pc.URL != "" {
		b.WriteString(dimStyle.Render("  URL:     "+m.config.pc.URL) + "\n")
	}
	if m.runID != "" {
		b.WriteString(dimStyle.Render("  Run:     "+m.runID) + "\n")
	}
	if url := m.getDashboardURL(); url != "" {
		b.WriteString(dimStyle.Render("  "+url) + "\n")
	}
	b.WriteString("\n")

	b.WriteString(m.renderSteps())

	// Show generation progress after setup completes
	if m.setupDone && m.err == nil {
		elapsed := time.Since(m.genStart).Truncate(time.Second)
		b.WriteString(fmt.Sprintf("  %s Generating %d scenarios  %s %s\n", yellowStyle.Render("●"), m.numSimulations, m.spinner(), dimStyle.Render(elapsed.String())))
	}

	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(redStyle.Render("  "+m.err.Error()) + "\n")
		if m.agent != nil {
			b.WriteString("\n")
			b.WriteString(m.renderLogs())
		}
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  q quit"))
		b.WriteString("\n")
	} else {
		b.WriteString("\n")
		if m.showLogs {
			b.WriteString(m.renderLogs())
		}
		b.WriteString(m.quoteAboveHint("  Ctrl+L logs"))
		b.WriteString("\n")
	}
	return b.String()
}

func (m *simulateModel) spinner() string {
	return yellowStyle.Render(simSpinnerFrames[m.spinnerIdx%len(simSpinnerFrames)])
}

var quoteStyleDim = lipgloss.NewStyle().Foreground(lipgloss.Color("237"))

// glowShades are brightness levels for the sweep effect (dark → bright → dark)
var glowShades = []lipgloss.Color{"237", "239", "242", "245", "248", "245", "242", "239", "237"}

func (m *simulateModel) quote() string {
	q := simulationQuotes[m.quoteIdx]
	if !q.glow {
		return quoteStyleDim.Render(q.text)
	}
	// Sweep a bright spot across the text, then stay dark for a long pause
	runes := []rune(q.text)
	sweepLen := len(runes) + len(glowShades)
	cycleLen := sweepLen + 250 // ~10s pause at 40ms tick
	center := m.glowIdx % cycleLen
	if center >= sweepLen {
		// In the pause phase — render all dim
		return quoteStyleDim.Render(q.text)
	}
	var b strings.Builder
	for i, r := range runes {
		dist := center - i
		if dist >= 0 && dist < len(glowShades) {
			style := lipgloss.NewStyle().Foreground(glowShades[dist])
			if dist >= 2 && dist <= 6 { // italic only for the brightest chars
				style = style.Italic(true)
			}
			b.WriteString(style.Render(string(r)))
		} else {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("237")).Render(string(r)))
		}
	}
	return b.String()
}

func (m *simulateModel) renderSteps() string {
	var b strings.Builder
	for _, s := range m.steps {
		switch s.status {
		case "done":
			elapsed := ""
			if s.elapsed > 0 {
				elapsed = " " + dimStyle.Render(s.elapsed.Round(time.Millisecond).String())
			}
			b.WriteString(fmt.Sprintf("  %s %s%s\n", greenStyle.Render("✓"), s.label, elapsed))
		case "running":
			b.WriteString(fmt.Sprintf("  %s %s\n", yellowStyle.Render("●"), s.label))
		case "failed":
			b.WriteString(fmt.Sprintf("  %s %s\n", redStyle.Render("✗"), s.label))
		default:
			b.WriteString(fmt.Sprintf("  %s %s\n", dimStyle.Render("○"), s.label))
		}
	}
	return b.String()
}

func (m *simulateModel) getDashboardURL() string {
	if m.runID == "" || m.config == nil || m.config.pc == nil || m.config.pc.ProjectId == "" {
		return ""
	}
	return fmt.Sprintf("%s/projects/%s/agents/simulations/%s", dashboardURL, m.config.pc.ProjectId, m.runID)
}

func (m *simulateModel) viewFailed() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(tagStyle.Render("Agent Simulation"))
	b.WriteString("  ")
	b.WriteString(dimStyle.Render(m.runID))
	b.WriteString("\n\n")
	b.WriteString("  " + redStyle.Bold(true).Render("Failed") + "\n\n")
	if m.run.Error != "" {
		for _, line := range strings.Split(m.run.Error, "\n") {
			b.WriteString(redStyle.Render("  "+line) + "\n")
		}
	} else {
		b.WriteString(redStyle.Render("  (no error details available)") + "\n")
	}
	b.WriteString("\n")
	if m.showLogs {
		b.WriteString(m.renderLogs())
	}
	b.WriteString(dimStyle.Render("  Ctrl+L logs · q quit"))
	b.WriteString("\n")
	return b.String()
}

func (m *simulateModel) viewRunning() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(tagStyle.Render("Agent Simulation"))
	b.WriteString("  ")
	b.WriteString(dimStyle.Render(m.runID))
	if url := m.getDashboardURL(); url != "" {
		b.WriteString("  " + dimStyle.Render(url))
	}
	b.WriteString("\n\n")

	// Agent description
	if m.run != nil && m.run.AgentDescription != "" {
		b.WriteString(boldStyle.Render("  Agent Description") + "\n")
		if m.showDescription {
			wrapped := dimStyle.Width(m.width - 4).Render(m.run.AgentDescription)
			for _, line := range strings.Split(wrapped, "\n") {
				b.WriteString("  " + line + "\n")
			}
			b.WriteString(dimStyle.Render("  (press d to collapse)") + "\n\n")
		} else {
			desc := firstMeaningfulLine(m.run.AgentDescription)
			if desc != "" {
				b.WriteString(dimStyle.Width(m.width-4).Render("  "+desc) + "\n")
				b.WriteString(dimStyle.Render("  (press d to expand)") + "\n\n")
			}
		}
	}

	// Header line
	b.WriteString(m.renderHeader())
	b.WriteString("\n")

	// Progress counts
	b.WriteString(m.renderCounts())
	b.WriteString("\n")

	// Filter tabs
	b.WriteString(m.renderFilterTabs())
	b.WriteString("\n\n")

	if m.detailJobID != "" {
		b.WriteString(m.renderDetail())
	} else {
		b.WriteString(m.renderJobList())

		// Show summary when run is completed and summary is available
		if m.run.Summary != nil {
			b.WriteString(m.renderSummary())
		}
	}

	b.WriteString("\n")
	if m.showLogs {
		b.WriteString(m.renderLogs())
	}
	b.WriteString(m.renderHint())
	b.WriteString("\n")
	return b.String()
}

func (m *simulateModel) renderHeader() string {
	var label, style string
	switch {
	case m.run.Status == livekit.SimulationRun_STATUS_COMPLETED || m.run.Status == livekit.SimulationRun_STATUS_FAILED || m.run.Status == livekit.SimulationRun_STATUS_CANCELLED:
		total, done, _, _ := m.jobCounts()
		allJobsDone := total > 0 && done == total
		if m.run.Status == livekit.SimulationRun_STATUS_CANCELLED {
			label = "Cancelled"
			style = "yellow"
		} else if m.run.Status == livekit.SimulationRun_STATUS_FAILED && !allJobsDone {
			label = "Failed"
			style = "red"
		} else {
			label = "Completed"
			style = "green"
			if allJobsDone && m.run.Summary == nil {
				label += " — summary unavailable"
			}
		}
	case m.run.Status == livekit.SimulationRun_STATUS_SUMMARIZING:
		label = "Summarizing..."
		style = "yellow"
	default:
		label = "Running"
		style = "yellow"
	}

	header := boldStyle.Render("Simulation") + " — "
	switch style {
	case "green":
		header += greenStyle.Bold(true).Render(label)
	case "red":
		header += redStyle.Bold(true).Render(label)
	case "yellow":
		header += yellowStyle.Bold(true).Render(label)
	}
	return "  " + header
}

func (m *simulateModel) jobCounts() (total, done, passed, failed int) {
	if m.run == nil {
		return
	}
	total = len(m.run.Jobs)
	for _, j := range m.run.Jobs {
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

func (m *simulateModel) renderCounts() string {
	total, done, passed, failed := m.jobCounts()
	running := 0
	if m.run != nil {
		for _, j := range m.run.Jobs {
			if j.Status == livekit.SimulationRun_Job_STATUS_RUNNING {
				running++
			}
		}
	}

	var parts []string
	parts = append(parts, boldStyle.Render(fmt.Sprintf("%d/%d", done, total)))
	if passed > 0 {
		parts = append(parts, greenStyle.Render(fmt.Sprintf("%d passed", passed)))
	}
	if failed > 0 {
		parts = append(parts, redStyle.Render(fmt.Sprintf("%d failed", failed)))
	}
	if running > 0 {
		parts = append(parts, yellowStyle.Render(fmt.Sprintf("%d running", running)))
	}

	elapsed := ""
	if !m.startTime.IsZero() {
		d := time.Since(m.startTime)
		secs := int(d.Seconds())
		mins := secs / 60
		secs = secs % 60
		if mins > 0 {
			elapsed = fmt.Sprintf("%dm%02ds", mins, secs)
		} else {
			elapsed = fmt.Sprintf("%ds", secs)
		}
	}

	result := "  " + strings.Join(parts, "  ")
	if elapsed != "" {
		result += "  " + dimStyle.Render(elapsed)
	}
	return result
}

func (m *simulateModel) renderFilterTabs() string {
	total, _, passed, failed := m.jobCounts()
	running := 0
	if m.run != nil {
		for _, j := range m.run.Jobs {
			if j.Status == livekit.SimulationRun_Job_STATUS_RUNNING {
				running++
			}
		}
	}

	counts := []int{total, failed, passed, running}
	styles := []lipgloss.Style{lipgloss.NewStyle(), redStyle, greenStyle, yellowStyle}

	var parts []string
	for i, name := range filterNames {
		label := fmt.Sprintf("%s: %d", name, counts[i])
		if i == m.filter {
			parts = append(parts, styles[i].Bold(true).Render(label))
		} else {
			parts = append(parts, dimStyle.Render(label))
		}
	}
	return "  " + strings.Join(parts, "  ")
}

func (m *simulateModel) renderJobList() string {
	jobs := m.filteredJobs()
	if len(jobs) == 0 {
		return dimStyle.Render("  (no jobs match this filter)")
	}

	// Clamp cursor
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(jobs) {
		m.cursor = len(jobs) - 1
	}

	// Compute visible window
	availHeight := m.height - 14
	if availHeight < 5 {
		availHeight = 5
	}

	if m.cursor < m.scrollOff {
		m.scrollOff = m.cursor
	} else if m.cursor >= m.scrollOff+availHeight {
		m.scrollOff = m.cursor - availHeight + 1
	}
	if m.scrollOff < 0 {
		m.scrollOff = 0
	}
	if m.scrollOff > len(jobs)-availHeight {
		m.scrollOff = len(jobs) - availHeight
	}
	if m.scrollOff < 0 {
		m.scrollOff = 0
	}

	winStart := m.scrollOff
	winEnd := m.scrollOff + availHeight
	if winEnd > len(jobs) {
		winEnd = len(jobs)
	}

	var b strings.Builder

	if winStart > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ... %d more above ...", winStart)))
		b.WriteString("\n")
	}

	for i := winStart; i < winEnd; i++ {
		ij := jobs[i]
		icon := jobIcon(ij.job)
		instr := ij.job.Instructions
		if len(instr) > 60 {
			instr = instr[:60] + "..."
		}
		if instr == "" {
			instr = "—"
		}

		var line string
		if i == m.cursor {
			// Build without inner styles so reverse applies cleanly
			line = fmt.Sprintf("  %s %3d. %s  %s", icon, ij.origIdx, ij.job.Id, instr)
			visible := lipgloss.Width(line)
			if visible < m.width {
				line += strings.Repeat(" ", m.width-visible)
			}
			line = reverseStyle.Render(line)
		} else {
			line = fmt.Sprintf("  %s %3d. %s  %s", icon, ij.origIdx, dimStyle.Render(ij.job.Id), instr)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	remaining := len(jobs) - winEnd
	if remaining > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ... %d more below ...", remaining)))
		b.WriteString("\n")
	}

	return b.String()
}

func (m *simulateModel) renderDetail() string {
	if m.run == nil {
		return ""
	}
	var job *livekit.SimulationRun_Job
	origIdx := 0
	for i, j := range m.run.Jobs {
		if j.Id == m.detailJobID {
			job = j
			origIdx = i + 1
			break
		}
	}
	if job == nil {
		m.detailJobID = ""
		return dimStyle.Render("  (job not found)\n")
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  %s %s %s\n",
		jobIcon(job),
		boldStyle.Render(fmt.Sprintf("Job %d", origIdx)),
		dimStyle.Render(job.Id),
	))
	b.WriteString("\n")

	wrapWidth := m.width - 6
	if wrapWidth < 40 {
		wrapWidth = 40
	}
	wrapStyle := lipgloss.NewStyle().Width(wrapWidth)

	b.WriteString(boldStyle.Render("  Instructions:"))
	b.WriteString("\n")
	instr := job.Instructions
	if instr == "" {
		instr = "—"
	}
	for _, line := range strings.Split(wrapStyle.Render(instr), "\n") {
		b.WriteString("    " + line + "\n")
	}
	b.WriteString("\n")

	b.WriteString(dimStyle.Bold(true).Render("  Expected:"))
	b.WriteString("\n")
	expect := job.AgentExpectations
	if expect == "" {
		expect = "—"
	}
	for _, line := range strings.Split(wrapStyle.Render(expect), "\n") {
		b.WriteString(dimStyle.Render("    "+line) + "\n")
	}

	if job.Error != "" {
		b.WriteString("\n")
		if job.Status == livekit.SimulationRun_Job_STATUS_COMPLETED {
			b.WriteString(greenStyle.Bold(true).Render("  Result:"))
			b.WriteString("\n")
			for _, line := range strings.Split(wrapStyle.Render(job.Error), "\n") {
				b.WriteString(greenStyle.Render("    "+line) + "\n")
			}
		} else {
			b.WriteString(redStyle.Bold(true).Render("  Error:"))
			b.WriteString("\n")
			for _, line := range strings.Split(wrapStyle.Render(job.Error), "\n") {
				b.WriteString(redStyle.Render("    "+line) + "\n")
			}
		}
	}

	// Show chat transcript if available
	b.WriteString(m.renderChatTranscript(job.Id))

	return b.String()
}

func (m *simulateModel) renderSummary() string {
	summary := m.run.Summary
	if summary == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  " + strings.Repeat("─", 40)))
	b.WriteString("\n\n")
	b.WriteString("  " + boldStyle.Render("Summary"))
	b.WriteString(fmt.Sprintf("  %s  %s\n\n",
		greenStyle.Render(fmt.Sprintf("%d passed", summary.Passed)),
		redStyle.Render(fmt.Sprintf("%d failed", summary.Failed)),
	))

	wrapWidth := m.width - 6
	if wrapWidth < 40 {
		wrapWidth = 40
	}

	if summary.GoingWell != "" {
		b.WriteString(greenStyle.Bold(true).Render("  Going well:"))
		b.WriteString("\n")
		wrapped := lipgloss.NewStyle().Width(wrapWidth).Render(summary.GoingWell)
		for _, line := range strings.Split(wrapped, "\n") {
			b.WriteString("    " + line + "\n")
		}
		b.WriteString("\n")
	}

	if summary.ToImprove != "" {
		b.WriteString(yellowStyle.Bold(true).Render("  To improve:"))
		b.WriteString("\n")
		wrapped := lipgloss.NewStyle().Width(wrapWidth).Render(summary.ToImprove)
		for _, line := range strings.Split(wrapped, "\n") {
			b.WriteString("    " + line + "\n")
		}
		b.WriteString("\n")
	}

	if len(summary.Issues) > 0 {
		b.WriteString(redStyle.Bold(true).Render("  Issues:"))
		b.WriteString("\n")
		issueWrap := wrapWidth - 4 // account for "    N. " prefix
		if issueWrap < 30 {
			issueWrap = 30
		}
		for i, issue := range summary.Issues {
			prefix := fmt.Sprintf("    %d. ", i+1)
			descWrapped := lipgloss.NewStyle().Width(issueWrap).Render(issue.Description)
			for j, line := range strings.Split(descWrapped, "\n") {
				if j == 0 {
					b.WriteString(prefix + line + "\n")
				} else {
					b.WriteString(strings.Repeat(" ", len(prefix)) + line + "\n")
				}
			}
			if issue.Suggestion != "" {
				sugWrapped := lipgloss.NewStyle().Width(issueWrap).Render("Suggestion: " + issue.Suggestion)
				for _, line := range strings.Split(sugWrapped, "\n") {
					b.WriteString(dimStyle.Render(strings.Repeat(" ", len(prefix))+line) + "\n")
				}
			}
		}
		b.WriteString("\n")
	}

	return b.String()
}

func (m *simulateModel) renderChatTranscript(jobID string) string {
	if m.run.Summary == nil || m.run.Summary.ChatHistory == nil {
		return ""
	}
	chatCtx, ok := m.run.Summary.ChatHistory[jobID]
	if !ok || chatCtx == nil || len(chatCtx.Items) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(boldStyle.Render("  Transcript:"))
	b.WriteString("\n\n")

	for _, item := range chatCtx.Items {
		switch v := item.Item.(type) {
		case *agent.ChatContext_ChatItem_Message:
			msg := v.Message
			role := chatRoleLabel(msg.Role)
			text := chatMessageText(msg)
			b.WriteString(fmt.Sprintf("    %s: %s\n", role, text))
		case *agent.ChatContext_ChatItem_FunctionCall:
			fc := v.FunctionCall
			args := fc.Arguments
			if len(args) > 80 {
				args = args[:80] + "..."
			}
			b.WriteString(dimStyle.Render(fmt.Sprintf("    [call] %s(%s)", fc.Name, args)))
			b.WriteString("\n")
		case *agent.ChatContext_ChatItem_FunctionCallOutput:
			fco := v.FunctionCallOutput
			output := fco.Output
			if len(output) > 80 {
				output = output[:80] + "..."
			}
			label := "output"
			if fco.IsError {
				label = "error"
			}
			b.WriteString(dimStyle.Render(fmt.Sprintf("    [%s] %s -> %s", label, fco.Name, output)))
			b.WriteString("\n")
		case *agent.ChatContext_ChatItem_AgentHandoff:
			h := v.AgentHandoff
			b.WriteString(dimStyle.Render(fmt.Sprintf("    [handoff] -> %s", h.NewAgentId)))
			b.WriteString("\n")
		case *agent.ChatContext_ChatItem_AgentConfigUpdate:
			b.WriteString(dimStyle.Render("    [config update]"))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func chatRoleLabel(role agent.ChatRole) string {
	switch role {
	case agent.ChatRole_USER:
		return cyanStyle.Render("User")
	case agent.ChatRole_ASSISTANT:
		return greenStyle.Render("Agent")
	case agent.ChatRole_SYSTEM:
		return dimStyle.Render("System")
	case agent.ChatRole_DEVELOPER:
		return dimStyle.Render("Developer")
	default:
		return dimStyle.Render("Unknown")
	}
}

func chatMessageText(msg *agent.ChatMessage) string {
	if msg == nil || len(msg.Content) == 0 {
		return ""
	}
	var parts []string
	for _, c := range msg.Content {
		if t := c.GetText(); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

func (m *simulateModel) renderLogs() string {
	if m.agent == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(dimStyle.Render("  " + strings.Repeat("─", 40)))
	b.WriteString("\n")
	logBudget := m.height - 15
	if logBudget < 3 {
		logBudget = 3
	}
	lines := m.agent.RecentLogs(logBudget)
	for _, line := range lines {
		b.WriteString(dimStyle.Render("  "+line) + "\n")
	}
	return b.String()
}

// firstMeaningfulLine returns the first non-empty, non-heading line from text.
func firstMeaningfulLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}

func (m *simulateModel) renderHint() string {
	var hint string
	if m.detailJobID != "" {
		hint = "  ESC/q back · Ctrl+L logs"
	} else {
		hint = "  ↑↓/Tab navigate · ENTER detail · ←→ filter · d description · Ctrl+L logs"
		if m.runFinished {
			hint += " · q quit"
		}
	}
	return m.quoteAboveHint(hint)
}

func (m *simulateModel) quoteAboveHint(hint string) string {
	q := m.quote()
	if !m.showLogs && lipgloss.Width(q) < m.width-4 {
		return "  " + q + "\n" + dimStyle.Render(hint)
	}
	return dimStyle.Render(hint)
}

func jobIcon(job *livekit.SimulationRun_Job) string {
	switch job.Status {
	case livekit.SimulationRun_Job_STATUS_COMPLETED:
		return greenStyle.Render("✓")
	case livekit.SimulationRun_Job_STATUS_FAILED:
		return redStyle.Render("✗")
	case livekit.SimulationRun_Job_STATUS_RUNNING:
		return yellowStyle.Render("●")
	default:
		return dimStyle.Render("○")
	}
}
