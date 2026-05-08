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

type subprocessExitMsg struct {
	err error
}

// --- Filter ---

const (
	agentRegisterTimeout = 20 * time.Second
)

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
	setupCtx    context.Context
	setupCancel context.CancelFunc

	// Setup phase
	steps       []step
	currentStep int
	setupDone   bool
	stepStart   time.Time

	// Run phase
	run            *livekit.SimulationRun
	runFinished    bool
	numSimulations int32
	startTime      time.Time
	endTime        time.Time
	genStart       time.Time

	spinnerIdx int

	filter          int
	cursor          int
	scrollOff       int
	detailJobID     string
	showLogs        bool
	showDescription bool

	matrix              matrixRain
	matrixSavedShowLogs bool

	width  int
	height int
	err    error
}

func newSimulateModel(config *simulateConfig) *simulateModel {
	return &simulateModel{
		config:         config,
		client:         config.client,
		numSimulations: config.numSimulations,
		width:          80,
		height:         24,
	}
}

// --- Setup messages ---

type agentStartedMsg struct {
	agent *AgentProcess
	err   error
}

type agentReadyMsg struct {
	elapsed time.Duration
	err     error
}

type simulationCreatedMsg struct {
	runID     string
	presigned *livekit.PresignedPostRequest
	elapsed   time.Duration
	err       error
}

type sourceUploadedMsg struct {
	elapsed time.Duration
	err     error
}

func (m *simulateModel) Init() tea.Cmd {
	return tea.Batch(
		m.runSetup(),
		tickCmd(),
		spinnerTickCmd(),
	)
}

func (m *simulateModel) runSetup() tea.Cmd {
	c := m.config

	m.steps = []step{
		{label: "Starting agent", status: "running"},
		{label: "Creating simulation", status: "pending"},
	}
	if c.mode == modeGenerateFromSource {
		m.steps = append(m.steps, step{label: "Uploading source", status: "pending"})
	}

	ctx, cancel := context.WithCancel(c.ctx)
	m.setupCtx = ctx
	m.setupCancel = cancel
	m.stepStart = time.Now()

	return m.startAgentCmd()
}

func (m *simulateModel) failSetupStep(err error) {
	m.steps[m.currentStep].status = "failed"
	m.err = err
	m.setupDone = true
	m.runFinished = true
}

func (m *simulateModel) advanceSetupStep(elapsed time.Duration) {
	m.steps[m.currentStep].status = "done"
	m.steps[m.currentStep].elapsed = elapsed
	m.currentStep++
	m.steps[m.currentStep].status = "running"
	m.stepStart = time.Now()
}

func (m *simulateModel) completeSetup(elapsed time.Duration) {
	m.steps[m.currentStep].status = "done"
	m.steps[m.currentStep].elapsed = elapsed
	m.setupDone = true
	m.genStart = time.Now()
}

func (m *simulateModel) startAgentCmd() tea.Cmd {
	c := m.config
	return func() tea.Msg {
		agent, err := startAgent(AgentStartConfig{
			Dir:         c.projectDir,
			Entrypoint:  c.entrypoint,
			ProjectType: c.projectType,
			CLIArgs: []string{
				"start",
				"--url", c.pc.URL,
				"--api-key", c.pc.APIKey,
				"--api-secret", c.pc.APISecret,
				"--log-format", "colored",
			},
			Env: []string{
				"LIVEKIT_AGENT_NAME=" + c.agentName,
				"LIVEKIT_URL=" + c.pc.URL,
				"LIVEKIT_API_KEY=" + c.pc.APIKey,
				"LIVEKIT_API_SECRET=" + c.pc.APISecret,
			},
			ReadySignal: "registered worker",
		})
		return agentStartedMsg{agent: agent, err: err}
	}
}

func (m *simulateModel) waitAgentReadyCmd() tea.Cmd {
	stepStart := m.stepStart
	return func() tea.Msg {
		timeout := time.NewTimer(agentRegisterTimeout)
		defer timeout.Stop()
		select {
		case <-m.agent.Ready():
			return agentReadyMsg{elapsed: time.Since(stepStart)}
		case err := <-m.agent.Done():
			if err != nil {
				return agentReadyMsg{err: fmt.Errorf("agent exited before registering: %w", err)}
			}
			return agentReadyMsg{err: fmt.Errorf("agent exited before registering")}
		case <-timeout.C:
			m.agent.Kill()
			return agentReadyMsg{err: fmt.Errorf("timed out waiting for agent to register (%s)", agentRegisterTimeout)}
		case <-m.setupCtx.Done():
			return agentReadyMsg{err: m.setupCtx.Err()}
		}
	}
}

func (m *simulateModel) createSimulationCmd() tea.Cmd {
	c := m.config
	return func() tea.Msg {
		start := time.Now()
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

		resp, err := c.client.CreateSimulationRun(m.setupCtx, req)
		if err != nil {
			return simulationCreatedMsg{err: fmt.Errorf("failed to create simulation: %w", err)}
		}
		return simulationCreatedMsg{
			runID:     resp.SimulationRunId,
			presigned: resp.PresignedPostRequest,
			elapsed:   time.Since(start),
		}
	}
}

func (m *simulateModel) uploadSourceCmd(presigned *livekit.PresignedPostRequest) tea.Cmd {
	c := m.config
	return func() tea.Msg {
		start := time.Now()
		if presigned == nil {
			return sourceUploadedMsg{err: fmt.Errorf("server did not return upload URL")}
		}

		sourceDir, _ := os.Getwd()
		var buf bytes.Buffer
		if err := cloudagents.CreateSourceTarball(os.DirFS(sourceDir), nil, &buf); err != nil {
			return sourceUploadedMsg{err: fmt.Errorf("failed to create source archive: %w", err)}
		}
		if err := cloudagents.MultipartUpload(presigned.Url, presigned.Values, &buf); err != nil {
			return sourceUploadedMsg{err: fmt.Errorf("failed to upload source: %w", err)}
		}
		if _, err := c.client.ConfirmSimulationSourceUpload(m.setupCtx, &livekit.SimulationRun_ConfirmSourceUpload_Request{
			SimulationRunId: m.runID,
		}); err != nil {
			return sourceUploadedMsg{err: fmt.Errorf("failed to confirm upload: %w", err)}
		}
		return sourceUploadedMsg{elapsed: time.Since(start)}
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
		if m.matrix.active {
			m.matrix.active = false
			m.showLogs = m.matrixSavedShowLogs
		}

	case agentStartedMsg:
		if msg.err != nil {
			m.failSetupStep(fmt.Errorf("failed to start agent: %w", msg.err))
			return m, nil
		}
		m.agent = msg.agent
		return m, m.waitAgentReadyCmd()

	case agentReadyMsg:
		if msg.err != nil {
			m.failSetupStep(msg.err)
			return m, nil
		}
		m.advanceSetupStep(msg.elapsed)
		return m, m.createSimulationCmd()

	case simulationCreatedMsg:
		if msg.err != nil {
			m.failSetupStep(msg.err)
			return m, nil
		}
		m.runID = msg.runID
		if m.config.mode == modeGenerateFromSource {
			m.advanceSetupStep(msg.elapsed)
			return m, m.uploadSourceCmd(msg.presigned)
		}
		m.completeSetup(msg.elapsed)
		return m, tea.Batch(m.pollSimulation(), m.waitSubprocess())

	case sourceUploadedMsg:
		if msg.err != nil {
			m.failSetupStep(msg.err)
			return m, nil
		}
		m.completeSetup(msg.elapsed)
		return m, tea.Batch(m.pollSimulation(), m.waitSubprocess())

	case simulationRunMsg:
		if msg.err == nil && msg.run != nil {
			m.run = msg.run
			if m.startTime.IsZero() && msg.run.Status == livekit.SimulationRun_STATUS_RUNNING {
				m.startTime = time.Now()
			}
			if msg.run.Status == livekit.SimulationRun_STATUS_COMPLETED ||
				msg.run.Status == livekit.SimulationRun_STATUS_FAILED ||
				msg.run.Status == livekit.SimulationRun_STATUS_CANCELLED {
				if !m.runFinished {
					m.endTime = time.Now()
				}
				m.runFinished = true
			}
		}

	case spinnerTickMsg:
		m.spinnerIdx++
		return m, spinnerTickCmd()

	case matrixTickMsg:
		if !m.matrix.active {
			return m, nil
		}
		m.matrix.step()
		if !m.matrix.active {
			m.showLogs = m.matrixSavedShowLogs
			return m, nil
		}
		return m, matrixTickCmd()

	case pollTickMsg:
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
	key := msg.String()
	if m.matrix.active {
		// Any keypress cancels rain so the user regains control immediately.
		// Pressing 'm' just cancels; every other key falls through to normal
		// handling so it can do its usual thing on the same press.
		m.matrix.active = false
		m.showLogs = m.matrixSavedShowLogs
		if key == "m" {
			return m, nil
		}
	}
	switch key {
	case "ctrl+c":
		if m.setupCancel != nil {
			m.setupCancel()
		}
		return m, tea.Quit
	case "m":
		if m.detailJobID != "" || m.run == nil || !m.setupDone || len(m.run.Jobs) == 0 || m.width < 10 {
			return m, nil
		}
		rows := m.buildMatrixRows()
		width := 0
		for _, r := range rows {
			if n := len(r.text); n > width {
				width = n
			}
		}
		if width < 1 || len(rows) < 3 {
			return m, nil
		}
		if width > m.width {
			width = m.width
		}
		// Skip columns that are always blank across every row (rain over empty
		// air looks noisy) and columns carrying a status icon (the status must
		// stay fully visible).
		skip := make([]bool, width)
		for col := 0; col < width; col++ {
			empty := true
			for _, r := range rows {
				if col < len(r.text) && r.text[col] != ' ' {
					empty = false
					break
				}
			}
			skip[col] = empty
		}
		for _, r := range rows {
			if r.iconCol >= 0 && r.iconCol < width {
				skip[r.iconCol] = true
			}
		}
		m.matrixSavedShowLogs = m.showLogs
		m.showLogs = false
		m.matrix.start(width, len(rows), skip)
		return m, matrixTickCmd()
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
		n := m.numSimulations
		if m.run != nil && m.run.GetNumSimulations() > 0 {
			n = m.run.GetNumSimulations()
		}
		b.WriteString(fmt.Sprintf("  %s Generating %d scenarios  %s %s\n", yellowStyle.Render("⏺"), n, m.spinner(), dimStyle.Render(elapsed.String())))
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
		if m.agent != nil {
			b.WriteString(m.renderLogs())
		}
	}
	return b.String()
}

func (m *simulateModel) hasLogs() bool {
	return m.agent != nil && m.agent.LogCount() > 0
}

func (m *simulateModel) spinner() string {
	return yellowStyle.Render(simSpinnerFrames[m.spinnerIdx%len(simSpinnerFrames)])
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
			elapsed := time.Since(m.stepStart).Truncate(time.Second)
			b.WriteString(fmt.Sprintf("  %s %s  %s %s\n", yellowStyle.Render("⏺"), s.label, m.spinner(), dimStyle.Render(elapsed.String())))
		case "failed":
			b.WriteString(fmt.Sprintf("  %s %s\n", redStyle.Render("✗"), s.label))
		default:
			b.WriteString(fmt.Sprintf("  %s %s\n", dimStyle.Render("–"), s.label))
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
	if m.hasLogs() {
		b.WriteString(dimStyle.Render("  Ctrl+L logs · q quit"))
	} else {
		b.WriteString(dimStyle.Render("  q quit"))
	}
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
	} else if m.matrix.active {
		b.WriteString(m.matrix.render(m.buildMatrixRows()))
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
		var d time.Duration
		if !m.endTime.IsZero() {
			d = m.endTime.Sub(m.startTime)
		} else {
			d = time.Since(m.startTime)
		}
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

// visibleWindow clamps m.cursor / m.scrollOff against the current filtered
// job list and returns the visible slice plus overflow counts.
func (m *simulateModel) visibleWindow() (jobs []indexedJob, winStart, winEnd, overflowAbove, overflowBelow int) {
	jobs = m.filteredJobs()
	if len(jobs) == 0 {
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(jobs) {
		m.cursor = len(jobs) - 1
	}
	availHeight := matrixAvailHeight(m.height)
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
	winStart = m.scrollOff
	winEnd = m.scrollOff + availHeight
	if winEnd > len(jobs) {
		winEnd = len(jobs)
	}
	overflowAbove = winStart
	overflowBelow = len(jobs) - winEnd
	return
}

func (m *simulateModel) renderJobList() string {
	jobs, winStart, winEnd, above, below := m.visibleWindow()
	if len(jobs) == 0 {
		return dimStyle.Render("  (no jobs match this filter)")
	}

	var b strings.Builder

	if above > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ... %d more above ...", above)))
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

	if below > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ... %d more below ...", below)))
		b.WriteString("\n")
	}

	return b.String()
}

// --- Matrix rain row construction ---
//
// The matrix renderer (in matrix_rain.go) consumes a []matrixRow describing
// the underlying text layer and its styled regions (status icon, dim ID range,
// cursor marker). This file provides the mapping from a simulation's job list
// into that neutral data shape.

func plainJobIcon(job *livekit.SimulationRun_Job) rune {
	switch job.Status {
	case livekit.SimulationRun_Job_STATUS_COMPLETED:
		return '✓'
	case livekit.SimulationRun_Job_STATUS_FAILED:
		return '✗'
	case livekit.SimulationRun_Job_STATUS_RUNNING:
		return '⏺'
	default:
		return '○'
	}
}

func jobIconStylePtr(job *livekit.SimulationRun_Job) *lipgloss.Style {
	switch job.Status {
	case livekit.SimulationRun_Job_STATUS_COMPLETED:
		return &greenStyle
	case livekit.SimulationRun_Job_STATUS_FAILED:
		return &redStyle
	case livekit.SimulationRun_Job_STATUS_RUNNING:
		return &yellowStyle
	default:
		return &dimStyle
	}
}

// buildMatrixRows produces one matrixRow per visible line of the job list,
// mirroring renderJobList's layout but in a form the matrix frame can consume
// cell-by-cell.
func (m *simulateModel) buildMatrixRows() []matrixRow {
	jobs, winStart, winEnd, above, below := m.visibleWindow()
	if len(jobs) == 0 {
		return []matrixRow{{text: []rune("  (no jobs match this filter)"), iconCol: -1, dimStart: -1}}
	}
	var rows []matrixRow
	if above > 0 {
		rows = append(rows, matrixRow{
			text:     []rune(fmt.Sprintf("  ... %d more above ...", above)),
			iconCol:  -1,
			dimStart: -1,
		})
	}
	for i := winStart; i < winEnd; i++ {
		ij := jobs[i]
		instr := ij.job.Instructions
		if len(instr) > 60 {
			instr = instr[:60] + "..."
		}
		if instr == "" {
			instr = "—"
		}
		iconCh := plainJobIcon(ij.job)
		line := fmt.Sprintf("  %c %3d. %s  %s", iconCh, ij.origIdx, ij.job.Id, instr)
		// Layout: "  " (2) + icon (1) + " " (1) + "%3d" (3) + ". " (2) = rune offset 9 for ID
		idLen := len([]rune(ij.job.Id))
		rows = append(rows, matrixRow{
			text:         []rune(line),
			iconCol:      2,
			iconCh:       iconCh,
			iconStyle:    jobIconStylePtr(ij.job),
			dimStart:     9,
			dimEnd:       9 + idLen,
			dimStyle:     &dimStyle,
			cursorMarker: i == m.cursor,
		})
	}
	if below > 0 {
		rows = append(rows, matrixRow{
			text:     []rune(fmt.Sprintf("  ... %d more below ...", below)),
			iconCol:  -1,
			dimStart: -1,
		})
	}
	return rows
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
	maxWidth := m.width - 4
	if maxWidth < 20 {
		maxWidth = 20
	}
	wrapStyle := lipgloss.NewStyle().Width(maxWidth)

	rawLines := m.agent.RecentLogs(logBudget * 3)
	var visualLines []string
	for _, line := range rawLines {
		wrapped := wrapStyle.Render(line)
		for _, wl := range strings.Split(wrapped, "\n") {
			visualLines = append(visualLines, wl)
		}
	}
	if len(visualLines) > logBudget {
		visualLines = visualLines[len(visualLines)-logBudget:]
	}
	for _, vl := range visualLines {
		b.WriteString("  " + vl + "\n")
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
		hint = "  ESC/q back"
		if m.hasLogs() {
			hint += " · Ctrl+L logs"
		}
	} else {
		hint = "  ↑↓/Tab navigate · ENTER detail · ←→ filter · d description"
		if m.hasLogs() {
			hint += " · Ctrl+L logs"
		}
		if m.runFinished {
			hint += " · q quit"
		}
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
		return yellowStyle.Render("⏺")
	default:
		return dimStyle.Render("○")
	}
}
