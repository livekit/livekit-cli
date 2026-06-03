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
	"strings"

	"github.com/charmbracelet/lipgloss"

	agent "github.com/livekit/protocol/livekit/agent"
)

// Styles for the headless session output. Named distinctly from the console
// TUI styles so both can coexist in the console-tagged build.
var (
	sessionCyan       = lipgloss.Color("#1fd5f9")
	sessionGreen      = lipgloss.Color("#6BCB77")
	sessionPurple     = lipgloss.Color("#8f83ff")
	sessionRed        = lipgloss.Color("#FF6B6B")
	sessionUserStyle  = lipgloss.NewStyle().Foreground(sessionCyan).Bold(true)
	sessionAgentStyle = lipgloss.NewStyle().Foreground(sessionGreen).Bold(true)
	sessionDimStyle   = lipgloss.NewStyle().Faint(true)
	sessionRedStyle   = lipgloss.NewStyle().Foreground(sessionRed)
)

// renderUserMessage formats the text the user said, echoed back by `say`.
func renderUserMessage(text string) string {
	var b strings.Builder
	b.WriteString("\n  ")
	b.WriteString(lipgloss.NewStyle().Foreground(sessionCyan).Render("● "))
	b.WriteString(sessionUserStyle.Render("You"))
	for _, line := range strings.Split(text, "\n") {
		b.WriteString("\n    ")
		b.WriteString(line)
	}
	return b.String()
}

// renderEvent turns an AgentSessionEvent into a printable line, or "" if the
// event carries nothing worth showing in text mode.
func renderEvent(ev *agent.AgentSessionEvent) string {
	if ev == nil {
		return ""
	}
	switch e := ev.Event.(type) {
	case *agent.AgentSessionEvent_ConversationItemAdded_:
		if item := e.ConversationItemAdded.Item; item != nil {
			return renderChatItem(item)
		}
	case *agent.AgentSessionEvent_FunctionToolsExecuted_:
		return renderFunctionTools(e.FunctionToolsExecuted)
	case *agent.AgentSessionEvent_Error_:
		return "  " + sessionRedStyle.Render("✗ "+e.Error.Message)
	}
	return ""
}

func renderChatItem(item *agent.ChatContext_ChatItem) string {
	switch i := item.Item.(type) {
	case *agent.ChatContext_ChatItem_Message:
		msg := i.Message
		if msg.Role == agent.ChatRole_USER {
			return "" // the user message is echoed separately by `say`
		}
		var parts []string
		for _, c := range msg.Content {
			if t := c.GetText(); t != "" {
				parts = append(parts, t)
			}
		}
		text := strings.Join(parts, "")
		if text == "" {
			return ""
		}
		var b strings.Builder
		b.WriteString("\n  ")
		b.WriteString(lipgloss.NewStyle().Foreground(sessionGreen).Render("● "))
		b.WriteString(sessionAgentStyle.Render("Agent"))
		for _, line := range strings.Split(text, "\n") {
			b.WriteString("\n    ")
			b.WriteString(line)
		}
		return b.String()

	case *agent.ChatContext_ChatItem_FunctionCall:
		return "\n  ● function_tool: " + i.FunctionCall.Name

	case *agent.ChatContext_ChatItem_FunctionCallOutput:
		fco := i.FunctionCallOutput
		prefix, style := "✓ ", sessionDimStyle
		if fco.IsError {
			prefix, style = "✗ ", sessionRedStyle
		}
		return "    " + style.Render(prefix+sessionSummarizeOutput(fco.Output))

	case *agent.ChatContext_ChatItem_AgentHandoff:
		h := i.AgentHandoff
		old := ""
		if h.OldAgentId != nil && *h.OldAgentId != "" {
			old = sessionDimStyle.Render(*h.OldAgentId) + " → "
		}
		return "  " + lipgloss.NewStyle().Foreground(sessionPurple).Render("● ") +
			sessionDimStyle.Render("handoff: ") + old + h.NewAgentId

	case *agent.ChatContext_ChatItem_AgentConfigUpdate:
		u := i.AgentConfigUpdate
		var parts []string
		if u.Instructions != nil {
			parts = append(parts, "instructions updated")
		}
		if len(u.ToolsAdded) > 0 {
			parts = append(parts, "tools added: "+strings.Join(u.ToolsAdded, ", "))
		}
		if len(u.ToolsRemoved) > 0 {
			parts = append(parts, "tools removed: "+strings.Join(u.ToolsRemoved, ", "))
		}
		if len(parts) == 0 {
			return ""
		}
		return "  " + lipgloss.NewStyle().Foreground(sessionPurple).Render("● ") +
			sessionDimStyle.Render("config: "+strings.Join(parts, "; "))
	}
	return ""
}

func renderFunctionTools(ft *agent.AgentSessionEvent_FunctionToolsExecuted) string {
	if ft == nil {
		return ""
	}
	outputs := make(map[string]*agent.FunctionCallOutput, len(ft.FunctionCallOutputs))
	for _, fco := range ft.FunctionCallOutputs {
		outputs[fco.CallId] = fco
	}
	var b strings.Builder
	for i, fc := range ft.FunctionCalls {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("\n  ● function_tool: ")
		b.WriteString(fc.Name)
		if fco, ok := outputs[fc.CallId]; ok {
			b.WriteString("\n    ")
			if fco.IsError {
				b.WriteString(sessionRedStyle.Render("✗ " + sessionSummarizeOutput(fco.Output)))
			} else {
				b.WriteString(sessionDimStyle.Render("✓ " + sessionSummarizeOutput(fco.Output)))
			}
		}
	}
	return b.String()
}

// sessionSummarizeOutput collapses a tool output to a single, length-capped line.
func sessionSummarizeOutput(out string) string {
	out = strings.TrimSpace(out)
	if idx := strings.IndexByte(out, '\n'); idx >= 0 {
		out = out[:idx]
	}
	const max = 120
	if len(out) > max {
		out = out[:max] + "…"
	}
	return out
}
