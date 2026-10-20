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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	agent "github.com/livekit/protocol/livekit/agent"

	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

func TestChatItemsToTurnEventsPairsToolCalls(t *testing.T) {
	old := "receptionist"
	items := []*agent.ChatContext_ChatItem{
		{Item: &agent.ChatContext_ChatItem_AgentHandoff{AgentHandoff: &agent.AgentHandoff{NewAgentId: "receptionist"}}},
		userItem("weather?"),
		{Item: &agent.ChatContext_ChatItem_FunctionCall{FunctionCall: &agent.FunctionCall{CallId: "c1", Name: "lookup_weather", Arguments: `{"location":"Tokyo"}`}}},
		{Item: &agent.ChatContext_ChatItem_FunctionCallOutput{FunctionCallOutput: &agent.FunctionCallOutput{CallId: "c1", Output: "Sunny", IsError: false}}},
		assistantItem("It is sunny."),
		{Item: &agent.ChatContext_ChatItem_AgentHandoff{AgentHandoff: &agent.AgentHandoff{OldAgentId: &old, NewAgentId: "billing"}}},
		// A system message never surfaces.
		{Item: &agent.ChatContext_ChatItem_Message{Message: &agent.ChatMessage{Role: agent.ChatRole_SYSTEM,
			Content: []*agent.ChatMessage_ChatContent{{Payload: &agent.ChatMessage_ChatContent_Text{Text: "secret"}}}}}},
	}
	events := chatItemsToTurnEvents(items)
	require.Len(t, events, 5)
	require.Equal(t, turnEvent{Type: "handoff", To: "receptionist"}, events[0])
	require.Equal(t, turnEvent{Type: "message", Role: "user", Text: "weather?"}, events[1])
	require.Equal(t, turnEvent{Type: "tool_call", Name: "lookup_weather", Arguments: `{"location":"Tokyo"}`, Output: "Sunny"}, events[2])
	require.Equal(t, turnEvent{Type: "message", Role: "assistant", Text: "It is sunny."}, events[3])
	require.Equal(t, turnEvent{Type: "handoff", From: "receptionist", To: "billing"}, events[4])
}

func TestRenderTurnEvent(t *testing.T) {
	plain := func(e turnEvent, opts renderOptions) string {
		return ansiEscapeRe.ReplaceAllString(renderTurnEvent(e, opts), "")
	}
	require.Equal(t, "\n  ● You\n    hi", plain(turnEvent{Type: "message", Role: "user", Text: "hi"}, renderOptions{}))
	require.Equal(t, "\n  ● Agent\n    line one\n    line two",
		plain(turnEvent{Type: "message", Role: "assistant", Text: "line one\nline two"}, renderOptions{}))
	require.Contains(t, plain(turnEvent{Type: "message", Role: "assistant", Text: "x", Earlier: true}, renderOptions{}), "(before this turn)")
	require.Contains(t, plain(turnEvent{Type: "message", Role: "assistant", Text: "x", Interrupted: true}, renderOptions{}), "(interrupted)")

	tool := plain(turnEvent{Type: "tool_call", Name: "lookup", Arguments: `{"q":1}`, Output: "42"}, renderOptions{})
	require.Equal(t, "\n  ● tool: lookup({\"q\":1})\n    ↳ 42", tool)
	require.Equal(t, "\n  ● tool: transfer()\n    ✗ boom",
		plain(turnEvent{Type: "tool_call", Name: "transfer", Arguments: "{}", Output: "boom", IsError: true}, renderOptions{}))

	// Tool output is never truncated: seeing exactly what a tool returned is
	// the point of the debugger.
	long := strings.Repeat("x", 5000)
	require.Contains(t, plain(turnEvent{Type: "tool_call", Name: "big", Output: long}, renderOptions{}), long)

	require.Equal(t, "\n  ● handoff: a → b", plain(turnEvent{Type: "handoff", From: "a", To: "b"}, renderOptions{}))
	require.Equal(t, "\n  ● agent: a", plain(turnEvent{Type: "handoff", To: "a"}, renderOptions{}))
	require.Equal(t, "    │ 12:00 INFO hi", plain(turnEvent{Type: "log", Text: "12:00 INFO hi"}, renderOptions{}))
	require.Equal(t, "\n  ✗ agent error: bad", plain(turnEvent{Type: "error", Text: "bad"}, renderOptions{}))
	// Only a recorded transcript carries system and developer messages; the
	// live conversions drop them.
	require.Equal(t, "\n  ● System\n    be nice", plain(turnEvent{Type: "message", Role: "system", Text: "be nice"}, renderOptions{}))
	require.Equal(t, "", renderTurnEvent(turnEvent{Type: "message", Role: "tool", Text: "hidden"}, renderOptions{}))
	require.Equal(t, "\n  ● item item_9 is of a kind this lk doesn't know; see --json", plain(turnEvent{Type: "unknown", Text: "item_9"}, renderOptions{}))

	metrics := plain(turnEvent{Type: "message", Role: "assistant", Text: "x", Metrics: map[string]float64{"llm_ttft": 0.45}}, renderOptions{Metrics: true})
	require.Contains(t, metrics, "llm_ttft 450ms")
}

// TestRenderTurnEventRecorded checks the options a recorded transcript
// renders with: times, a named user, user metrics and redaction.
func TestRenderTurnEventRecorded(t *testing.T) {
	prevLocal := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = prevLocal })

	plain := func(e turnEvent, opts renderOptions) string {
		return ansiEscapeRe.ReplaceAllString(renderTurnEvent(e, opts), "")
	}
	opts := renderOptions{Metrics: true, Times: true, UserLabel: "User"}
	confidence := 0.93
	require.Equal(t, "\n  ● User 11:00:01 (redacted)\n    hi\n    ⏱ end_of_turn 320ms · confidence 0.93",
		plain(turnEvent{Type: "message", Role: "user", Text: "hi", Time: "2026-10-07T11:00:01Z", Redacted: true,
			Metrics: map[string]float64{"end_of_turn_delay": 0.32}, Confidence: &confidence}, opts))
	require.Equal(t, "\n  ● tool: lookup() 11:00:03\n    ↳ 42",
		plain(turnEvent{Type: "tool_call", Name: "lookup", Output: "42", Time: "2026-10-07T11:00:03Z"}, opts))
	require.Equal(t, "\n  ● handoff: a → b 11:00:05",
		plain(turnEvent{Type: "handoff", From: "a", To: "b", Time: "2026-10-07T11:00:05Z"}, opts))
	// No time, no suffix.
	require.Equal(t, "\n  ● User\n    hi", plain(turnEvent{Type: "message", Role: "user", Text: "hi"}, opts))
}

func TestTranscriptItemsToTurnEvents(t *testing.T) {
	const raw = `[
	  {"id": "item_0", "timestamp": "2026-10-07T11:00:00Z", "agentHandoff": {"toAgentId": "greeter"}},
	  {"id": "item_1", "timestamp": "2026-10-07T11:00:01Z", "message": {"role": "ROLE_USER", "text": " hi\nthere ", "transcriptConfidence": 0.93, "endOfTurnDelayMs": 320}},
	  {"id": "item_2", "timestamp": "2026-10-07T11:00:02Z", "message": {"role": "ROLE_AGENT", "text": "hello", "interrupted": true, "e2eLatencyMs": 820}},
	  {"id": "item_3", "message": {"role": "ROLE_SYSTEM", "text": "be nice", "redacted": true}},
	  {"id": "item_4", "message": {"role": "ROLE_AGENT", "text": ""}},
	  {"id": "item_5", "timestamp": "2026-10-07T11:00:03Z", "toolCall": {"name": "lookup", "callId": "c1", "arguments": "{\"q\":1}"}},
	  {"id": "item_6", "toolResult": {"name": "lookup", "callId": "c1", "output": "boom", "isError": true}},
	  {"id": "item_7", "toolResult": {"name": "orphan", "callId": "c9", "output": "42"}},
	  {"id": "item_8", "configUpdate": {"instructions": "new", "toolsAdded": ["book"]}},
	  {"id": "item_9", "configUpdate": {}},
	  {"id": "item_10"}
	]`
	var items []public.TranscriptItem
	require.NoError(t, json.Unmarshal([]byte(raw), &items))
	confidence := 0.93
	require.Equal(t, []turnEvent{
		{Type: "handoff", Time: "2026-10-07T11:00:00.000Z", To: "greeter"},
		{Type: "message", Time: "2026-10-07T11:00:01.000Z", Role: "user", Text: "hi\nthere",
			Metrics: map[string]float64{"end_of_turn_delay": 0.32}, Confidence: &confidence},
		{Type: "message", Time: "2026-10-07T11:00:02.000Z", Role: "assistant", Text: "hello", Interrupted: true,
			Metrics: map[string]float64{"e2e_latency": 0.82}},
		{Type: "message", Role: "system", Text: "be nice", Redacted: true},
		// The result joins its call, which keeps the call's time.
		{Type: "tool_call", Time: "2026-10-07T11:00:03.000Z", Name: "lookup", Arguments: `{"q":1}`, Output: "boom", IsError: true},
		{Type: "tool_call", Name: "orphan", Output: "42"},
		{Type: "config", Changes: []string{"instructions updated", "tools added: book"}},
		{Type: "unknown", Text: "item_10"},
	}, transcriptItemsToTurnEvents(items))
}

// TestTranscriptItemsToTurnEventsStripsEscapes checks what an LLM, a tool or
// a participant put in a recorded transcript reaches the renderer with the
// control characters a terminal acts on stripped, newlines kept.
func TestTranscriptItemsToTurnEventsStripsEscapes(t *testing.T) {
	text := "hi\x1b]0;pwned\x07\x1b[2J\nthere\u009b"
	items := []public.TranscriptItem{
		{Message: &oapi.LivekitPublicapiObservabilityV1TranscriptItemMessage{Text: &text}},
		{ToolCall: &oapi.LivekitPublicapiObservabilityV1TranscriptItemToolCall{Name: &text, Arguments: &text}},
		{ToolResult: &oapi.LivekitPublicapiObservabilityV1TranscriptItemToolResult{Name: &text, Output: &text}},
		{AgentHandoff: &oapi.LivekitPublicapiObservabilityV1TranscriptItemAgentHandoff{FromAgentId: &text, ToAgentId: &text}},
		{ConfigUpdate: &oapi.LivekitPublicapiObservabilityV1TranscriptItemConfigUpdate{ToolsAdded: &[]string{text}}},
		{ID: text},
	}
	events := transcriptItemsToTurnEvents(items)
	require.Len(t, events, len(items))
	b, err := json.Marshal(events)
	require.NoError(t, err)
	for _, c := range []string{`\u001b`, `\u0007`, `\u009b`} {
		require.NotContains(t, string(b), c)
	}
	require.Equal(t, "hi]0;pwned[2J\nthere", events[0].Text)
}

func TestRenderEventLine(t *testing.T) {
	plain := func(e turnEvent) string { return ansiEscapeRe.ReplaceAllString(renderEventLine(e), "") }
	line := plain(turnEvent{Type: "tool_call", Time: "2026-09-16T10:00:00.123Z", Name: "lookup", Arguments: "{\"q\":\n 1}", Output: "line one\nline two"})
	require.Contains(t, line, "tool    lookup({\"q\": 1}) ↳ line one line two")
	require.NotContains(t, line, "\n")
	require.Contains(t, plain(turnEvent{Type: "message", Role: "user", Text: "hi"}), "user    hi")
	require.Contains(t, plain(turnEvent{Type: "state", From: "listening", To: "thinking"}), "state   listening → thinking")
	require.Contains(t, plain(turnEvent{Type: "handoff", From: "a", To: "b"}), "handoff a → b")
	require.Contains(t, plain(turnEvent{Type: "tool_call", Name: "x", Output: "boom", IsError: true}), "✗ boom")
}
