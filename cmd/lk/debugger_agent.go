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

	"github.com/livekit/livekit-cli/v2/pkg/ipc"

	agent "github.com/livekit/protocol/livekit/agent"
)

// turnEvent is the normalized, transport-agnostic record of one thing that
// happened in a text-mode agent session: a message, a tool call, a handoff, an
// agent error, or an agent log line. The daemon ships these to `say`/`history`
// clients as JSON, the scripted console prints them locally, and `--json`
// emits them verbatim, so the schema doubles as the machine-readable contract
// for coding agents driving the CLI.
type turnEvent struct {
	// Type is one of: message, tool_call, handoff, config, error, log, state.
	// "state" (agent state transitions, From → To) is only reported by the
	// events stream, never inside a turn.
	Type string `json:"type"`
	// Time is when the event happened (RFC 3339 with milliseconds), when known.
	Time string `json:"time,omitempty"`
	// Role is "user" or "assistant" for message events.
	Role string `json:"role,omitempty"`
	// Text is the message text, error message, or log line.
	Text string `json:"text,omitempty"`
	// Interrupted marks an assistant message cut short by the user.
	Interrupted bool `json:"interrupted,omitempty"`
	// Metrics carries per-message latencies in seconds (llm_ttft, tts_ttfb,
	// e2e_latency) when the agent reported them.
	Metrics map[string]float64 `json:"metrics,omitempty"`
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

// textSession drives an agent connected over the console IPC socket purely in
// text mode: it owns the read loop, routes responses to their requests, fans
// events out to whoever is listening, and buffers anything nobody was listening
// for so it can be reported later instead of silently dropped.
type textSession struct {
	conn    net.Conn
	reader  io.Reader
	writeMu sync.Mutex

	mu          sync.Mutex
	pending     map[string]chan *agent.SessionResponse
	subs        map[*eventSub]struct{}
	observers   map[*eventSub]struct{} // `events --follow` taps; never affect buffering
	recent      []turnEvent            // ring of the latest events for `events`
	undelivered []turnEvent
	agentState  agent.AgentState
	activity    chan struct{}

	turnSem    chan struct{} // one RunInput in flight at a time
	turns      atomic.Int64
	reqCounter atomic.Int64

	done     chan struct{}
	doneOnce sync.Once
	readErr  error
}

type eventSub struct {
	ch chan turnEvent
}

// newTextSession wraps an accepted agent connection. reader lets the caller
// hand back bytes it already peeked from conn (see classifyConn).
func newTextSession(conn net.Conn, reader io.Reader) *textSession {
	if reader == nil {
		reader = conn
	}
	s := &textSession{
		conn:      conn,
		reader:    reader,
		pending:   make(map[string]chan *agent.SessionResponse),
		subs:      make(map[*eventSub]struct{}),
		observers: make(map[*eventSub]struct{}),
		activity:  make(chan struct{}, 1),
		turnSem:   make(chan struct{}, 1),
		done:      make(chan struct{}),
	}
	go s.readLoop()
	return s
}

// Done is closed when the agent connection ends.
func (s *textSession) Done() <-chan struct{} { return s.done }

// Turns reports how many RunInput turns have been sent.
func (s *textSession) Turns() int { return int(s.turns.Load()) }

// TurnInProgress reports whether a RunInput is currently awaiting its reply.
func (s *textSession) TurnInProgress() bool { return len(s.turnSem) > 0 }

func (s *textSession) finish(err error) {
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

func (s *textSession) readLoop() {
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
		case *agent.AgentSessionMessage_AudioOutput, *agent.AgentSessionMessage_AudioPlaybackClear:
			// No audio sink in text mode: drop.
		case *agent.AgentSessionMessage_AudioPlaybackFlush:
			// Nothing to drain, so ack immediately or the agent's turn (and the
			// RunInputResponse we await) never completes.
			_ = s.write(&agent.AgentSessionMessage{
				Message: &agent.AgentSessionMessage_AudioPlaybackFinished{
					AudioPlaybackFinished: &agent.AgentSessionMessage_ConsoleIO_AudioPlaybackFinished{},
				},
			})
		}
	}
}

func (s *textSession) write(msg *agent.AgentSessionMessage) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return ipc.WriteProto(s.conn, msg)
}

func (s *textSession) notifyActivity() {
	select {
	case s.activity <- struct{}{}:
	default:
	}
}

// recentEventsMax bounds the ring of events kept for `events`.
const recentEventsMax = 500

func eventTimestamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z07:00") }

func (s *textSession) handleEvent(ev *agent.AgentSessionEvent) {
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
func (s *textSession) publishLocked(events []turnEvent, toTurns bool) {
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
func (s *textSession) observe(last int) (*eventSub, []turnEvent) {
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

func (s *textSession) unobserve(obs *eventSub) {
	s.mu.Lock()
	delete(s.observers, obs)
	s.mu.Unlock()
}

// RecentEvents returns the most recent `last` events (all if last <= 0).
func (s *textSession) RecentEvents(last int) []turnEvent {
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
func (s *textSession) subscribe() (*eventSub, []turnEvent) {
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

func (s *textSession) unsubscribe(sub *eventSub) {
	s.mu.Lock()
	delete(s.subs, sub)
	s.mu.Unlock()
}

// takeUndelivered returns (and clears) events nobody has been shown yet.
func (s *textSession) takeUndelivered() []turnEvent {
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
func (s *textSession) AgentState() agent.AgentState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agentState
}

func (s *textSession) nextRequestID(prefix string) string {
	return prefix + "-" + strconv.FormatInt(s.reqCounter.Add(1), 10)
}

// request sends req and waits for its response, ctx, or the connection ending.
func (s *textSession) request(ctx context.Context, req *agent.SessionRequest) (*agent.SessionResponse, error) {
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
func (s *textSession) SetTextMode(ctx context.Context) error {
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
func (s *textSession) WaitForGreeting(ctx context.Context, settle, maxWait time.Duration) {
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
func (s *textSession) Listen(ctx context.Context, idle, maxWait time.Duration, sink func(turnEvent)) bool {
	sub, earlier := s.subscribe()
	defer s.unsubscribe(sub)

	// Markers (the initial agent handoff, config updates) are delivered but do
	// not count as the agent speaking, so they never shorten the wait.
	delivered := false
	for _, e := range earlier {
		sink(e)
		delivered = delivered || e.isOutput()
	}

	deadline := time.NewTimer(maxWait)
	defer deadline.Stop()
	quiet := time.NewTimer(idle)
	defer quiet.Stop()
	if delivered {
		// Something was already waiting; only linger for a burst in progress.
		quiet.Reset(listenSettle)
	}

	busy := false
	for {
		select {
		case e := <-sub.ch:
			sink(e)
			if !e.isOutput() {
				continue
			}
			delivered = true
			if !busy {
				quiet.Reset(listenSettle)
			}
		case <-s.activity:
			s.mu.Lock()
			state := s.agentState
			s.mu.Unlock()
			switch state {
			case agent.AgentState_AS_THINKING, agent.AgentState_AS_SPEAKING:
				busy = true
				quiet.Stop()
			default:
				if busy {
					busy = false
					quiet.Reset(listenSettle)
				}
			}
		case <-quiet.C:
			return delivered
		case <-deadline.C:
			return delivered
		case <-ctx.Done():
			return delivered
		case <-s.done:
			return delivered
		}
	}
}

// turnResult is what Say reports once the agent's turn completes.
type turnResult struct {
	Reply    string // concatenated assistant text produced during the turn
	Silent   bool   // the turn completed without any agent output
	Duration time.Duration
}

// Say runs one user turn: it reports events nobody has seen yet, echoes the
// user text, sends RunInput, streams the agent's events to sink as they
// happen, and returns when the agent reports the turn complete. Turns are
// serialized; a second caller blocks until the first finishes. Cancel ctx to
// stop waiting; any events that arrive afterwards are held for the next turn.
func (s *textSession) Say(ctx context.Context, text string, sink func(turnEvent)) (turnResult, error) {
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

	s.turns.Add(1)
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
			res := turnResult{Reply: reply.String(), Silent: !sawAgent, Duration: time.Since(start)}
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

// handoffIntroWait bounds how long Say waits for a new agent's opening line
// after a handoff; handoffIntroSettle is the quiet period after it arrives.
const (
	handoffIntroWait   = 4 * time.Second
	handoffIntroSettle = 300 * time.Millisecond
)

func (s *textSession) awaitHandoffIntro(ctx context.Context, sub *eventSub, deliver func(turnEvent), spoke func() bool) {
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
func (s *textSession) History(ctx context.Context) ([]turnEvent, error) {
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
func (s *textSession) AgentInfo(ctx context.Context) (*agent.SessionResponse_GetAgentInfoResponse, error) {
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
	if m := msg.GetMetrics(); m != nil {
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
		if len(metrics) > 0 {
			e.Metrics = metrics
		}
	}
	return e, true
}

// agentStateName renders the proto enum as the lowercase word the SDK uses.
func agentStateName(st agent.AgentState) string {
	name := strings.TrimPrefix(st.String(), "AS_")
	return strings.ToLower(name)
}
