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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	agent "github.com/livekit/protocol/livekit/agent"

	"github.com/livekit/livekit-cli/v2/pkg/console"
)

// Console-specific styles (tagStyle, greenStyle, redStyle, dimStyle, boldStyle, cyanStyle
// are inherited from simulate_tui.go which is always compiled)
var (
	lkCyan   = lipgloss.Color("#1fd5f9")
	lkPurple = lipgloss.Color("#8f83ff")
	lkGreen  = lipgloss.Color("#6BCB77")
	lkRed = lipgloss.Color("#EF4444")

	labelStyle     = lipgloss.NewStyle().Foreground(lkPurple)
	cyanBoldStyle  = lipgloss.NewStyle().Foreground(lkCyan).Bold(true)
	greenBoldStyle = lipgloss.NewStyle().Foreground(lkGreen).Bold(true)
	redBoldStyle   = lipgloss.NewStyle().Foreground(lkRed).Bold(true)
)

// Unicode block characters for frequency visualizer (matching Python console)
var blocks = []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

type consoleTickMsg struct{}
type sessionEventMsg struct{ event *agent.SessionEvent }
type agentLogMsg struct{ line string }

type consoleModel struct {
	pipeline  *console.AudioPipeline
	agentProc *AgentProcess
	inputDev  string
	outputDev string

	width int

	// Partial user transcription (not yet final)
	partialTranscript string

	// Text mode
	textMode  bool
	textInput textinput.Model

	// Shortcut help toggle (? key)
	showShortcuts bool

	// Last turn metrics text (cleared on next thinking state)
	metricsText string

	// Request counter for unique IDs
	reqCounter int
}

func newConsoleModel(pipeline *console.AudioPipeline, agentProc *AgentProcess, inputDev, outputDev string) consoleModel {
	ti := textinput.New()
	ti.Placeholder = "Type to talk to your agent"
	ti.CharLimit = 1000
	ti.Width = 60
	ti.Prompt = "❯ "
	ti.PromptStyle = boldStyle

	return consoleModel{
		pipeline:  pipeline,
		agentProc: agentProc,
		inputDev:  inputDev,
		outputDev: outputDev,
		textInput: ti,
	}
}

func (m consoleModel) Init() tea.Cmd {
	cmds := []tea.Cmd{
		consoleTickCmd(),
		pollEventsCmd(m.pipeline),
	}
	if m.agentProc != nil && m.agentProc.LogStream != nil {
		cmds = append(cmds, pollLogsCmd(m.agentProc.LogStream))
	}
	return tea.Batch(cmds...)
}

func consoleTickCmd() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg {
		return consoleTickMsg{}
	})
}

func pollEventsCmd(pipeline *console.AudioPipeline) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-pipeline.Events
		if !ok {
			return nil
		}
		return sessionEventMsg{event: ev}
	}
}

func pollLogsCmd(ch chan string) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return nil
		}
		return agentLogMsg{line: line}
	}
}

func (m consoleModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.textMode {
			return m.updateTextMode(msg)
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "m":
			m.pipeline.SetMuted(!m.pipeline.Muted())
		case "ctrl+t":
			m.textMode = true
			m.showShortcuts = false
			m.textInput.Focus()
			return m, textinput.Blink
		case "?":
			m.showShortcuts = !m.showShortcuts
		case "esc":
			m.showShortcuts = false
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width

	case consoleTickMsg:
		return m, consoleTickCmd()

	case sessionEventMsg:
		cmds := m.handleSessionEvent(msg.event)
		cmds = append(cmds, pollEventsCmd(m.pipeline))
		return m, tea.Batch(cmds...)

	case agentLogMsg:
		cmd := tea.Println(dimStyle.Render(msg.line))
		var nextCmd tea.Cmd
		if m.agentProc != nil && m.agentProc.LogStream != nil {
			nextCmd = pollLogsCmd(m.agentProc.LogStream)
		}
		return m, tea.Batch(cmd, nextCmd)
	}

	return m, nil
}

func (m *consoleModel) updateTextMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "ctrl+t":
		m.textMode = false
		m.showShortcuts = false
		m.textInput.Blur()
		return m, nil
	case "esc":
		if m.showShortcuts {
			m.showShortcuts = false
			return m, nil
		}
		m.textMode = false
		m.textInput.Blur()
		return m, nil
	case "?":
		if m.textInput.Value() == "" {
			m.showShortcuts = !m.showShortcuts
			return m, nil
		}
	case "enter":
		text := strings.TrimSpace(m.textInput.Value())
		if text != "" {
			m.reqCounter++
			reqID := fmt.Sprintf("console-%d", m.reqCounter)
			m.textInput.SetValue("")

			// Print user message matching the old console format:
			//   ● You
			//     text here
			printCmd := tea.Println(
				"\n  " + lipgloss.NewStyle().Foreground(lkCyan).Render("● ") +
					cyanBoldStyle.Render("You") +
					"\n    " + text,
			)

			req := &agent.SessionRequest{
				RequestId: reqID,
				Request: &agent.SessionRequest_SendMessage{
					SendMessage: &agent.SendMessageRequest{Text: text},
				},
			}
			go m.pipeline.SendRequest(req)
			return m, printCmd
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m *consoleModel) handleSessionEvent(ev *agent.SessionEvent) []tea.Cmd {
	if ev == nil {
		return nil
	}
	var cmds []tea.Cmd

	switch e := ev.Event.(type) {
	case *agent.SessionEvent_AgentStateChanged:
		if e.AgentStateChanged.NewState == agent.AgentState_AGENT_STATE_THINKING {
			m.metricsText = ""
		}

	case *agent.SessionEvent_UserInputTranscribed:
		if e.UserInputTranscribed.IsFinal {
			m.partialTranscript = ""
			if text := e.UserInputTranscribed.Transcript; text != "" {
				cmds = append(cmds, tea.Println(
					"\n  "+lipgloss.NewStyle().Foreground(lkCyan).Render("● ")+
						cyanBoldStyle.Render("You")+
						"\n    "+text,
				))
			}
		} else {
			m.partialTranscript = e.UserInputTranscribed.Transcript
		}

	case *agent.SessionEvent_ConversationItemAdded:
		if item := e.ConversationItemAdded.Item; item != nil {
			// Extract metrics from ChatMessage (matching Python console pattern)
			if msg := item.GetMessage(); msg != nil {
				if text := formatMetrics(msg.Metrics); text != "" {
					m.metricsText = text
				}
			}
			lines := formatChatItem(item)
			for _, line := range lines {
				cmds = append(cmds, tea.Println(line))
			}
		}

	case *agent.SessionEvent_FunctionToolsExecuted:
		for _, fc := range e.FunctionToolsExecuted.FunctionCalls {
			cmds = append(cmds, tea.Println(
				"  "+lipgloss.NewStyle().Foreground(lkCyan).Render("➜ ")+
					cyanBoldStyle.Render(fc.Name),
			))
		}
		for _, fco := range e.FunctionToolsExecuted.FunctionCallOutputs {
			if fco.IsError {
				cmds = append(cmds, tea.Println(
					"    "+redBoldStyle.Render("✗ ")+redStyle.Render(truncateOutput(fco.Output)),
				))
			} else {
				cmds = append(cmds, tea.Println(
					"    "+greenStyle.Render("✓ ")+dimStyle.Render(summarizeOutput(fco.Output)),
				))
			}
		}

	case *agent.SessionEvent_Error:
		cmds = append(cmds, tea.Println(
			"  "+redBoldStyle.Render("✗ ")+redStyle.Render(e.Error.Message),
		))
	}

	return cmds
}

// formatChatItem returns lines to print for a conversation item,
// matching the old Python console format.
func formatChatItem(item *agent.ChatContext_ChatItem) []string {
	switch i := item.Item.(type) {
	case *agent.ChatContext_ChatItem_Message:
		msg := i.Message
		// User messages are printed from UserInputTranscribed (final) to avoid
		// ordering issues with partial transcripts.
		if msg.Role == agent.ChatRole_USER {
			return nil
		}
		var textParts []string
		for _, c := range msg.Content {
			if t := c.GetText(); t != "" {
				textParts = append(textParts, t)
			}
		}
		text := strings.Join(textParts, "")
		if text == "" {
			return nil
		}

		var lines []string
		lines = append(lines,
			"\n  "+lipgloss.NewStyle().Foreground(lkGreen).Render("● ")+
				greenBoldStyle.Render("Agent"),
		)
		for _, tl := range strings.Split(text, "\n") {
			lines = append(lines, "    "+tl)
		}
		return lines

	case *agent.ChatContext_ChatItem_FunctionCall:
		return []string{
			"  " + lipgloss.NewStyle().Foreground(lkCyan).Render("➜ ") +
				cyanBoldStyle.Render(i.FunctionCall.Name),
		}

	case *agent.ChatContext_ChatItem_FunctionCallOutput:
		if i.FunctionCallOutput.IsError {
			return []string{
				"    " + redBoldStyle.Render("✗ ") + redStyle.Render(truncateOutput(i.FunctionCallOutput.Output)),
			}
		}
		return []string{
			"    " + greenStyle.Render("✓ ") + dimStyle.Render(summarizeOutput(i.FunctionCallOutput.Output)),
		}
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────
// View — compact status area at the bottom (not fullscreen).
// Logs and conversation scroll up via tea.Println.
// Layout matches the old Python console (FrequencyVisualizer + prompt).
// ──────────────────────────────────────────────────────────────────

func (m consoleModel) View() string {
	var b strings.Builder

	if m.textMode {
		// ── Text input (matching old Python prompt layout) ──
		w := m.width
		if w <= 0 {
			w = 80
		}
		sep := dimStyle.Render(strings.Repeat("─", min(w, 80)))
		b.WriteString(sep)
		b.WriteString("\n")
		b.WriteString(m.textInput.View())
		b.WriteString("\n")
		b.WriteString(sep)

		if m.showShortcuts {
			b.WriteString("\n")
			m.writeShortcutsInline(&b, []shortcut{
				{"Ctrl+T", "audio mode"},
				{"Ctrl+C", "exit"},
			})
		} else {
			b.WriteString("\n")
			b.WriteString(dimStyle.Render("   ? for shortcuts"))
		}
	} else {
		// ── Audio visualizer (matching old Python FrequencyVisualizer) ──
		b.WriteString("   ")
		b.WriteString(labelStyle.Render(m.inputDev))
		b.WriteString("  ")
		bands := m.pipeline.FFTBands()
		for _, band := range bands {
			idx := int(band * float64(len(blocks)-1))
			if idx >= len(blocks) {
				idx = len(blocks) - 1
			}
			if idx < 0 {
				idx = 0
			}
			b.WriteString(" ")
			b.WriteString(blocks[idx])
		}

		if m.pipeline.Muted() {
			b.WriteString("  ")
			b.WriteString(redBoldStyle.Render("MUTED"))
		}

		// Partial transcription on same line (dim)
		if m.partialTranscript != "" {
			b.WriteString("  ")
			b.WriteString(dimStyle.Render("● " + m.partialTranscript + "..."))
		}

		// ERLE > 6dB means the AEC is actively cancelling echo — show as a
		// reassuring status indicator, not a warning.
		if m.pipeline.IsPlaying() {
			if stats := m.pipeline.AECStats(); stats != nil && stats.HasERLE && stats.EchoReturnLossEnhancement > 2 {
				b.WriteString("  ")
				b.WriteString(dimStyle.Render("echo cancelling"))
			}
		}

		// Metrics on same line (right side)
		if m.metricsText != "" {
			b.WriteString("  ")
			b.WriteString(m.metricsText)
		}

		if m.showShortcuts {
			b.WriteString("\n")
			m.writeShortcutsInline(&b, []shortcut{
				{"m", "mute/unmute"},
				{"Ctrl+T", "text mode"},
				{"q", "quit"},
			})
		} else {
			b.WriteString("\n")
			b.WriteString(dimStyle.Render("   ? for shortcuts"))
		}
	}

	return b.String()
}

type shortcut struct {
	key  string
	desc string
}

func (m consoleModel) writeShortcutsInline(b *strings.Builder, shortcuts []shortcut) {
	dimBoldStyle := lipgloss.NewStyle().Faint(true).Bold(true)
	b.WriteString("  ")
	for i, s := range shortcuts {
		if i > 0 {
			b.WriteString(dimStyle.Render("  ·  "))
		}
		b.WriteString(dimBoldStyle.Render(s.key))
		b.WriteString(" ")
		b.WriteString(dimStyle.Render(s.desc))
	}
}

// formatMetrics formats a MetricsReport matching the Python console display.
func formatMetrics(m *agent.MetricsReport) string {
	if m == nil {
		return ""
	}

	var parts []string
	sep := dimStyle.Render(" · ")

	if m.LlmNodeTtft != nil {
		parts = append(parts, dimStyle.Render("llm_ttft ")+dimStyle.Render(formatMs(*m.LlmNodeTtft)))
	}
	if m.TtsNodeTtfb != nil {
		parts = append(parts, dimStyle.Render("tts_ttfb ")+dimStyle.Render(formatMs(*m.TtsNodeTtfb)))
	}
	if m.E2ELatency != nil {
		label := "e2e " + formatMs(*m.E2ELatency)
		if *m.E2ELatency >= 1.0 {
			parts = append(parts, redStyle.Render(label))
		} else {
			parts = append(parts, dimStyle.Render(label))
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, sep)
}

func formatMs(seconds float64) string {
	ms := seconds * 1000
	if ms >= 100 {
		return fmt.Sprintf("%.0fms", ms)
	}
	return fmt.Sprintf("%.1fms", ms)
}

// summarizeOutput tries to parse JSON and produce a "key=value, key=value" summary
// matching the old Python console behavior. Falls back to truncation.
func summarizeOutput(output string) string {
	jsonStart := strings.Index(output, "{")
	if jsonStart < 0 {
		return truncateOutput(output)
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(output[jsonStart:]), &data); err != nil {
		return truncateOutput(output)
	}

	var parts []string
	for k, v := range data {
		if v == nil || k == "type" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		if len(parts) >= 3 {
			break
		}
	}
	result := strings.Join(parts, ", ")
	if len(data) > 3 {
		result += ", ..."
	}
	if result == "" {
		return truncateOutput(output)
	}
	return result
}

func truncateOutput(output string) string {
	if len(output) > 200 {
		return output[:197] + "..."
	}
	return output
}
