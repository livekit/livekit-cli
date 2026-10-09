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
	"fmt"
	"testing"

	"github.com/livekit/protocol/livekit/agent"
	"github.com/stretchr/testify/require"
)

// recordedOverlay collects the transcript messages a session sends.
type recordedOverlay struct {
	turns []string            // turn ids in the order they first appear
	text  map[string]string   // "turn/segment" → latest text
	seen  map[string]struct{} // turn ids seen
}

func (r *recordedOverlay) Send(msg any) error {
	t, ok := msg.(overlayTranscript)
	if !ok {
		return nil
	}
	if _, ok := r.seen[t.Turn]; !ok {
		r.seen[t.Turn] = struct{}{}
		r.turns = append(r.turns, t.Turn)
	}
	r.text[fmt.Sprintf("%s/%d", t.Turn, t.Segment)] = t.Text
	return nil
}

func newRecordedSession() (*assistantSession, *recordedOverlay) {
	rec := &recordedOverlay{text: map[string]string{}, seen: map[string]struct{}{}}
	s := newAssistantSession(nil, nil)
	s.out = rec
	return s, rec
}

func TestAssistantTurnsConversation(t *testing.T) {
	s, rec := newRecordedSession()

	// The agent greets.
	s.beginAgentSpeech()
	s.agentText("Hi.")
	s.agentFlush()
	s.agentMessage(`<expr type="expression" label="happy"/> Hi.`)

	// The user asks something.
	s.userTranscript("How do I", false)
	s.userTranscript("How do I deploy?", true)
	s.userCommitted("How do I deploy?")

	// The agent answers.
	s.beginAgentSpeech()
	s.agentText("Run lk agent create.")
	s.agentFlush()
	s.agentMessage("Run lk agent create.")

	require.Equal(t, []string{"a1", "u2", "a3"}, rec.turns)
	require.Equal(t, "Hi.", rec.text["a1/0"])
	require.Equal(t, "How do I deploy?", rec.text["u2/0"])
	require.Equal(t, "Run lk agent create.", rec.text["a3/0"])
}

func TestAssistantTurnsInterruption(t *testing.T) {
	s, rec := newRecordedSession()

	// The agent starts speaking.
	s.beginAgentSpeech()
	s.agentText("You can")
	s.agentText(" use the")

	// The user interrupts while words are still arriving.
	s.userTranscript("Wait", false)
	s.agentText(" starter")
	s.agentFlush()
	s.agentMessage("You can use the starter")
	s.userTranscript("Wait, in Node?", true)
	s.userCommitted("Wait, in Node?")

	// The agent replies to the interruption.
	s.beginAgentSpeech()
	s.agentText("Yes, in Node too.")
	s.agentFlush()
	s.agentMessage("Yes, in Node too.")

	// No turn is duplicated: the end of the interrupted speech stays in its
	// turn, and the user's speech stays in one turn.
	require.Equal(t, []string{"a1", "u2", "a3"}, rec.turns)
	require.Equal(t, "You can use the starter", rec.text["a1/0"])
	require.Equal(t, "Wait, in Node?", rec.text["u2/0"])
	require.Equal(t, "Yes, in Node too.", rec.text["a3/0"])
}

func TestAssistantTurnsUnstreamedMessage(t *testing.T) {
	s, rec := newRecordedSession()

	// Text that wasn't streamed shows from the committed message, without
	// expressive markup.
	s.agentMessage(`<expr type="expression" label="calm"/> Hello <expr type="prosody" label="emphasis">there</expr>.`)

	require.Equal(t, []string{"a1"}, rec.turns)
	require.Equal(t, "Hello there.", rec.text["a1/0"])
}

func TestAssistantTurnsPause(t *testing.T) {
	s, rec := newRecordedSession()

	// The user pauses mid-thought, and the agent commits each part before
	// it replies.
	s.userTranscript("I want to build something", true)
	s.userCommitted("I want to build something")
	s.userTranscript("for my", false)
	s.userTranscript("for my support team.", true)
	s.userCommitted("for my support team.")

	s.beginAgentSpeech()
	s.agentText("Great.")
	s.agentFlush()
	s.agentMessage("Great.")

	require.Equal(t, []string{"u1", "a2"}, rec.turns)
	require.Equal(t, "I want to build something for my support team.", rec.text["u1/0"])
}

func TestAssistantTurnsEarlyCommit(t *testing.T) {
	s, rec := newRecordedSession()

	// The turn detector ends the user's turn mid-sentence, and the agent
	// starts looking things up before the rest arrives.
	s.userTranscript("I want a voice agent for", true)
	s.userCommitted("I want a voice agent for")
	s.toolCall(&agent.FunctionCall{CallId: "c1", Name: "get_docs_overview", Arguments: "{}"})
	s.userTranscript("restaurant reservations.", true)
	s.userCommitted("restaurant reservations.")

	s.beginAgentSpeech()
	s.agentText("Sounds good.")
	s.agentFlush()
	s.agentMessage("Sounds good.")

	// The next exchange starts new turns.
	s.userTranscript("What about Node?", true)
	s.userCommitted("What about Node?")
	s.beginAgentSpeech()
	s.agentText("Node works too.")
	s.agentFlush()
	s.agentMessage("Node works too.")

	require.Equal(t, []string{"u1", "a2", "u3", "a4"}, rec.turns)
	require.Equal(t, "I want a voice agent for restaurant reservations.", rec.text["u1/0"])
	require.Equal(t, "What about Node?", rec.text["u3/0"])
	require.Equal(t, "Node works too.", rec.text["a4/0"])
}

func TestIsAuthError(t *testing.T) {
	require.True(t, isAuthError("type='llm_error' timestamp=1760024012.5 label='livekit.agents.inference.llm.LLM' error=APIStatusError('unauthorized', status_code=401, request_id=None) recoverable=False"))
	require.True(t, isAuthError("Invalid API key"))
	// A timestamp that happens to contain 401 isn't an auth error.
	require.False(t, isAuthError("type='tts_error' timestamp=1791565401.871234 label='livekit.agents.inference.tts.TTS' error=APIConnectionError('connection failed') recoverable=True"))
}
