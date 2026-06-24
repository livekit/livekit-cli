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
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/console"
	"github.com/livekit/livekit-cli/v2/pkg/ipc"

	agent "github.com/livekit/protocol/livekit/agent"
)

// runSessionDaemon is the entry point for the hidden `lk agent daemon serve`
// subcommand that `lk agent daemon start` re-execs. It runs the detached
// daemon to completion (until the agent exits or `stop` is received).
func runSessionDaemon() {
	ready := readyWriter()
	port, _ := strconv.Atoi(os.Getenv(envSessionPort))

	// The fixed port is the singleton: if the bind fails, a session already
	// owns it, which is how `lk agent daemon start` learns to reject.
	server, err := console.NewTCPServer(sessionAddr(port))
	if err != nil {
		signalReady(ready, "error: a session is already running on "+sessionAddr(port))
		os.Exit(1)
	}
	defer server.Close()

	// TODO(node): detect a node/JS agent project and build the equivalent
	// `node <entry> console --connect-addr <addr>` argv.
	agentProc, err := startAgent(AgentStartConfig{
		Dir:         os.Getenv(envSessionDir),
		Entrypoint:  os.Getenv(envSessionEntry),
		ProjectType: agentfs.ProjectType(os.Getenv(envSessionPType)),
		CLIArgs:     buildConsoleArgs(server.Addr().String(), false),
	})
	if err != nil {
		signalReady(ready, "error: failed to start agent: "+err.Error())
		os.Exit(1)
	}

	d := &sessionDaemon{
		server:     server,
		agentProc:  agentProc,
		events:     make(chan *agent.AgentSessionEvent, 64),
		responses:  make(chan *agent.SessionResponse, 8),
		queue:      make(chan *sessionCommand, 16),
		agentReady: make(chan struct{}),
		agentDone:  make(chan struct{}),
		shutdown:   make(chan struct{}),
	}

	go d.acceptLoop()

	select {
	case <-d.agentReady:
		d.setTextMode()
		signalReady(ready, "ready")
	case waitErr := <-agentProc.Done():
		msg := "error: agent exited before connecting"
		if waitErr != nil {
			msg += ": " + waitErr.Error()
		}
		signalReady(ready, msg)
		agentProc.Kill()
		os.Exit(1)
	case <-time.After(60 * time.Second):
		signalReady(ready, "error: timed out waiting for agent to connect")
		agentProc.Kill()
		os.Exit(1)
	}

	go d.worker()

	select {
	case <-d.agentDone:
	case <-d.shutdown:
	}
	agentProc.Kill()
}

// readyWriter returns the path of the readiness file `lk agent daemon start`
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

	agentMu   sync.Mutex
	agentConn net.Conn
	agentRead io.Reader
	writeMu   sync.Mutex // serializes writes to the agent connection

	events    chan *agent.AgentSessionEvent
	responses chan *agent.SessionResponse
	queue     chan *sessionCommand

	agentReady chan struct{}
	agentDone  chan struct{}
	doneOnce   sync.Once
	shutdown   chan struct{}
	shutOnce   sync.Once

	reqCounter int
}

type sessionCommand struct {
	kind string
	text string
	out  net.Conn
	done chan struct{}
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
	d.agentMu.Lock()
	if d.agentConn != nil {
		d.agentMu.Unlock()
		conn.Close()
		return
	}
	d.agentConn = conn
	d.agentRead = reader
	d.agentMu.Unlock()

	close(d.agentReady)
	go d.agentMessageLoop()
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

func (d *sessionDaemon) agentMessageLoop() {
	for {
		msg := &agent.AgentSessionMessage{}
		if err := ipc.ReadProto(d.agentRead, msg); err != nil {
			d.doneOnce.Do(func() { close(d.agentDone) })
			return
		}
		switch m := msg.Message.(type) {
		case *agent.AgentSessionMessage_Event:
			select {
			case d.events <- m.Event:
			default:
			}
		case *agent.AgentSessionMessage_Response:
			if m.Response != nil {
				select {
				case d.responses <- m.Response:
				default:
				}
			}
		case *agent.AgentSessionMessage_AudioOutput, *agent.AgentSessionMessage_AudioPlaybackClear:
			// No audio sink in text mode: drop.
		case *agent.AgentSessionMessage_AudioPlaybackFlush:
			// Nothing to drain, so ack immediately or the agent's turn (and the
			// RunInputResponse we await) never completes.
			_ = d.writeAgent(&agent.AgentSessionMessage{
				Message: &agent.AgentSessionMessage_AudioPlaybackFinished{
					AudioPlaybackFinished: &agent.AgentSessionMessage_ConsoleIO_AudioPlaybackFinished{},
				},
			})
		}
	}
}

func (d *sessionDaemon) writeAgent(msg *agent.AgentSessionMessage) error {
	d.agentMu.Lock()
	conn := d.agentConn
	d.agentMu.Unlock()
	if conn == nil {
		return fmt.Errorf("agent not connected")
	}
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	return ipc.WriteProto(conn, msg)
}

// setTextMode disables the agent's audio I/O so it runs as a pure text turn
// handler, matching what `lk agent console` does when switching to text mode.
func (d *sessionDaemon) setTextMode() {
	off := false
	_ = d.writeAgent(&agent.AgentSessionMessage{
		Message: &agent.AgentSessionMessage_Request{
			Request: &agent.SessionRequest{
				RequestId: "session-io",
				Request: &agent.SessionRequest_UpdateIo{
					UpdateIo: &agent.SessionRequest_UpdateIO{
						Input:  &agent.SessionRequest_UpdateIO_Input{AudioEnabled: &off},
						Output: &agent.SessionRequest_UpdateIO_Output{AudioEnabled: &off, TranscriptionEnabled: &off},
					},
				},
			},
		},
	})
}

func (d *sessionDaemon) handleControlConn(conn net.Conn) {
	var req controlRequest
	if err := readControlFrame(conn, &req); err != nil {
		conn.Close()
		return
	}
	cmd := &sessionCommand{kind: req.Cmd, text: req.Text, out: conn, done: make(chan struct{})}
	select {
	case d.queue <- cmd:
	case <-d.shutdown:
		conn.Close()
		return
	}
	<-cmd.done
	conn.Close()
}

func (d *sessionDaemon) worker() {
	for {
		select {
		case cmd := <-d.queue:
			d.runCommand(cmd)
		case <-d.shutdown:
			return
		}
	}
}

func (d *sessionDaemon) runCommand(cmd *sessionCommand) {
	defer close(cmd.done)
	switch cmd.kind {
	case "say":
		d.runSay(cmd)
	case "stop":
		_ = writeControlFrame(cmd.out, controlReply{Done: true})
		d.shutOnce.Do(func() { close(d.shutdown) })
	default:
		_ = writeControlFrame(cmd.out, controlReply{Done: true, Error: "unknown command: " + cmd.kind})
	}
}

func (d *sessionDaemon) runSay(cmd *sessionCommand) {
	d.reqCounter++
	reqID := "session-" + strconv.Itoa(d.reqCounter)

	d.drainEvents() // discard anything emitted before this turn (e.g. greeting)
	_ = writeControlFrame(cmd.out, controlReply{Line: renderUserMessage(cmd.text)})

	if err := d.writeAgent(&agent.AgentSessionMessage{
		Message: &agent.AgentSessionMessage_Request{
			Request: &agent.SessionRequest{
				RequestId: reqID,
				Request: &agent.SessionRequest_RunInput_{
					RunInput: &agent.SessionRequest_RunInput{Text: cmd.text},
				},
			},
		},
	}); err != nil {
		_ = writeControlFrame(cmd.out, controlReply{Done: true, Error: err.Error()})
		return
	}

	for {
		select {
		case ev := <-d.events:
			if line := renderEvent(ev); line != "" {
				if err := writeControlFrame(cmd.out, controlReply{Line: line}); err != nil {
					return
				}
			}
		case resp := <-d.responses:
			if resp.GetRequestId() == reqID {
				d.flushEvents(cmd.out)
				_ = writeControlFrame(cmd.out, controlReply{Done: true})
				return
			}
		case <-d.agentDone:
			_ = writeControlFrame(cmd.out, controlReply{Done: true, Error: "agent exited"})
			return
		}
	}
}

func (d *sessionDaemon) drainEvents() {
	for {
		select {
		case <-d.events:
		default:
			return
		}
	}
}

func (d *sessionDaemon) flushEvents(out net.Conn) {
	for {
		select {
		case ev := <-d.events:
			if line := renderEvent(ev); line != "" {
				_ = writeControlFrame(out, controlReply{Line: line})
			}
		default:
			return
		}
	}
}
