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
	"strings"
	"testing"

	"github.com/livekit/protocol/livekit"
	agent "github.com/livekit/protocol/livekit/agent"
	"github.com/stretchr/testify/require"
)

func chatMsg(role agent.ChatRole, text string) *agent.ChatContext_ChatItem {
	return &agent.ChatContext_ChatItem{
		Item: &agent.ChatContext_ChatItem_Message{
			Message: &agent.ChatMessage{
				Role: role,
				Content: []*agent.ChatMessage_ChatContent{
					{Payload: &agent.ChatMessage_ChatContent_Text{Text: text}},
				},
			},
		},
	}
}

// speakerOfLine returns the nearest speaker header ("You"/"Agent") above the
// first transcript line containing needle.
func speakerOfLine(t *testing.T, transcript, needle string) string {
	t.Helper()
	lines := strings.Split(transcript, "\n")
	idx := -1
	for i, line := range lines {
		if strings.Contains(line, needle) {
			idx = i
			break
		}
	}
	require.NotEqual(t, -1, idx, "line %q not found in transcript:\n%s", needle, transcript)
	for i := idx - 1; i >= 0; i-- {
		switch strings.TrimSpace(lines[i]) {
		case "You", "Agent":
			return strings.TrimSpace(lines[i])
		}
	}
	t.Fatalf("no speaker header above %q in transcript:\n%s", needle, transcript)
	return ""
}

func TestRenderChatTranscript_ToolCallsAttributedToAgent(t *testing.T) {
	oldAgent := "get_email_task"
	m := &simulateModel{
		width: 100,
		summary: &livekit.SimulationRunSummary{
			ChatHistory: map[string]*agent.ChatContext{
				"job1": {Items: []*agent.ChatContext_ChatItem{
					chatMsg(agent.ChatRole_ASSISTANT, "Could you please provide your email address?"),
					// The agent's tool call and handoff happen after the user
					// message and before the agent's spoken reply.
					chatMsg(agent.ChatRole_USER, "Do you really need that? It's a@b.com."),
					{Item: &agent.ChatContext_ChatItem_FunctionCall{
						FunctionCall: &agent.FunctionCall{Name: "update_email_address", Arguments: `{"email":"a@b.com"}`},
					}},
					{Item: &agent.ChatContext_ChatItem_AgentHandoff{
						AgentHandoff: &agent.AgentHandoff{OldAgentId: &oldAgent, NewAgentId: "front_desk_agent"},
					}},
					chatMsg(agent.ChatRole_ASSISTANT, "Got it, you're all set."),
				}},
			},
		},
	}

	out := m.renderChatTranscript("job1")

	require.Equal(t, "Agent", speakerOfLine(t, out, "ƒ update_email_address"),
		"tool calls are agent actions and must not render under the user's block")
	require.Equal(t, "Agent", speakerOfLine(t, out, "⤳ get_email_task → front_desk_agent"),
		"handoffs are agent actions and must not render under the user's block")
	require.Equal(t, "You", speakerOfLine(t, out, "Do you really need that?"))
	require.Equal(t, "Agent", speakerOfLine(t, out, "Got it, you're all set."))
}
