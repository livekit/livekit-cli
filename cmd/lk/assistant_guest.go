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
	"slices"
	"strings"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/assistant"
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

// overlayGuestStatus shows that the user's agent is starting, or that it
// failed to start. When it connects, an overlaySpeaker switches to it.
type overlayGuestStatus struct {
	Type   string `json:"type"`
	Status string `json:"status"` // starting or failed
	Name   string `json:"name"`
}

type overlaySpeaker struct {
	Type  string `json:"type"`
	Name  string `json:"name"`  // who the agent turns are from
	Guest bool   `json:"guest"` // the user's own agent, not the assistant
}

// proposeTryProject proposes running the user's agent in the overlay. It's
// only for the project created this session: a starter agent always opens a
// voice session in console mode, but an existing agent may need its frontend,
// dispatch, or other services first. For an existing agent, the assistant
// shows the command to run it in a terminal.
func (s *assistantSession) proposeTryProject(requestID string, confirmed bool) {
	t := &s.tools
	dir := t.lastProject
	if dir == "" {
		s.replyTool(requestID, existingAgentAdvice(t.dir), nil)
		return
	}
	if s.guest != nil || s.guestStarting {
		s.replyTool(requestID, "The user's agent is already starting or running, so nothing else was started. Don't start it again.", nil)
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
	if confirmed {
		s.confirmByVoice(requestID, id, true)
		return
	}
	s.replyTool(requestID, fmt.Sprintf(
		"Waiting for the user to confirm. Confirmation ID: %s. This runs their agent, %s, here so they can talk to it. "+
			"Ask the user in a few words whether to start it. They can answer by voice or on screen. "+
			"If they agree by voice, call confirm_action with this ID and approved=true.", id, name), nil)
}

// existingAgentAdvice tells the assistant how the user can run an agent that
// wasn't created this session.
func existingAgentAdvice(dir string) string {
	command := "lk agent console"
	if paths := assistant.DescribeFolder(dir).AgentPaths(); len(paths) == 1 && paths[0] != "." {
		command = "cd " + paths[0] + " && lk agent console"
	}
	return "You can only run an agent here that you created in this session. Existing agents can need their " +
		"frontend, dispatch, or other services, so they run in the user's own terminal. In one short sentence, " +
		"tell the user you can't start it here, and that they can run it in their terminal to talk to it. Then " +
		`call show_code with title "Terminal", language "shell", and this command: ` + command +
		". If you don't know which folder their agent is in, ask them first, and tell them to run the command in that folder."
}

// startGuest starts the user's agent and switches to it when it connects.
// The assistant tells the user it's starting, which takes a few seconds. The
// overlay shows the switch.
func (s *assistantSession) startGuest(requestID, confirmID, name, dir, entrypoint string, projectType agentfs.ProjectType) {
	t := &s.tools
	s.guestStarting = true
	starting := fmt.Sprintf("Starting the user's agent, %s. It takes a few seconds, and the screen shows when it's connected. "+
		"In one or two short sentences, tell them it's starting, that once it's connected they can talk to it, saying hello if it doesn't greet them, "+
		"and that they can press B to come back to you. "+
		"Don't say you've handed them over: you'll hear if it fails to start. "+
		"If the user talks to you before it connects, tell them in a few words that it's still starting.", name)
	s.out.Send(overlayGuestStatus{Type: "guest_status", Status: "starting", Name: name})
	s.out.Send(overlayConfirmStatus{Type: "confirm_status", ID: confirmID, Status: "running", Text: "Starting " + name + "…"})
	if requestID != "" {
		s.replyTool(requestID, starting, nil)
	} else {
		s.tellUser(starting)
	}
	s.inBackground(func(ctx context.Context) toolResult {
		failed := func(err error) toolResult {
			return toolResult{then: func(toolResult) {
				s.guestStarting = false
				s.out.Send(overlayGuestStatus{Type: "guest_status", Status: "failed", Name: name})
				s.out.Send(overlayConfirmStatus{Type: "confirm_status", ID: confirmID, Status: "failed", Text: "Your agent didn't start. See the terminal"})
				out.Statusf("Your agent didn't start: %v", err)
				s.tellUser(fmt.Sprintf("The user's agent, %s, didn't start, so they're still talking to you. The error:\n%s\n"+
					"In one or two short sentences, tell them what went wrong in plain words, and that the full log is in the terminal.", name, truncate(err.Error(), 1500)))
			}}
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
			FailSignals: guestEndSignals,
		})
		if err != nil {
			server.Close()
			return failed(fmt.Errorf("couldn't start the agent: %w", err))
		}
		conn, err := acceptAgent(ctx, server, proc, "your agent")
		if err != nil {
			proc.Kill()
			server.Close()
			return failed(err)
		}
		return toolResult{then: func(toolResult) {
			s.guestStarting = false
			s.guest = &guestAgent{name: name, dir: dir, proc: proc, conn: conn}
			s.pipeline.AddConn(t.ctx, conn)
			s.pipeline.SetActive(conn)
			s.startSpeakerTurns(overlaySpeaker{Type: "speaker", Name: name, Guest: true})
			s.out.Send(overlayConfirmStatus{Type: "confirm_status", ID: confirmID, Status: "done"})
			go func() {
				// The worker keeps running when its job ends, so watch for
				// the job's end as well as for the exit.
				var err error
				select {
				case err = <-proc.Done():
				case <-proc.Failed():
					if jobCrashed(proc) {
						err = errors.New("its job crashed")
					}
				}
				select {
				case t.results <- toolResult{then: func(toolResult) { s.guestExited(proc, err) }}:
				case <-t.ctx.Done():
				}
			}()
		}}
	})
}

// guestEndSignals are output markers meaning the guest's job ended, though
// its worker may stay alive. An agent whose job ends before it connects, such
// as one that needs data from its frontend, fails right away instead of
// waiting out the connect timeout. "job exiting" is a Python SDK debug log.
var guestEndSignals = append(slices.Clone(consoleCrashSignals), "job exiting")

// jobCrashed reports whether the guest's job ended because it crashed.
func jobCrashed(proc *AgentProcess) bool {
	for _, line := range proc.RecentLogs(50) {
		for _, sig := range consoleCrashSignals {
			if strings.Contains(line, sig) {
				return true
			}
		}
	}
	return false
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
	notice := "Your agent's session ended"
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
