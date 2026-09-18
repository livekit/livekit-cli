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
	"time"

	"charm.land/lipgloss/v2"

	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// Transcript styles. Colors come from the active theme palette at render time
// (so they follow `lk set-theme`) and are shared by console and debugger.
func transcriptUserBullet() lipgloss.Style { return lipgloss.NewStyle().Foreground(util.Brand()) }
func transcriptUserLabel() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(util.Brand()).Bold(true)
}
func transcriptAgentBullet() lipgloss.Style { return lipgloss.NewStyle().Foreground(util.Success()) }
func transcriptAgentLabel() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(util.Success()).Bold(true)
}
func transcriptToolStyle() lipgloss.Style  { return lipgloss.NewStyle().Foreground(util.Warning()) }
func transcriptAccent() lipgloss.Style     { return lipgloss.NewStyle().Foreground(util.Accent()) }
func transcriptErrorStyle() lipgloss.Style { return lipgloss.NewStyle().Foreground(util.Error()) }

var transcriptDim = lipgloss.NewStyle().Faint(true)

// renderOptions controls how much detail the text renderer shows.
type renderOptions struct {
	// Metrics shows per-message latency metrics (llm_ttft, tts_ttfb, e2e).
	Metrics bool
}

// renderTurnEvent turns one normalized session event into printable lines, or
// "" if the event carries nothing worth showing.
func renderTurnEvent(e turnEvent, opts renderOptions) string {
	switch e.Type {
	case "message":
		switch e.Role {
		case "user":
			return renderSpeaker(transcriptUserBullet(), transcriptUserLabel(), "You", e.Text, "")
		case "assistant":
			var suffix string
			if e.Earlier {
				suffix += " " + transcriptDim.Render("(before this turn)")
			}
			if e.Interrupted {
				suffix += " " + transcriptDim.Render("(interrupted)")
			}
			s := renderSpeaker(transcriptAgentBullet(), transcriptAgentLabel(), "Agent", e.Text, suffix)
			if opts.Metrics && len(e.Metrics) > 0 {
				s += "\n    " + transcriptDim.Render(renderMetrics(e.Metrics))
			}
			return s
		}
	case "tool_call":
		var b strings.Builder
		b.WriteString("\n  ")
		b.WriteString(transcriptToolStyle().Render("● "))
		b.WriteString(transcriptDim.Render("tool: "))
		b.WriteString(transcriptToolStyle().Render(e.Name))
		if args := strings.TrimSpace(e.Arguments); args != "" && args != "{}" {
			b.WriteString(transcriptDim.Render("(" + args + ")"))
		} else {
			b.WriteString(transcriptDim.Render("()"))
		}
		if e.Earlier {
			b.WriteString(" " + transcriptDim.Render("(before this turn)"))
		}
		output := strings.TrimSpace(e.Output)
		if e.IsError {
			if output == "" {
				output = "error"
			}
			writeIndented(&b, transcriptErrorStyle(), "✗ ", output)
		} else if output != "" {
			writeIndented(&b, transcriptDim, "↳ ", output)
		}
		return b.String()
	case "handoff":
		if e.From == "" {
			// The session's first agent is reported as a handoff from nobody.
			return "\n  " + transcriptAccent().Render("● ") +
				transcriptDim.Render("agent: ") + e.To
		}
		return "\n  " + transcriptAccent().Render("● ") +
			transcriptDim.Render("handoff: "+e.From+" → ") + e.To
	case "config":
		return "\n  " + transcriptAccent().Render("● ") +
			transcriptDim.Render("config: "+strings.Join(e.Changes, "; "))
	case "error":
		return "\n  " + transcriptErrorStyle().Render("✗ agent error: "+e.Text)
	case "log":
		return "    " + transcriptDim.Render("│ "+e.Text)
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
	return "\n  " + transcriptAgentBullet().Render("● ") +
		transcriptAgentLabel().Render("Agent") + "\n    " + transcriptDim.Render("(no reply)")
}

// writeIndented appends text under a bullet, one line per output line.
func writeIndented(b *strings.Builder, style lipgloss.Style, marker, text string) {
	for i, line := range strings.Split(text, "\n") {
		b.WriteString("\n    ")
		if i == 0 {
			b.WriteString(style.Render(marker + line))
		} else {
			b.WriteString(style.Render("  " + line))
		}
	}
}

// renderMetrics formats a message's latency metrics for both the debugger's
// --metrics output and the console's status line. An end-to-end latency of a
// second or more is called out in the error color.
func renderMetrics(m map[string]float64) string {
	var parts []string
	for _, key := range []string{"llm_ttft", "tts_ttfb", "e2e_latency"} {
		v, ok := m[key]
		if !ok {
			continue
		}
		part := fmt.Sprintf("%s %dms", key, int(v*1000))
		if key == "e2e_latency" && v >= 1.0 {
			part = transcriptErrorStyle().Render(part)
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return ""
	}
	return "⏱ " + strings.Join(parts, " · ")
}

// renderEventLine formats one event as a single line for the `events` stream:
// a timestamp, a kind, and the payload with newlines collapsed. Unlike the
// transcript renderer it never spans lines, so the output greps and tails well.
func renderEventLine(e turnEvent) string {
	ts := "            "
	if t, err := time.Parse(time.RFC3339Nano, e.Time); err == nil {
		ts = t.Local().Format("15:04:05.000")
	}
	kind := func(label string, style lipgloss.Style) string {
		return style.Render(fmt.Sprintf("%-8s", label))
	}
	var body string
	switch e.Type {
	case "message":
		text := oneLine(e.Text)
		if e.Role == "user" {
			body = kind("user", transcriptUserLabel()) + text
		} else {
			if e.Interrupted {
				text += " " + transcriptDim.Render("(interrupted)")
			}
			body = kind("agent", transcriptAgentLabel()) + text
		}
	case "tool_call":
		call := e.Name + "(" + oneLine(e.Arguments) + ")"
		switch {
		case e.IsError:
			body = kind("tool", transcriptToolStyle()) + call + " " + transcriptErrorStyle().Render("✗ "+oneLine(e.Output))
		case e.Output != "":
			body = kind("tool", transcriptToolStyle()) + call + " " + transcriptDim.Render("↳ "+oneLine(e.Output))
		default:
			body = kind("tool", transcriptToolStyle()) + call
		}
	case "handoff":
		if e.From == "" {
			body = kind("agent", transcriptAccent()) + transcriptDim.Render("active: ") + e.To
		} else {
			body = kind("handoff", transcriptAccent()) + e.From + " → " + e.To
		}
	case "config":
		body = kind("config", transcriptAccent()) + transcriptDim.Render(strings.Join(e.Changes, "; "))
	case "error":
		body = kind("error", transcriptErrorStyle()) + transcriptErrorStyle().Render(oneLine(e.Text))
	case "log":
		body = kind("log", transcriptDim) + transcriptDim.Render(oneLine(e.Text))
	case "state":
		body = kind("state", transcriptDim) + transcriptDim.Render(e.From+" → "+e.To)
	default:
		return ""
	}
	return transcriptDim.Render(ts) + "  " + body
}

// oneLine collapses whitespace runs (including newlines) into single spaces.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
