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
	"bytes"
	"context"
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/console"
	"github.com/livekit/livekit-cli/v2/pkg/ipc"

	agent "github.com/livekit/protocol/livekit/agent"
)

// fakeAgent stands in for the Python/Node agent on the far end of the console
// IPC socket: it records requests and lets the test script events/responses.
type fakeAgent struct {
	t      *testing.T
	conn   net.Conn
	reqs   chan *agent.SessionRequest
	acks   chan struct{} // AudioPlaybackFinished acks from the CLI
	spoken chan string   // text the session asked TTS to speak
	heard  atomic.Int64  // speech samples (non-silence) received on the mic input
}

// fakeSpeech is what the fake TTS produces for any text: 100ms of a constant,
// non-silent sample, so the mic input can be told apart from silence.
const fakeSpeechSamples = 4800

// newFakeAgent connects a text-mode session to a fake agent.
func newFakeAgent(t *testing.T) (*fakeAgent, *agentSession) {
	t.Helper()
	return newFakeAgentMode(t, false)
}

// newAudioFakeAgent connects an audio-mode session to a fake agent, with a
// fake TTS that records what it was asked to speak.
func newAudioFakeAgent(t *testing.T) (*fakeAgent, *agentSession) {
	t.Helper()
	return newFakeAgentMode(t, true)
}

func newFakeAgentMode(t *testing.T, audio bool) (*fakeAgent, *agentSession) {
	t.Helper()
	client, server := net.Pipe()
	fa := &fakeAgent{
		t: t, conn: client,
		reqs:   make(chan *agent.SessionRequest, 16),
		acks:   make(chan struct{}, 16),
		spoken: make(chan string, 16),
	}
	go func() {
		for {
			msg := &agent.AgentSessionMessage{}
			if err := ipc.ReadProto(client, msg); err != nil {
				close(fa.reqs)
				return
			}
			switch m := msg.Message.(type) {
			case *agent.AgentSessionMessage_Request:
				fa.reqs <- m.Request
			case *agent.AgentSessionMessage_AudioPlaybackFinished:
				fa.acks <- struct{}{}
			case *agent.AgentSessionMessage_AudioInput:
				for _, v := range console.BytesToSamples(m.AudioInput.Data) {
					if v != 0 {
						fa.heard.Add(1)
					}
				}
			}
		}
	}()
	var speak speakFunc = func(ctx context.Context, text string, onAudio func([]int16)) error {
		fa.spoken <- text
		speech := make([]int16, fakeSpeechSamples)
		for i := range speech {
			speech[i] = 1000
		}
		onAudio(speech)
		return nil
	}
	if !audio {
		speak = nil
	}
	sess := newAgentSession(server, nil, speak)
	t.Cleanup(func() { client.Close(); server.Close() })
	return fa, sess
}

func (fa *fakeAgent) send(msg *agent.AgentSessionMessage) {
	require.NoError(fa.t, ipc.WriteProto(fa.conn, msg))
}

func (fa *fakeAgent) sendEvent(ev *agent.AgentSessionEvent) {
	fa.send(&agent.AgentSessionMessage{Message: &agent.AgentSessionMessage_Event{Event: ev}})
}

func (fa *fakeAgent) sendState(st agent.AgentState) {
	fa.sendEvent(&agent.AgentSessionEvent{Event: &agent.AgentSessionEvent_AgentStateChanged_{
		AgentStateChanged: &agent.AgentSessionEvent_AgentStateChanged{NewState: st},
	}})
}

func (fa *fakeAgent) sendAssistant(text string) {
	fa.sendEvent(&agent.AgentSessionEvent{Event: &agent.AgentSessionEvent_ConversationItemAdded_{
		ConversationItemAdded: &agent.AgentSessionEvent_ConversationItemAdded{Item: assistantItem(text)},
	}})
}

func (fa *fakeAgent) sendUser(text string) {
	fa.sendEvent(&agent.AgentSessionEvent{Event: &agent.AgentSessionEvent_ConversationItemAdded_{
		ConversationItemAdded: &agent.AgentSessionEvent_ConversationItemAdded{Item: userItem(text)},
	}})
}

func (fa *fakeAgent) sendError(msg string) {
	fa.sendEvent(&agent.AgentSessionEvent{Event: &agent.AgentSessionEvent_Error_{
		Error: &agent.AgentSessionEvent_Error{Message: msg},
	}})
}

// nextSpoken waits for the session to speak a user turn.
func (fa *fakeAgent) nextSpoken() string {
	select {
	case text := <-fa.spoken:
		return text
	case <-time.After(5 * time.Second):
		fa.t.Fatal("timed out waiting for the CLI to speak a turn")
		return ""
	}
}

func (fa *fakeAgent) respond(resp *agent.SessionResponse) {
	fa.send(&agent.AgentSessionMessage{Message: &agent.AgentSessionMessage_Response{Response: resp}})
}

func (fa *fakeAgent) nextRequest() *agent.SessionRequest {
	select {
	case r := <-fa.reqs:
		return r
	case <-time.After(5 * time.Second):
		fa.t.Fatal("timed out waiting for a request from the CLI")
		return nil
	}
}

func assistantItem(text string) *agent.ChatContext_ChatItem {
	return &agent.ChatContext_ChatItem{Item: &agent.ChatContext_ChatItem_Message{Message: &agent.ChatMessage{
		Role:    agent.ChatRole_ASSISTANT,
		Content: []*agent.ChatMessage_ChatContent{{Payload: &agent.ChatMessage_ChatContent_Text{Text: text}}},
	}}}
}

func userItem(text string) *agent.ChatContext_ChatItem {
	return &agent.ChatContext_ChatItem{Item: &agent.ChatContext_ChatItem_Message{Message: &agent.ChatMessage{
		Role:    agent.ChatRole_USER,
		Content: []*agent.ChatMessage_ChatContent{{Payload: &agent.ChatMessage_ChatContent_Text{Text: text}}},
	}}}
}

func TestAgentSessionSayStreamsEventsAndEarlierItems(t *testing.T) {
	fa, sess := newFakeAgent(t)

	// A greeting nobody was listening for must be held, not dropped.
	fa.sendAssistant("Welcome!")
	require.Eventually(t, func() bool {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		return len(sess.undelivered) == 1
	}, time.Second, 10*time.Millisecond)

	var got []turnEvent
	done := make(chan struct{})
	var res turnResult
	var sayErr error
	go func() {
		defer close(done)
		res, sayErr = sess.Say(context.Background(), "hello", func(e turnEvent) { got = append(got, e) })
	}()

	req := fa.nextRequest()
	require.Equal(t, "hello", req.GetRunInput().GetText())
	require.True(t, sess.TurnInProgress())

	fa.sendEvent(&agent.AgentSessionEvent{Event: &agent.AgentSessionEvent_FunctionToolsExecuted_{
		FunctionToolsExecuted: &agent.AgentSessionEvent_FunctionToolsExecuted{
			FunctionCalls:       []*agent.FunctionCall{{CallId: "c1", Name: "lookup", Arguments: `{"q":1}`}},
			FunctionCallOutputs: []*agent.FunctionCallOutput{{CallId: "c1", Output: "42"}},
		},
	}})
	fa.sendAssistant("The answer is 42.")
	fa.respond(&agent.SessionResponse{RequestId: req.RequestId, Response: &agent.SessionResponse_RunInput{
		RunInput: &agent.SessionResponse_RunInputResponse{},
	}})

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Say did not return")
	}
	require.NoError(t, sayErr)
	require.False(t, sess.TurnInProgress())
	require.Equal(t, "The answer is 42.", res.Reply)
	require.False(t, res.Silent)

	types := make([]string, 0, len(got))
	for _, e := range got {
		types = append(types, e.Type+"/"+e.Role)
	}
	require.Equal(t, []string{"message/assistant", "message/user", "tool_call/", "message/assistant"}, types)
	require.True(t, got[0].Earlier, "greeting should be flagged as predating the turn")
	require.Equal(t, "Welcome!", got[0].Text)
	require.Equal(t, "lookup", got[2].Name)
	require.Equal(t, `{"q":1}`, got[2].Arguments)
	require.Equal(t, "42", got[2].Output)
	require.Equal(t, 1, sess.Turns())
}

func TestAgentSessionSayReportsAgentErrorAndSilence(t *testing.T) {
	fa, sess := newFakeAgent(t)

	done := make(chan struct{})
	var res turnResult
	var sayErr error
	go func() {
		defer close(done)
		res, sayErr = sess.Say(context.Background(), "hi", func(turnEvent) {})
	}()
	req := fa.nextRequest()
	errMsg := "llm exploded"
	fa.respond(&agent.SessionResponse{RequestId: req.RequestId, Error: &errMsg})
	<-done
	require.ErrorContains(t, sayErr, "llm exploded")
	require.True(t, res.Silent)
}

func TestAgentSessionSayTimeoutLeavesLateEventsForNextTurn(t *testing.T) {
	fa, sess := newFakeAgent(t)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := sess.Say(ctx, "slow", func(turnEvent) {})
	require.ErrorContains(t, err, "timed out")
	require.False(t, sess.TurnInProgress())

	// The agent finishes late: its output is held for the next turn.
	fa.sendAssistant("late reply")
	require.Eventually(t, func() bool {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		return len(sess.undelivered) == 1
	}, time.Second, 10*time.Millisecond)
	earlier := sess.takeUndelivered()
	require.Len(t, earlier, 1)
	require.True(t, earlier[0].Earlier)
}

func TestAgentSessionRoutesConcurrentResponses(t *testing.T) {
	fa, sess := newFakeAgent(t)

	// A history request issued while a turn is in flight must get its own
	// response, not the turn's.
	sayDone := make(chan error, 1)
	go func() {
		_, err := sess.Say(context.Background(), "hi", func(turnEvent) {})
		sayDone <- err
	}()
	sayReq := fa.nextRequest()

	histDone := make(chan []turnEvent, 1)
	go func() {
		events, err := sess.History(context.Background())
		require.NoError(t, err)
		histDone <- events
	}()
	histReq := fa.nextRequest()
	require.NotNil(t, histReq.GetGetChatHistory())

	fa.respond(&agent.SessionResponse{RequestId: histReq.RequestId, Response: &agent.SessionResponse_GetChatHistory{
		GetChatHistory: &agent.SessionResponse_GetChatHistoryResponse{Items: []*agent.ChatContext_ChatItem{
			userItem("hi"), assistantItem("hello"),
		}},
	}})
	events := <-histDone
	require.Len(t, events, 2)
	require.Equal(t, "user", events[0].Role)
	require.Equal(t, "assistant", events[1].Role)

	fa.respond(&agent.SessionResponse{RequestId: sayReq.RequestId, Response: &agent.SessionResponse_RunInput{
		RunInput: &agent.SessionResponse_RunInputResponse{},
	}})
	require.NoError(t, <-sayDone)
}

func TestAgentSessionAcksPlaybackFlush(t *testing.T) {
	// Without this ack the agent's turn never completes in text mode.
	fa, _ := newFakeAgent(t)
	fa.send(&agent.AgentSessionMessage{Message: &agent.AgentSessionMessage_AudioPlaybackFlush{
		AudioPlaybackFlush: &agent.AgentSessionMessage_ConsoleIO_AudioPlaybackFlush{},
	}})
	select {
	case <-fa.acks:
	case <-time.After(2 * time.Second):
		t.Fatal("no AudioPlaybackFinished ack for the flush")
	}
}

func TestWaitForGreeting(t *testing.T) {
	t.Run("returns after settle when the agent stays quiet", func(t *testing.T) {
		fa, sess := newFakeAgent(t)
		fa.sendState(agent.AgentState_AS_LISTENING)
		start := time.Now()
		sess.WaitForGreeting(context.Background(), 100*time.Millisecond, 5*time.Second)
		require.Less(t, time.Since(start), time.Second)
		require.Empty(t, sess.takeUndelivered())
	})

	t.Run("waits through a reply and returns once it lands", func(t *testing.T) {
		fa, sess := newFakeAgent(t)
		go func() {
			fa.sendState(agent.AgentState_AS_THINKING)
			time.Sleep(300 * time.Millisecond) // longer than settle
			fa.sendAssistant("Welcome to Acme")
			fa.sendState(agent.AgentState_AS_LISTENING)
		}()
		sess.WaitForGreeting(context.Background(), 100*time.Millisecond, 5*time.Second)
		greeting := sess.takeUndelivered()
		require.Len(t, greeting, 1)
		require.Equal(t, "Welcome to Acme", greeting[0].Text)
	})

	t.Run("gives up at maxWait if the agent never finishes", func(t *testing.T) {
		fa, sess := newFakeAgent(t)
		fa.sendState(agent.AgentState_AS_THINKING)
		start := time.Now()
		sess.WaitForGreeting(context.Background(), 50*time.Millisecond, 300*time.Millisecond)
		require.InDelta(t, 300, time.Since(start).Milliseconds(), 250)
	})
}

func TestControlFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	ev := turnEvent{Type: "tool_call", Name: "lookup", Arguments: `{"q":1}`, Output: "42"}
	require.NoError(t, writeControlFrame(&buf, controlReply{Event: &ev}))
	require.NoError(t, writeControlFrame(&buf, controlReply{Done: true, Reply: "42", DurationMs: 12}))

	var first, second controlReply
	require.NoError(t, readControlFrame(&buf, &first))
	require.NoError(t, readControlFrame(&buf, &second))
	require.Equal(t, ev, *first.Event)
	require.True(t, second.Done)
	require.Equal(t, "42", second.Reply)

	// The JSON schema is the contract coding agents parse: keep the field
	// names stable.
	data, err := json.Marshal(ev)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"tool_call","name":"lookup","arguments":"{\"q\":1}","output":"42"}`, string(data))
}

func TestListen(t *testing.T) {
	t.Run("returns silent after idle when nothing happens", func(t *testing.T) {
		_, sess := newFakeAgent(t)
		start := time.Now()
		var got []turnEvent
		delivered := sess.Listen(context.Background(), 150*time.Millisecond, 5*time.Second, func(e turnEvent) { got = append(got, e) })
		require.False(t, delivered)
		require.Empty(t, got)
		require.Less(t, time.Since(start), time.Second)
	})

	t.Run("delivers buffered events immediately", func(t *testing.T) {
		fa, sess := newFakeAgent(t)
		fa.sendAssistant("earlier")
		require.Eventually(t, func() bool {
			sess.mu.Lock()
			defer sess.mu.Unlock()
			return len(sess.undelivered) == 1
		}, time.Second, 10*time.Millisecond)
		var got []turnEvent
		delivered := sess.Listen(context.Background(), 5*time.Second, 5*time.Second, func(e turnEvent) { got = append(got, e) })
		require.True(t, delivered)
		require.Len(t, got, 1)
		require.Equal(t, "earlier", got[0].Text)
	})

	t.Run("collects a burst that starts late and stops when the agent is idle", func(t *testing.T) {
		fa, sess := newFakeAgent(t)
		go func() {
			time.Sleep(200 * time.Millisecond)
			fa.sendState(agent.AgentState_AS_THINKING)
			time.Sleep(400 * time.Millisecond) // longer than listenSettle
			fa.sendAssistant("reminder one")
			fa.sendAssistant("reminder two")
			fa.sendState(agent.AgentState_AS_LISTENING)
		}()
		var got []turnEvent
		delivered := sess.Listen(context.Background(), 2*time.Second, 5*time.Second, func(e turnEvent) { got = append(got, e) })
		require.True(t, delivered)
		require.Len(t, got, 2)
		require.Equal(t, "reminder two", got[1].Text)
	})
}

func TestListenIgnoresMarkersForTiming(t *testing.T) {
	fa, sess := newFakeAgent(t)
	// The initial agent marker arrives at session start; it must not make Listen
	// (and therefore the greeting wait) give up early.
	fa.sendEvent(&agent.AgentSessionEvent{Event: &agent.AgentSessionEvent_ConversationItemAdded_{
		ConversationItemAdded: &agent.AgentSessionEvent_ConversationItemAdded{Item: &agent.ChatContext_ChatItem{
			Item: &agent.ChatContext_ChatItem_AgentHandoff{AgentHandoff: &agent.AgentHandoff{NewAgentId: "assistant"}},
		}},
	}})
	require.Eventually(t, func() bool {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		return len(sess.undelivered) == 1
	}, time.Second, 10*time.Millisecond)

	go func() {
		time.Sleep(700 * time.Millisecond) // well past listenSettle, inside idle
		fa.sendAssistant("Welcome!")
	}()
	var got []turnEvent
	delivered := sess.Listen(context.Background(), 2*time.Second, 5*time.Second, func(e turnEvent) { got = append(got, e) })
	require.True(t, delivered)
	require.Len(t, got, 2)
	require.Equal(t, "handoff", got[0].Type)
	require.Equal(t, "Welcome!", got[1].Text)
}

func TestSayWaitsForHandoffIntroduction(t *testing.T) {
	fa, sess := newFakeAgent(t)
	var got []turnEvent
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = sess.Say(context.Background(), "billing please", func(e turnEvent) { got = append(got, e) })
	}()
	req := fa.nextRequest()
	old := "assistant"
	fa.sendEvent(&agent.AgentSessionEvent{Event: &agent.AgentSessionEvent_ConversationItemAdded_{
		ConversationItemAdded: &agent.AgentSessionEvent_ConversationItemAdded{Item: &agent.ChatContext_ChatItem{
			Item: &agent.ChatContext_ChatItem_AgentHandoff{AgentHandoff: &agent.AgentHandoff{OldAgentId: &old, NewAgentId: "billing"}},
		}},
	}})
	// The turn is reported done before the new agent has introduced itself.
	fa.respond(&agent.SessionResponse{RequestId: req.RequestId, Response: &agent.SessionResponse_RunInput{
		RunInput: &agent.SessionResponse_RunInputResponse{},
	}})
	time.Sleep(500 * time.Millisecond)
	fa.sendAssistant("Hi, billing here.")

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Say did not return")
	}
	require.Len(t, got, 3)
	require.Equal(t, "handoff", got[1].Type)
	require.Equal(t, "Hi, billing here.", got[2].Text)
}

func TestObserveStreamDoesNotAffectTurns(t *testing.T) {
	fa, sess := newFakeAgent(t)

	obs, recent := sess.observe(0)
	defer sess.unobserve(obs)
	require.Empty(t, recent)

	// With only an observer attached, agent output must still be buffered for
	// the next turn, and the observer must see it too, with a timestamp.
	fa.sendState(agent.AgentState_AS_THINKING)
	fa.sendAssistant("unprompted")
	var seen []turnEvent
	require.Eventually(t, func() bool {
		for {
			select {
			case e := <-obs.ch:
				seen = append(seen, e)
			default:
				return len(seen) == 2
			}
		}
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, "state", seen[0].Type)
	require.Equal(t, "thinking", seen[0].To)
	require.Equal(t, "unprompted", seen[1].Text)
	require.NotEmpty(t, seen[1].Time)

	held := sess.takeUndelivered()
	require.Len(t, held, 1, "observer must not consume events meant for turns")
	require.Equal(t, "unprompted", held[0].Text)

	// State transitions never reach turns.
	for _, e := range held {
		require.NotEqual(t, "state", e.Type)
	}

	// The ring keeps both for a later `events` call, most recent last.
	all := sess.RecentEvents(0)
	require.Len(t, all, 2)
	require.Len(t, sess.RecentEvents(1), 1)
	require.Equal(t, "unprompted", sess.RecentEvents(1)[0].Text)
}

// sayAsync runs Say in the background, collecting what it streams.
type sayRun struct {
	done chan struct{}
	got  []turnEvent
	res  turnResult
	err  error
}

func sayAsync(ctx context.Context, sess *agentSession, text string) *sayRun {
	r := &sayRun{done: make(chan struct{})}
	go func() {
		defer close(r.done)
		r.res, r.err = sess.Say(ctx, text, func(e turnEvent) { r.got = append(r.got, e) })
	}()
	return r
}

func (r *sayRun) wait(t *testing.T) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(10 * time.Second):
		t.Fatal("Say did not return")
	}
}

func TestAudioSayStreamsEventsAndEarlierItems(t *testing.T) {
	fa, sess := newAudioFakeAgent(t)

	// A greeting nobody was listening for must be held, not dropped.
	fa.sendAssistant("Welcome!")
	require.Eventually(t, func() bool {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		return len(sess.undelivered) == 1
	}, time.Second, 10*time.Millisecond)

	run := sayAsync(context.Background(), sess, "My name is Sara, S A R A")
	require.Equal(t, "My name is Sara, S A R A", fa.nextSpoken())
	require.True(t, sess.TurnInProgress())

	// The agent hears the speech (imperfectly), calls a tool, and replies.
	fa.sendUser("My name is Sarah. S A R A.")
	fa.sendState(agent.AgentState_AS_THINKING)
	fa.sendEvent(&agent.AgentSessionEvent{Event: &agent.AgentSessionEvent_FunctionToolsExecuted_{
		FunctionToolsExecuted: &agent.AgentSessionEvent_FunctionToolsExecuted{
			FunctionCalls:       []*agent.FunctionCall{{CallId: "c1", Name: "lookup", Arguments: `{"q":1}`}},
			FunctionCallOutputs: []*agent.FunctionCallOutput{{CallId: "c1", Output: "42"}},
		},
	}})
	fa.sendState(agent.AgentState_AS_SPEAKING)
	fa.sendAssistant("The answer is 42.")
	fa.sendState(agent.AgentState_AS_LISTENING)

	run.wait(t)
	require.NoError(t, run.err)
	require.False(t, sess.TurnInProgress())
	require.Equal(t, "The answer is 42.", run.res.Reply)
	require.Equal(t, "My name is Sarah. S A R A.", run.res.Heard)
	require.False(t, run.res.Silent)
	require.EqualValues(t, fakeSpeechSamples, fa.heard.Load(), "the whole utterance must reach the agent's mic input")

	types := make([]string, 0, len(run.got))
	for _, e := range run.got {
		types = append(types, e.Type+"/"+e.Role)
	}
	require.Equal(t, []string{"message/assistant", "message/user", "tool_call/", "message/assistant"}, types)
	require.True(t, run.got[0].Earlier, "greeting should be flagged as predating the turn")
	require.Equal(t, "Welcome!", run.got[0].Text)
	require.Equal(t, "lookup", run.got[2].Name)
	require.Equal(t, `{"q":1}`, run.got[2].Arguments)
	require.Equal(t, "42", run.got[2].Output)
	require.Equal(t, 1, sess.Turns())
}

func TestAudioSayReportsAgentError(t *testing.T) {
	fa, sess := newAudioFakeAgent(t)
	run := sayAsync(context.Background(), sess, "hi")
	fa.nextSpoken()
	fa.sendUser("hi")
	fa.sendState(agent.AgentState_AS_THINKING)
	fa.sendError("llm exploded")
	fa.sendState(agent.AgentState_AS_LISTENING)
	run.wait(t)
	require.ErrorContains(t, run.err, "llm exploded")
}

func TestAudioSayReportsSilence(t *testing.T) {
	fa, sess := newAudioFakeAgent(t)
	sess.replyWait = 200 * time.Millisecond
	run := sayAsync(context.Background(), sess, "hello?")
	fa.nextSpoken()
	run.wait(t)
	require.NoError(t, run.err)
	require.True(t, run.res.Silent)
	require.Empty(t, run.res.Heard)
}

func TestAudioSayWaitsForTheAgentToHearTheUser(t *testing.T) {
	fa, sess := newAudioFakeAgent(t)
	run := sayAsync(context.Background(), sess, "My name is Sara")
	fa.nextSpoken()

	// The tail of an earlier reply lands while the user is speaking; it must
	// not pass for the answer to this turn.
	fa.sendState(agent.AgentState_AS_SPEAKING)
	fa.sendAssistant("June tenth has already passed.")
	fa.sendState(agent.AgentState_AS_LISTENING)
	time.Sleep(turnSettle + 500*time.Millisecond)
	select {
	case <-run.done:
		t.Fatal("the turn ended before the agent heard the user")
	default:
	}

	fa.sendUser("My name is Sarah")
	fa.sendState(agent.AgentState_AS_SPEAKING)
	fa.sendAssistant("Thanks, Sarah.")
	fa.sendState(agent.AgentState_AS_LISTENING)
	run.wait(t)
	require.NoError(t, run.err)
	require.False(t, run.res.Silent)
	require.Equal(t, "My name is Sarah", run.res.Heard)
	require.Equal(t, "June tenth has already passed.\nThanks, Sarah.", run.res.Reply)
}

func TestAudioSayReportsSilenceAfterHearing(t *testing.T) {
	// The agent hears the user but never answers: the failure to catch.
	fa, sess := newAudioFakeAgent(t)
	sess.replyWait = 300 * time.Millisecond
	run := sayAsync(context.Background(), sess, "Are you there?")
	fa.nextSpoken()
	fa.sendUser("Are you there?")
	run.wait(t)
	require.NoError(t, run.err)
	require.True(t, run.res.Silent)
	require.Equal(t, "Are you there?", run.res.Heard)
}

func TestAudioSayTimeoutLeavesLateEventsForNextTurn(t *testing.T) {
	fa, sess := newAudioFakeAgent(t)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	run := sayAsync(ctx, sess, "slow")
	fa.nextSpoken()
	fa.sendState(agent.AgentState_AS_THINKING)
	run.wait(t)
	require.ErrorContains(t, run.err, "timed out")
	require.False(t, sess.TurnInProgress())

	// The agent finishes late: its output is held for the next turn.
	fa.sendAssistant("late reply")
	require.Eventually(t, func() bool {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		return len(sess.undelivered) == 1
	}, time.Second, 10*time.Millisecond)
	earlier := sess.takeUndelivered()
	require.Len(t, earlier, 1)
	require.True(t, earlier[0].Earlier)
}

// sendAgentAudio sends d of agent speech followed by a playback flush.
func (fa *fakeAgent) sendAgentAudio(d time.Duration) {
	fa.send(&agent.AgentSessionMessage{Message: &agent.AgentSessionMessage_AudioOutput{
		AudioOutput: &agent.AgentSessionMessage_ConsoleIO_AudioFrame{
			SampleRate: 48000, NumChannels: 1, SamplesPerChannel: uint32(48000 * d / time.Second),
		},
	}})
	fa.send(&agent.AgentSessionMessage{Message: &agent.AgentSessionMessage_AudioPlaybackFlush{
		AudioPlaybackFlush: &agent.AgentSessionMessage_ConsoleIO_AudioPlaybackFlush{},
	}})
}

func TestAudioAcksPlaybackAfterPlayout(t *testing.T) {
	fa, _ := newAudioFakeAgent(t)
	start := time.Now()
	fa.sendAgentAudio(400 * time.Millisecond)
	select {
	case <-fa.acks:
		require.GreaterOrEqual(t, time.Since(start), 350*time.Millisecond, "flush acked before the audio could have played")
	case <-time.After(2 * time.Second):
		t.Fatal("no AudioPlaybackFinished ack for the flush")
	}
}

func TestAudioClearCancelsPlaybackAck(t *testing.T) {
	fa, _ := newAudioFakeAgent(t)
	fa.sendAgentAudio(300 * time.Millisecond)
	fa.send(&agent.AgentSessionMessage{Message: &agent.AgentSessionMessage_AudioPlaybackClear{
		AudioPlaybackClear: &agent.AgentSessionMessage_ConsoleIO_AudioPlaybackClear{},
	}})
	select {
	case <-fa.acks:
		t.Fatal("an interrupted playout must not be acked as finished")
	case <-time.After(600 * time.Millisecond):
	}
}

func TestSameWords(t *testing.T) {
	require.True(t, sameWords("My name is Sara, S A R A", "my name is sara. S A R A!"))
	require.False(t, sameWords("My name is Sara", "My name is Sarah"))
}

func TestAudioSayWaitsForHandoffIntroduction(t *testing.T) {
	fa, sess := newAudioFakeAgent(t)
	run := sayAsync(context.Background(), sess, "billing please")
	fa.nextSpoken()
	fa.sendUser("billing please")
	fa.sendState(agent.AgentState_AS_THINKING)
	old := "assistant"
	fa.sendEvent(&agent.AgentSessionEvent{Event: &agent.AgentSessionEvent_ConversationItemAdded_{
		ConversationItemAdded: &agent.AgentSessionEvent_ConversationItemAdded{Item: &agent.ChatContext_ChatItem{
			Item: &agent.ChatContext_ChatItem_AgentHandoff{AgentHandoff: &agent.AgentHandoff{OldAgentId: &old, NewAgentId: "billing"}},
		}},
	}})
	// The old agent goes quiet before the new one has introduced itself.
	fa.sendState(agent.AgentState_AS_LISTENING)
	time.Sleep(turnSettle + 500*time.Millisecond)
	fa.sendAssistant("Hi, billing here.")

	run.wait(t)
	require.Len(t, run.got, 3)
	require.Equal(t, "handoff", run.got[1].Type)
	require.Equal(t, "Hi, billing here.", run.got[2].Text)
}
