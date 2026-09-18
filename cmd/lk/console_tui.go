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
	"io"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	agent "github.com/livekit/protocol/livekit/agent"

	"github.com/livekit/livekit-cli/v2/pkg/console"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// Console-specific styles (tagStyle, greenStyle, redStyle, dimStyle, boldStyle, cyanStyle
// are inherited from simulate_tui.go which is always compiled). Colors are pulled from the
// active theme palette at render time, so they follow `lk set-theme`.
func labelStyle() lipgloss.Style   { return lipgloss.NewStyle().Foreground(util.Accent()) }
func redBoldStyle() lipgloss.Style { return lipgloss.NewStyle().Foreground(util.Error()).Bold(true) }

// Unicode block characters for frequency visualizer (matching Python console)
var blocks = []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

// Braille spinner frames (matching Rich's "dots" spinner)
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// startSpinner shows a braille spinner on stderr with the given message.
// Returns a stop function that clears the spinner line.
func startSpinner(msg string) func() {
	return startSpinnerTo(os.Stderr, msg)
}

func startSpinnerTo(w io.Writer, msg string) func() {
	done := make(chan struct{})
	cleared := make(chan struct{})
	go func() {
		defer close(cleared)
		for i := 0; ; i++ {
			fmt.Fprintf(w, "\r  %s %s", spinnerFrames[i%len(spinnerFrames)], msg)
			select {
			case <-done:
				fmt.Fprintf(w, "\r\033[K")
				return
			case <-time.After(80 * time.Millisecond):
			}
		}
	}()
	// Stopping blocks until the line is cleared, so the caller's next print
	// (e.g. an error) never lands on the leftover spinner text.
	return func() {
		close(done)
		<-cleared
	}
}

type consoleTickMsg struct{}
type sessionEventMsg struct{ event *agent.AgentSessionEvent }
type sessionResponseMsg struct{ resp *agent.SessionResponse }
type audioInitResultMsg struct{ err error }
type agentLogMsg struct{ line string }
type agentExitedMsg struct{}
type shutdownTimeoutMsg struct{}

type consoleModel struct {
	pipeline       *console.AudioPipeline
	pipelineCancel context.CancelFunc
	agentProc      *AgentProcess
	inputDev       string
	outputDev      string

	width int

	// Partial user transcription (not yet final)
	partialTranscript string

	// Text mode
	textMode  bool
	textInput textinput.Model

	// Shortcut help toggle (? key)
	showShortcuts bool

	// Audio init error (shown when switching from text to audio fails)
	audioError string

	// Last turn metrics text (cleared on next thinking state)
	metricsText string

	// Request counter for unique IDs
	reqCounter int

	// Waiting for agent response (text mode loading indicator)
	waitingForAgent bool

	// Shutdown state
	shuttingDown bool
}

func newConsoleModel(pipeline *console.AudioPipeline, pipelineCancel context.CancelFunc, agentProc *AgentProcess, inputDev, outputDev string, textMode bool) consoleModel {
	ti := textinput.New()
	ti.Placeholder = "Type to talk to your agent"
	ti.CharLimit = 1000
	ti.SetWidth(60)
	ti.Prompt = "❯ "
	// v2 moved per-state styling behind Styles/SetStyles, and renders a real
	// terminal cursor unless the virtual one is enabled. The status area is
	// composed into a larger view, so keep the inline virtual cursor rather than
	// positioning the terminal cursor from here.
	tiStyles := ti.Styles()
	tiStyles.Focused.Prompt = boldStyle
	tiStyles.Blurred.Prompt = boldStyle
	ti.SetStyles(tiStyles)
	ti.SetVirtualCursor(true)

	if textMode {
		ti.Focus()
	}

	return consoleModel{
		pipeline:       pipeline,
		pipelineCancel: pipelineCancel,
		agentProc:      agentProc,
		inputDev:       inputDev,
		outputDev:      outputDev,
		textInput:      ti,
		textMode:       textMode,
	}
}

func (m consoleModel) Init() tea.Cmd {
	cmds := []tea.Cmd{
		consoleTickCmd(),
		pollEventsCmd(m.pipeline),
		pollResponsesCmd(m.pipeline),
	}
	if m.agentProc != nil && m.agentProc.LogStream != nil {
		cmds = append(cmds, pollLogsCmd(m.agentProc.LogStream))
	}
	if m.textMode {
		cmds = append(cmds, textinput.Blink)
		m.applyTextMode(true)
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

func pollResponsesCmd(pipeline *console.AudioPipeline) tea.Cmd {
	return func() tea.Msg {
		resp, ok := <-pipeline.Responses
		if !ok {
			return nil
		}
		return sessionResponseMsg{resp: resp}
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
		if m.shuttingDown {
			if msg.String() == "ctrl+c" {
				m.agentProc.ForceKill()
				m.pipelineCancel()
				go m.pipeline.Stop()
				return m, tea.Quit
			}
			return m, nil
		}
		if m.textMode {
			return m.updateTextMode(msg)
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, m.beginShutdown()
		case "m":
			m.pipeline.SetMuted(!m.pipeline.Muted())
		case "ctrl+t":
			m.textMode = true
			m.showShortcuts = false
			m.partialTranscript = ""
			m.textInput.Focus()
			m.applyTextMode(true)
			return m, textinput.Blink
		case "?":
			m.showShortcuts = !m.showShortcuts
		case "esc":
			m.showShortcuts = false
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width

	case consoleTickMsg:
		if m.shuttingDown {
			return m, nil
		}
		return m, consoleTickCmd()

	case sessionEventMsg:
		if m.shuttingDown {
			return m, nil
		}
		cmds := m.handleSessionEvent(msg.event)
		cmds = append(cmds, pollEventsCmd(m.pipeline))
		return m, tea.Batch(cmds...)

	case sessionResponseMsg:
		if m.waitingForAgent {
			m.waitingForAgent = false
			if m.textMode {
				m.textInput.Focus()
			}
		}
		return m, pollResponsesCmd(m.pipeline)

	case audioInitResultMsg:
		if msg.err != nil {
			m.audioError = msg.err.Error()
		} else {
			m.textMode = false
			m.showShortcuts = false
			m.textInput.Blur()
			m.audioError = ""
			m.inputDev = "Default Input"
			m.outputDev = "Default Output"
			m.applyTextMode(false)
		}
		return m, nil

	case agentLogMsg:
		cmd := tea.Println(dimStyle.Render(msg.line))
		var nextCmd tea.Cmd
		if m.agentProc != nil && m.agentProc.LogStream != nil {
			nextCmd = pollLogsCmd(m.agentProc.LogStream)
		}
		return m, tea.Batch(cmd, nextCmd)

	case agentExitedMsg:
		return m, tea.Quit

	case shutdownTimeoutMsg:
		m.agentProc.ForceKill()
		m.pipelineCancel()
		go m.pipeline.Stop()
		return m, tea.Quit
	}

	return m, nil
}

func (m *consoleModel) switchToAudio() tea.Cmd {
	if m.pipeline.HasAudio() {
		m.textMode = false
		m.showShortcuts = false
		m.textInput.Blur()
		m.audioError = ""
		m.applyTextMode(false)
		return nil
	}
	// Lazy init audio in a goroutine
	return func() tea.Msg {
		return audioInitResultMsg{err: m.pipeline.EnableAudio()}
	}
}

// applyTextMode pauses the local audio pipeline and asks the agent to
// disable/enable audio I/O so STT/TTS aren't running in text mode.
func (m *consoleModel) applyTextMode(text bool) {
	if m.pipeline.HasAudio() {
		m.pipeline.SetPaused(text)
	}

	m.reqCounter++
	reqID := fmt.Sprintf("console-io-%d", m.reqCounter)
	audioOn := !text
	transcriptionOn := !text
	req := &agent.SessionRequest{
		RequestId: reqID,
		Request: &agent.SessionRequest_UpdateIo{
			UpdateIo: &agent.SessionRequest_UpdateIO{
				Input: &agent.SessionRequest_UpdateIO_Input{
					AudioEnabled: &audioOn,
				},
				Output: &agent.SessionRequest_UpdateIO_Output{
					AudioEnabled:         &audioOn,
					TranscriptionEnabled: &transcriptionOn,
				},
			},
		},
	}
	go m.pipeline.SendRequest(req)
}

func (m *consoleModel) beginShutdown() tea.Cmd {
	m.shuttingDown = true
	m.textMode = false
	m.showShortcuts = false

	// Close the audio pipeline/TCP connection first so the agent's audio
	// input ends and STT stops receiving data. Then send SIGINT so the
	// agent's session.aclose() runs with nothing left to drain.
	m.pipelineCancel()
	go m.pipeline.Stop()

	m.agentProc.Shutdown()

	// Wait for agent exit or timeout.
	return tea.Batch(
		func() tea.Msg {
			<-m.agentProc.Done()
			return agentExitedMsg{}
		},
		tea.Tick(5*time.Second, func(time.Time) tea.Msg {
			return shutdownTimeoutMsg{}
		}),
	)
}

func (m *consoleModel) updateTextMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, m.beginShutdown()
	case "ctrl+t":
		return m, m.switchToAudio()
	case "esc":
		if m.showShortcuts {
			m.showShortcuts = false
			return m, nil
		}
		return m, m.switchToAudio()
	case "?":
		if m.textInput.Value() == "" {
			m.showShortcuts = !m.showShortcuts
			return m, nil
		}
	case "enter":
		if m.waitingForAgent {
			return m, nil
		}
		text := strings.TrimSpace(m.textInput.Value())
		if text != "" {
			m.reqCounter++
			reqID := fmt.Sprintf("console-%d", m.reqCounter)
			m.textInput.SetValue("")
			m.waitingForAgent = true

			printCmd := tea.Println(renderTurnEvent(turnEvent{Type: "message", Role: "user", Text: text}, renderOptions{}) + "\n")

			req := &agent.SessionRequest{
				RequestId: reqID,
				Request: &agent.SessionRequest_RunInput_{
					RunInput: &agent.SessionRequest_RunInput{Text: text},
				},
			}
			go m.pipeline.SendRequest(req)
			return m, tea.Batch(printCmd, consoleTickCmd())
		}
		return m, nil
	}

	m.audioError = "" // clear on any key press
	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m *consoleModel) handleSessionEvent(ev *agent.AgentSessionEvent) []tea.Cmd {
	if ev == nil {
		return nil
	}
	var cmds []tea.Cmd

	switch e := ev.Event.(type) {
	case *agent.AgentSessionEvent_AgentStateChanged_:
		if e.AgentStateChanged.NewState == agent.AgentState_AS_THINKING {
			m.metricsText = ""
		}
		return nil

	case *agent.AgentSessionEvent_UserInputTranscribed_:
		// Voice mode only: in text mode the typed turn is echoed when sent.
		if m.textMode {
			return nil
		}
		if !e.UserInputTranscribed.IsFinal {
			m.partialTranscript = e.UserInputTranscribed.Transcript
			return nil
		}
		m.partialTranscript = ""
		if text := e.UserInputTranscribed.Transcript; text != "" {
			cmds = append(cmds, tea.Println(renderTurnEvent(turnEvent{Type: "message", Role: "user", Text: text}, renderOptions{})+"\n"))
		}
		return cmds
	}

	// Everything else (messages, tool calls, handoffs, config changes, errors)
	// goes through the transcript model shared with `lk agent debugger`.
	for _, te := range eventToTurnEvents(ev) {
		if te.Type == "message" && te.Role == "assistant" && len(te.Metrics) > 0 {
			m.metricsText = renderMetrics(te.Metrics)
		}
		if line := renderTurnEvent(te, renderOptions{}); line != "" {
			cmds = append(cmds, tea.Println(line+"\n"))
		}
	}
	return cmds
}

// ──────────────────────────────────────────────────────────────────
// View — compact status area at the bottom (not fullscreen).
// Logs and conversation scroll up via tea.Println.
// Layout matches the old Python console (FrequencyVisualizer + prompt).
// ──────────────────────────────────────────────────────────────────

func (m consoleModel) View() tea.View {
	return tea.NewView(m.render())
}

func (m consoleModel) render() string {
	var b strings.Builder

	if m.shuttingDown {
		b.WriteString("\n  ")
		b.WriteString(labelStyle().Render("Shutting down agent..."))
		b.WriteString("  ")
		b.WriteString(dimStyle.Render("ctrl+C to force"))
		b.WriteString("\n")
		return b.String()
	}

	if m.textMode {
		if m.waitingForAgent {
			// Braille spinner (matching Rich's "dots" spinner)
			frame := spinnerFrames[int(time.Now().UnixMilli()/80)%len(spinnerFrames)]
			b.WriteString("  ")
			b.WriteString(dimStyle.Render(frame + " thinking"))
		} else {
			// ── Text input ──
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
		}

		if m.audioError != "" {
			for _, line := range wrapLines("audio: "+m.audioError, m.width-2) {
				b.WriteString("\n  ")
				b.WriteString(redStyle().Render(line))
			}
		}

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
		b.WriteString(labelStyle().Render(m.inputDev))
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
			b.WriteString(redBoldStyle().Render("MUTED"))
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
