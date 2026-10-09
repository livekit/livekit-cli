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

package console

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/livekit/protocol/livekit/agent"
)

func stateEvent(state agent.AgentState) *agent.AgentSessionMessage {
	return &agent.AgentSessionMessage{Message: &agent.AgentSessionMessage_Event{Event: &agent.AgentSessionEvent{
		Event: &agent.AgentSessionEvent_AgentStateChanged_{AgentStateChanged: &agent.AgentSessionEvent_AgentStateChanged{NewState: state}},
	}}}
}

func flushMessage() *agent.AgentSessionMessage {
	return &agent.AgentSessionMessage{Message: &agent.AgentSessionMessage_AudioPlaybackFlush{
		AudioPlaybackFlush: &agent.AgentSessionMessage_ConsoleIO_AudioPlaybackFlush{},
	}}
}

// nextEvent returns the next event's new agent state, or -1 if none arrives.
func nextEvent(t *testing.T, p *AudioPipeline) agent.AgentState {
	t.Helper()
	select {
	case ev := <-p.Events:
		return ev.GetAgentStateChanged().GetNewState()
	case <-time.After(200 * time.Millisecond):
		return -1
	}
}

func send(t *testing.T, conn net.Conn, msg *agent.AgentSessionMessage) {
	t.Helper()
	if err := WriteSessionMessage(conn, msg); err != nil {
		t.Fatal(err)
	}
}

func TestPipelineSwitchesAgents(t *testing.T) {
	// No audio devices: the pipeline only relays session messages.
	first, firstAgent := net.Pipe()
	second, secondAgent := net.Pipe()
	p, err := NewPipeline(PipelineConfig{Conn: first})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	p.AddConn(ctx, second)

	// The second agent starts inactive: its events are dropped, and its
	// playback is acknowledged right away.
	send(t, firstAgent, stateEvent(agent.AgentState_AS_LISTENING))
	if got := nextEvent(t, p); got != agent.AgentState_AS_LISTENING {
		t.Fatalf("active agent's event: got %v", got)
	}
	send(t, secondAgent, stateEvent(agent.AgentState_AS_SPEAKING))
	if got := nextEvent(t, p); got != -1 {
		t.Fatalf("inactive agent's event should be dropped, got %v", got)
	}
	acked := make(chan *agent.AgentSessionMessage, 1)
	go func() {
		msg, _ := ReadSessionMessage(secondAgent)
		acked <- msg
	}()
	send(t, secondAgent, flushMessage())
	select {
	case msg := <-acked:
		if msg.GetAudioPlaybackFinished() == nil {
			t.Fatalf("inactive agent's flush: got %v, want playback finished", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("inactive agent's flush wasn't acknowledged")
	}

	// After switching, it's the other way around.
	p.SetActive(second)
	send(t, secondAgent, stateEvent(agent.AgentState_AS_THINKING))
	if got := nextEvent(t, p); got != agent.AgentState_AS_THINKING {
		t.Fatalf("newly active agent's event: got %v", got)
	}
	send(t, firstAgent, stateEvent(agent.AgentState_AS_SPEAKING))
	if got := nextEvent(t, p); got != -1 {
		t.Fatalf("now-inactive agent's event should be dropped, got %v", got)
	}
}

func TestPipelineSwitchAcksPendingPlayback(t *testing.T) {
	first, firstAgent := net.Pipe()
	second, secondAgent := net.Pipe()
	defer firstAgent.Close()
	defer secondAgent.Close()
	p, err := NewPipeline(PipelineConfig{Conn: first})
	if err != nil {
		t.Fatal(err)
	}
	// A playback ring that nothing drains, so a flush waits for its audio.
	p.playbackRing = NewRingBuffer(SamplesPerFrame)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	p.AddConn(ctx, second)

	send(t, firstAgent, &agent.AgentSessionMessage{Message: &agent.AgentSessionMessage_AudioOutput{
		AudioOutput: &agent.AgentSessionMessage_ConsoleIO_AudioFrame{Data: make([]byte, 960)},
	}})
	send(t, firstAgent, flushMessage())
	time.Sleep(20 * time.Millisecond)

	read := func(c net.Conn) <-chan *agent.AgentSessionMessage {
		ch := make(chan *agent.AgentSessionMessage, 1)
		go func() {
			if msg, err := ReadSessionMessage(c); err == nil {
				ch <- msg
			}
		}()
		return ch
	}
	firstAcks, secondAcks := read(firstAgent), read(secondAgent)

	// Switching drops the first agent's queued audio, so its playback is
	// over: the first agent gets the ack, and the second gets none.
	p.SetActive(second)
	select {
	case msg := <-firstAcks:
		if msg.GetAudioPlaybackFinished() == nil {
			t.Fatalf("first agent: got %v, want playback finished", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("the first agent's pending playback wasn't acknowledged")
	}
	select {
	case msg := <-secondAcks:
		t.Fatalf("the second agent got a message it didn't ask for: %v", msg)
	case <-time.After(100 * time.Millisecond):
	}
}
