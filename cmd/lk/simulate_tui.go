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
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/livekit"
	agent "github.com/livekit/protocol/livekit/agent"
)

func runSimulateTUI(config *simulateConfig) error {
	m := newSimulateModel(config)
	// No mouse capture, so the terminal keeps native drag-to-select. Arrow keys
	// scroll (the wheel maps to them in alt-screen); i/j/k/l navigate the list.
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, runErr := p.Run()

	if m.launcher != nil {
		// A second ctrl+c during cleanup would kill the CLI and leak the worker
		// (own process group, port stays bound); escalate to SIGKILL instead.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt)
		defer signal.Stop(sigCh)
		go func() {
			<-sigCh
			m.launcher.ForceStop()
			os.Exit(130)
		}()

		if agentProc := m.launcher.Stop(); agentProc != nil {
			if m.brokenAgent {
				writeBrokenAgentNote(out.WarnWriter(), agentProc)
				fmt.Fprintln(out.WarnWriter())
			}
			if agentProc.LogPath != "" {
				out.Statusf("Agent logs: %s", agentProc.LogPath)
			}
			m.agent = agentProc
		}
	}

	// generated scenarios are saved to a temp file so they're never lost
	if config.mode == modeGenerateFromSource && m.run != nil {
		if path, err := writeGeneratedScenariosTemp(m.run); err == nil && path != "" {
			out.Statusf("Generated scenarios: %s", path)
		}
	}

	// Always leave a plain-text record of the run, like the agent log.
	if path := m.reporter.Finish(m.run, m.agent, m.brokenAgent, m.getDashboardURL()); path != "" {
		out.Statusf("Run report: %s", path)
	}

	if url := m.getDashboardURL(); url != "" {
		out.Statusf("Dashboard:  %s", url)
	}

	if m.config.mode == modeView {
		fmt.Fprintf(os.Stderr, "To re-open this simulation, run: %s\n", viewCommandHint(m.config.viewModeRunID))
	} else if m.runID != "" && !m.runFinished {
		cancelSimulationRun(config.client, m.runID)
	} else if m.runID != "" {
		fmt.Fprintf(os.Stderr, "To re-open this simulation, run: %s\n", viewCommandHint(m.runID))
	}

	if runErr != nil {
		return fmt.Errorf("TUI error: %w", runErr)
	}
	if m.err != nil && m.err != context.Canceled {
		return m.err
	}
	return nil
}

// --- Styles ---

// Color styles are functions so they read the active theme palette at render time and
// follow `lk set-theme`. The colorless styles below stay vars.
func tagStyle() lipgloss.Style {
	return lipgloss.NewStyle().Background(util.Brand()).Foreground(lipgloss.Color("0")).Bold(true).Padding(0, 1)
}
func greenStyle() lipgloss.Style  { return lipgloss.NewStyle().Foreground(util.Success()) }
func redStyle() lipgloss.Style    { return lipgloss.NewStyle().Foreground(util.Error()) }
func yellowStyle() lipgloss.Style { return lipgloss.NewStyle().Foreground(util.Warning()) }
func cyanStyle() lipgloss.Style   { return lipgloss.NewStyle().Foreground(util.Brand()).Bold(true) }

var (
	dimStyle     = lipgloss.NewStyle().Faint(true)
	boldStyle    = lipgloss.NewStyle().Bold(true)
	reverseStyle = lipgloss.NewStyle().Reverse(true)

	simSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
)

// wrapLines splits text into rows no wider than width; unknown or tiny
// widths leave the lines unwrapped.
func wrapLines(text string, width int) []string {
	if width >= 20 {
		text = lipgloss.NewStyle().Width(width).Render(text)
	}
	return strings.Split(text, "\n")
}

// writeWrappedLines appends text to b as one styled row per line, wrapped to
// the window width when it is known. Rendering line by line keeps lipgloss
// from padding the block to its widest line with trailing spaces, and
// wrapping keeps long lines (e.g. the full agent command in an error) from
// being hard-wrapped by the terminal, which breaks the inline layout.
func writeWrappedLines(b *strings.Builder, style lipgloss.Style, indent, text string, width int) {
	for _, line := range wrapLines(text, width-len(indent)-2) {
		b.WriteString(style.Render(indent+line) + "\n")
	}
}

// --- Message types ---

type simulationRunMsg struct {
	run *livekit.SimulationRun
	err error
}

type pollTickMsg struct{}
type spinnerTickMsg struct{}

type toastExpireMsg struct{ id int }

type subprocessExitMsg struct {
	err error
}

// --- Model ---

type step struct {
	label   string
	status  string // "pending", "running", "done", "failed"
	elapsed time.Duration
}

type simulateModel struct {
	config      *simulateConfig
	launcher    *agentLauncher
	reporter    *runReporter
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
	summary        *livekit.SimulationRunSummary
	runFinished    bool
	brokenAgent    bool
	numSimulations int32
	startTime      time.Time
	endTime        time.Time
	genStart       time.Time

	spinnerIdx int

	cursor          int
	detailJobID     string
	detailScrollOff int
	showLogs        bool
	logScrollOff    int
	logPinned       bool
	logPinnedTotal  int
	showDescription bool
	descScrollOff   int
	// whole-page scrolling for the main list view: the full content (header,
	// job list, summary, logs) is composed unbounded, then windowed to the
	// terminal height with the toast and hint bar pinned below.
	viewScrollOff  int
	followCursor   bool // scroll the page to keep the cursor row visible
	cursorViewLine int  // absolute line of the cursor row in the composed page
	pageOverflow   bool // last render had more lines than fit

	toast   string
	toastOK bool
	toastID int

	// quit confirmation while the run is in progress; sel 0 = keep, 1 = stop
	confirmQuit    bool
	confirmQuitSel int

	// inference-quota (429) dialog; shows at most once per run
	quotaWarning   *quotaInfo
	quotaDismissed bool
	quotaSuggested int
	peakRunning    int

	saving    bool
	saveInput textinput.Model
	saveErr   string

	matrix              matrixRain
	matrixSavedShowLogs bool

	width  int
	height int
	err    error
}

func (m *simulateModel) hasDescription() bool {
	return m.run != nil && m.run.AgentDescription != ""
}

func (m *simulateModel) descriptionExpanded() bool {
	return m.detailJobID == "" && m.showDescription && m.hasDescription()
}

func (m *simulateModel) quotaModalActive() bool {
	return m.quotaWarning != nil && !m.quotaDismissed
}

// A run that started from a scenarios.yaml has nothing new to export.
func (m *simulateModel) canExportScenarios() bool {
	return m.config != nil && m.config.mode == modeGenerateFromSource &&
		m.run.GetScenarioGroup() != nil && len(m.run.GetScenarioGroup().GetScenarios()) > 0
}

func (m *simulateModel) handleSaveKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := strings.TrimSpace(m.saveInput.Value())
		if name == "" {
			m.saveErr = "enter a file name"
			return m, nil
		}
		status, ok := m.saveScenarios(name)
		if !ok {
			m.saveErr = status
			return m, nil
		}
		m.saving = false
		m.saveInput.Blur()
		return m, m.showToast(status, true)
	case "esc", "ctrl+c":
		m.saving = false
		m.saveErr = ""
		m.saveInput.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.saveInput, cmd = m.saveInput.Update(msg)
	return m, cmd
}

// saveScenarios writes to projectDir/name, never overwriting (ok=false on conflict).
func (m *simulateModel) saveScenarios(name string) (string, bool) {
	group := m.run.GetScenarioGroup()
	out, err := scenarioGroupToYAML(group)
	if err != nil {
		return "save failed: " + err.Error(), false
	}
	if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
		name += ".yaml"
	}
	path := filepath.Join(m.config.projectDir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if os.IsExist(err) {
		return name + " already exists, pick another name", false
	}
	if err != nil {
		return "save failed: " + err.Error(), false
	}
	_, werr := f.Write(out)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return "save failed: " + werr.Error(), false
	}
	return fmt.Sprintf("Saved %d scenarios to %s", len(group.GetScenarios()), name), true
}

// copyScenario puts the job's scenario on the clipboard as a one-entry scenarios.yaml.
func (m *simulateModel) copyScenario(jobID string) (string, bool) {
	job := m.findJob(jobID)
	if job == nil {
		return "Copy failed: scenario not found", false
	}
	group := &livekit.ScenarioGroup{
		Scenarios: []*livekit.Scenario{{
			Label:             job.GetLabel(),
			Instructions:      job.GetInstructions(),
			AgentExpectations: job.GetAgentExpectations(),
		}},
	}
	out, err := scenarioGroupToYAML(group)
	if err != nil {
		return "Copy failed: " + err.Error(), false
	}
	if err := clipboard.WriteAll(string(out)); err != nil {
		return "Copy failed: " + err.Error(), false
	}
	return "Scenario copied to clipboard as scenarios.yaml", true
}

// The toast id keeps an old expiry tick from clearing a newer toast.
func (m *simulateModel) showToast(text string, ok bool) tea.Cmd {
	m.toast = text
	m.toastOK = ok
	m.toastID++
	id := m.toastID
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return toastExpireMsg{id: id} })
}

func newSimulateModel(config *simulateConfig) *simulateModel {
	ti := textinput.New()
	ti.Placeholder = "scenarios.yaml"
	ti.CharLimit = 128
	ti.Prompt = ""
	return &simulateModel{
		config:         config,
		reporter:       newRunReporter(),
		numSimulations: config.numSimulations,
		width:          80,
		height:         24,
		saveInput:      ti,
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

	if c.mode == modeView {
		ctx, cancel := context.WithTimeout(context.Background(), simulationAPITimeout)
		defer cancel()
		run, err := getSimulationRun(ctx, m.config.client, m.config.viewModeRunID)
		if err != nil {
			m.err = err
		}
		m.run = run
		m.summary = decodeRunSummary(run)
		m.setupDone = true
		return nil
	}

	m.steps = nil
	if c.mode == modeScenarios && c.scenarioGroup != nil {
		// loaded before the TUI started, so it renders as already done
		noun := "scenarios"
		if len(c.scenarioGroup.GetScenarios()) == 1 {
			noun = "scenario"
		}
		label := fmt.Sprintf("Loaded %d %s from %s", len(c.scenarioGroup.GetScenarios()), noun, c.scenariosPath)
		if name := c.scenarioGroup.GetName(); name != "" {
			label += fmt.Sprintf(" (%s)", name)
		}
		m.steps = append(m.steps, step{label: label, status: "done"})
	}
	m.currentStep = len(m.steps)

	m.reporter.BeginSetup()
	for _, w := range c.warnings {
		m.reporter.ConfigWarning(w)
	}
	if c.mode == modeScenarios && c.scenarioGroup != nil {
		m.reporter.ScenariosLoaded(c.scenarioGroup, c.scenariosPath)
	}

	ctx, cancel := context.WithCancel(c.ctx)
	m.setupCtx = ctx
	m.setupCancel = cancel
	m.stepStart = time.Now()

	if c.liveAgent {
		m.steps = append(m.steps, step{label: "Creating simulation", status: "running"})
		return m.createSimulationCmd()
	}

	m.steps = append(m.steps,
		step{label: "Starting agent", status: "running"},
		step{label: "Creating simulation", status: "pending"},
	)
	if c.mode == modeGenerateFromSource {
		m.steps = append(m.steps, step{label: "Uploading source", status: "pending"})
	}
	m.reporter.StartingAgent()

	m.launcher = launchSimulationAgent(c)
	return m.startAgentCmd()
}

func (m *simulateModel) failSetupStep(err error) {
	m.steps[m.currentStep].status = "failed"
	m.reporter.SetupFailed(err)
	m.reporter.EndSetup()
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
	m.reporter.EndSetup()
	m.reporter.RunCreated(m.runID, m.getDashboardURL())
}

func (m *simulateModel) startAgentCmd() tea.Cmd {
	l := m.launcher
	return func() tea.Msg {
		agent, err := l.Wait()
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
		case <-m.agent.Done():
			return agentReadyMsg{err: fmt.Errorf("the agent exited before registering.\n\nCommand used to start agent: %s\n\n%s", m.agent.cmd.String(), agentExitDetail(m.agent))}
		case <-timeout.C:
			m.agent.Kill()
			return agentReadyMsg{err: fmt.Errorf("timed out after %s waiting for the agent to register.\n\n%s", agentRegisterTimeout, agentExitDetail(m.agent))}
		case <-m.setupCtx.Done():
			return agentReadyMsg{err: m.setupCtx.Err()}
		}
	}
}

func (m *simulateModel) createSimulationCmd() tea.Cmd {
	return func() tea.Msg {
		start := time.Now()
		runID, presigned, err := createSimulationRun(m.setupCtx, m.config)
		return simulationCreatedMsg{runID: runID, presigned: presigned, elapsed: time.Since(start), err: err}
	}
}

func (m *simulateModel) uploadSourceCmd(presigned *livekit.PresignedPostRequest) tea.Cmd {
	c := m.config
	return func() tea.Msg {
		start := time.Now()
		err := uploadSource(m.setupCtx, c.client, m.runID, presigned, c.projectDir, c.entrypoint)
		return sourceUploadedMsg{elapsed: time.Since(start), err: err}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(simulationPollInterval, func(t time.Time) tea.Msg {
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
		ctx, cancel := context.WithTimeout(context.Background(), simulationAPITimeout)
		defer cancel()
		run, err := getSimulationRun(ctx, m.config.client, m.runID)
		return simulationRunMsg{run: run, err: err}
	}
}

func (m *simulateModel) cancelRunCmd() tea.Cmd {
	client, runID := m.config.client, m.runID
	return func() tea.Msg {
		cancelSimulationRun(client, runID)
		return nil
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
		m.reporter.WaitingForRegister()
		return m, m.waitAgentReadyCmd()

	case agentReadyMsg:
		if msg.err != nil {
			m.failSetupStep(msg.err)
			if m.setupCancel != nil {
				m.setupCancel()
			}
			return m, tea.Quit
		}
		m.reporter.AgentRegistered(msg.elapsed)
		m.advanceSetupStep(msg.elapsed)
		return m, m.createSimulationCmd()

	case simulationCreatedMsg:
		if msg.err != nil {
			m.failSetupStep(msg.err)
			return m, nil
		}
		m.runID = msg.runID
		m.reporter.SimulationCreated(msg.elapsed)
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
		m.reporter.SourceUploaded(msg.elapsed)
		m.completeSetup(msg.elapsed)
		return m, tea.Batch(m.pollSimulation(), m.waitSubprocess())

	case simulationRunMsg:
		if msg.err == nil && msg.run != nil {
			m.run = msg.run
			m.summary = decodeRunSummary(msg.run)
			m.reporter.RunUpdate(msg.run, m.config.numSimulations)
			if m.startTime.IsZero() && msg.run.Status == livekit.SimulationRun_STATUS_RUNNING {
				m.startTime = time.Now()
			}
			if running := runningJobCount(msg.run); running > m.peakRunning {
				m.peakRunning = running
			}
			if m.quotaWarning == nil && !m.quotaDismissed && m.agent != nil {
				if info := detectQuotaExceeded(m.agent.RecentLogs(0)); info != nil {
					m.quotaWarning = info
					m.quotaSuggested = suggestConcurrency(m.config.concurrency, m.peakRunning)
					m.reporter.QuotaExceeded(info.describe(), m.quotaSuggested)
				}
			}
			// the worker is failing systemically: cancel the run and surface
			// its log on exit
			if !m.brokenAgent && agentBroken(msg.run, m.agent) {
				m.brokenAgent = true
				m.showLogs = true
				m.reporter.BrokenAgent()
				return m, m.cancelRunCmd()
			}
			if isTerminalRunStatus(msg.run.Status) {
				if !m.runFinished {
					m.endTime = time.Now()
				}
				m.runFinished = true
			}
		}

	case spinnerTickMsg:
		m.spinnerIdx++
		return m, spinnerTickCmd()

	case toastExpireMsg:
		if msg.id == m.toastID {
			m.toast = ""
		}

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
		// the TUI stays up; the exit is surfaced via agentBroken / on quit

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

const pageScroll = 20

// scrollActive scrolls the focused pane by delta lines (positive toward the
// bottom); false if nothing is focused so the caller falls back to the list.
func (m *simulateModel) scrollActive(delta int, includeLogs bool) bool {
	switch {
	case m.detailJobID != "":
		m.detailScrollOff += delta
		if m.detailScrollOff < 0 {
			m.detailScrollOff = 0
		}
	case m.descriptionExpanded():
		m.descScrollOff += delta
		if m.descScrollOff < 0 {
			m.descScrollOff = 0
		}
	case includeLogs && m.showLogs:
		// logScrollOff counts up from the bottom; scrolling down decreases it
		m.logScrollOff -= delta
		if m.logScrollOff <= 0 {
			m.logScrollOff = 0
			m.logPinned = false
		} else {
			m.logPinned = true
		}
	default:
		return false
	}
	return true
}

// scrollBy scrolls the focused pane by delta lines (one arrow/wheel step),
// falling back to scrolling the whole page.
func (m *simulateModel) scrollBy(delta int) {
	if m.scrollActive(delta, true) {
		return
	}
	m.viewScrollOff += delta // clamped on render
	if m.viewScrollOff < 0 {
		m.viewScrollOff = 0
	}
}

func (m *simulateModel) moveCursor(delta int) {
	if n := len(m.filteredJobs()); n > 0 {
		m.cursor = ((m.cursor+delta)%n + n) % n
		m.followCursor = true
	}
}

func (m *simulateModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.matrix.active {
		// any key cancels rain; all but 'm' fall through to normal handling
		m.matrix.active = false
		m.showLogs = m.matrixSavedShowLogs
		if key == "m" {
			return m, nil
		}
	}
	if m.saving {
		return m.handleSaveKey(msg)
	}
	if m.confirmQuit {
		switch key {
		case "left", "right", "tab", "shift+tab", "up", "down":
			m.confirmQuitSel = 1 - m.confirmQuitSel
		case "enter":
			if m.confirmQuitSel == 1 {
				if m.setupCancel != nil {
					m.setupCancel()
				}
				return m, tea.Quit
			}
			m.confirmQuit = false
		case "y":
			if m.setupCancel != nil {
				m.setupCancel()
			}
			return m, tea.Quit
		case "esc", "q", "n":
			m.confirmQuit = false
		case "ctrl+c":
			return m, tea.Quit
		}
		return m, nil
	}
	if m.quotaModalActive() {
		switch key {
		case "enter", "esc", "q", " ":
			m.quotaDismissed = true
		case "ctrl+c":
			if m.setupCancel != nil {
				m.setupCancel()
			}
			return m, tea.Quit
		}
		return m, nil
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
		// skip always-blank columns (rain over empty air looks noisy) and
		// status-icon columns (the status must stay visible)
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
		m.logScrollOff = 0
		m.logPinned = false
	case "d":
		if m.detailJobID == "" && m.hasDescription() {
			m.showDescription = !m.showDescription
			m.descScrollOff = 0
		}
	case "s":
		if m.canExportScenarios() && m.detailJobID == "" {
			m.saving = true
			m.saveErr = ""
			m.toast = ""
			m.saveInput.SetValue("scenarios.yaml")
			m.saveInput.CursorEnd()
			return m, m.saveInput.Focus()
		}
	case "c":
		if m.detailJobID != "" {
			text, ok := m.copyScenario(m.detailJobID)
			return m, m.showToast(text, ok)
		}
	case "up", "down":
		// arrows scroll (the mouse wheel maps here in alt-screen): the focused
		// pane if any, otherwise the whole page. i/k navigate the list.
		delta := -1
		if key == "down" {
			delta = 1
		}
		m.scrollBy(delta)
	case "i", "shift+tab":
		// in a detail/description pane there is no cursor, so fall back to scroll
		if !m.scrollActive(-1, false) {
			m.moveCursor(-1)
		}
	case "k", "tab":
		if !m.scrollActive(1, false) {
			m.moveCursor(1)
		}
	case "pgup":
		if !m.scrollActive(-pageScroll, true) {
			m.viewScrollOff -= pageScroll
			if m.viewScrollOff < 0 {
				m.viewScrollOff = 0
			}
		}
	case "pgdown":
		if !m.scrollActive(pageScroll, true) {
			m.viewScrollOff += pageScroll // clamped on render
		}
	case "enter", "l":
		if m.detailJobID == "" {
			jobs := m.filteredJobs()
			if m.cursor >= 0 && m.cursor < len(jobs) {
				m.detailJobID = jobs[m.cursor].job.Id
				m.detailScrollOff = 0
			}
		}
	case "esc", "j", "backspace":
		if m.detailJobID != "" {
			m.detailJobID = ""
			m.detailScrollOff = 0
		} else if m.showDescription {
			m.showDescription = false
			m.descScrollOff = 0
		}
	case "q":
		switch {
		case m.detailJobID != "":
			m.detailJobID = ""
			m.detailScrollOff = 0
		case m.showDescription:
			m.showDescription = false
			m.descScrollOff = 0
		case m.runID != "" && !m.runFinished:
			m.confirmQuit = true
			m.confirmQuitSel = 0
		default:
			if m.setupCancel != nil {
				m.setupCancel()
			}
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
	// sort by job ID: the backend's ordering shuffles rows as statuses change
	jobs := make([]*livekit.SimulationRun_Job, len(m.run.Jobs))
	copy(jobs, m.run.Jobs)
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].GetId() < jobs[j].GetId() })

	result := make([]indexedJob, 0, len(jobs))
	for i, j := range jobs {
		result = append(result, indexedJob{origIdx: i + 1, job: j})
	}
	return result
}

func (m *simulateModel) findJob(id string) *livekit.SimulationRun_Job {
	if m.run == nil {
		return nil
	}
	for _, j := range m.run.Jobs {
		if j.Id == id {
			return j
		}
	}
	return nil
}

func (m *simulateModel) View() string {
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
	b.WriteString(tagStyle().Render("Agent Simulation"))
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

	for _, w := range m.config.warnings {
		writeWrappedLines(&b, yellowStyle(), "  ", "⚠ "+w, m.width)
	}

	// in file mode the scenarios are already known, nothing is generated
	if m.setupDone && m.err == nil && m.config.mode == modeGenerateFromSource {
		elapsed := time.Since(m.genStart).Truncate(time.Second)
		n := m.numSimulations
		if m.run != nil && m.run.GetNumSimulations() > 0 {
			n = m.run.GetNumSimulations()
		}
		fmt.Fprintf(&b, "  %s Generating %d scenarios  %s %s\n", yellowStyle().Render("⏺"), n, m.spinner(), dimStyle.Render(elapsed.String()))
	}

	if m.err != nil {
		b.WriteString("\n")
		writeWrappedLines(&b, redStyle(), "  ", m.err.Error(), m.width)
		if m.agent != nil {
			b.WriteString("\n")
			b.WriteString(m.renderLogs(""))
		}
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(""))
		b.WriteString("\n")
	} else {
		b.WriteString("\n")
		if m.agent != nil {
			b.WriteString(m.renderLogs(""))
		}
	}
	if m.confirmQuit {
		b.WriteString("\n")
		b.WriteString(m.renderQuitConfirm())
		b.WriteString("\n")
	}
	return b.String()
}

func (m *simulateModel) hasLogs() bool {
	return m.agent != nil && m.agent.LogCount() > 0
}

func (m *simulateModel) spinner() string {
	return yellowStyle().Render(simSpinnerFrames[m.spinnerIdx%len(simSpinnerFrames)])
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
			fmt.Fprintf(&b, "  %s %s%s\n", greenStyle().Render("✓"), s.label, elapsed)
		case "running":
			elapsed := time.Since(m.stepStart).Truncate(time.Second)
			fmt.Fprintf(&b, "  %s %s  %s %s\n", yellowStyle().Render("⏺"), s.label, m.spinner(), dimStyle.Render(elapsed.String()))
		case "failed":
			fmt.Fprintf(&b, "  %s %s\n", redStyle().Render("✗"), s.label)
		default:
			fmt.Fprintf(&b, "  %s %s\n", dimStyle.Render("–"), s.label)
		}
	}
	return b.String()
}

func (m *simulateModel) projectID() string {
	if m.run != nil && m.run.GetProjectId() != "" {
		return m.run.GetProjectId()
	}
	if m.config != nil && m.config.pc != nil {
		return m.config.pc.ProjectId
	}
	return ""
}

func (m *simulateModel) getDashboardURL() string {
	return simulationDashboardURL(m.projectID(), m.runID)
}

func (m *simulateModel) viewFailed() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(tagStyle().Render("Agent Simulation"))
	b.WriteString("  ")
	b.WriteString(dimStyle.Render(m.runID))
	if url := m.getDashboardURL(); url != "" {
		b.WriteString("  " + dimStyle.Render(url))
	}
	b.WriteString("\n\n")
	b.WriteString("  " + redStyle().Bold(true).Render("Failed") + "\n\n")
	if m.run.Error != "" {
		writeWrappedLines(&b, redStyle(), "  ", m.run.Error, m.width)
	} else {
		b.WriteString(redStyle().Render("  (no error details available)") + "\n")
	}
	b.WriteString("\n")
	if m.showLogs {
		b.WriteString(m.renderLogs(""))
	}
	if m.hasLogs() {
		b.WriteString(dimStyle.Render("  Ctrl+L logs "))
	} else {
		b.WriteString(dimStyle.Render(""))
	}
	b.WriteString("\n")
	return b.String()
}

func (m *simulateModel) viewRunning() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(tagStyle().Render("Agent Simulation"))
	b.WriteString("  ")
	b.WriteString(dimStyle.Render(m.runID))
	if url := m.getDashboardURL(); url != "" {
		b.WriteString("  " + dimStyle.Render(url))
	}
	b.WriteString("\n\n")

	if m.detailJobID == "" && m.hasDescription() {
		b.WriteString(boldStyle.Render("  Agent Description") + "\n")
		if m.showDescription {
			// bounded window so expanding never pushes the list off-screen
			wrapped := dimStyle.Width(m.width - 4).Render(m.run.AgentDescription)
			lines := strings.Split(wrapped, "\n")
			const descBudget = 8
			if len(lines) <= descBudget {
				m.descScrollOff = 0
			} else if maxScroll := len(lines) - descBudget; m.descScrollOff > maxScroll {
				m.descScrollOff = maxScroll
			}
			start := m.descScrollOff
			end := start + descBudget
			if end > len(lines) {
				end = len(lines)
			}
			if start > 0 {
				b.WriteString(dimStyle.Render(fmt.Sprintf("  ↑ %d more", start)) + "\n")
			}
			for _, line := range lines[start:end] {
				b.WriteString("  " + line + "\n")
			}
			if end < len(lines) {
				b.WriteString(dimStyle.Render(fmt.Sprintf("  ↓ %d more", len(lines)-end)) + "\n")
			}
			b.WriteString("\n")
		} else {
			desc := firstMeaningfulLine(m.run.AgentDescription)
			if desc != "" {
				b.WriteString(dimStyle.Width(m.width-4).Render("  "+desc) + "\n")
				b.WriteString(dimStyle.Render("  (press d to expand)") + "\n\n")
			}
		}
	}

	b.WriteString(m.renderHeader())
	b.WriteString("\n")

	b.WriteString(m.renderCounts())
	b.WriteString("\n")

	b.WriteString("\n")

	if m.detailJobID != "" {
		b.WriteString(m.scrolledDetail())
	} else if m.matrix.active {
		b.WriteString(m.matrix.render(m.buildMatrixRows()))
	} else {
		// the job list renders in full; pageWindow scrolls the whole view.
		// track the cursor row's absolute line so the page can follow it.
		m.cursorViewLine = strings.Count(b.String(), "\n") + m.cursor
		b.WriteString(m.renderJobList())

		if m.run.Status == livekit.SimulationRun_STATUS_SUMMARIZING {
			fmt.Fprintf(&b, "\n  %s %s  %s\n", yellowStyle().Render("⏺"), yellowStyle().Render("Generating summary..."), m.spinner())
		} else if m.summary != nil {
			b.WriteString(m.renderSummary())
		} else if isTerminalRunStatus(m.run.Status) {
			msg := "The summary for this run is not available"
			if m.run.Error != "" {
				msg = m.run.Error
			}
			fmt.Fprintf(&b, "\n  %s %s\n", yellowStyle().Render("⚠"), yellowStyle().Render(msg))
		}
	}

	b.WriteString("\n")
	if m.showLogs && m.detailJobID == "" {
		b.WriteString(m.renderLogs(""))
	}
	content := b.String()
	if m.detailJobID == "" && !m.matrix.active {
		content = m.pageWindow(content)
	}
	return content + m.renderToast() + m.renderHint() + "\n"
}

// pageWindow clamps the composed page to the terminal height, showing a
// viewScrollOff-positioned window with overflow markers. Without this a long
// summary or job list pushes the header and the top of the list off-screen on
// short terminals. The toast and hint bar are appended after windowing so
// they stay pinned at the bottom.
func (m *simulateModel) pageWindow(content string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	reserved := 2 // hint bar + trailing newline
	if m.toast != "" {
		reserved += strings.Count(m.renderToast(), "\n")
	}
	budget := m.height - reserved
	if budget < 5 {
		budget = 5
	}
	if len(lines) <= budget {
		m.viewScrollOff = 0
		m.pageOverflow = false
		return content
	}
	m.pageOverflow = true

	window := budget - 2 // top and bottom marker rows
	maxScroll := len(lines) - window
	if m.followCursor {
		if m.cursorViewLine < m.viewScrollOff {
			m.viewScrollOff = m.cursorViewLine
		} else if m.cursorViewLine >= m.viewScrollOff+window {
			m.viewScrollOff = m.cursorViewLine - window + 1
		}
		m.followCursor = false
	}
	if m.viewScrollOff > maxScroll {
		m.viewScrollOff = maxScroll
	}
	if m.viewScrollOff < 0 {
		m.viewScrollOff = 0
	}

	start := m.viewScrollOff
	end := start + window
	var b strings.Builder
	if start > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ↑ %d more lines", start)))
	}
	b.WriteString("\n")
	b.WriteString(strings.Join(lines[start:end], "\n"))
	b.WriteString("\n")
	if rem := len(lines) - end; rem > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ↓ %d more lines", rem)))
	}
	b.WriteString("\n")
	return b.String()
}

func (m *simulateModel) renderHeader() string {
	var label, style string
	switch {
	case isTerminalRunStatus(m.run.Status):
		total, done, _, _ := simulationJobCounts(m.run)
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
		}
	case m.run.Status == livekit.SimulationRun_STATUS_SUMMARIZING:
		label = "Summarizing..."
		style = "yellow"
	default:
		label = "Running"
		style = "yellow"
	}

	header := boldStyle.Render("Simulation") + " · "
	switch style {
	case "green":
		header += greenStyle().Bold(true).Render(label)
	case "red":
		header += redStyle().Bold(true).Render(label)
	case "yellow":
		header += yellowStyle().Bold(true).Render(label)
	}
	return "  " + header
}

func (m *simulateModel) renderCounts() string {
	total, done, passed, failed := simulationJobCounts(m.run)
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
		parts = append(parts, greenStyle().Render(fmt.Sprintf("%d passed", passed)))
	}
	if failed > 0 {
		parts = append(parts, redStyle().Render(fmt.Sprintf("%d failed", failed)))
	}
	if running > 0 {
		parts = append(parts, yellowStyle().Render(fmt.Sprintf("%d running", running)))
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

// visibleWindow clamps m.cursor against the current filtered job list. The
// list renders in full — pageWindow scrolls the whole view — so there is no
// internal windowing and the overflow counts are always zero.
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
	return jobs, 0, len(jobs), 0, 0
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

	// Compute labels and max width for consistent hover highlight
	type rowData struct {
		ij    indexedJob
		label string
	}
	rows := make([]rowData, 0, winEnd-winStart)
	maxWidth := 0
	for i := winStart; i < winEnd; i++ {
		ij := jobs[i]
		row := rowData{ij: ij, label: jobLabel(ij.job)}
		rows = append(rows, row)
		w := lipgloss.Width(fmt.Sprintf("  ⏺ %3d. %s %s", ij.origIdx, ij.job.Id, row.label))
		if w > maxWidth {
			maxWidth = w
		}
	}

	for i, row := range rows {
		idx := winStart + i
		pr, _ := jobStatusIcon(row.ij.job)
		plainIcon := string(pr)
		var line string
		if idx == m.cursor {
			line = fmt.Sprintf("  %s %3d. %s %s", plainIcon, row.ij.origIdx, row.ij.job.Id, row.label)
			if pad := maxWidth - lipgloss.Width(line); pad > 0 {
				line += strings.Repeat(" ", pad)
			}
			line = reverseStyle.Render(line)
		} else {
			icon := jobIcon(row.ij.job)
			line = fmt.Sprintf("  %s %3d. %s %s", icon, row.ij.origIdx, dimStyle.Render(row.ij.job.Id), row.label)
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

// --- Matrix rain row construction (the renderer lives in matrix_rain.go) ---

func jobLabel(job *livekit.SimulationRun_Job) string {
	label := job.Label
	if label == "" {
		label = job.Instructions
		if len(label) > 60 {
			label = label[:60] + "..."
		}
	}
	if label == "" {
		label = "—"
	}
	return label
}

func jobStatusIcon(job *livekit.SimulationRun_Job) (rune, *lipgloss.Style) {
	var s lipgloss.Style
	switch job.Status {
	case livekit.SimulationRun_Job_STATUS_COMPLETED:
		s = greenStyle()
		return '✓', &s
	case livekit.SimulationRun_Job_STATUS_FAILED:
		s = redStyle()
		return '✗', &s
	case livekit.SimulationRun_Job_STATUS_RUNNING:
		s = yellowStyle()
		return '⏺', &s
	default:
		return '⏺', &dimStyle
	}
}

func (m *simulateModel) buildMatrixRows() []matrixRow {
	jobs, winStart, winEnd, above, below := m.visibleWindow()
	if len(jobs) == 0 {
		return []matrixRow{{text: []rune("  (no jobs)"), iconCol: -1}}
	}
	var rows []matrixRow
	if above > 0 {
		rows = append(rows, matrixRow{
			text:    []rune(fmt.Sprintf("  ... %d more above ...", above)),
			iconCol: -1,
		})
	}
	for i := winStart; i < winEnd; i++ {
		ij := jobs[i]
		label := jobLabel(ij.job)
		iconCh, iconStyle := jobStatusIcon(ij.job)
		line := fmt.Sprintf("  %c %3d. %s %s", iconCh, ij.origIdx, ij.job.Id, label)
		rows = append(rows, matrixRow{
			text:         []rune(line),
			iconCol:      2,
			iconCh:       iconCh,
			iconStyle:    iconStyle,
			cursorMarker: i == m.cursor,
		})
	}
	if below > 0 {
		rows = append(rows, matrixRow{
			text:    []rune(fmt.Sprintf("  ... %d more below ...", below)),
			iconCol: -1,
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
	fmt.Fprintf(&b, "  %s %s %s\n",
		jobIcon(job),
		boldStyle.Render(fmt.Sprintf("Job %d", origIdx)),
		dimStyle.Render(job.Id),
	)
	if url := simulationJobDashboardURL(m.projectID(), m.runID, job.Id); url != "" {
		b.WriteString("  " + dimStyle.Render(url) + "\n")
	}
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
	for line := range strings.SplitSeq(wrapStyle.Render(instr), "\n") {
		b.WriteString("    " + line + "\n")
	}
	b.WriteString("\n")

	b.WriteString(dimStyle.Bold(true).Render("  Expected:"))
	b.WriteString("\n")
	expect := job.AgentExpectations
	if expect == "" {
		expect = "—"
	}
	for line := range strings.SplitSeq(wrapStyle.Render(expect), "\n") {
		b.WriteString(dimStyle.Render("    "+line) + "\n")
	}

	if job.Error != "" {
		b.WriteString("\n")
		if job.Status == livekit.SimulationRun_Job_STATUS_COMPLETED {
			b.WriteString(greenStyle().Bold(true).Render("  Result:"))
			b.WriteString("\n")
			for line := range strings.SplitSeq(wrapStyle.Render(job.Error), "\n") {
				b.WriteString(greenStyle().Render("    "+line) + "\n")
			}
		} else {
			b.WriteString(redStyle().Bold(true).Render("  Error:"))
			b.WriteString("\n")
			for line := range strings.SplitSeq(wrapStyle.Render(job.Error), "\n") {
				b.WriteString(redStyle().Render("    "+line) + "\n")
			}
		}
	}

	b.WriteString(m.renderChatTranscript(job.Id))

	if m.showLogs && m.agent != nil {
		roomFilter := ""
		if job.RoomName != "" {
			roomFilter = job.RoomName
		}
		var rawLines []string
		if roomFilter != "" {
			rawLines = m.agent.RecentRoomLogsByPrefix(0, roomFilter)
		} else {
			rawLines = m.agent.RecentLogs(0)
		}
		if len(rawLines) > 0 {
			b.WriteString("\n")
			b.WriteString("\n")
			b.WriteString(boldStyle.Render("  Logs:"))
			b.WriteString("\n")
			maxWidth := m.width - 4
			if maxWidth < 20 {
				maxWidth = 20
			}
			wrapLogStyle := lipgloss.NewStyle().Width(maxWidth)
			for _, line := range rawLines {
				wrapped := wrapLogStyle.Render(line)
				for wl := range strings.SplitSeq(wrapped, "\n") {
					b.WriteString("  " + wl + "\n")
				}
			}
		}
	}

	return b.String()
}

func (m *simulateModel) scrolledDetail() string {
	content := m.renderDetail()
	lines := strings.Split(content, "\n")
	budget := m.height - 12
	if budget < 5 {
		budget = 5
	}
	if len(lines) <= budget {
		m.detailScrollOff = 0
		return content
	}

	maxScroll := len(lines) - budget
	if m.detailScrollOff > maxScroll {
		m.detailScrollOff = maxScroll
	}
	if m.detailScrollOff < 0 {
		m.detailScrollOff = 0
	}

	start := m.detailScrollOff
	end := start + budget
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder
	if start > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ↑ %d more lines above", start)))
		b.WriteString("\n")
	}
	b.WriteString(strings.Join(lines[start:end], "\n"))
	b.WriteString("\n")
	if end < len(lines) {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ↓ %d more lines below", len(lines)-end)))
		b.WriteString("\n")
	}
	return b.String()
}

func (m *simulateModel) renderSummary() string {
	summary := m.summary
	if summary == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  " + boldStyle.Render("Summary"))
	fmt.Fprintf(&b, "  %s  %s\n\n",
		greenStyle().Render(fmt.Sprintf("%d passed", summary.Passed)),
		redStyle().Render(fmt.Sprintf("%d failed", summary.Failed)),
	)

	wrapWidth := m.width - 6
	if wrapWidth < 40 {
		wrapWidth = 40
	}

	if summary.GoingWell != "" {
		b.WriteString(greenStyle().Bold(true).Render("  Going well:"))
		b.WriteString("\n")
		wrapped := lipgloss.NewStyle().Width(wrapWidth).Render(summary.GoingWell)
		for line := range strings.SplitSeq(wrapped, "\n") {
			b.WriteString("    " + line + "\n")
		}
		b.WriteString("\n")
	}

	if summary.ToImprove != "" {
		b.WriteString(yellowStyle().Bold(true).Render("  To improve:"))
		b.WriteString("\n")
		wrapped := lipgloss.NewStyle().Width(wrapWidth).Render(summary.ToImprove)
		for line := range strings.SplitSeq(wrapped, "\n") {
			b.WriteString("    " + line + "\n")
		}
		b.WriteString("\n")
	}

	if len(summary.Issues) > 0 {
		b.WriteString(redStyle().Bold(true).Render("  Issues:"))
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
				for line := range strings.SplitSeq(sugWrapped, "\n") {
					b.WriteString(dimStyle.Render(strings.Repeat(" ", len(prefix))+line) + "\n")
				}
			}
		}
		b.WriteString("\n")
	}

	return b.String()
}

func (m *simulateModel) renderChatTranscript(jobID string) string {
	if m.summary == nil || m.summary.ChatHistory == nil {
		return ""
	}
	chatCtx, ok := m.summary.ChatHistory[jobID]
	if !ok || chatCtx == nil || len(chatCtx.Items) == 0 {
		return ""
	}

	userStyle := lipgloss.NewStyle().Foreground(util.Brand()).Bold(true)
	agentStyle := lipgloss.NewStyle().Foreground(util.Success()).Bold(true)

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(boldStyle.Render("  Transcript:"))
	b.WriteString("\n")

	wrapWidth := m.width - 8
	if wrapWidth < 40 {
		wrapWidth = 40
	}
	wrapStyle := lipgloss.NewStyle().Width(wrapWidth)

	for _, item := range chatCtx.Items {
		switch v := item.Item.(type) {
		case *agent.ChatContext_ChatItem_Message:
			msg := v.Message
			text := chatMessageText(msg)
			if text == "" {
				continue
			}
			b.WriteString("\n")
			switch msg.Role {
			case agent.ChatRole_USER:
				fmt.Fprintf(&b, "    %s\n", userStyle.Render("You"))
			case agent.ChatRole_ASSISTANT:
				fmt.Fprintf(&b, "    %s\n", agentStyle.Render("Agent"))
			default:
				fmt.Fprintf(&b, "    %s\n", dimStyle.Render(string(msg.Role)))
			}
			for line := range strings.SplitSeq(wrapStyle.Render(text), "\n") {
				b.WriteString("      " + line + "\n")
			}
		case *agent.ChatContext_ChatItem_FunctionCall:
			fc := v.FunctionCall
			args := fc.Arguments
			if len(args) > 80 {
				args = args[:80] + "..."
			}
			b.WriteString(dimStyle.Render(fmt.Sprintf("      ƒ %s(%s)", fc.Name, args)))
			b.WriteString("\n")
		case *agent.ChatContext_ChatItem_FunctionCallOutput:
			fco := v.FunctionCallOutput
			output := strings.TrimSpace(fco.Output)
			if output == "" {
				continue
			}
			if len(output) > 80 {
				output = output[:80] + "..."
			}
			b.WriteString(dimStyle.Render(fmt.Sprintf("      → %s", output)))
			b.WriteString("\n")
		case *agent.ChatContext_ChatItem_AgentHandoff:
			h := v.AgentHandoff
			old := ""
			if h.OldAgentId != nil && *h.OldAgentId != "" {
				old = *h.OldAgentId + " → "
			}
			b.WriteString(dimStyle.Render(fmt.Sprintf("      ⤳ %s%s", old, h.NewAgentId)))
			b.WriteString("\n")
		}
	}
	return b.String()
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
	return strings.Join(parts, "")
}

func (m *simulateModel) renderLogs(roomName string) string {
	if m.agent == nil {
		return ""
	}
	var b strings.Builder
	logBudget := m.height / 3
	if logBudget < 3 {
		logBudget = 3
	}
	maxWidth := m.width - 4
	if maxWidth < 20 {
		maxWidth = 20
	}
	wrapStyle := lipgloss.NewStyle().Width(maxWidth)

	var rawLines []string
	if roomName != "" {
		rawLines = m.agent.RecentRoomLogsByPrefix(0, roomName)
	} else {
		rawLines = m.agent.RecentLogs(0)
	}
	var visualLines []string
	for _, line := range rawLines {
		wrapped := wrapStyle.Render(line)
		for wl := range strings.SplitSeq(wrapped, "\n") {
			visualLines = append(visualLines, wl)
		}
	}

	total := len(visualLines)
	if total == 0 {
		return b.String()
	}

	maxScroll := total - logBudget
	if maxScroll < 0 {
		maxScroll = 0
	}

	if m.logPinned {
		// pinned: new lines arriving shouldn't move the viewport
		pinnedStart := m.logPinnedTotal - logBudget - m.logScrollOff
		newOffset := total - logBudget - pinnedStart
		if newOffset < 0 {
			newOffset = 0
		}
		m.logScrollOff = newOffset
	}
	m.logPinnedTotal = total

	if m.logScrollOff > maxScroll {
		m.logScrollOff = maxScroll
	}
	if m.logScrollOff < 0 {
		m.logScrollOff = 0
	}

	start := total - logBudget - m.logScrollOff
	if start < 0 {
		start = 0
	}
	end := start + logBudget
	if end > total {
		end = total
	}

	if m.logScrollOff > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ↑ %d more lines above", start)))
		b.WriteString("\n")
	}
	for _, vl := range visualLines[start:end] {
		b.WriteString("  " + vl + "\n")
	}
	if end < total {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ↓ %d more lines below", total-end)))
		b.WriteString("\n")
	}
	return b.String()
}

func firstMeaningfulLine(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}

func (m *simulateModel) renderHint() string {
	if m.confirmQuit {
		return m.renderQuitConfirm()
	}
	if m.saving {
		return m.renderSaveDialog()
	}
	if m.quotaModalActive() {
		return m.renderQuotaWarning()
	}
	var parts []string
	switch {
	case m.detailJobID != "":
		parts = append(parts, "↑↓ scroll · c copy scenario · ESC/j back")
		if m.hasLogs() {
			if m.showLogs {
				parts = append(parts, "Ctrl+L hide logs")
			} else {
				parts = append(parts, "Ctrl+L logs")
			}
		}
	case m.descriptionExpanded():
		parts = append(parts, "↑↓ scroll · d collapse description")
	default:
		// the collapsed description block already carries "(press d to expand)"
		nav := "i/k navigate · ENTER/l detail · ↑↓ scroll"
		if m.pageOverflow || m.viewScrollOff > 0 {
			nav += " · PgUp/PgDn page"
		}
		if m.canExportScenarios() {
			nav += " · s save scenarios"
		}
		parts = append(parts, nav)
		if m.hasLogs() {
			if m.showLogs {
				parts = append(parts, "PgUp/PgDn scroll logs · Ctrl+L hide logs")
			} else {
				parts = append(parts, "Ctrl+L logs")
			}
		}
	}
	parts = append(parts, "q quit")
	return dimStyle.Render("  " + strings.Join(parts, " · "))
}

func (m *simulateModel) renderQuitConfirm() string {
	keep := "Keep running"
	stop := "Stop simulation"
	if m.confirmQuitSel == 0 {
		keep = reverseStyle.Bold(true).Render(" " + keep + " ")
		stop = dimStyle.Render(" " + stop + " ")
	} else {
		keep = dimStyle.Render(" " + keep + " ")
		stop = lipgloss.NewStyle().Background(util.Error()).Foreground(lipgloss.Color("15")).Bold(true).Render(" " + stop + " ")
	}
	var b strings.Builder
	b.WriteString(boldStyle.Render("Stop simulation?") + "\n")
	b.WriteString(dimStyle.Render("The run is still in progress. Quitting will cancel it.") + "\n\n")
	b.WriteString(keep + "  " + stop + "\n\n")
	b.WriteString(dimStyle.Render("←→ select · enter confirm · esc dismiss"))
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(util.Warning()).
		Padding(0, 1).
		Render(b.String())
	return indentLines(box, "  ")
}

func (m *simulateModel) renderSaveDialog() string {
	n := len(m.run.GetScenarioGroup().GetScenarios())
	noun := "scenarios"
	if n == 1 {
		noun = "scenario"
	}
	subtitle := fmt.Sprintf("%d generated %s → %s", n, noun, m.config.projectDir)
	// wide enough for the destination path, within the terminal
	width := 50
	if w := lipgloss.Width(subtitle); w > width {
		width = w
	}
	if max := m.width - 8; width > max {
		width = max
	}
	if width < 24 {
		width = 24
	}
	var b strings.Builder
	b.WriteString(boldStyle.Render("Save scenarios") + "\n")
	b.WriteString(dimStyle.Render(subtitle) + "\n\n")
	b.WriteString("File: " + m.saveInput.View())
	if m.saveErr != "" {
		b.WriteString("\n" + redStyle().Render(m.saveErr))
	}
	b.WriteString("\n\n" + dimStyle.Render("enter save · esc cancel"))
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(util.Brand()).
		Padding(0, 1).
		Width(width).
		Render(b.String())
	return indentLines(box, "  ")
}

// renderQuotaWarning is the once-per-run 429 dialog: the project's inference
// quota is exhausted, so every LLM completion — and with it every job — is
// failing. Pinned where the hint bar renders, dismissed from the keyboard
// (the TUI runs without mouse capture).
func (m *simulateModel) renderQuotaWarning() string {
	suggested := m.quotaSuggested
	width := 56
	if max := m.width - 8; width > max {
		width = max
	}
	if width < 24 {
		width = 24
	}
	body := lipgloss.NewStyle().Width(width)
	var b strings.Builder
	b.WriteString(boldStyle.Render("Inference quota exceeded") + "\n")
	b.WriteString(body.Render(fmt.Sprintf(
		"This project is hitting its %s. LLM completions are being rejected (429), and simulation jobs are failing with them.",
		m.quotaWarning.describe())) + "\n\n")
	b.WriteString(body.Render(fmt.Sprintf(
		"Suggested fix: re-run with --concurrency %d", suggested)) + "\n\n")
	btn := reverseStyle.Bold(true).Render(" Dismiss ")
	b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(btn))
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(util.Error()).
		Padding(0, 1).
		Render(b.String() + "\n" + dimStyle.Render("enter/esc dismiss"))
	return indentLines(box, "  ")
}

func (m *simulateModel) renderToast() string {
	if m.toast == "" {
		return ""
	}
	borderColor := util.Success()
	line := greenStyle().Render("✓") + " " + m.toast
	if !m.toastOK {
		borderColor = util.Error()
		line = redStyle().Render("✗") + " " + m.toast
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Render(line)
	return indentLines(box, "  ") + "\n"
}

func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func jobIcon(job *livekit.SimulationRun_Job) string {
	r, st := jobStatusIcon(job)
	return st.Render(string(r))
}
