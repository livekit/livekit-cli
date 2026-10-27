// Copyright 2026 LiveKit, Inc.
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
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	agent "github.com/livekit/protocol/livekit/agent"
)

func TestStopSpinner_ClearsLineBeforeReturning(t *testing.T) {
	// Models the caller printing an error right after stopping the spinner:
	// by the time stop returns, the spinner line must already be cleared, or
	// the error lands on the same line as the leftover "⠋ Starting agent".
	var buf bytes.Buffer
	stop := startSpinnerTo(&buf, "Starting agent")
	stop()
	out := buf.String()
	require.True(t, strings.HasSuffix(out, "\r\033[K"),
		"spinner line not cleared when stop returned; output: %q", out)
}

func TestHandleSessionEvent_PrintsUserTurnOnce(t *testing.T) {
	itemAdded := func(item *agent.ChatContext_ChatItem) *agent.AgentSessionEvent {
		return &agent.AgentSessionEvent{Event: &agent.AgentSessionEvent_ConversationItemAdded_{
			ConversationItemAdded: &agent.AgentSessionEvent_ConversationItemAdded{Item: item},
		}}
	}
	for _, textMode := range []bool{false, true} {
		m := consoleFixture(textMode)
		// The final transcript (voice) or the send (text) already printed the
		// user's turn, so the chat item that follows must not print it again.
		require.Empty(t, m.handleSessionEvent(itemAdded(userItem("A robust chef"))), "textMode=%v", textMode)
		require.Len(t, m.handleSessionEvent(itemAdded(assistantItem("Hello!"))), 1, "textMode=%v", textMode)
	}

	m := consoleFixture(false)
	final := &agent.AgentSessionEvent{Event: &agent.AgentSessionEvent_UserInputTranscribed_{
		UserInputTranscribed: &agent.AgentSessionEvent_UserInputTranscribed{Transcript: "A robust chef", IsFinal: true},
	}}
	require.Len(t, m.handleSessionEvent(final), 1)
}

func TestHandleSessionEvent_PrintsToolStepAsOneBlock(t *testing.T) {
	m := consoleFixture(false)
	ev := &agent.AgentSessionEvent{Event: &agent.AgentSessionEvent_FunctionToolsExecuted_{
		FunctionToolsExecuted: &agent.AgentSessionEvent_FunctionToolsExecuted{
			FunctionCalls: []*agent.FunctionCall{{CallId: "a", Name: "first"}, {CallId: "b", Name: "second"}},
		},
	}}
	require.Len(t, m.handleSessionEvent(ev), 1)
}

func TestHandleSessionEvent_MetricsFromTextlessReply(t *testing.T) {
	m := consoleFixture(false)
	ttfb := 0.25
	ev := &agent.AgentSessionEvent{Event: &agent.AgentSessionEvent_ConversationItemAdded_{
		ConversationItemAdded: &agent.AgentSessionEvent_ConversationItemAdded{Item: &agent.ChatContext_ChatItem{
			Item: &agent.ChatContext_ChatItem_Message{Message: &agent.ChatMessage{
				Role:    agent.ChatRole_ASSISTANT,
				Metrics: &agent.MetricsReport{TtsNodeTtfb: &ttfb},
			}},
		}},
	}}
	require.Empty(t, m.handleSessionEvent(ev))
	require.NotEmpty(t, m.metricsText)
}
