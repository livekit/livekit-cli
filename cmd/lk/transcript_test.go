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
	"testing"

	"github.com/stretchr/testify/require"

	agent "github.com/livekit/protocol/livekit/agent"
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
	require.Equal(t, "", renderTurnEvent(turnEvent{Type: "message", Role: "system", Text: "hidden"}, renderOptions{}))

	metrics := plain(turnEvent{Type: "message", Role: "assistant", Text: "x", Metrics: map[string]float64{"llm_ttft": 0.45}}, renderOptions{Metrics: true})
	require.Contains(t, metrics, "llm_ttft 450ms")
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
