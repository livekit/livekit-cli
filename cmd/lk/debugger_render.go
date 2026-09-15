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
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// Styles for the headless session output. Named distinctly from the console
// TUI styles so both can coexist in the console-tagged build.
var (
	sessionCyan       = lipgloss.Color("#1fd5f9")
	sessionGreen      = lipgloss.Color("#6BCB77")
	sessionPurple     = lipgloss.Color("#8f83ff")
	sessionYellow     = lipgloss.Color("#f5c451")
	sessionRed        = lipgloss.Color("#FF6B6B")
	sessionUserStyle  = lipgloss.NewStyle().Foreground(sessionCyan).Bold(true)
	sessionAgentStyle = lipgloss.NewStyle().Foreground(sessionGreen).Bold(true)
	sessionToolStyle  = lipgloss.NewStyle().Foreground(sessionYellow)
	sessionDimStyle   = lipgloss.NewStyle().Faint(true)
	sessionRedStyle   = lipgloss.NewStyle().Foreground(sessionRed)
)

// renderOptions controls how much detail the text renderer shows.
type renderOptions struct {
	// Metrics shows per-message latency metrics (llm_ttft, tts_ttfb, e2e).
	Metrics bool
	// FullOutput shows tool output in full instead of a capped excerpt.
	FullOutput bool
}

// toolOutputLimit caps tool output unless --full-output is given, so a
// tool that returns a large payload doesn't flood the transcript.
const toolOutputLimit = 600

// renderTurnEvent turns one normalized session event into printable lines, or
// "" if the event carries nothing worth showing.
func renderTurnEvent(e turnEvent, opts renderOptions) string {
	switch e.Type {
	case "message":
		switch e.Role {
		case "user":
			return renderSpeaker(lipgloss.NewStyle().Foreground(sessionCyan), sessionUserStyle, "You", e.Text, "")
		case "assistant":
			var suffix string
			if e.Earlier {
				suffix += " " + sessionDimStyle.Render("(before this turn)")
			}
			if e.Interrupted {
				suffix += " " + sessionDimStyle.Render("(interrupted)")
			}
			s := renderSpeaker(lipgloss.NewStyle().Foreground(sessionGreen), sessionAgentStyle, "Agent", e.Text, suffix)
			if opts.Metrics && len(e.Metrics) > 0 {
				s += "\n    " + sessionDimStyle.Render(renderMetrics(e.Metrics))
			}
			return s
		}
	case "tool_call":
		var b strings.Builder
		b.WriteString("\n  ")
		b.WriteString(sessionToolStyle.Render("● "))
		b.WriteString(sessionDimStyle.Render("tool: "))
		b.WriteString(sessionToolStyle.Render(e.Name))
		if args := strings.TrimSpace(e.Arguments); args != "" && args != "{}" {
			b.WriteString(sessionDimStyle.Render("(" + args + ")"))
		} else {
			b.WriteString(sessionDimStyle.Render("()"))
		}
		if e.Earlier {
			b.WriteString(" " + sessionDimStyle.Render("(before this turn)"))
		}
		output := strings.TrimSpace(e.Output)
		if e.IsError {
			if output == "" {
				output = "error"
			}
			writeIndented(&b, sessionRedStyle, "✗ ", output, opts)
		} else if output != "" {
			writeIndented(&b, sessionDimStyle, "↳ ", output, opts)
		}
		return b.String()
	case "handoff":
		if e.From == "" {
			// The session's first agent is reported as a handoff from nobody.
			return "\n  " + lipgloss.NewStyle().Foreground(sessionPurple).Render("● ") +
				sessionDimStyle.Render("agent: ") + e.To
		}
		return "\n  " + lipgloss.NewStyle().Foreground(sessionPurple).Render("● ") +
			sessionDimStyle.Render("handoff: "+e.From+" → ") + e.To
	case "config":
		return "\n  " + lipgloss.NewStyle().Foreground(sessionPurple).Render("● ") +
			sessionDimStyle.Render("config: "+strings.Join(e.Changes, "; "))
	case "error":
		return "\n  " + sessionRedStyle.Render("✗ agent error: "+e.Text)
	case "log":
		return "    " + sessionDimStyle.Render("│ "+e.Text)
	}
	return ""
}

// renderSpeaker prints a labelled speech block:
//
//	● Label
//	  line one
//	  line two
func renderSpeaker(bullet, label lipgloss.Style, name, text, suffix string) string {
	var b strings.Builder
	b.WriteString("\n  ")
	b.WriteString(bullet.Render("● "))
	b.WriteString(label.Render(name))
	b.WriteString(suffix)
	for _, line := range strings.Split(text, "\n") {
		b.WriteString("\n    ")
		b.WriteString(line)
	}
	return b.String()
}

// renderSilentTurn is shown when the agent completed a turn without producing
// any output (a tool asked it to stay quiet, or it chose not to answer).
func renderSilentTurn() string {
	return "\n  " + lipgloss.NewStyle().Foreground(sessionGreen).Render("● ") +
		sessionAgentStyle.Render("Agent") + "\n    " + sessionDimStyle.Render("(no reply)")
}

// writeIndented appends text under a bullet, capping it unless FullOutput.
func writeIndented(b *strings.Builder, style lipgloss.Style, marker, text string, opts renderOptions) {
	truncated := 0
	if !opts.FullOutput && len(text) > toolOutputLimit {
		truncated = len(text) - toolOutputLimit
		text = text[:toolOutputLimit]
	}
	for i, line := range strings.Split(text, "\n") {
		b.WriteString("\n    ")
		if i == 0 {
			b.WriteString(style.Render(marker + line))
		} else {
			b.WriteString(style.Render("  " + line))
		}
	}
	if truncated > 0 {
		b.WriteString("\n    ")
		b.WriteString(sessionDimStyle.Render(fmt.Sprintf("  … %d more bytes (use --full-output to see it all)", truncated)))
	}
}

func renderMetrics(m map[string]float64) string {
	var parts []string
	for _, key := range []string{"llm_ttft", "tts_ttfb", "e2e_latency"} {
		if v, ok := m[key]; ok {
			parts = append(parts, fmt.Sprintf("%s %dms", key, int(v*1000)))
		}
	}
	return "⏱ " + strings.Join(parts, " · ")
}
