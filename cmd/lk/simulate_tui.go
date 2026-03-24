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
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
)

// --- Message types ---

type simulationRunMsg struct {
	run *livekit.SimulationRun
	err error
}

type pollTickMsg struct{}

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

type simulateModel struct {
	client         *lksdk.AgentSimulationClient
	runID          string
	numSimulations int32
	agent          *AgentProcess

	run         *livekit.SimulationRun
	runFinished bool
	startTime   time.Time

	filter      int
	cursor      int
	scrollOff   int
	detailJobID string
	showLogs    bool

	width  int
	height int
	err    error
}

func newSimulateModel(client *lksdk.AgentSimulationClient, runID string, numSimulations int32, agent *AgentProcess) *simulateModel {
	return &simulateModel{
		client:         client,
		runID:          runID,
		numSimulations: numSimulations,
		agent:          agent,
		width:          80,
		height:         24,
	}
}

func (m *simulateModel) Init() tea.Cmd {
	return tea.Batch(
		m.pollSimulation(),
		m.waitSubprocess(),
		tickCmd(),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return pollTickMsg{}
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

	case pollTickMsg:
		var cmds []tea.Cmd
		if !m.runFinished {
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
		return m, tea.Quit
	case "ctrl+l":
		m.showLogs = !m.showLogs
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
	if m.run == nil {
		return m.viewWaiting()
	}
	switch m.run.Status {
	case livekit.SimulationRun_STATUS_GENERATING:
		return m.viewGenerating()
	default:
		return m.viewRunning()
	}
}

func (m *simulateModel) viewWaiting() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(tagStyle.Render("Simulate"))
	b.WriteString(" ")
	b.WriteString(cyanStyle.Render(m.runID))
	b.WriteString("\n\n")
	b.WriteString("  [1/4] Starting...\n")
	if m.showLogs {
		b.WriteString(m.renderLogs())
	}
	b.WriteString(dimStyle.Render("  Ctrl+L logs"))
	b.WriteString("\n")
	return b.String()
}

func (m *simulateModel) viewGenerating() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(tagStyle.Render("Simulate"))
	b.WriteString(" ")
	b.WriteString(cyanStyle.Render(m.runID))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  [1/4] Generating %d scenarios...\n", m.numSimulations))
	if m.showLogs {
		b.WriteString(m.renderLogs())
	}
	b.WriteString(dimStyle.Render("  Ctrl+L logs"))
	b.WriteString("\n")
	return b.String()
}

func (m *simulateModel) viewRunning() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(tagStyle.Render("Simulate"))
	b.WriteString(" ")
	b.WriteString(cyanStyle.Render(m.runID))
	b.WriteString("\n\n")

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
	var step, label, style string
	switch {
	case m.run.Status == livekit.SimulationRun_STATUS_COMPLETED || m.run.Status == livekit.SimulationRun_STATUS_FAILED || m.run.Status == livekit.SimulationRun_STATUS_CANCELLED:
		step = "[4/4]"
		_, _, failed, _ := m.jobCounts()
		if m.run.Status == livekit.SimulationRun_STATUS_CANCELLED {
			label = "Cancelled"
			style = "yellow"
		} else if m.run.Status == livekit.SimulationRun_STATUS_FAILED {
			label = "Failed"
			style = "red"
		} else if failed > 0 {
			label = "Completed with failures"
			style = "yellow"
		} else {
			label = "Completed"
			style = "green"
		}
	case m.run.Status == livekit.SimulationRun_STATUS_SUMMARIZING:
		step = "[3/4]"
		label = "Summarizing"
		style = "yellow"
	default:
		step = "[2/4]"
		label = "Running"
		style = "yellow"
	}

	header := dimStyle.Render(step) + " " + boldStyle.Render("Simulation") + " — "
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

		line := fmt.Sprintf("  %s %3d. %s  %s", icon, ij.origIdx, dimStyle.Render(ij.job.Id), instr)

		if i == m.cursor {
			line = reverseStyle.Render(line)
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

	b.WriteString(boldStyle.Render("  Instructions:"))
	b.WriteString("\n")
	instr := job.Instructions
	if instr == "" {
		instr = "—"
	}
	for _, line := range strings.Split(instr, "\n") {
		b.WriteString("    " + line + "\n")
	}
	b.WriteString("\n")

	b.WriteString(dimStyle.Bold(true).Render("  Expected:"))
	b.WriteString("\n")
	expect := job.AgentExpectations
	if expect == "" {
		expect = "—"
	}
	for _, line := range strings.Split(expect, "\n") {
		b.WriteString(dimStyle.Render("    "+line) + "\n")
	}

	if job.Error != "" {
		b.WriteString("\n")
		if job.Status == livekit.SimulationRun_Job_STATUS_COMPLETED {
			b.WriteString(greenStyle.Bold(true).Render("  Result:"))
			b.WriteString("\n")
			for _, line := range strings.Split(job.Error, "\n") {
				b.WriteString(greenStyle.Render("    "+line) + "\n")
			}
		} else {
			b.WriteString(redStyle.Bold(true).Render("  Error:"))
			b.WriteString("\n")
			for _, line := range strings.Split(job.Error, "\n") {
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

	if summary.GoingWell != "" {
		b.WriteString(greenStyle.Bold(true).Render("  Going well:"))
		b.WriteString("\n")
		for _, line := range strings.Split(summary.GoingWell, "\n") {
			b.WriteString("    " + line + "\n")
		}
		b.WriteString("\n")
	}

	if summary.ToImprove != "" {
		b.WriteString(yellowStyle.Bold(true).Render("  To improve:"))
		b.WriteString("\n")
		for _, line := range strings.Split(summary.ToImprove, "\n") {
			b.WriteString("    " + line + "\n")
		}
		b.WriteString("\n")
	}

	if len(summary.Issues) > 0 {
		b.WriteString(redStyle.Bold(true).Render("  Issues:"))
		b.WriteString("\n")
		for i, issue := range summary.Issues {
			b.WriteString(fmt.Sprintf("    %d. %s\n", i+1, issue.Description))
			if issue.Suggestion != "" {
				b.WriteString(dimStyle.Render(fmt.Sprintf("       Suggestion: %s", issue.Suggestion)))
				b.WriteString("\n")
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

func (m *simulateModel) renderHint() string {
	if m.detailJobID != "" {
		return dimStyle.Render("  ESC/q back · Ctrl+L logs")
	}
	hint := "  ↑↓/Tab navigate · ENTER detail · ←→ filter · Ctrl+L logs"
	if m.runFinished {
		hint += " · q quit"
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
