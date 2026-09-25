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
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/livekit/livekit-cli/v2/pkg/console"
	"github.com/livekit/livekit-cli/v2/pkg/ipc"

	agent "github.com/livekit/protocol/livekit/agent"
)

// agentSession drives an agent connected over the console IPC socket: it owns
// the read loop, routes responses to their requests, fans events out to
// whoever is listening, and buffers anything nobody was listening for so it can
// be reported later instead of silently dropped. In text mode the user's turns
// are sent as text with the agent's audio I/O off; in audio mode they are
// spoken into its microphone input and its replies play out in real time.
type agentSession struct {
	conn    net.Conn
	reader  io.Reader
	writeMu sync.Mutex

	mu          sync.Mutex
	pending     map[string]chan *agent.SessionResponse
	subs        map[*eventSub]struct{}
	observers   map[*eventSub]struct{} // `events` taps; never affect buffering
	recent      []turnEvent            // ring of the latest events for `events`
	undelivered []turnEvent
	agentState  agent.AgentState
	activity    chan struct{}

	speak     speakFunc // nil in text mode
	replyWait time.Duration
	mic       micStream
	speaker   playout

	turnSem    chan struct{} // one turn in flight at a time
	turns      atomic.Int64
	reqCounter atomic.Int64

	done     chan struct{}
	doneOnce sync.Once
	readErr  error
}

type eventSub struct {
	ch chan turnEvent
}

// newAgentSession wraps an accepted agent connection. reader lets the caller
// hand back bytes it already peeked from conn (see classifyConn). A nil speak
// runs the session in text mode.
func newAgentSession(conn net.Conn, reader io.Reader, speak speakFunc) *agentSession {
	if reader == nil {
		reader = conn
	}
	s := &agentSession{
		conn:      conn,
		reader:    reader,
		speak:     speak,
		replyWait: defaultReplyWait,
		pending:   make(map[string]chan *agent.SessionResponse),
		subs:      make(map[*eventSub]struct{}),
		observers: make(map[*eventSub]struct{}),
		activity:  make(chan struct{}, 1),
		turnSem:   make(chan struct{}, 1),
		done:      make(chan struct{}),
	}
	go s.readLoop()
	if speak == nil {
		return s
	}
	go s.mic.run(s.done, func(samples []int16) error {
		return s.write(&agent.AgentSessionMessage{
			Message: &agent.AgentSessionMessage_AudioInput{
				AudioInput: &agent.AgentSessionMessage_ConsoleIO_AudioFrame{
					Data:              console.SamplesToBytes(samples),
					SampleRate:        console.SampleRate,
					NumChannels:       console.Channels,
					SamplesPerChannel: uint32(len(samples)),
				},
			},
		})
	})
	return s
}

// Done is closed when the agent connection ends.
func (s *agentSession) Done() <-chan struct{} { return s.done }

// Turns reports how many user turns have been sent.
func (s *agentSession) Turns() int { return int(s.turns.Load()) }

// TurnInProgress reports whether a turn is currently awaiting its reply.
func (s *agentSession) TurnInProgress() bool { return len(s.turnSem) > 0 }

func (s *agentSession) finish(err error) {
	s.doneOnce.Do(func() {
		s.mu.Lock()
		s.readErr = err
		for id, ch := range s.pending {
			close(ch)
			delete(s.pending, id)
		}
		s.mu.Unlock()
		close(s.done)
	})
}

func (s *agentSession) readLoop() {
	for {
		msg := &agent.AgentSessionMessage{}
		if err := ipc.ReadProto(s.reader, msg); err != nil {
			s.finish(err)
			return
		}
		switch m := msg.Message.(type) {
		case *agent.AgentSessionMessage_Event:
			s.handleEvent(m.Event)
		case *agent.AgentSessionMessage_Response:
			if m.Response != nil {
				s.mu.Lock()
				ch, ok := s.pending[m.Response.GetRequestId()]
				if ok {
					delete(s.pending, m.Response.GetRequestId())
				}
				s.mu.Unlock()
				if ok {
					ch <- m.Response
					close(ch)
				}
			}
		case *agent.AgentSessionMessage_AudioOutput:
			s.speaker.write(m.AudioOutput.GetSamplesPerChannel(), m.AudioOutput.GetSampleRate())
		case *agent.AgentSessionMessage_AudioPlaybackClear:
			s.speaker.clear()
		case *agent.AgentSessionMessage_AudioPlaybackFlush:
			// The agent's speech only completes once this is acknowledged. In
			// text mode nothing was buffered, so it is acknowledged at once.
			s.speaker.flush(func() {
				_ = s.write(&agent.AgentSessionMessage{
					Message: &agent.AgentSessionMessage_AudioPlaybackFinished{
						AudioPlaybackFinished: &agent.AgentSessionMessage_ConsoleIO_AudioPlaybackFinished{},
					},
				})
			})
		}
	}
}

func (s *agentSession) write(msg *agent.AgentSessionMessage) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return ipc.WriteProto(s.conn, msg)
}

func (s *agentSession) notifyActivity() {
	select {
	case s.activity <- struct{}{}:
	default:
	}
}

// recentEventsMax bounds the ring of events kept for `events`.
const recentEventsMax = 500

func (s *agentSession) handleEvent(ev *agent.AgentSessionEvent) {
	if ev == nil {
		return
	}
	now := eventTimestamp(time.Now())
	if sc, ok := ev.Event.(*agent.AgentSessionEvent_AgentStateChanged_); ok && sc.AgentStateChanged != nil {
		s.mu.Lock()
		old := s.agentState
		s.agentState = sc.AgentStateChanged.NewState
		// State transitions are noise inside a turn but useful on the event
		// stream, so they go to observers (and the ring) only.
		s.publishLocked([]turnEvent{{
			Type: "state", Time: now,
			From: agentStateName(old), To: agentStateName(sc.AgentStateChanged.NewState),
		}}, false)
		s.mu.Unlock()
		s.notifyActivity()
		return
	}
	events := eventToTurnEvents(ev)
	if len(events) == 0 {
		return
	}
	// Live events are stamped on arrival so the stream reads in order; the
	// SDK's own created_at (kept for chat-history items) can predate the state
	// transitions that surround it.
	for i := range events {
		events[i].Time = now
	}
	s.mu.Lock()
	s.publishLocked(events, true)
	s.mu.Unlock()
	s.notifyActivity()
}

// publishLocked records events in the ring and hands them to observers; when
// toTurns is set they also go to turn subscribers, or to the undelivered
// buffer if nobody is listening. Caller holds s.mu.
func (s *agentSession) publishLocked(events []turnEvent, toTurns bool) {
	s.recent = append(s.recent, events...)
	if over := len(s.recent) - recentEventsMax; over > 0 {
		s.recent = append([]turnEvent(nil), s.recent[over:]...)
	}
	for obs := range s.observers {
		for _, e := range events {
			select {
			case obs.ch <- e:
			default:
			}
		}
	}
	if !toTurns {
		return
	}
	if len(s.subs) == 0 {
		s.undelivered = append(s.undelivered, events...)
		return
	}
	for sub := range s.subs {
		for _, e := range events {
			select {
			case sub.ch <- e:
			default:
			}
		}
	}
}

// observe taps the live event stream without affecting what turns see. It
// returns the subscription and the most recent `last` events (all if last <= 0).
func (s *agentSession) observe(last int) (*eventSub, []turnEvent) {
	obs := &eventSub{ch: make(chan turnEvent, 1024)}
	s.mu.Lock()
	recent := s.recent
	if last > 0 && last < len(recent) {
		recent = recent[len(recent)-last:]
	}
	snapshot := append([]turnEvent(nil), recent...)
	s.observers[obs] = struct{}{}
	s.mu.Unlock()
	return obs, snapshot
}

func (s *agentSession) unobserve(obs *eventSub) {
	s.mu.Lock()
	delete(s.observers, obs)
	s.mu.Unlock()
}

// RecentEvents returns the most recent `last` events (all if last <= 0).
func (s *agentSession) RecentEvents(last int) []turnEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	recent := s.recent
	if last > 0 && last < len(recent) {
		recent = recent[len(recent)-last:]
	}
	return append([]turnEvent(nil), recent...)
}

// subscribe starts receiving live events and, atomically with that, returns
// anything that arrived while nobody was listening (marked Earlier) so a turn
// can report it before its own output.
func (s *agentSession) subscribe() (*eventSub, []turnEvent) {
	sub := &eventSub{ch: make(chan turnEvent, 256)}
	s.mu.Lock()
	earlier := s.undelivered
	s.undelivered = nil
	s.subs[sub] = struct{}{}
	s.mu.Unlock()
	for i := range earlier {
		earlier[i].Earlier = true
	}
	return sub, earlier
}

func (s *agentSession) unsubscribe(sub *eventSub) {
	s.mu.Lock()
	delete(s.subs, sub)
	s.mu.Unlock()
}

// takeUndelivered returns (and clears) events nobody has been shown yet.
func (s *agentSession) takeUndelivered() []turnEvent {
	s.mu.Lock()
	earlier := s.undelivered
	s.undelivered = nil
	s.mu.Unlock()
	for i := range earlier {
		earlier[i].Earlier = true
	}
	return earlier
}

// AgentState returns the last state the agent reported.
func (s *agentSession) AgentState() agent.AgentState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agentState
}

func (s *agentSession) nextRequestID(prefix string) string {
	return prefix + "-" + strconv.FormatInt(s.reqCounter.Add(1), 10)
}

// request sends req and waits for its response, ctx, or the connection ending.
func (s *agentSession) request(ctx context.Context, req *agent.SessionRequest) (*agent.SessionResponse, error) {
	ch := make(chan *agent.SessionResponse, 1)
	s.mu.Lock()
	if s.readErr != nil {
		s.mu.Unlock()
		return nil, errAgentGone
	}
	s.pending[req.RequestId] = ch
	s.mu.Unlock()

	if err := s.write(&agent.AgentSessionMessage{
		Message: &agent.AgentSessionMessage_Request{Request: req},
	}); err != nil {
		s.mu.Lock()
		delete(s.pending, req.RequestId)
		s.mu.Unlock()
		return nil, fmt.Errorf("send to agent: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok || resp == nil {
			return nil, errAgentGone
		}
		return resp, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, req.RequestId)
		s.mu.Unlock()
		return nil, ctx.Err()
	case <-s.done:
		return nil, errAgentGone
	}
}

var errAgentGone = errors.New("agent exited")

// SetTextMode disables the agent's audio I/O so it runs as a pure text turn
// handler, matching what `lk agent console` does when switching to text mode.
func (s *agentSession) SetTextMode(ctx context.Context) error {
	off := false
	_, err := s.request(ctx, &agent.SessionRequest{
		RequestId: s.nextRequestID("io"),
		Request: &agent.SessionRequest_UpdateIo{
			UpdateIo: &agent.SessionRequest_UpdateIO{
				Input:  &agent.SessionRequest_UpdateIO_Input{AudioEnabled: &off},
				Output: &agent.SessionRequest_UpdateIO_Output{AudioEnabled: &off, TranscriptionEnabled: &off},
			},
		},
	})
	return err
}

// WaitForGreeting gives an agent that speaks first (generate_reply in
// on_enter) a chance to finish before the session is reported ready, so the
// greeting is shown instead of being lost between turns. Whatever arrives is
// left in the undelivered buffer for `start` to print via "pending".
func (s *agentSession) WaitForGreeting(ctx context.Context, settle, maxWait time.Duration) {
	s.Listen(ctx, settle, maxWait, func(e turnEvent) {
		e.Earlier = false
		s.mu.Lock()
		s.undelivered = append(s.undelivered, e)
		s.mu.Unlock()
	})
}

// listenSettle is how long the agent must be idle, after it has produced
// something, before Listen decides the burst is over.
const listenSettle = 300 * time.Millisecond

// Listen reports agent activity that no user turn asked for: an opening
// greeting, a reply the agent volunteers between turns (a timer, a follow-up
// after silence), or output that landed after a `say` gave up waiting.
// Anything already buffered is delivered right away. It then waits up to idle
// for the agent to start doing something; once it does, it keeps delivering
// until the agent has been quiet for listenSettle, bounded by maxWait overall.
// It returns true if any event was delivered.
func (s *agentSession) Listen(ctx context.Context, idle, maxWait time.Duration, sink func(turnEvent)) bool {
	sub, earlier := s.subscribe()
	defer s.unsubscribe(sub)

	// Markers (the initial agent handoff, config updates) are delivered but do
	// not count as the agent speaking, so they never shorten the wait.
	delivered := false
	deliver := func(e turnEvent) {
		sink(e)
		delivered = delivered || e.isAgentOutput()
	}
	for _, e := range earlier {
		deliver(e)
	}

	deadline := time.NewTimer(maxWait)
	defer deadline.Stop()
	s.awaitQuiet(ctx, sub, idle, listenSettle, deadline.C, deliver, func() bool { return delivered })
	return delivered
}

func isBusy(state agent.AgentState) bool {
	return state == agent.AgentState_AS_THINKING || state == agent.AgentState_AS_SPEAKING
}

// awaitQuiet hands sub's events to deliver until the agent goes quiet: settle
// after it last produced output or stopped being busy once responded reports
// true, or idle while it has not. It also stops at deadline, ctx, or the
// connection ending.
func (s *agentSession) awaitQuiet(ctx context.Context, sub *eventSub, idle, settle time.Duration, deadline <-chan time.Time, deliver func(turnEvent), responded func() bool) {
	wait := func() time.Duration {
		if responded() {
			return settle
		}
		return idle
	}
	quiet := time.NewTimer(wait())
	defer quiet.Stop()
	busy := isBusy(s.AgentState())
	if busy {
		quiet.Stop()
	}
	for {
		select {
		case e := <-sub.ch:
			deliver(e)
			if !busy && responded() {
				quiet.Reset(settle)
			}
		case <-s.activity:
			switch {
			case isBusy(s.AgentState()):
				busy = true
				quiet.Stop()
			case busy:
				busy = false
				quiet.Reset(wait())
			}
		case <-quiet.C:
			return
		case <-deadline:
			return
		case <-ctx.Done():
			return
		case <-s.done:
			return
		}
	}
}

// turnResult is what Say reports once the agent's turn completes.
type turnResult struct {
	Reply    string // concatenated assistant text produced during the turn
	Heard    string // what the agent transcribed from the user's speech
	Silent   bool   // the agent produced no output after hearing the user
	Duration time.Duration
}

const (
	// defaultReplyWait is how long a turn waits, once the user's speech has
	// played and the agent is not busy, for it to transcribe and respond before
	// calling the turn silent. It covers endpointing plus the first LLM round trip.
	defaultReplyWait = 10 * time.Second
	// turnSettle is how long the agent must stay quiet after responding before
	// the turn is over. A delegating model (GPT Live) acknowledges, then speaks
	// the delegated answer after a pause with no session event marking it, so
	// this has to outlast that pause.
	turnSettle = 3 * time.Second
)

// Say runs one user turn: it reports events nobody has seen yet, hands the
// text to the agent (typed in text mode, spoken into its microphone input in
// audio mode), streams the agent's events to sink as they happen, and returns
// when the turn is over. Turns are serialized; a second caller blocks until the
// first finishes. Cancel ctx to stop waiting; any events that arrive afterwards
// are held for the next turn.
func (s *agentSession) Say(ctx context.Context, text string, sink func(turnEvent)) (turnResult, error) {
	select {
	case s.turnSem <- struct{}{}:
		defer func() { <-s.turnSem }()
	case <-ctx.Done():
		return turnResult{}, fmt.Errorf("another turn is still in progress")
	case <-s.done:
		return turnResult{}, errAgentGone
	}

	start := time.Now()
	sub, earlier := s.subscribe()
	defer s.unsubscribe(sub)
	for _, e := range earlier {
		sink(e)
	}
	s.turns.Add(1)
	if s.speak == nil {
		return s.sayText(ctx, text, start, sub, sink)
	}
	return s.sayAudio(ctx, text, start, sub, sink)
}

// sayText echoes the user text, sends RunInput, and returns when the agent
// reports the turn complete.
func (s *agentSession) sayText(ctx context.Context, text string, start time.Time, sub *eventSub, sink func(turnEvent)) (turnResult, error) {
	sink(turnEvent{Type: "message", Role: "user", Text: text})

	var (
		reply      strings.Builder
		sawAgent   bool
		sawHandoff bool
		spokeAfter bool // an assistant message arrived after the last handoff
	)
	deliver := func(e turnEvent) {
		if e.Type == "message" && e.Role == "user" {
			return // the user message is echoed above
		}
		sawAgent = true
		switch {
		case e.Type == "handoff":
			sawHandoff = true
			spokeAfter = false
		case e.Type == "message" && e.Role == "assistant":
			spokeAfter = true
			if reply.Len() > 0 {
				reply.WriteString("\n")
			}
			reply.WriteString(e.Text)
		}
		sink(e)
	}

	respCh := make(chan struct {
		resp *agent.SessionResponse
		err  error
	}, 1)
	go func() {
		resp, err := s.request(ctx, &agent.SessionRequest{
			RequestId: s.nextRequestID("say"),
			Request: &agent.SessionRequest_RunInput_{
				RunInput: &agent.SessionRequest_RunInput{Text: text},
			},
		})
		respCh <- struct {
			resp *agent.SessionResponse
			err  error
		}{resp, err}
	}()

	for {
		select {
		case e := <-sub.ch:
			deliver(e)
		case r := <-respCh:
			// Drain anything the read loop queued before the response landed.
			for {
				select {
				case e := <-sub.ch:
					deliver(e)
					continue
				default:
				}
				break
			}
			// A handoff target usually introduces itself from on_enter, which
			// can complete after the turn is reported done. Hold the turn open
			// briefly so that introduction reads as part of the handoff.
			if r.err == nil && sawHandoff && !spokeAfter {
				s.awaitHandoffIntro(ctx, sub, deliver, func() bool { return spokeAfter })
			}
			res := turnResult{Reply: reply.String(), Heard: text, Silent: !sawAgent, Duration: time.Since(start)}
			if r.err != nil {
				if errors.Is(r.err, context.DeadlineExceeded) {
					return res, fmt.Errorf("timed out waiting for the agent's reply; the turn may still be running (check `lk agent debugger logs`)")
				}
				return res, r.err
			}
			if msg := r.resp.GetError(); msg != "" {
				return res, fmt.Errorf("agent error: %s", msg)
			}
			return res, nil
		}
	}
}

// sayAudio speaks the text into the agent's microphone input and returns once
// the agent has heard it, responded, and gone quiet, or never responded.
func (s *agentSession) sayAudio(ctx context.Context, text string, start time.Time, sub *eventSub, sink func(turnEvent)) (turnResult, error) {
	var (
		reply, heard strings.Builder
		agentErr     string
		responded    bool // the agent produced output after hearing the user
		sawHandoff   bool
		spokeAfter   bool // an assistant message arrived after the last handoff
	)
	appendLine := func(b *strings.Builder, line string) {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(line)
	}
	deliver := func(e turnEvent) {
		switch {
		case e.Type == "message" && e.Role == "user":
			appendLine(&heard, e.Text)
		case e.Type == "handoff":
			sawHandoff = true
			spokeAfter = false
		case e.Type == "message" && e.Role == "assistant":
			spokeAfter = true
			appendLine(&reply, e.Text)
		case e.Type == "error" && agentErr == "":
			agentErr = e.Text
		}
		if heard.Len() > 0 && e.isAgentOutput() {
			responded = true
		}
		sink(e)
	}
	result := func() turnResult {
		return turnResult{Reply: reply.String(), Heard: heard.String(), Silent: !responded, Duration: time.Since(start)}
	}

	spoken := make(chan error, 1)
	go func() {
		if err := s.speak(ctx, text, s.mic.push); err != nil {
			spoken <- err
			return
		}
		select {
		case <-s.mic.drained():
		case <-ctx.Done():
		}
		spoken <- nil
	}()
speaking:
	for {
		select {
		case e := <-sub.ch:
			deliver(e)
		case err := <-spoken:
			if err != nil {
				return result(), err
			}
			break speaking
		case <-s.done:
			return result(), errAgentGone
		}
	}

	// Output while the user was still being heard (the tail of an earlier
	// reply) does not answer this turn, so the turn waits for the agent to
	// transcribe the speech and respond to it.
	s.awaitQuiet(ctx, sub, s.replyWait, turnSettle, nil, deliver, func() bool { return responded })
	// A handoff target usually introduces itself from on_enter, which can
	// start after the previous agent went quiet. Hold the turn open briefly so
	// that introduction reads as part of the handoff.
	if ctx.Err() == nil && sawHandoff && !spokeAfter {
		s.awaitHandoffIntro(ctx, sub, deliver, func() bool { return spokeAfter })
	}

	res := result()
	select {
	case <-s.done:
		return res, errAgentGone
	default:
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return res, fmt.Errorf("timed out waiting for the agent's reply; the turn may still be running (check `lk agent debugger logs`)")
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}
	if agentErr != "" {
		return res, fmt.Errorf("agent error: %s", agentErr)
	}
	return res, nil
}

// handoffIntroWait bounds how long Say waits for a new agent's opening line
// after a handoff; handoffIntroSettle is the quiet period after it arrives.
const (
	handoffIntroWait   = 4 * time.Second
	handoffIntroSettle = 300 * time.Millisecond
)

func (s *agentSession) awaitHandoffIntro(ctx context.Context, sub *eventSub, deliver func(turnEvent), spoke func() bool) {
	timer := time.NewTimer(handoffIntroWait)
	defer timer.Stop()
	for {
		select {
		case e := <-sub.ch:
			deliver(e)
			if spoke() {
				timer.Reset(handoffIntroSettle)
			}
		case <-timer.C:
			return
		case <-ctx.Done():
			return
		case <-s.done:
			return
		}
	}
}

// History fetches the agent's authoritative chat history.
func (s *agentSession) History(ctx context.Context) ([]turnEvent, error) {
	resp, err := s.request(ctx, &agent.SessionRequest{
		RequestId: s.nextRequestID("history"),
		Request:   &agent.SessionRequest_GetChatHistory_{GetChatHistory: &agent.SessionRequest_GetChatHistory{}},
	})
	if err != nil {
		return nil, err
	}
	if msg := resp.GetError(); msg != "" {
		return nil, fmt.Errorf("agent error: %s", msg)
	}
	return chatItemsToTurnEvents(resp.GetGetChatHistory().GetItems()), nil
}

// AgentInfo fetches the current agent's id, instructions, and tool names.
func (s *agentSession) AgentInfo(ctx context.Context) (*agent.SessionResponse_GetAgentInfoResponse, error) {
	resp, err := s.request(ctx, &agent.SessionRequest{
		RequestId: s.nextRequestID("info"),
		Request:   &agent.SessionRequest_GetAgentInfo_{GetAgentInfo: &agent.SessionRequest_GetAgentInfo{}},
	})
	if err != nil {
		return nil, err
	}
	if msg := resp.GetError(); msg != "" {
		return nil, fmt.Errorf("agent error: %s", msg)
	}
	return resp.GetGetAgentInfo(), nil
}
