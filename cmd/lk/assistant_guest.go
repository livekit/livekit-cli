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
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/console"
)

// "Meet your agent": the user's own agent runs in console mode and joins the
// same audio pipeline as the assistant. Switching changes which agent hears
// the microphone. The assistant keeps running, silent, so the user returns to
// the same conversation.

// guestAgent is the user's agent while it runs in the overlay.
type guestAgent struct {
	name string // shown as the speaker
	dir  string
	proc *AgentProcess
	conn net.Conn
}

type overlaySpeaker struct {
	Type  string `json:"type"`
	Name  string `json:"name"`  // who the agent turns are from
	Guest bool   `json:"guest"` // the user's own agent, not the assistant
}

// proposeTryProject proposes running the user's agent in the overlay. path
// is relative to the project directory; empty means the project created this
// session, or the current directory.
func (s *assistantSession) proposeTryProject(requestID, path string) {
	t := &s.tools
	dir := t.dir
	switch {
	case path != "":
		dir = filepath.Join(t.dir, path)
	case t.lastProject != "":
		dir = t.lastProject
	}
	if s.guest != nil {
		s.replyTool(requestID, "", errors.New("the user's agent is already running; they can press B to come back first"))
		return
	}
	root, projectType, err := agentfs.DetectProjectRoot(dir)
	if err != nil || (!projectType.IsPython() && !projectType.IsNode()) {
		s.replyTool(requestID, "", fmt.Errorf("there's no Python or Node.js agent project in %s", tildePath(dir)))
		return
	}
	entrypoint, err := findEntrypoint(root, "", projectType)
	if err != nil {
		s.replyTool(requestID, "", err)
		return
	}
	name := filepath.Base(root)

	t.confirmN++
	id := fmt.Sprintf("confirm%d", t.confirmN)
	t.pending[id] = pendingTool{
		command: "lk agent console",
		start: func(requestID, confirmID string) {
			s.startGuest(requestID, confirmID, name, root, entrypoint, projectType)
		},
	}
	s.agentTurnForReply()
	s.out.Send(overlayConfirm{
		Type: "confirm", Turn: s.agentTurn, ID: id,
		Heading: "Run in " + tildePath(root),
		Command: "lk agent console",
		Detail:  fmt.Sprintf("Runs your agent, %s, on this machine and switches the conversation to it. Press B to come back to LK.", name),
		Options: []overlayConfirmOption{{ID: confirmRun, Name: "Talk to it"}, {ID: confirmCancel, Name: "Cancel"}},
	})
	s.replyTool(requestID, fmt.Sprintf(
		"Waiting for the user to confirm. Confirmation ID: %s. This runs their agent, %s, here so they can talk to it. "+
			"Ask the user, in one short sentence, whether to start it. They can answer by voice or on screen. "+
			"If they agree by voice, call confirm_action with this ID and approved=true.", id, name), nil)
}

// startGuest starts the user's agent and switches to it as soon as it
// connects. It greets right away, so there's no waiting: the assistant says
// its hand-off sentence while the agent starts up, which takes a few seconds.
func (s *assistantSession) startGuest(requestID, confirmID, name, dir, entrypoint string, projectType agentfs.ProjectType) {
	t := &s.tools
	handOff := fmt.Sprintf("Starting the user's agent, %s. It takes a few seconds. In one or two short sentences, tell them you're handing them over to it, that they should say hello to start (starter agents wait for the user to speak first), and that they can press B to come back to you.", name)
	if requestID != "" {
		s.replyTool(requestID, handOff, nil)
	} else {
		s.tellUser(handOff)
	}
	s.inBackground(func(ctx context.Context) toolResult {
		failed := func(err error) toolResult {
			return toolResult{confirmID: confirmID, command: "lk agent console", err: err}
		}
		// The guest needs its own console server: the assistant's accepts
		// only one agent, and closes once the assistant connects.
		server, err := console.NewTCPServer("127.0.0.1:0")
		if err != nil {
			return failed(err)
		}
		proc, err := startAgent(AgentStartConfig{
			Dir:         dir,
			Entrypoint:  entrypoint,
			ProjectType: projectType,
			CLIArgs:     buildConsoleArgs(server.Addr().String(), false),
			Env:         t.creds,
			FailSignals: consoleCrashSignals,
		})
		if err != nil {
			server.Close()
			return failed(fmt.Errorf("couldn't start the agent: %w", err))
		}
		conn, err := acceptAgent(ctx, server, proc)
		if err != nil {
			proc.Kill()
			server.Close()
			return failed(err)
		}
		return toolResult{then: func(toolResult) {
			s.guest = &guestAgent{name: name, dir: dir, proc: proc, conn: conn}
			s.pipeline.AddConn(t.ctx, conn)
			s.pipeline.SetActive(conn)
			s.startSpeakerTurns(overlaySpeaker{Type: "speaker", Name: name, Guest: true})
			s.out.Send(overlayConfirmStatus{Type: "confirm_status", ID: confirmID, Status: "done"})
			go func() {
				// The worker keeps running when its job crashes, so watch
				// for the crash as well as for the exit.
				var err error
				select {
				case err = <-proc.Done():
				case <-proc.Failed():
					err = errors.New("its job crashed")
				}
				select {
				case t.results <- toolResult{then: func(toolResult) { s.guestExited(proc, err) }}:
				case <-t.ctx.Done():
				}
			}()
		}}
	})
}

// returnToAssistant switches back to the assistant and stops the guest.
func (s *assistantSession) returnToAssistant(notice string) {
	g := s.guest
	if g == nil {
		return
	}
	s.guest = nil
	s.pipeline.SetActive(s.assistantConn)
	g.conn.Close()
	go g.proc.Kill()
	s.startSpeakerTurns(overlaySpeaker{Type: "speaker", Name: "LiveKit"})
	if notice != "" {
		s.out.Send(overlayNotice{Type: "notice", Text: notice})
	}
	s.tellUser(fmt.Sprintf("The user is back from talking to their agent, %s. Welcome them back in one short sentence and ask how it went or what they'd like to change.", g.name))
}

func (s *assistantSession) guestExited(proc *AgentProcess, err error) {
	if s.guest == nil || s.guest.proc != proc {
		return
	}
	notice := "Your agent stopped"
	if err != nil {
		notice = "Your agent stopped with an error. See the terminal"
		out.Statusf("Your agent stopped: %v\n%s", err, agentExitDetail(proc))
	}
	s.returnToAssistant(notice)
}

// startSpeakerTurns makes the next turns start fresh, labeled for speaker.
func (s *assistantSession) startSpeakerTurns(speaker overlaySpeaker) {
	s.agentOpen = false
	s.userOpen = false
	s.agentSpoke = true
	s.agentSpeaking = false
	s.out.Send(speaker)
	s.out.Send(overlayState{"state", "listening"})
}

// guestActive reports whether the user's agent is the active one.
func (s *assistantSession) guestActive() bool {
	return s.guest != nil
}
