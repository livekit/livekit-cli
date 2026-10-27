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
	"time"

	agent "github.com/livekit/protocol/livekit/agent"

	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/livekit-cli/v2/pkg/public/render"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// This file is the shared model of "what happened in an agent session": the
// turnEvent schema and the conversion from the SDK's protobuf events and chat
// items into it. Both `lk agent console` and `lk agent debugger` go through
// it, as does `lk analytics session transcript` for a session's recorded
// transcript, and transcript_render.go draws from it, so a change to how a
// tool call or handoff is represented is made once.

// turnEvent is the normalized, transport-agnostic record of one thing that
// happened in an agent session: a message, a tool call, a handoff, an
// agent error, or an agent log line. The daemon ships these to `say`/`history`
// clients as JSON, the scripted console prints them locally, and `--json`
// emits them verbatim, so the schema doubles as the machine-readable contract
// for coding agents driving the CLI.
type turnEvent struct {
	// Type is one of: message, tool_call, handoff, config, error, log, state,
	// unknown. "state" (agent state transitions, From → To) is only reported
	// by the events stream, never inside a turn. "unknown" is a recorded
	// transcript item of a kind this lk doesn't know; Text holds its id.
	Type string `json:"type"`
	// Time is when the event happened (RFC 3339 with milliseconds), when known.
	Time string `json:"time,omitempty"`
	// Role is "user" or "assistant" for message events; a recorded transcript
	// can also hold "system" and "developer" messages.
	Role string `json:"role,omitempty"`
	// Text is the message text, error message, or log line.
	Text string `json:"text,omitempty"`
	// Interrupted marks an assistant message cut short by the user.
	Interrupted bool `json:"interrupted,omitempty"`
	// Redacted marks a recorded message the project's PII redaction rewrote.
	Redacted bool `json:"redacted,omitempty"`
	// Metrics carries per-message latencies in seconds when the agent reported
	// them: llm_ttft, tts_ttfb and e2e_latency on an assistant message, and in
	// a recorded transcript transcription_delay, end_of_turn_delay and
	// on_user_turn_completed_delay on a user message.
	Metrics map[string]float64 `json:"metrics,omitempty"`
	// Confidence is the speech-to-text confidence of a recorded user message,
	// when the agent reported it.
	Confidence *float64 `json:"confidence,omitempty"`
	// Name and Arguments describe a tool_call; Output and IsError its result.
	// Output is absent while a call is still running (history only).
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
	// From and To name the agents involved in a handoff.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Changes lists config updates (instructions/tools) for config events.
	Changes []string `json:"changes,omitempty"`
	// Earlier marks an event that happened before the turn it is reported with
	// (an opening greeting, or speech the agent produced between turns).
	Earlier bool `json:"earlier,omitempty"`
}

// isOutput reports whether the event is something the agent said or did (a
// message, tool call, or error), as opposed to a bookkeeping marker such as the
// initial agent handoff or a config update.
func (e turnEvent) isOutput() bool {
	switch e.Type {
	case "message", "tool_call", "error":
		return true
	}
	return false
}

// isAgentOutput reports whether the event is the agent responding, which
// excludes what it transcribed from the user.
func (e turnEvent) isAgentOutput() bool {
	return e.isOutput() && (e.Type != "message" || e.Role != "user")
}

// eventTimestamp formats a time the way turnEvent.Time carries it.
func eventTimestamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z07:00") }

// ── proto → turnEvent normalization ────────────────────────────────

func eventToTurnEvents(ev *agent.AgentSessionEvent) []turnEvent {
	switch e := ev.Event.(type) {
	case *agent.AgentSessionEvent_ConversationItemAdded_:
		if item := e.ConversationItemAdded.GetItem(); item != nil {
			return chatItemsToTurnEvents([]*agent.ChatContext_ChatItem{item})
		}
	case *agent.AgentSessionEvent_FunctionToolsExecuted_:
		return functionToolsToTurnEvents(e.FunctionToolsExecuted)
	case *agent.AgentSessionEvent_Error_:
		if msg := e.Error.GetMessage(); msg != "" {
			return []turnEvent{{Type: "error", Text: msg}}
		}
	}
	return nil
}

func functionToolsToTurnEvents(ft *agent.AgentSessionEvent_FunctionToolsExecuted) []turnEvent {
	if ft == nil {
		return nil
	}
	outputs := make(map[string]*agent.FunctionCallOutput, len(ft.FunctionCallOutputs))
	for _, fco := range ft.FunctionCallOutputs {
		outputs[fco.GetCallId()] = fco
	}
	events := make([]turnEvent, 0, len(ft.FunctionCalls))
	for _, fc := range ft.FunctionCalls {
		e := turnEvent{Type: "tool_call", Name: fc.GetName(), Arguments: fc.GetArguments()}
		if fco, ok := outputs[fc.GetCallId()]; ok {
			e.Output = fco.GetOutput()
			e.IsError = fco.GetIsError()
		}
		events = append(events, e)
	}
	return events
}

// chatItemsToTurnEvents converts chat items (from a live event, a RunInput
// response, or GetChatHistory) into events, pairing each function call with
// its output so a tool shows up once.
func chatItemsToTurnEvents(items []*agent.ChatContext_ChatItem) []turnEvent {
	var events []turnEvent
	callIndex := map[string]int{} // call_id → index into events
	for _, item := range items {
		switch i := item.GetItem().(type) {
		case *agent.ChatContext_ChatItem_Message:
			if e, ok := messageToTurnEvent(i.Message); ok {
				events = append(events, e)
			}
		case *agent.ChatContext_ChatItem_FunctionCall:
			fc := i.FunctionCall
			callIndex[fc.GetCallId()] = len(events)
			e := turnEvent{Type: "tool_call", Name: fc.GetName(), Arguments: fc.GetArguments()}
			if ts := fc.GetCreatedAt(); ts != nil {
				e.Time = eventTimestamp(ts.AsTime())
			}
			events = append(events, e)
		case *agent.ChatContext_ChatItem_FunctionCallOutput:
			fco := i.FunctionCallOutput
			if idx, ok := callIndex[fco.GetCallId()]; ok {
				events[idx].Output = fco.GetOutput()
				events[idx].IsError = fco.GetIsError()
			} else {
				events = append(events, turnEvent{Type: "tool_call", Name: fco.GetName(), Output: fco.GetOutput(), IsError: fco.GetIsError()})
			}
		case *agent.ChatContext_ChatItem_AgentHandoff:
			h := i.AgentHandoff
			e := turnEvent{Type: "handoff", To: h.GetNewAgentId()}
			if h.OldAgentId != nil {
				e.From = *h.OldAgentId
			}
			events = append(events, e)
		case *agent.ChatContext_ChatItem_AgentConfigUpdate:
			u := i.AgentConfigUpdate
			var changes []string
			if u.Instructions != nil {
				changes = append(changes, "instructions updated")
			}
			if len(u.ToolsAdded) > 0 {
				changes = append(changes, "tools added: "+strings.Join(u.ToolsAdded, ", "))
			}
			if len(u.ToolsRemoved) > 0 {
				changes = append(changes, "tools removed: "+strings.Join(u.ToolsRemoved, ", "))
			}
			if len(changes) > 0 {
				events = append(events, turnEvent{Type: "config", Changes: changes})
			}
		}
	}
	return events
}

func messageToTurnEvent(msg *agent.ChatMessage) (turnEvent, bool) {
	if msg == nil {
		return turnEvent{}, false
	}
	var parts []string
	for _, c := range msg.Content {
		if t := c.GetText(); t != "" {
			parts = append(parts, t)
		}
	}
	text := strings.Join(parts, "")
	role := ""
	switch msg.GetRole() {
	case agent.ChatRole_USER:
		role = "user"
	case agent.ChatRole_ASSISTANT:
		role = "assistant"
	case agent.ChatRole_SYSTEM, agent.ChatRole_DEVELOPER:
		return turnEvent{}, false
	}
	if text == "" || role == "" {
		return turnEvent{}, false
	}
	e := turnEvent{Type: "message", Role: role, Text: text, Interrupted: msg.GetInterrupted()}
	if ts := msg.GetCreatedAt(); ts != nil {
		e.Time = eventTimestamp(ts.AsTime())
	}
	e.Metrics = messageMetrics(msg.GetMetrics())
	return e, true
}

// messageMetrics is a live message's latencies in seconds, or nil when it
// reported none.
func messageMetrics(m *agent.MetricsReport) map[string]float64 {
	if m == nil {
		return nil
	}
	metrics := map[string]float64{}
	if m.LlmNodeTtft != nil {
		metrics["llm_ttft"] = *m.LlmNodeTtft
	}
	if m.TtsNodeTtfb != nil {
		metrics["tts_ttfb"] = *m.TtsNodeTtfb
	}
	if m.E2ELatency != nil {
		metrics["e2e_latency"] = *m.E2ELatency
	}
	if len(metrics) == 0 {
		return nil
	}
	return metrics
}

// ── recorded transcript → turnEvent ────────────────────────────────

// transcriptItemsToTurnEvents converts a page of a session's recorded
// transcript, as the Public API returns it, into events, pairing each tool
// call with its result so a tool shows up once, as chatItemsToTurnEvents does.
// The text came from an agent, an LLM, a tool or a participant, so what a
// terminal acts on is stripped from it; --json prints the items as sent.
func transcriptItemsToTurnEvents(items []public.TranscriptItem) []turnEvent {
	var events []turnEvent
	callIndex := map[string]int{} // call_id → index into events
	for _, it := range items {
		e := turnEvent{}
		if it.Timestamp != nil && !it.Timestamp.IsZero() {
			e.Time = eventTimestamp(*it.Timestamp)
		}
		switch {
		case it.Message != nil:
			var ok bool
			if e, ok = transcriptMessageToTurnEvent(*it.Message, e); !ok {
				continue
			}
		case it.ToolCall != nil:
			c := it.ToolCall
			e.Type, e.Name, e.Arguments = "tool_call", cleanText(c.Name), cleanText(c.Arguments)
			if id := util.Deref(c.CallId); id != "" {
				callIndex[id] = len(events)
			}
		case it.ToolResult != nil:
			r := it.ToolResult
			if idx, ok := callIndex[util.Deref(r.CallId)]; ok && util.Deref(r.CallId) != "" {
				events[idx].Output = cleanText(r.Output)
				events[idx].IsError = util.Deref(r.IsError)
				continue
			}
			e.Type, e.Name, e.Output, e.IsError = "tool_call", cleanText(r.Name), cleanText(r.Output), util.Deref(r.IsError)
		case it.AgentHandoff != nil:
			e.Type, e.From, e.To = "handoff", cleanText(it.AgentHandoff.FromAgentId), cleanText(it.AgentHandoff.ToAgentId)
		case it.ConfigUpdate != nil:
			u := it.ConfigUpdate
			if util.Deref(u.Instructions) != "" {
				e.Changes = append(e.Changes, "instructions updated")
			}
			if added := util.Deref(u.ToolsAdded); len(added) > 0 {
				e.Changes = append(e.Changes, "tools added: "+render.StripControls(strings.Join(added, ", ")))
			}
			if removed := util.Deref(u.ToolsRemoved); len(removed) > 0 {
				e.Changes = append(e.Changes, "tools removed: "+render.StripControls(strings.Join(removed, ", ")))
			}
			if len(e.Changes) == 0 {
				continue
			}
			e.Type = "config"
		default:
			e.Type, e.Text = "unknown", render.StripControls(it.ID)
		}
		events = append(events, e)
	}
	return events
}

// transcriptMessageToTurnEvent fills e from a recorded message, reporting
// false for one with no text. Its latencies arrive in milliseconds and are
// kept in seconds, as the agent reported them.
func transcriptMessageToTurnEvent(m oapi.LivekitPublicapiObservabilityV1TranscriptItemMessage, e turnEvent) (turnEvent, bool) {
	e.Type = "message"
	e.Text = cleanText(m.Text)
	if e.Text == "" {
		return e, false
	}
	switch util.Deref(m.Role) {
	case oapi.ROLEUSER:
		e.Role = "user"
	case oapi.ROLESYSTEM:
		e.Role = "system"
	case oapi.ROLEDEVELOPER:
		e.Role = "developer"
	default:
		e.Role = "assistant"
	}
	e.Interrupted = util.Deref(m.Interrupted)
	e.Redacted = util.Deref(m.Redacted)
	e.Confidence = m.TranscriptConfidence
	metrics := map[string]float64{}
	for _, l := range []struct {
		key string
		ms  *float64
	}{
		{"llm_ttft", m.LlmTtftMs},
		{"tts_ttfb", m.TtsTtfbMs},
		{"e2e_latency", m.E2eLatencyMs},
		{"transcription_delay", m.TranscriptionDelayMs},
		{"end_of_turn_delay", m.EndOfTurnDelayMs},
		{"on_user_turn_completed_delay", m.OnUserTurnCompletedDelayMs},
	} {
		if l.ms != nil {
			metrics[l.key] = *l.ms / 1000
		}
	}
	if len(metrics) > 0 {
		e.Metrics = metrics
	}
	return e, true
}

// cleanText is optional recorded text, trimmed, with what a terminal acts on
// stripped.
func cleanText(s *string) string {
	return strings.TrimSpace(render.StripControls(util.Deref(s)))
}

// agentStateName renders the proto enum as the lowercase word the SDK uses.
func agentStateName(st agent.AgentState) string {
	name := strings.TrimPrefix(st.String(), "AS_")
	return strings.ToLower(name)
}
