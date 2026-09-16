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
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/console"
)

const (
	// defaultSayTimeout bounds how long a `say` waits for the agent's turn to
	// complete when the client doesn't specify one.
	defaultSayTimeout = 120 * time.Second
	// greetingSettle is how long the daemon waits, once the agent's session is
	// up, for an opening reply before reporting ready without one. In text mode
	// the agent state stays "listening" while it generates an unprompted reply,
	// so there is no early "busy" signal to shorten this; it has to cover a
	// typical LLM round trip on its own.
	greetingSettle      = 3 * time.Second
	greetingMaxWait     = 30 * time.Second
	agentConnectTimeout = 60 * time.Second
)

// runSessionDaemon is the entry point for the hidden `lk agent debugger serve`
// subcommand that `lk agent debugger start` re-execs. It runs the detached
// daemon to completion (until the agent exits or `stop` is received).
func runSessionDaemon() {
	ready := readyWriter()
	port, _ := strconv.Atoi(os.Getenv(envSessionPort))

	// The fixed port is the singleton: if the bind fails, a session already
	// owns it, which is how `lk agent debugger start` learns to reject.
	server, err := console.NewTCPServer(sessionAddr(port))
	if err != nil {
		signalReady(ready, "error: a session is already running on "+sessionAddr(port)+
			" (use `lk agent debugger stop` or `lk agent debugger restart`, or pick another --port)")
		os.Exit(1)
	}
	defer server.Close()

	dir := os.Getenv(envSessionDir)
	entry := os.Getenv(envSessionEntry)
	idleTimeout, _ := time.ParseDuration(os.Getenv(envSessionIdle))
	ptype := agentfs.ProjectType(os.Getenv(envSessionPType))

	// startAgent branches on ProjectType, so Node entrypoints run as
	// `node <entry> console ...` without any extra wiring here.
	agentProc, err := startAgent(AgentStartConfig{
		Dir:         dir,
		Entrypoint:  entry,
		ProjectType: ptype,
		CLIArgs:     buildConsoleArgs(server.Addr().String(), false),
		FailSignals: consoleCrashSignals,
	})
	if err != nil {
		signalReady(ready, "error: failed to start agent: "+err.Error())
		os.Exit(1)
	}
	agentProc.LogStream = make(chan string, 256)

	d := &sessionDaemon{
		server:      server,
		agentProc:   agentProc,
		port:        port,
		projectDir:  dir,
		entrypoint:  entry,
		projectType: ptype,
		startedAt:   time.Now(),
		idleTimeout: idleTimeout,
		agentReady:  make(chan struct{}),
		logSubs:     make(map[chan string]struct{}),
		shutdown:    make(chan struct{}),
		exited:      make(chan struct{}),
	}

	d.touch()
	go d.fanOutLogs()
	go d.acceptLoop()
	go d.idleWatch()

	select {
	case <-d.agentReady:
	case waitErr := <-agentProc.Done():
		msg := "error: the agent exited before connecting"
		if waitErr != nil {
			msg += " (" + waitErr.Error() + ")"
		}
		signalReady(ready, msg+"\n"+agentExitDetail(agentProc))
		agentProc.Kill()
		os.Exit(1)
	case <-agentProc.Failed():
		// The crash marker arrives mid-traceback; give trailing output a moment.
		time.Sleep(500 * time.Millisecond)
		signalReady(ready, "error: the agent job crashed before connecting\n"+agentExitDetail(agentProc))
		agentProc.Kill()
		os.Exit(1)
	case <-time.After(agentConnectTimeout):
		signalReady(ready, "error: timed out waiting for the agent to connect\n"+agentExitDetail(agentProc))
		agentProc.Kill()
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	_ = d.session.SetTextMode(ctx)
	cancel()
	// Let an agent that greets first finish, so `start` can show the greeting.
	d.session.WaitForGreeting(context.Background(), greetingSettle, greetingMaxWait)
	signalReady(ready, "ready")

	select {
	case <-d.session.Done():
	case <-agentProc.Done():
	case <-d.shutdown:
	}
	agentProc.Kill()
	close(d.exited)

	// Give an in-flight `stop` a moment to acknowledge before the listener
	// closes under it.
	waitGroupTimeout(&d.handlers, 2*time.Second)
}

func waitGroupTimeout(wg *sync.WaitGroup, d time.Duration) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}

// readyWriter returns the path of the readiness file `lk agent debugger start`
// polls to learn the daemon became ready (or failed). Empty if not launched
// via start.
func readyWriter() string {
	return os.Getenv(envSessionReadyFile)
}

// signalReady atomically writes the daemon's status to the readiness file the
// parent `start` is polling. The write-then-rename keeps the parent from
// reading a partial line.
func signalReady(path, msg string) {
	if path == "" {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(msg+"\n"), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

type sessionDaemon struct {
	server    *console.TCPServer
	agentProc *AgentProcess

	port        int
	projectDir  string
	entrypoint  string
	projectType agentfs.ProjectType
	startedAt   time.Time
	idleTimeout time.Duration
	lastActive  atomic.Int64 // unix nanos of the last control command

	sessionMu  sync.Mutex
	session    *textSession
	agentReady chan struct{}

	logMu   sync.Mutex
	logSubs map[chan string]struct{}

	handlers sync.WaitGroup
	shutdown chan struct{}
	shutOnce sync.Once
	exited   chan struct{}
}

// touch records that a client just talked to us, for the idle timeout.
func (d *sessionDaemon) touch() { d.lastActive.Store(time.Now().UnixNano()) }

func (d *sessionDaemon) idleFor() time.Duration {
	return time.Since(time.Unix(0, d.lastActive.Load()))
}

// idleWatch stops the session once no command has arrived for idleTimeout,
// so a caller that never runs `stop` does not leave an agent process behind.
// A turn in progress counts as activity.
func (d *sessionDaemon) idleWatch() {
	if d.idleTimeout <= 0 {
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if d.session != nil && d.session.TurnInProgress() {
				d.touch()
				continue
			}
			if d.idleFor() >= d.idleTimeout {
				fmt.Fprintf(os.Stderr, "no commands for %s; stopping the session\n", d.idleTimeout)
				d.shutOnce.Do(func() { close(d.shutdown) })
				return
			}
		case <-d.shutdown:
			return
		case <-d.exited:
			return
		}
	}
}

func (d *sessionDaemon) acceptLoop() {
	for {
		conn, err := d.server.AcceptConn()
		if err != nil {
			return // listener closed
		}
		go d.handleConn(conn)
	}
}

func (d *sessionDaemon) handleConn(conn net.Conn) {
	isControl, reader, err := classifyConn(conn)
	if err != nil {
		conn.Close()
		return
	}
	if isControl {
		d.handleControlConn(conn)
		return
	}

	// First non-control connection is the agent.
	d.sessionMu.Lock()
	if d.session != nil {
		d.sessionMu.Unlock()
		conn.Close()
		return
	}
	d.session = newTextSession(conn, reader)
	d.sessionMu.Unlock()
	close(d.agentReady)
}

// classifyConn routes a connection by its 4-byte preamble. Control clients send
// the magic; the unmodified agent never does. "LKCP" decodes to a ~1.28 GB
// length prefix, which exceeds pkg/ipc's 1 MB cap, so a real agent frame can
// never begin with these bytes.
func classifyConn(conn net.Conn) (bool, io.Reader, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return false, nil, err
	}
	if string(hdr[:]) == sessionMagic {
		return true, conn, nil
	}
	// Push the peeked bytes back so proto framing sees a complete frame.
	return false, io.MultiReader(bytes.NewReader(hdr[:]), conn), nil
}

// fanOutLogs relays agent log lines (ANSI-stripped) to every subscriber:
// `logs --follow` clients and `say --logs` turns.
func (d *sessionDaemon) fanOutLogs() {
	for line := range d.agentProc.LogStream {
		clean := ansiEscapeRe.ReplaceAllString(line, "")
		d.logMu.Lock()
		for sub := range d.logSubs {
			select {
			case sub <- clean:
			default:
			}
		}
		d.logMu.Unlock()
	}
}

func (d *sessionDaemon) subscribeLogs() chan string {
	ch := make(chan string, 512)
	d.logMu.Lock()
	d.logSubs[ch] = struct{}{}
	d.logMu.Unlock()
	return ch
}

func (d *sessionDaemon) unsubscribeLogs(ch chan string) {
	d.logMu.Lock()
	delete(d.logSubs, ch)
	d.logMu.Unlock()
}

// controlConn wraps a control connection so concurrent writers (turn events
// and interleaved log lines) don't corrupt frames.
type controlConn struct {
	net.Conn
	mu     sync.Mutex
	closed chan struct{}
}

func (c *controlConn) reply(r controlReply) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return writeControlFrame(c.Conn, r)
}

// watchClose closes c.closed once the client hangs up. Clients send exactly
// one request frame, so any further read completing means EOF.
func (c *controlConn) watchClose() {
	var buf [1]byte
	_, _ = io.ReadFull(c.Conn, buf[:])
	close(c.closed)
}

func (d *sessionDaemon) handleControlConn(raw net.Conn) {
	d.handlers.Add(1)
	defer d.handlers.Done()
	defer raw.Close()

	var req controlRequest
	if err := readControlFrame(raw, &req); err != nil {
		return
	}
	d.touch()
	defer d.touch()
	conn := &controlConn{Conn: raw, closed: make(chan struct{})}
	go conn.watchClose()

	select {
	case <-d.shutdown:
		_ = conn.reply(controlReply{Done: true, Error: "session is shutting down"})
		return
	default:
	}

	switch req.Cmd {
	case "say":
		d.handleSay(conn, req)
	case "pending":
		_ = conn.reply(controlReply{Events: d.session.takeUndelivered(), Done: true})
	case "chat-history":
		d.handleHistory(conn)
	case "events":
		d.handleEvents(conn, req)
	case "status":
		_ = conn.reply(controlReply{Status: d.collectStatus(), Done: true})
	case "logs":
		d.handleLogs(conn, req)
	case "stop":
		d.handleStop(conn, req)
	default:
		_ = conn.reply(controlReply{Done: true, Error: "unknown command: " + req.Cmd +
			" (this `lk` may be newer than the running daemon; run `lk agent debugger restart`)"})
	}
}

func (d *sessionDaemon) handleSay(conn *controlConn, req controlRequest) {
	timeout := defaultSayTimeout
	if req.TimeoutMs > 0 {
		timeout = time.Duration(req.TimeoutMs) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	go func() {
		select {
		case <-conn.closed:
			cancel() // client gave up; stop waiting on its behalf
		case <-ctx.Done():
		}
	}()

	// Interleave agent log lines with the turn's events when asked.
	if req.Logs {
		logCh := d.subscribeLogs()
		logDone := make(chan struct{})
		go func() {
			defer close(logDone)
			for {
				select {
				case line := <-logCh:
					_ = conn.reply(controlReply{Event: &turnEvent{Type: "log", Text: line}})
				case <-ctx.Done():
					return
				}
			}
		}()
		defer func() {
			d.unsubscribeLogs(logCh)
			cancel()
			<-logDone
		}()
	}

	sink := func(e turnEvent) {
		_ = conn.reply(controlReply{Event: &e})
	}
	res, err := d.session.Say(ctx, req.Text, sink)
	done := controlReply{
		Done:       true,
		Reply:      res.Reply,
		Silent:     res.Silent,
		DurationMs: res.Duration.Milliseconds(),
	}
	if err != nil {
		done.Error = err.Error()
	}
	_ = conn.reply(done)
}

// handleStop gathers a closing summary (and, on request, the chat history and
// the agent's full log) before tearing the session down, so `stop` can leave
// a record of what happened.
func (d *sessionDaemon) handleStop(conn *controlConn, req controlRequest) {
	st := d.collectStatus()
	var transcript []turnEvent
	if req.Transcript {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		transcript, _ = d.session.History(ctx)
		cancel()
	}
	if req.Lines != 0 {
		for _, line := range d.agentProc.RecentLogs(req.Lines) {
			_ = conn.reply(controlReply{Line: ansiEscapeRe.ReplaceAllString(line, "")})
		}
	}

	d.shutOnce.Do(func() { close(d.shutdown) })
	select {
	case <-d.exited:
	case <-time.After(8 * time.Second):
	}
	_ = conn.reply(controlReply{Done: true, Status: st, Events: transcript})
}

func (d *sessionDaemon) handleHistory(conn *controlConn) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	events, err := d.session.History(ctx)
	if err != nil {
		_ = conn.reply(controlReply{Done: true, Error: err.Error()})
		return
	}
	_ = conn.reply(controlReply{Events: events, Done: true})
}

// handleEvents replays the recent event ring and then taps the live stream
// (plus agent log lines when asked) until the client hangs up. Follow=false is
// still honored for older clients: replay only.
func (d *sessionDaemon) handleEvents(conn *controlConn, req controlRequest) {
	last := req.Lines
	if last == 0 {
		last = 50
	} else if last < 0 {
		last = 0 // everything kept
	}
	if !req.Follow {
		for _, e := range d.session.RecentEvents(last) {
			e := e
			_ = conn.reply(controlReply{Event: &e})
		}
		_ = conn.reply(controlReply{Done: true})
		return
	}
	obs, recent := d.session.observe(last)
	defer d.session.unobserve(obs)
	var logCh chan string
	if req.Logs {
		logCh = d.subscribeLogs()
		defer d.unsubscribeLogs(logCh)
	}
	for _, e := range recent {
		e := e
		if err := conn.reply(controlReply{Event: &e}); err != nil {
			return
		}
	}
	for {
		select {
		case e := <-obs.ch:
			if err := conn.reply(controlReply{Event: &e}); err != nil {
				return
			}
		case line := <-logCh:
			e := turnEvent{Type: "log", Time: eventTimestamp(time.Now()), Text: line}
			if err := conn.reply(controlReply{Event: &e}); err != nil {
				return
			}
		case <-conn.closed:
			return
		case <-d.exited:
			_ = conn.reply(controlReply{Done: true, Error: "agent exited"})
			return
		}
	}
}

func (d *sessionDaemon) handleLogs(conn *controlConn, req controlRequest) {
	n := req.Lines
	switch {
	case n == 0:
		n = 50
	case n < 0:
		n = 0 // RecentLogs(0) returns everything
	}
	var logCh chan string
	if req.Follow {
		// Subscribe before snapshotting so no line falls in the gap.
		logCh = d.subscribeLogs()
		defer d.unsubscribeLogs(logCh)
	}
	for _, line := range d.agentProc.RecentLogs(n) {
		_ = conn.reply(controlReply{Line: ansiEscapeRe.ReplaceAllString(line, "")})
	}
	if !req.Follow {
		_ = conn.reply(controlReply{Done: true})
		return
	}
	for {
		select {
		case line := <-logCh:
			if err := conn.reply(controlReply{Line: line}); err != nil {
				return
			}
		case <-conn.closed:
			return
		case <-d.exited:
			_ = conn.reply(controlReply{Done: true, Error: "agent exited"})
			return
		}
	}
}

// sessionStatus is what `lk agent debugger status` reports.
type sessionStatus struct {
	Port               int       `json:"port"`
	Pid                int       `json:"pid"`
	ProjectDir         string    `json:"project_dir"`
	Entrypoint         string    `json:"entrypoint"`
	ProjectType        string    `json:"project_type"`
	StartedAt          time.Time `json:"started_at"`
	UptimeSeconds      int64     `json:"uptime_seconds"`
	Turns              int       `json:"turns"`
	TurnInProgress     bool      `json:"turn_in_progress"`
	AgentState         string    `json:"agent_state"`
	AgentID            string    `json:"agent_id,omitempty"`
	Tools              []string  `json:"tools,omitempty"`
	Instructions       string    `json:"instructions,omitempty"`
	LogPath            string    `json:"log_path"`
	IdleTimeoutSeconds int64     `json:"idle_timeout_seconds"`
	IdleSeconds        int64     `json:"idle_seconds"`
	UnseenEvents       int       `json:"unseen_events"`
	Error              string    `json:"error,omitempty"`
}

func (d *sessionDaemon) collectStatus() *sessionStatus {
	st := &sessionStatus{
		Port:               d.port,
		Pid:                d.agentProc.Pid(),
		ProjectDir:         d.projectDir,
		Entrypoint:         d.entrypoint,
		ProjectType:        string(d.projectType),
		StartedAt:          d.startedAt,
		UptimeSeconds:      int64(time.Since(d.startedAt).Seconds()),
		Turns:              d.session.Turns(),
		TurnInProgress:     d.session.TurnInProgress(),
		AgentState:         agentStateName(d.session.AgentState()),
		LogPath:            d.agentProc.LogPath,
		IdleTimeoutSeconds: int64(d.idleTimeout.Seconds()),
		IdleSeconds:        int64(d.idleFor().Seconds()),
	}
	d.session.mu.Lock()
	st.UnseenEvents = len(d.session.undelivered)
	d.session.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := d.session.AgentInfo(ctx)
	if err != nil {
		st.Error = "agent info unavailable: " + err.Error()
		return st
	}
	st.AgentID = info.GetId()
	st.Tools = info.GetTools()
	if info.Instructions != nil {
		st.Instructions = *info.Instructions
	}
	return st
}
