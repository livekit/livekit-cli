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
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/atotto/clipboard"
	"github.com/livekit/protocol/livekit/agent"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/assistant"
	"github.com/livekit/livekit-cli/v2/pkg/console"
	"github.com/livekit/livekit-cli/v2/pkg/overlay"
	"github.com/livekit/livekit-cli/v2/pkg/portaudio"
)

// overlaySender sends messages to the overlay page.
type overlaySender interface {
	Send(msg any) error
}

// overlayBandCount is how many bands the overlay page's meter expects.
const overlayBandCount = 24

// assistantSession runs the assistant agent in console mode, connects it to
// the microphone and speakers, and shows the conversation in the overlay.
type assistantSession struct {
	ov  *overlay.Overlay
	out overlaySender // messages to the overlay page, through log, except in tests
	log *sessionLog
	cmd *cli.Command

	pipeline      *console.AudioPipeline
	assistantConn net.Conn    // the assistant agent's console connection
	guest         *guestAgent // the user's agent, while it runs here

	// Conversation state, mapped onto the overlay's turns and segments. A
	// turn stays the target for text that finishes it (such as the rest of
	// an interrupted speech) after the other side starts a new turn; "open"
	// only says whether new speech joins it.
	turnN        int
	userTurn     string // latest user turn, or ""
	userOpen     bool   // new user speech joins userTurn
	userCommits  string // text the agent committed so far in userTurn
	agentSpoke   bool   // the agent has spoken since userTurn started
	userFinal    string // final transcripts so far in userTurn
	agentTurn    string // latest agent turn, or ""
	agentOpen    bool   // new agent speech joins agentTurn
	agentSegment int
	segmentText  string // text of the current agent segment so far
	segmentDone  bool   // the current agent segment is complete
	finalPending bool   // a streamed segment awaits the agent's committed text
	shownCalls   map[string]bool
	snippetN     int
	toolTurns    map[string]string // tool call id → the turn showing it

	// Ending: set when the agent calls end_call. The overlay closes once the
	// agent's goodbye has finished playing.
	agentSpeaking bool
	lastAudio     time.Time
	ending        bool
	endingSince   time.Time
	spokeGoodbye  bool // the agent spoke after end_call
	agentExited   bool
	dismissed     bool
	snippets      map[string]string

	// The local connection to the assistant agent, and the lk commands it
	// runs through it.
	channel *agentChannel
	tools   toolState
}

// agentMessage is a line from the assistant agent: its spoken text ("text",
// then "flush" at the end of a segment) or a request to run an lk tool
// ("lk_tool").
type agentMessage struct {
	Type string         `json:"type"`
	ID   string         `json:"id"`
	Text string         `json:"text"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`

	// show_code: a code card from the agent itself.
	Title    string `json:"title"`
	Language string `json:"language"`
	Code     string `json:"code"`

	// pipeline: the agent's models, for X-ray mode.
	STT           string `json:"stt"`
	LLM           string `json:"llm"`
	TTS           string `json:"tts"`
	TurnDetection string `json:"turn_detection"`
}

// agentChannel is the local connection to the assistant agent, as JSON lines
// in both directions. The agent connects once it starts.
type agentChannel struct {
	mu   sync.Mutex
	conn net.Conn
}

// serve accepts the agent's connection and sends each line it writes to out.
func (c *agentChannel) serve(ln net.Listener, out chan<- agentMessage) {
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var m agentMessage
		if json.Unmarshal(scanner.Bytes(), &m) == nil {
			out <- m
		}
	}
}

// send writes a message to the agent. It's dropped if the agent isn't
// connected.
func (c *agentChannel) send(msg any) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		_, _ = c.conn.Write(append(b, '\n'))
	}
}

func newAssistantSession(ov *overlay.Overlay, cmd *cli.Command) *assistantSession {
	log := newSessionLog(ov)
	return &assistantSession{
		ov:         ov,
		out:        log,
		log:        log,
		cmd:        cmd,
		shownCalls: map[string]bool{},
		toolTurns:  map[string]string{},
		snippets:   map[string]string{},
	}
}

// run starts the agent and relays between it and the overlay until ctx ends
// or the overlay closes. Errors are also shown in the overlay.
func (s *assistantSession) run(ctx context.Context) (err error) {
	if !s.waitReady(ctx) {
		return nil
	}
	defer func() {
		if err != nil && ctx.Err() != nil {
			// The overlay closed, or lk was interrupted, while the session
			// was still starting. That isn't a failure.
			err = nil
		}
		if err != nil {
			s.showError(startupError(err))
			// The run loop has stopped, but the error card and any code
			// cards are still on screen: keep their Copy buttons working.
			s.serveCopies(ctx)
		}
	}()

	s.connecting("Starting")

	creds, err := resolveCredentials(s.cmd)
	if err != nil {
		return errNoCredentials{err}
	}

	dir := s.cmd.String("agent-dir")
	projectType := agentfs.ProjectTypePythonUV
	if dir == "" {
		if dir, err = assistant.DefaultDir(); err != nil {
			return err
		}
		if err := assistant.Prepare(ctx, dir, s.connecting); err != nil {
			return err
		}
	} else if _, projectType, err = agentfs.DetectProjectRoot(dir); err != nil {
		return fmt.Errorf("no agent project in %s: %w", dir, err)
	}

	if err := portaudio.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize PortAudio: %w", err)
	}
	defer portaudio.Terminate()
	inputDev, err := portaudio.DefaultInputDevice()
	if err != nil {
		return fmt.Errorf("input device: %w", err)
	}
	outputDev, err := portaudio.DefaultOutputDevice()
	if err != nil {
		return fmt.Errorf("output device: %w", err)
	}

	server, err := console.NewTCPServer("127.0.0.1:0")
	if err != nil {
		return err
	}
	defer server.Close()

	// The agent connects here to stream what it says, in time with its
	// speech, and to run lk commands. Console mode itself only
	// reports the agent's text once it finishes speaking.
	channelLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer channelLn.Close()
	agentMessages := make(chan agentMessage, 256)
	s.channel = &agentChannel{}
	go s.channel.serve(channelLn, agentMessages)

	projectDir, err := os.Getwd()
	if err != nil {
		return err
	}
	toolsCtx, cancelTools := context.WithCancel(ctx)
	defer cancelTools()
	s.tools = toolState{
		ctx:     toolsCtx,
		dir:     projectDir,
		creds:   creds,
		results: make(chan toolResult, 16),
		pending: map[string]pendingTool{},
	}

	s.connecting("Starting the assistant agent")
	agentProc, err := startAgent(AgentStartConfig{
		Dir:         dir,
		Entrypoint:  assistant.Entrypoint,
		ProjectType: projectType,
		CLIArgs:     buildConsoleArgs(server.Addr().String(), false),
		Env: append(creds,
			"LK_ASSISTANT_ADDR="+channelLn.Addr().String(),
			"LK_ASSISTANT_PROJECT="+projectDir,
		),
		FailSignals: consoleCrashSignals,
	})
	if err != nil {
		return fmt.Errorf("failed to start the assistant agent: %w", err)
	}
	defer agentProc.Kill()

	conn, err := acceptAgent(ctx, server, agentProc)
	if err != nil {
		return err
	}
	s.assistantConn = conn
	defer func() {
		if s.guest != nil {
			s.guest.conn.Close()
			s.guest.proc.Kill()
		}
	}()

	s.pipeline, err = console.NewPipeline(console.PipelineConfig{
		InputDevice:  inputDev,
		OutputDevice: outputDev,
		Conn:         conn,
	})
	if err != nil {
		return fmt.Errorf("pipeline: %w", err)
	}
	pipelineCtx, cancelPipeline := context.WithCancel(ctx)
	go s.pipeline.Start(pipelineCtx)
	defer func() {
		cancelPipeline()
		// Stop can block forever in PortAudio once the streams are aborted,
		// so, like lk agent console, don't wait for it. The deferred
		// agentProc.Kill runs next either way.
		go s.pipeline.Stop()
	}()

	ticker := time.NewTicker(demoFrame * 2)
	defer ticker.Stop()
	agentDone, agentFailed := agentProc.Done(), agentProc.Failed()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.ov.Done():
			return nil
		case err := <-agentDone:
			if s.ending {
				// The session shut down after end_call, as expected. Its
				// goodbye may still be playing; checkEnding closes the
				// overlay once it has.
				s.agentExited = true
				agentDone = nil
				continue
			}
			s.out.Send(overlayState{"state", "idle"})
			if err != nil {
				return fmt.Errorf("the assistant agent exited: %w\n\n%s", err, agentExitDetail(agentProc))
			}
			return fmt.Errorf("the assistant agent exited.\n\n%s", agentExitDetail(agentProc))
		case <-agentFailed:
			// The worker keeps running when its job crashes, so agentDone
			// doesn't fire.
			if s.ending {
				s.agentExited = true
				agentFailed = nil
				continue
			}
			// The crash marker arrives mid-traceback; give trailing output a moment.
			time.Sleep(500 * time.Millisecond)
			s.out.Send(overlayState{"state", "idle"})
			return fmt.Errorf("the assistant agent crashed.\n\n%s", agentExitDetail(agentProc))
		case ev := <-s.pipeline.Events:
			s.handleEvent(ev)
		case m := <-agentMessages:
			switch m.Type {
			case "text":
				// The assistant is silent while the user's agent is active;
				// anything it says then isn't heard, so don't show it.
				if !s.guestActive() {
					s.agentText(m.Text)
				}
			case "flush":
				if !s.guestActive() {
					s.agentFlush()
				}
			case "lk_tool":
				s.runTool(m.ID, m.Name, m.Args)
			case "show_code":
				s.showAgentCode(m)
			case "pipeline":
				s.out.Send(overlayPipeline{Type: "pipeline", STT: m.STT, LLM: m.LLM, TTS: m.TTS, TurnDetection: m.TurnDetection})
			}
		case r := <-s.tools.results:
			s.toolFinished(r)
		case ev := <-s.ov.Events():
			switch ev.Type {
			case "copy", "copy_text":
				s.handleCopy(ev)
			case "confirm":
				s.confirmTool(ev.ID, ev.Option)
			case "back":
				s.returnToAssistant("")
			case "mute":
				s.pipeline.SetMuted(ev.On)
			}
		case <-ticker.C:
			s.sendBands()
			s.checkEnding()
		}
	}
}

func (s *assistantSession) waitReady(ctx context.Context) bool {
	for {
		select {
		case ev := <-s.ov.Events():
			if ev.Type == "ready" {
				return true
			}
		case <-ctx.Done():
			return false
		case <-s.ov.Done():
			return false
		}
	}
}

// handleCopy copies a code card ("copy") or the text the page sends
// ("copy_text") to the clipboard.
func (s *assistantSession) handleCopy(ev overlay.Event) {
	switch ev.Type {
	case "copy":
		s.copy(ev.ID)
	case "copy_text":
		_ = clipboard.WriteAll(ev.Text)
	}
}

// serveCopies handles copy requests from the page until the overlay closes.
func (s *assistantSession) serveCopies(ctx context.Context) {
	for {
		select {
		case ev := <-s.ov.Events():
			s.handleCopy(ev)
		case <-ctx.Done():
			return
		case <-s.ov.Done():
			return
		}
	}
}

// acceptAgent waits for the agent subprocess to connect to the console server.
func acceptAgent(ctx context.Context, server *console.TCPServer, agentProc *AgentProcess) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	accepted := make(chan result, 1)
	go func() {
		conn, err := server.Accept()
		accepted <- result{conn, err}
	}()

	select {
	case res := <-accepted:
		if res.err != nil {
			return nil, fmt.Errorf("agent connection: %w", res.err)
		}
		return res.conn, nil
	case err := <-agentProc.Done():
		if err != nil {
			return nil, fmt.Errorf("the assistant agent exited before connecting: %w\n\n%s", err, agentExitDetail(agentProc))
		}
		return nil, fmt.Errorf("the assistant agent exited before connecting.\n\n%s", agentExitDetail(agentProc))
	case <-agentProc.Failed():
		// The crash marker arrives mid-traceback; give trailing output a moment.
		time.Sleep(500 * time.Millisecond)
		return nil, fmt.Errorf("the assistant agent crashed before connecting.\n\n%s", agentExitDetail(agentProc))
	case <-time.After(90 * time.Second):
		return nil, fmt.Errorf("timed out waiting for the assistant agent to connect.\n\n%s", agentExitDetail(agentProc))
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *assistantSession) connecting(label string) {
	s.out.Send(map[string]string{"type": "state", "state": "connecting", "label": label})
}

// sendBands sends the levels of whoever is audible: the agent while its audio
// plays, otherwise the (echo-cancelled) microphone.
func (s *assistantSession) sendBands() {
	var bands [console.NumFFTBands]float64
	var level float64
	if s.pipeline.IsPlaying() {
		s.lastAudio = time.Now()
		bands, level = s.pipeline.PlaybackFFTBands(), s.pipeline.PlaybackLevel()
	} else {
		bands, level = s.pipeline.FFTBands(), s.pipeline.Level()
	}
	// The bands are peak-normalized per frame, so room noise would fill the
	// meter. Gate them by the overall level.
	gate := smoothstep(-55, -38, level)
	if gate == 0 {
		return
	}
	out := resampleBands(bands[:], overlayBandCount)
	for i := range out {
		out[i] = math.Round(out[i]*gate*100) / 100
	}
	s.out.Send(overlayBands{"bands", out})
}

func (s *assistantSession) handleEvent(ev *agent.AgentSessionEvent) {
	switch e := ev.GetEvent().(type) {
	case *agent.AgentSessionEvent_AgentStateChanged_:
		s.agentSpeaking = e.AgentStateChanged.GetNewState() == agent.AgentState_AS_SPEAKING
		if s.agentSpeaking && s.ending {
			s.spokeGoodbye = true
		}
		switch e.AgentStateChanged.GetNewState() {
		case agent.AgentState_AS_IDLE:
			s.out.Send(overlayState{"state", "idle"})
		case agent.AgentState_AS_LISTENING:
			s.out.Send(overlayState{"state", "listening"})
		case agent.AgentState_AS_THINKING:
			s.out.Send(overlayState{"state", "thinking"})
		case agent.AgentState_AS_SPEAKING:
			s.beginAgentSpeech()
			s.out.Send(overlayState{"state", "speaking"})
		}

	case *agent.AgentSessionEvent_UserInputTranscribed_:
		s.userTranscript(e.UserInputTranscribed.GetTranscript(), e.UserInputTranscribed.GetIsFinal())

	case *agent.AgentSessionEvent_ConversationItemAdded_:
		switch item := e.ConversationItemAdded.GetItem().GetItem().(type) {
		case *agent.ChatContext_ChatItem_Message:
			te, ok := messageToTurnEvent(item.Message)
			if !ok {
				return
			}
			if te.Role == "user" {
				s.userCommitted(te.Text)
			} else {
				s.agentMessage(te.Text)
			}
			if m := item.Message.GetMetrics(); m != nil {
				s.out.Send(metricsMessage(te.Role, m))
			}
		case *agent.ChatContext_ChatItem_FunctionCall:
			s.toolCall(item.FunctionCall)
		}

	case *agent.AgentSessionEvent_FunctionToolsStarted_:
		for _, fc := range e.FunctionToolsStarted.GetFunctionCalls() {
			s.toolCall(fc)
		}

	case *agent.AgentSessionEvent_FunctionToolsExecuted_:
		failed := map[string]bool{}
		for _, out := range e.FunctionToolsExecuted.GetFunctionCallOutputs() {
			failed[out.GetCallId()] = out.GetIsError()
		}
		for _, fc := range e.FunctionToolsExecuted.GetFunctionCalls() {
			s.toolCall(fc)
			s.toolDone(fc.GetCallId(), failed[fc.GetCallId()])
		}

	case *agent.AgentSessionEvent_Error_:
		// Session errors (such as rejected credentials) usually end the
		// conversation, so keep them on screen. Errors the agent retries
		// don't stay.
		msg := e.Error.GetMessage()
		if msg == "" {
			return
		}
		// The user's own agent uses its own providers and keys.
		if !s.guestActive() && isAuthError(msg) {
			s.showError(overlayError{Type: "error",
				Title:  "LiveKit Cloud rejected your project's API key",
				Detail: "The assistant uses LiveKit Inference through your default lk project, and its key didn't work. Sign in again, or pick a different project with --project.",
				Fix:    "lk cloud auth"})
			return
		}
		s.out.Send(overlayNotice{Type: "notice", Text: msg, Sticky: !strings.Contains(msg, "recoverable=True")})

	case *agent.AgentSessionEvent_EotPrediction_:
		s.out.Send(overlayEOT{Type: "eot", Probability: e.EotPrediction.GetProbability(), Threshold: e.EotPrediction.GetThreshold()})

	case *agent.AgentSessionEvent_SessionUsageUpdated_:
		s.out.Send(usageMessage(e.SessionUsageUpdated.GetUsage()))
	}
}

// isAuthError reports whether a session error says the API key was rejected.
// The message is the agent's error event as text, such as "type='llm_error'
// timestamp=... error=APIStatusError('...', status_code=401, ...)
// recoverable=False".
func isAuthError(msg string) bool {
	msg = strings.ToLower(msg)
	return strings.Contains(msg, "status_code=401") || strings.Contains(msg, "invalid api key")
}

// userTurnForSpeech makes sure new user speech has an open turn to join. A
// new user turn means the agent's next reply starts a new turn too.
func (s *assistantSession) userTurnForSpeech() {
	if s.userOpen {
		return
	}
	if s.userTurn != "" && !s.agentSpoke {
		// The agent hasn't said anything since the user's last turn (at most
		// it started looking things up), so the user is still finishing the
		// same thought, such as after a mid-sentence pause.
		s.userOpen = true
		return
	}
	s.agentSpoke = false
	s.turnN++
	s.userTurn = fmt.Sprintf("u%d", s.turnN)
	s.userOpen = true
	s.userCommits = ""
	s.userFinal = ""
	s.agentOpen = false
}

// agentTurnForReply makes sure a new agent reply has an open turn to join.
// It closes the user's turn, so the user's next speech starts a new turn,
// unless the agent hasn't spoken yet (see userTurnForSpeech). That holds
// even when the reply joins an agent turn opened earlier, such as by a tool
// call while the user was still talking.
func (s *assistantSession) agentTurnForReply() {
	s.userOpen = false
	if s.agentOpen {
		return
	}
	s.turnN++
	s.agentTurn = fmt.Sprintf("a%d", s.turnN)
	s.agentOpen = true
	s.agentSegment = 0
	s.segmentText = ""
	s.segmentDone = false
	s.finalPending = false
}

// streaming reports whether the agent is partway through speaking the
// current segment, so its remaining text belongs there.
func (s *assistantSession) streaming() bool {
	return s.agentTurn != "" && s.segmentText != "" && !s.segmentDone
}

// nextSegment moves to a new agent paragraph unless the current one is unused.
func (s *assistantSession) nextSegment() {
	if s.segmentText == "" && !s.segmentDone {
		return
	}
	s.agentSegment++
	s.segmentText = ""
	s.segmentDone = false
}

func (s *assistantSession) sendAgentSegment() {
	s.out.Send(overlayTranscript{"transcript", s.agentTurn, "agent", s.agentSegment, s.segmentText})
}

func (s *assistantSession) userTranscript(text string, final bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.userTurnForSpeech()
	full := strings.TrimSpace(s.userFinal + " " + text)
	if final {
		s.userFinal = full
	}
	s.out.Send(overlayTranscript{"transcript", s.userTurn, "user", 0, full})
}

// userCommitted replaces the live transcript with the text the agent
// committed to the conversation. A user who pauses mid-thought can get
// several commits in a row; until the agent starts replying, they're one
// turn, and the turn stays open for more speech.
func (s *assistantSession) userCommitted(text string) {
	s.userTurnForSpeech()
	s.userCommits = strings.TrimSpace(s.userCommits + " " + text)
	s.userFinal = s.userCommits
	s.out.Send(overlayTranscript{"transcript", s.userTurn, "user", 0, s.userCommits})
}

// beginAgentSpeech adds an empty agent paragraph so the page shows its cursor
// until the first words arrive. A speech that resumes keeps its paragraph.
func (s *assistantSession) beginAgentSpeech() {
	s.agentSpoke = true
	if s.streaming() && s.agentOpen {
		return
	}
	s.agentTurnForReply()
	if s.segmentDone {
		s.nextSegment()
	}
	s.sendAgentSegment()
}

// agentText adds text the agent is speaking right now. The rest of a speech
// the user interrupted stays in that speech's turn.
func (s *assistantSession) agentText(text string) {
	s.agentSpoke = true
	if !s.streaming() {
		s.agentTurnForReply()
		if s.segmentDone {
			s.nextSegment()
		}
	}
	s.segmentText += text
	s.sendAgentSegment()
}

func (s *assistantSession) agentFlush() {
	if s.segmentText != "" {
		s.segmentDone = true
		s.finalPending = true
	}
}

// agentMessage handles text the agent committed to the conversation after
// speaking. If the same speech was streamed, the streamed text stays: it is
// already free of expressive markup and stops where an interruption cut the
// speech off. Otherwise the committed text is shown, with its markup removed.
func (s *assistantSession) agentMessage(text string) {
	s.agentSpoke = true
	if s.finalPending || s.streaming() {
		s.segmentDone = true
		s.finalPending = false
		return
	}
	s.agentTurnForReply()
	s.nextSegment()
	s.segmentText = stripMarkup(text)
	s.segmentDone = true
	s.sendAgentSegment()
}

var (
	markupTagRe   = regexp.MustCompile(`</?[A-Za-z][^>]*>`)
	markupSpaceRe = regexp.MustCompile(`\s{2,}`)
)

// stripMarkup removes expressive markup tags, such as <expr .../>, from
// committed agent text. The agent strips streamed text with the SDK's own
// stripper; this is only the fallback for text that wasn't streamed.
func stripMarkup(text string) string {
	return strings.TrimSpace(markupSpaceRe.ReplaceAllString(markupTagRe.ReplaceAllString(text, ""), " "))
}

// toolCall handles a tool call the agent made. show_code calls become code
// cards, end_call starts closing the overlay, and other calls (docs searches)
// show as a line in the agent's turn. Each call arrives in several events;
// only the first counts.
func (s *assistantSession) toolCall(fc *agent.FunctionCall) {
	switch fc.GetName() {
	case assistant.ShowCodeTool:
		s.showCode(fc)
	case assistant.HandOffTaskTool:
		s.handOffTask(fc)
	case assistant.EndCallTool:
		// Only the assistant's end_call closes the overlay; the user's own
		// agent may have one too.
		if !s.ending && !s.guestActive() {
			s.ending = true
			s.endingSince = time.Now()
		}
	default:
		id := fc.GetCallId()
		if _, ok := s.toolTurns[id]; ok {
			return
		}
		s.agentTurnForReply()
		s.toolTurns[id] = s.agentTurn
		label, detail := describeTool(fc.GetName(), fc.GetArguments())
		s.out.Send(overlayTool{Type: "tool", Turn: s.agentTurn, ID: id, Label: label, Detail: detail, Status: "running"})
	}
}

// toolDone marks a tool call's line as finished.
func (s *assistantSession) toolDone(id string, failed bool) {
	turn, ok := s.toolTurns[id]
	if !ok {
		return
	}
	status := "done"
	if failed {
		status = "error"
	}
	s.out.Send(overlayTool{Type: "tool", Turn: turn, ID: id, Status: status})
}

// describeTool returns a short label for a tool call and the main argument,
// such as the search query.
func describeTool(name, arguments string) (label, detail string) {
	var args map[string]any
	_ = json.Unmarshal([]byte(arguments), &args)
	for _, key := range []string{"query", "paths", "path", "package", "repo", "template", "name"} {
		switch v := args[key].(type) {
		case string:
			detail = v
		case []any:
			parts := make([]string, 0, len(v))
			for _, p := range v {
				if s, ok := p.(string); ok {
					parts = append(parts, s)
				}
			}
			detail = strings.Join(parts, ", ")
		}
		if detail != "" {
			break
		}
	}
	switch name {
	case "get_docs_overview":
		return "Read the docs overview", detail
	case "docs_search":
		return "Searched the docs", detail
	case "get_pages":
		return "Read the docs", detail
	case "code_search":
		return "Searched LiveKit code", detail
	case "get_project_info":
		return "Checked your LiveKit project", detail
	case "list_cloud_agents":
		return "Listed your agents", detail
	case "list_templates":
		return "Listed the templates", detail
	case "create_project":
		return "Create a project", detail
	case "confirm_action":
		var a struct {
			Approved bool `json:"approved"`
		}
		_ = json.Unmarshal([]byte(arguments), &a)
		if a.Approved {
			return "Confirmed by voice", ""
		}
		return "Declined by voice", ""
	case "set_up_coding_agent":
		return "Set up your coding agent", detail
	default:
		return strings.ReplaceAll(name, "_", " "), detail
	}
}

// checkEnding closes the overlay after end_call, once the agent's goodbye has
// finished playing. The goodbye comes after the tool call, so a pause before
// it doesn't count. It gives up waiting after endingTimeout.
func (s *assistantSession) checkEnding() {
	if !s.ending || s.dismissed {
		return
	}
	playing := s.pipeline.IsPlaying()
	if playing {
		s.spokeGoodbye = true
	}
	quiet := !playing && !s.agentSpeaking && time.Since(s.lastAudio) > 800*time.Millisecond
	if ((s.spokeGoodbye || s.agentExited) && quiet) || time.Since(s.endingSince) > endingTimeout {
		s.dismiss()
	}
}

const endingTimeout = 15 * time.Second

// dismiss plays the overlay's exit animation, which then closes it.
func (s *assistantSession) dismiss() {
	if s.dismissed {
		return
	}
	s.dismissed = true
	s.out.Send(map[string]string{"type": "dismiss"})
}

// showCode shows a show_code call as a code card.
func (s *assistantSession) showCode(fc *agent.FunctionCall) {
	if s.shownCalls[fc.GetCallId()] {
		return
	}
	var args struct {
		Title    string `json:"title"`
		Language string `json:"language"`
		Code     string `json:"code"`
	}
	if err := json.Unmarshal([]byte(fc.GetArguments()), &args); err != nil || args.Code == "" {
		return
	}
	s.shownCalls[fc.GetCallId()] = true
	s.agentTurnForReply()

	id := fc.GetCallId()
	s.snippets[id] = args.Code
	s.out.Send(overlaySnippet{"snippet", s.agentTurn, id, args.Title, normalizeLang(args.Language), args.Code, false})
}

// handOffTask shows a hand_off_task call as a prompt card for the user to
// copy into their coding agent.
func (s *assistantSession) handOffTask(fc *agent.FunctionCall) {
	if s.shownCalls[fc.GetCallId()] {
		return
	}
	var args struct {
		Task string `json:"task"`
	}
	if err := json.Unmarshal([]byte(fc.GetArguments()), &args); err != nil || args.Task == "" {
		return
	}
	s.shownCalls[fc.GetCallId()] = true
	s.agentTurnForReply()

	id := fc.GetCallId()
	s.snippets[id] = args.Task
	s.out.Send(overlaySnippet{"snippet", s.agentTurn, id, "Prompt for your coding agent", "prompt", args.Task, false})
}

func (s *assistantSession) copy(id string) {
	code, ok := s.snippets[id]
	if !ok || clipboard.WriteAll(code) != nil {
		return
	}
	s.out.Send(overlayCopied{"copied", id})
}

// normalizeLang maps language names to the ones the page highlights.
func normalizeLang(lang string) string {
	switch l := strings.ToLower(strings.TrimSpace(lang)); l {
	case "bash", "sh", "zsh", "console", "terminal":
		return "shell"
	case "py":
		return "python"
	case "ts", "tsx", "typescript":
		return "typescript"
	case "js", "jsx", "mjs", "javascript", "node", "nodejs":
		return "javascript"
	default:
		return l
	}
}

// resampleBands linearly interpolates bands to n values.
func resampleBands(src []float64, n int) []float64 {
	out := make([]float64, n)
	if len(src) == 0 {
		return out
	}
	for i := range out {
		f := float64(i) * float64(len(src)-1) / float64(n-1)
		j := int(f)
		if j >= len(src)-1 {
			out[i] = src[len(src)-1]
			continue
		}
		t := f - float64(j)
		out[i] = src[j]*(1-t) + src[j+1]*t
	}
	return out
}
