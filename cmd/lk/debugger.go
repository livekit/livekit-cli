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
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// Single-session model: the fixed loopback port is the singleton registry.
// The daemon binds it; whoever wins the bind() is "the session". start, say,
// and stop all rendezvous on this one port. No session id, manifest, or dir.
const (
	sessionMagic       = "LKCP" // 4-byte preamble that marks a control connection
	sessionHost        = "127.0.0.1"
	defaultSessionPort = 8775

	envSessionPort      = "LK_SESSION_PORT"         // fixed port
	envSessionDir       = "LK_SESSION_DIR"          // resolved project dir
	envSessionEntry     = "LK_SESSION_ENTRY"        // resolved entrypoint (project-relative)
	envSessionPType     = "LK_SESSION_PTYPE"        // agentfs.ProjectType string
	envSessionReadyFile = "LK_SESSION_READY_FILE"   // path the daemon writes its status to
	envSessionIdle      = "LK_SESSION_IDLE_TIMEOUT" // stop after this long without commands (0 = never)
	envSessionAudio     = "LK_SESSION_AUDIO"        // set: speak turns instead of sending text

	// The project credentials an audio session speaks the user's turns with.
	// They are namespaced so the agent, which inherits the daemon's
	// environment, still resolves its own LIVEKIT_* credentials.
	envSessionURL       = "LK_SESSION_LIVEKIT_URL"
	envSessionAPIKey    = "LK_SESSION_LIVEKIT_API_KEY"
	envSessionAPISecret = "LK_SESSION_LIVEKIT_API_SECRET"

	// defaultIdleTimeout is how long the daemon waits for a command before
	// stopping itself, so a caller that never runs `stop` doesn't leave an
	// agent process behind.
	defaultIdleTimeout = 30 * time.Minute

	// sessionDaemonSubcommand is the hidden entrypoint `start` re-execs into.
	sessionDaemonSubcommand = "serve"
)

var sessionPortFlag = &cli.IntFlag{
	Name:    "port",
	Sources: cli.EnvVars(envSessionPort),
	Value:   defaultSessionPort,
	Usage:   "Loopback port the session listens on (one session per port)",
}

var sessionIdleFlag = &cli.DurationFlag{
	Name:  "idle-timeout",
	Value: defaultIdleTimeout,
	Usage: "Stop the session after `DURATION` without any command, such as 30m, 2h, or 90s (0 keeps it running until stop)",
}

var sessionAudioFlag = &cli.BoolFlag{
	Name:  "audio",
	Usage: "Speak each turn into the agent's microphone input instead of sending text, running its full audio pipeline",
}

var sessionMetricsFlag = &cli.BoolFlag{
	Name:  "metrics",
	Usage: "Show latency metrics: time to first token per reply and how long each turn took",
}

// renderFlags reads the shared rendering flags off a command.
func renderFlags(cmd *cli.Command) renderOptions {
	return renderOptions{Metrics: cmd.Bool("metrics")}
}

func init() {
	// Register under the `agent` group as `lk agent debugger`, mirroring how
	// `lk agent console` attaches itself. Unlike console, this command is not
	// gated behind the `console` build tag: it is CGO-free and ships in the
	// default binary.
	// "daemon" is the name this shipped under first; keep it working without
	// advertising it. cli/v3 lists aliases in help, so a hidden copy is used
	// instead of Aliases.
	alias := *agentDaemonCommand
	alias.Name = "daemon"
	alias.Hidden = true
	AgentCommands[0].Commands = append(AgentCommands[0].Commands, agentDaemonCommand, &alias)
}

var agentDaemonCommand = &cli.Command{
	Name:    "debugger",
	Aliases: []string{"dbg"},
	Usage:   "Drive a conversation with a local agent from a script or coding agent",
	Description: `Runs your agent locally as a background process, then lets you drive a
multi-turn conversation one command at a time. Each "say" sends a user turn and
prints everything the agent did in response: tool calls with their arguments
and results, handoffs, errors, and the reply. Nothing is sent to a LiveKit room.

By default the conversation is text: the agent runs in console mode with
STT/TTS disabled, so a turn costs only what your LLM (and tools) cost. Start
with --audio to speak each turn instead: the user's line is spoken with LiveKit
Inference TTS into the agent's microphone input, and the agent runs its full
audio pipeline (STT, turn detection, TTS, or a realtime model that only takes
audio), so misheard names, spelled-out digits, and endpointing show up here.
Audio mode uses your project's credentials for the TTS, resolved like other lk
commands (--project, LIVEKIT_* variables, livekit.toml, or the default project).

This is built for coding agents (Claude Code, Codex, Cursor, ...) and shell
scripts: the caller stands in for the user, decides the next line based on the
reply, and can inspect logs and history between turns. Output, exit codes, and
--json are shaped for a program driving it. To talk to your agent yourself, by
voice or typing, use "lk agent console" instead.

Typical flow, run from the agent project directory:

   lk agent debugger start                  # starts the agent, prints its greeting (if any); --audio to speak turns
   lk agent debugger say "Hi, what can you do?"
   lk agent debugger say "Book me a table for two tonight"
   lk agent debugger logs --last 40         # agent process logs (tracebacks, warnings)
   lk agent debugger chat-history           # full transcript so far
   lk agent debugger events                 # live one-line stream of every session event
   lk agent debugger stop --chat-history    # closing summary, plus the conversation

The agent is found the same way as for "lk agent console": the project in the
current directory (or the nearest parent) with its default entrypoint, or the
file you name explicitly:

   lk agent debugger start src/my_agent.py
   lk agent debugger start agent.ts -- --env-file=.env   # args after -- go to node/python

After editing the agent's code, run "lk agent debugger restart" to relaunch it
with a fresh conversation. Add --json to any command for machine-readable
output: each turn is a document with "text", "reply", "duration_ms", and an
"events" list of message, tool_call, handoff, config, error, and log entries.
Exit codes are non-zero when a turn fails or times out, or no session is
running. Use --port to run several agents side by side (one session per port).
"lk agent dbg" is short for "lk agent debugger".`,
	Commands: []*cli.Command{
		{
			Name:      "start",
			Usage:     "Start the agent in the background and wait until it is ready",
			ArgsUsage: "[entrypoint] [-- node/python-args...]",
			Description: `Launches the agent as a detached process and returns once it is connected
and ready for turns, spoken ones with --audio. If the agent speaks first (a greeting from on_enter),
that is printed here. The summary line names the active agent and its tools.

Without an entrypoint, the project in the current directory (or nearest parent)
is used with its default entrypoint: agent.py or src/agent.py for Python,
main.ts, src/main.ts, or src/main.js for Node. Pass a file to override:

   lk agent debugger start src/my_agent.py
   lk agent debugger start agent.ts -- --env-file=.env

The agent reads its own .env for credentials, exactly like "lk agent console".
Only one session runs per port; use --port for more, or "restart" to replace
the current one. The session stops itself after --idle-timeout (default 30m)
without any command, so a forgotten session does not linger.`,
			Flags:  []cli.Flag{sessionPortFlag, sessionIdleFlag, sessionAudioFlag, jsonFlag},
			Action: runSessionStart,
		},
		{
			Name:      "say",
			Usage:     "Send one user turn and print the agent's tool calls and reply",
			ArgsUsage: "<text>   (or pipe the text on stdin)",
			Description: `Sends the text as the user's turn and streams what the agent does until the
turn completes: tool calls with their arguments and results, handoffs to other
agents, errors, and the reply. Anything the agent said since the previous turn
(for example a timer firing) is printed first, marked "(before this turn)".

In an audio session (start --audio) the text is spoken, and the turn lasts until
the agent has heard it, responded, and gone quiet. The user line is what the
agent transcribed, with the text you sent shown alongside when the words
differ; a turn the agent hears but never answers is reported as silent.

Exit code is non-zero if the agent reported an error or the turn timed out; the
agent keeps running either way. With --logs, the agent's log lines emitted
during the turn are shown beneath the step they belong to, which puts a tool's
traceback right under the sanitized error the user would hear. Text can also
be piped on stdin:

   echo "Book me a table for two" | lk agent debugger say`,
			Flags: []cli.Flag{
				sessionPortFlag,
				jsonFlag,
				sessionMetricsFlag,
				&cli.DurationFlag{
					Name:  "timeout",
					Value: defaultSayTimeout,
					Usage: "Give up waiting for the agent's reply after `DURATION`, such as 2m or 90s",
				},
				&cli.BoolFlag{
					Name:  "logs",
					Usage: "Interleave agent log lines emitted during the turn",
				},
			},
			Action: runSessionSay,
		},
		{
			Name:    "chat-history",
			Aliases: []string{"transcript"},
			Usage:   "Print the conversation so far, as the agent recorded it",
			Description: `Fetches the agent's own chat history, so it reflects exactly what the LLM has
seen: user and agent messages, tool calls with results, handoffs, and
instruction/tool changes. Works while a turn is in progress.`,
			Flags:  []cli.Flag{sessionPortFlag, jsonFlag, sessionMetricsFlag},
			Action: runSessionHistory,
		},
		{
			Name:  "events",
			Usage: "Stream the session's events, one line each, until interrupted",
			Description: `Streams what happens in the session as a flat, timestamped feed: user and
agent messages, tool calls with arguments and results, handoffs, config
changes, errors, and agent state transitions, one line each. It observes
without taking part, so it works alongside "say" from another shell or a
script driving the session.

The most recent events are printed first (--last), then new ones as they
happen until you interrupt it. --logs adds the agent's log lines. --json
emits one JSON object per line (NDJSON), suitable for piping into jq or
another program in real time.`,
			Flags: []cli.Flag{
				sessionPortFlag,
				jsonFlag,
				&cli.IntFlag{Name: "last", Aliases: []string{"n"}, Value: 50, Usage: "How many recent events to replay before streaming (0 for all kept, up to 500)"},
				&cli.BoolFlag{Name: "logs", Usage: "Include the agent's log lines in the stream"},
			},
			Action: runSessionEvents,
		},
		{
			Name:  "status",
			Usage: "Show whether a session is running, which agent is active, and its tools",
			Description: `Reports the project and entrypoint, the agent process id, the currently active
agent (after handoffs) with its state, tools, and the first line of its
instructions, the number of turns so far, and the path of the agent's log
file. Exits 1 when no session is running. With --json the full instructions
are included, and "running": false is printed instead of an error.`,
			Flags:  []cli.Flag{sessionPortFlag, jsonFlag},
			Action: runSessionStatus,
		},
		{
			Name:  "logs",
			Usage: "Print the agent process's recent log output",
			Description: `Prints the agent's stdout/stderr as captured by the session (ANSI colors
stripped). The whole log is also kept in a file; "status" shows its path. Use
"say --logs" to see the lines emitted during a single turn instead.`,
			Flags: []cli.Flag{
				sessionPortFlag,
				&cli.IntFlag{Name: "last", Aliases: []string{"n"}, Value: 50, Usage: "How many of the most recent lines to print (0 for the whole log)"},
				&cli.BoolFlag{Name: "follow", Aliases: []string{"f"}, Usage: "Keep streaming new lines until interrupted"},
			},
			Action: runSessionLogs,
		},
		{
			Name:  "restart",
			Usage: "Stop the session and start it again with the same agent (picks up code changes)",
			Description: `Stops the running session, then starts a new one with the same entrypoint and
port. The agent process is relaunched, so code changes take effect and the
conversation starts fresh; the new greeting (if any) is printed like "start".`,
			Flags:  []cli.Flag{sessionPortFlag, jsonFlag},
			Action: runSessionRestart,
		},
		{
			Name:  "stop",
			Usage: "Stop the running session and its agent, printing a closing summary",
			Description: `Shuts the agent down and prints how many turns ran, for how long, which agent
was active at the end, and where the agent's log file is kept. Add
--chat-history to print the whole conversation and --logs to print the agent's
entire log before the summary; --json returns everything in one document.`,
			Flags: []cli.Flag{
				sessionPortFlag,
				jsonFlag,
				sessionMetricsFlag,
				&cli.BoolFlag{
					Name:  "chat-history",
					Usage: "Also print the full conversation",
				},
				&cli.BoolFlag{
					Name:  "logs",
					Usage: "Also print the agent process's entire log",
				},
			},
			Action: runSessionStop,
		},
		{
			Name:   sessionDaemonSubcommand,
			Hidden: true,
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if os.Getenv(envSessionReadyFile) == "" {
					return fmt.Errorf("`lk agent debugger serve` is an internal entrypoint; run `lk agent debugger start <entrypoint>` instead")
				}
				runSessionDaemon()
				return nil
			},
		},
	},
}

func sessionAddr(port int) string {
	return fmt.Sprintf("%s:%d", sessionHost, port)
}

func runSessionStart(ctx context.Context, cmd *cli.Command) error {
	projectDir, projectType, entrypoint, err := detectProject(cmd)
	if err != nil {
		return err
	}
	port := int(cmd.Int("port"))
	audioEnv, err := sessionAudioEnv(cmd, cmd.Bool("audio"))
	if err != nil {
		return err
	}
	out.Statusf("Detected %s agent (%s in %s)", projectType.Lang(), util.Accented(entrypoint), util.Accented(projectDir))
	return startSessionDaemon(port, projectDir, projectType, entrypoint, cmd.Duration("idle-timeout"), audioEnv, cmd.Bool("json"))
}

func sessionMode(audio bool) string {
	if audio {
		return "audio"
	}
	return "text"
}

// sessionAudioEnv returns the daemon environment for an audio session: the
// mode, plus the project credentials the user's turns are spoken with.
func sessionAudioEnv(cmd *cli.Command, audio bool) ([]string, error) {
	if !audio {
		return nil, nil
	}
	creds, err := resolveCredentials(cmd)
	if err != nil {
		return nil, fmt.Errorf("--audio speaks your turns with LiveKit Inference, which needs project credentials: %w", err)
	}
	env := []string{envSessionAudio + "=1"}
	for _, kv := range creds {
		env = append(env, "LK_SESSION_"+kv)
	}
	return env, nil
}

// startSessionDaemon launches the detached daemon, waits for it to report
// ready, then prints the agent's opening message (if it produced one) and a
// short status line.
func startSessionDaemon(port int, projectDir string, projectType agentfs.ProjectType, entrypoint string, idleTimeout time.Duration, audioEnv []string, asJSON bool) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not resolve own binary: %w", err)
	}

	// Readiness file the daemon writes once it is up (or failed) before we
	// return, so we don't race a TCP probe against the agent's own connect.
	readyFile, err := os.CreateTemp("", "lk-session-ready-*.txt")
	if err != nil {
		return err
	}
	readyPath := readyFile.Name()
	readyFile.Close()
	defer os.Remove(readyPath)

	// The daemon is detached, so its own stdout/stderr (panics etc.) go to a
	// temp log rather than the user's terminal.
	logFile, err := os.CreateTemp("", "lk-session-daemon-*.log")
	if err != nil {
		return err
	}

	daemon := exec.Command(exe, "agent", "debugger", sessionDaemonSubcommand)
	daemon.Env = append(os.Environ(),
		envSessionPort+"="+strconv.Itoa(port),
		envSessionIdle+"="+idleTimeout.String(),
		envSessionDir+"="+projectDir,
		envSessionEntry+"="+entrypoint,
		envSessionPType+"="+string(projectType),
		envSessionReadyFile+"="+readyPath,
	)
	daemon.Env = append(daemon.Env, audioEnv...)
	daemon.Stdout = logFile
	daemon.Stderr = logFile
	setDetachedProcAttr(daemon)

	if err := daemon.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("failed to start session daemon: %w", err)
	}
	logFile.Close()

	status := awaitDaemonReady(daemon, readyPath)
	switch {
	case status == "ready":
	case strings.HasPrefix(status, "error:"):
		return fmt.Errorf("%s", strings.TrimSpace(strings.TrimPrefix(status, "error:")))
	default:
		return fmt.Errorf("session daemon exited before becoming ready (see %s)", logFile.Name())
	}

	// The daemon held the agent's opening reply (if any) for us; show it.
	greeting, err := controlRoundTrip(port, controlRequest{Cmd: "pending"}, 15*time.Second)
	if err != nil {
		return err
	}
	st, _ := controlRoundTrip(port, controlRequest{Cmd: "status"}, 15*time.Second)

	if asJSON {
		doc := map[string]any{"events": greeting.Events}
		if st != nil && st.Status != nil {
			doc["status"] = st.Status
		}
		return printJSON(doc)
	}
	for _, e := range greeting.Events {
		e.Earlier = false // shown at start, there is no "this turn" yet
		if line := renderTurnEvent(e, renderOptions{}); line != "" {
			out.Result(line)
		}
	}
	if len(greeting.Events) > 0 {
		out.Result("")
	}
	summary := "Session started."
	if st != nil && st.Status != nil {
		s := st.Status
		summary = fmt.Sprintf("Session started in %s mode. Listening on %s, agent pid %d.", sessionMode(s.Audio), sessionAddr(s.Port), s.Pid)
		if s.AgentID != "" {
			summary += fmt.Sprintf(" Agent %q", s.AgentID)
			if len(s.Tools) > 0 {
				summary += fmt.Sprintf(" with %d tool(s): %s", len(s.Tools), strings.Join(s.Tools, ", "))
			} else {
				summary += " (no tools)"
			}
			summary += "."
		}
		out.Status(summary)
		out.Statusf("Agent logs: %s", s.LogPath)
		if s.IdleTimeoutSeconds > 0 {
			out.Statusf("Stops on its own after %s without commands (--idle-timeout).",
				(time.Duration(s.IdleTimeoutSeconds) * time.Second).String())
		}
	} else {
		out.Status(summary)
	}
	out.Status("Next: `lk agent debugger say \"...\"` to talk, `lk agent debugger logs` for agent output, `lk agent debugger stop` when done.")
	return nil
}

// awaitDaemonReady waits for the detached daemon to report via the readiness
// file, returning its status line ("ready" or "error: ...") or "" if the
// daemon exits or times out without reporting.
func awaitDaemonReady(daemon *exec.Cmd, readyPath string) string {
	exited := make(chan struct{})
	go func() { _ = daemon.Wait(); close(exited) }()

	// The daemon waits up to agentConnectTimeout for the agent to connect and
	// then up to greetingMaxWait for an opening reply; give it a little more so
	// its own error report reaches us before we give up.
	timeout := time.After(agentConnectTimeout + greetingMaxWait + 5*time.Second)
	for {
		if status, ok := readReadyStatus(readyPath); ok {
			return status
		}
		select {
		case <-exited:
			if status, ok := readReadyStatus(readyPath); ok {
				return status
			}
			return ""
		case <-timeout:
			return ""
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// readReadyStatus returns the daemon's status once the readiness file has
// content (written atomically via rename), or ok=false while it is still empty.
func readReadyStatus(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

// sayJSON is the --json document `say` prints for one turn.
type sayJSON struct {
	Text       string      `json:"text"`
	Heard      string      `json:"heard"` // what the agent took the user to say: the text, or its transcript of the speech
	Events     []turnEvent `json:"events"`
	Reply      string      `json:"reply"`
	Silent     bool        `json:"silent,omitempty"`
	Error      string      `json:"error,omitempty"`
	DurationMs int64       `json:"duration_ms"`
}

func runSessionSay(ctx context.Context, cmd *cli.Command) error {
	text := strings.TrimSpace(strings.Join(cmd.Args().Slice(), " "))
	if text == "" && !isatty.IsTerminal(os.Stdin.Fd()) {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		text = strings.TrimSpace(string(data))
	}
	if text == "" {
		return fmt.Errorf("usage: lk agent debugger say <text>  (or pipe the text on stdin)")
	}

	timeout := cmd.Duration("timeout")
	conn, err := dialControl(int(cmd.Int("port")))
	if err != nil {
		return err
	}
	defer conn.Close()
	// Safety net behind the daemon-side timeout, so a wedged daemon can't hang us.
	_ = conn.SetReadDeadline(time.Now().Add(timeout + 15*time.Second))

	if err := writeControlFrame(conn, controlRequest{
		Cmd:       "say",
		Text:      text,
		TimeoutMs: timeout.Milliseconds(),
		Logs:      cmd.Bool("logs"),
	}); err != nil {
		return err
	}

	asJSON := cmd.Bool("json")
	opts := renderFlags(cmd)
	doc := sayJSON{Text: text, Events: []turnEvent{}}

	// Agent log lines are emitted while the agent works on what comes next
	// (a tool executing, a reply generating), so they are held and printed
	// beneath the event that follows them: a tool's traceback lands under the
	// tool's error line instead of floating above it.
	var pendingLogs []turnEvent
	flushLogs := func() {
		for _, l := range pendingLogs {
			out.Result(renderTurnEvent(l, opts))
		}
		pendingLogs = nil
	}
	final, err := streamControlReplies(conn, func(r controlReply) {
		if r.Event == nil {
			return
		}
		e := *r.Event
		if asJSON {
			doc.Events = append(doc.Events, e)
			return
		}
		if e.Type == "log" {
			pendingLogs = append(pendingLogs, e)
			return
		}
		if line := renderTurnEvent(e, opts); line != "" {
			out.Result(line)
		}
		if e.Type == "message" && e.Role == "user" && !e.Earlier && !sameWords(e.Text, text) {
			out.Result("    " + transcriptDim.Render(fmt.Sprintf("(sent: %q)", text)))
		}
		flushLogs()
	})
	if err != nil {
		return err
	}
	if !asJSON {
		flushLogs()
	}

	doc.Reply = final.Reply
	doc.Heard = final.Heard
	doc.Silent = final.Silent
	doc.Error = final.Error
	doc.DurationMs = final.DurationMs
	if asJSON {
		if err := printJSON(doc); err != nil {
			return err
		}
		if final.Error != "" {
			return cli.Exit("", 1)
		}
		return nil
	}

	if final.Error == "" && final.Heard == "" {
		out.Result("    " + transcriptDim.Render("(the agent transcribed no speech this turn)"))
	}
	if final.Error == "" && final.Silent {
		out.Result(renderSilentTurn())
	}
	if opts.Metrics {
		out.Result("    " + transcriptDim.Render(fmt.Sprintf("⏱ turn took %.1fs", float64(final.DurationMs)/1000)))
	}
	out.Result("")
	if final.Error != "" {
		return fmt.Errorf("%s", final.Error)
	}
	return nil
}

// sameWords reports whether a and b say the same words, ignoring case and
// punctuation, so only a real transcription difference is flagged.
func sameWords(a, b string) bool {
	words := func(s string) string {
		return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}), " ")
	}
	return words(a) == words(b)
}

func runSessionEvents(ctx context.Context, cmd *cli.Command) error {
	conn, err := dialControl(int(cmd.Int("port")))
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		conn.Close() // ctrl-C stops streaming cleanly
	}()
	last := int(cmd.Int("last"))
	if last == 0 {
		last = -1 // explicit 0: everything the daemon kept
	}
	if err := writeControlFrame(conn, controlRequest{Cmd: "events", Lines: last, Follow: true, Logs: cmd.Bool("logs")}); err != nil {
		return err
	}
	asJSON := cmd.Bool("json")
	enc := json.NewEncoder(out.ResultWriter())
	_, err = streamControlReplies(conn, func(r controlReply) {
		if r.Event == nil {
			return
		}
		if asJSON {
			_ = enc.Encode(r.Event)
			return
		}
		if line := renderEventLine(*r.Event); line != "" {
			out.Result(line)
		}
	})
	if err != nil && ctx.Err() != nil {
		return nil
	}
	return err
}

func runSessionHistory(ctx context.Context, cmd *cli.Command) error {
	reply, err := controlRoundTrip(int(cmd.Int("port")), controlRequest{Cmd: "chat-history"}, 30*time.Second)
	if err != nil {
		return err
	}
	if cmd.Bool("json") {
		events := reply.Events
		if events == nil {
			events = []turnEvent{}
		}
		return printJSON(map[string]any{"events": events})
	}
	if len(reply.Events) == 0 {
		out.Status("No conversation yet.")
		return nil
	}
	opts := renderFlags(cmd)
	for _, e := range reply.Events {
		if line := renderTurnEvent(e, opts); line != "" {
			out.Result(line)
		}
	}
	out.Result("")
	return nil
}

func runSessionStatus(ctx context.Context, cmd *cli.Command) error {
	port := int(cmd.Int("port"))
	reply, err := controlRoundTrip(port, controlRequest{Cmd: "status"}, 15*time.Second)
	if err != nil {
		if cmd.Bool("json") {
			_ = printJSON(map[string]any{"running": false, "port": port, "error": err.Error()})
			return cli.Exit("", 1)
		}
		return err
	}
	st := reply.Status
	if st == nil {
		return fmt.Errorf("daemon returned no status")
	}
	if cmd.Bool("json") {
		return printJSON(struct {
			Running bool `json:"running"`
			*sessionStatus
		}{true, st})
	}
	label := func(s string) string { return util.Dimmed(fmt.Sprintf("  %-14s", s)) }
	out.Resultf("Session running on %s (pid %d, up %s)\n", sessionAddr(st.Port), st.Pid, (time.Duration(st.UptimeSeconds) * time.Second).String())
	out.Resultf("%s%s (%s, %s)\n", label("Project:"), st.ProjectDir, agentfs.ProjectType(st.ProjectType).Lang(), st.Entrypoint)
	out.Resultf("%s%s\n", label("Mode:"), sessionMode(st.Audio))
	agentLine := st.AgentID
	if agentLine == "" {
		agentLine = util.Dimmed("(unknown)")
	}
	state := st.AgentState
	if st.TurnInProgress {
		state += ", turn in progress"
	}
	out.Resultf("%s%s (state: %s)\n", label("Agent:"), agentLine, state)
	if len(st.Tools) > 0 {
		out.Resultf("%s%s\n", label("Tools:"), strings.Join(st.Tools, ", "))
	} else {
		out.Resultf("%s%s\n", label("Tools:"), util.Dimmed("none"))
	}
	if st.Instructions != "" {
		first := strings.TrimSpace(strings.SplitN(st.Instructions, "\n", 2)[0])
		if len(first) > 100 {
			first = first[:100] + "…"
		}
		if strings.Count(st.Instructions, "\n") > 0 {
			first += util.Dimmed(" (use --json for the full text)")
		}
		out.Resultf("%s%s\n", label("Instructions:"), first)
	}
	turns := strconv.Itoa(st.Turns)
	if st.UnseenEvents > 0 {
		turns += util.Dimmed(fmt.Sprintf(" (+%d agent event(s) not yet shown; the next `say` reports them, or check `chat-history`)", st.UnseenEvents))
	}
	out.Resultf("%s%s\n", label("Turns:"), turns)
	if st.IdleTimeoutSeconds > 0 {
		out.Resultf("%s%s (stops after %s idle)\n", label("Idle:"),
			(time.Duration(st.IdleSeconds) * time.Second).String(),
			(time.Duration(st.IdleTimeoutSeconds) * time.Second).String())
	}
	out.Resultf("%s%s\n", label("Agent logs:"), st.LogPath)
	if st.Error != "" {
		out.Resultf("%s%s\n", label("Warning:"), st.Error)
	}
	return nil
}

func runSessionLogs(ctx context.Context, cmd *cli.Command) error {
	conn, err := dialControl(int(cmd.Int("port")))
	if err != nil {
		return err
	}
	defer conn.Close()
	follow := cmd.Bool("follow")
	if !follow {
		_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	}
	lines := int(cmd.Int("last"))
	if lines == 0 {
		lines = -1 // explicit 0: the whole log
	}
	if err := writeControlFrame(conn, controlRequest{Cmd: "logs", Lines: lines, Follow: follow}); err != nil {
		return err
	}
	if follow {
		// Stop cleanly on ctrl-C instead of surfacing a read error.
		go func() {
			<-ctx.Done()
			conn.Close()
		}()
	}
	_, err = streamControlReplies(conn, func(r controlReply) {
		if r.Line != "" {
			out.Result(r.Line)
		}
	})
	if err != nil && ctx.Err() != nil {
		return nil
	}
	return err
}

// stopJSON is the --json document `stop` prints.
type stopJSON struct {
	*sessionStatus
	Events []turnEvent `json:"events,omitempty"`
	Logs   []string    `json:"logs,omitempty"`
}

func runSessionStop(ctx context.Context, cmd *cli.Command) error {
	req := controlRequest{Cmd: "stop", Transcript: cmd.Bool("chat-history")}
	if cmd.Bool("logs") {
		req.Lines = -1 // the whole log
	}
	final, logs, err := stopSession(int(cmd.Int("port")), req)
	if err != nil {
		return err
	}
	st := final.Status

	if cmd.Bool("json") {
		return printJSON(stopJSON{sessionStatus: st, Events: final.Events, Logs: logs})
	}

	if cmd.Bool("chat-history") && len(final.Events) > 0 {
		opts := renderFlags(cmd)
		for _, e := range final.Events {
			if line := renderTurnEvent(e, opts); line != "" {
				out.Result(line)
			}
		}
		out.Result("")
	}
	if cmd.Bool("logs") {
		for _, line := range logs {
			out.Result(line)
		}
	}

	if st == nil {
		out.Status("Session ended.")
		return nil
	}
	agentLabel := ""
	if st.AgentID != "" {
		agentLabel = fmt.Sprintf(", ending with agent %q", st.AgentID)
	}
	out.Statusf("Session ended. %d turn(s) over %s%s.", st.Turns,
		(time.Duration(st.UptimeSeconds) * time.Second).String(), agentLabel)
	if st.LogPath != "" {
		out.Statusf("Agent log kept at: %s", st.LogPath)
	}
	return nil
}

// stopSession asks the daemon to shut down and returns its closing frame plus
// any log lines it streamed first.
func stopSession(port int, req controlRequest) (*controlReply, []string, error) {
	conn, err := dialControl(port)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	if err := writeControlFrame(conn, req); err != nil {
		return nil, nil, err
	}
	var logs []string
	final, err := streamControlReplies(conn, func(r controlReply) {
		if r.Line != "" {
			logs = append(logs, r.Line)
		}
	})
	if err != nil {
		return nil, nil, err
	}
	if final.Error != "" {
		return nil, nil, fmt.Errorf("%s", final.Error)
	}
	return final, logs, nil
}

func runSessionRestart(ctx context.Context, cmd *cli.Command) error {
	port := int(cmd.Int("port"))
	reply, err := controlRoundTrip(port, controlRequest{Cmd: "status"}, 15*time.Second)
	if err != nil {
		return err
	}
	st := reply.Status
	if st == nil {
		return fmt.Errorf("daemon returned no status")
	}
	if _, _, err := stopSession(port, controlRequest{Cmd: "stop"}); err != nil {
		return err
	}
	// The daemon frees the port as it exits; wait for it before rebinding.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, derr := net.DialTimeout("tcp", sessionAddr(port), 200*time.Millisecond)
		if derr != nil {
			break
		}
		c.Close()
		time.Sleep(100 * time.Millisecond)
	}
	audioEnv, err := sessionAudioEnv(cmd, st.Audio)
	if err != nil {
		return err
	}
	out.Statusf("Restarting %s agent (%s in %s)", agentfs.ProjectType(st.ProjectType).Lang(), st.Entrypoint, st.ProjectDir)
	return startSessionDaemon(port, st.ProjectDir, agentfs.ProjectType(st.ProjectType), st.Entrypoint,
		time.Duration(st.IdleTimeoutSeconds)*time.Second, audioEnv, cmd.Bool("json"))
}

// dialControl connects to the session daemon and sends the control preamble.
func dialControl(port int) (net.Conn, error) {
	conn, err := net.Dial("tcp", sessionAddr(port))
	if err != nil {
		return nil, fmt.Errorf("no session running on %s (run `lk agent debugger start` first)", sessionAddr(port))
	}
	if _, err := conn.Write([]byte(sessionMagic)); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// controlRoundTrip sends one request and collects replies until the daemon
// reports done, returning the final frame (with any streamed Events/Line
// frames folded into it).
func controlRoundTrip(port int, req controlRequest, timeout time.Duration) (*controlReply, error) {
	conn, err := dialControl(port)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	if err := writeControlFrame(conn, req); err != nil {
		return nil, err
	}
	var events []turnEvent
	final, err := streamControlReplies(conn, func(r controlReply) {
		if r.Event != nil {
			events = append(events, *r.Event)
		}
		events = append(events, r.Events...)
	})
	if err != nil {
		return nil, err
	}
	if final.Error != "" {
		return nil, fmt.Errorf("%s", final.Error)
	}
	final.Events = events
	return final, nil
}

// streamControlReplies reads frames until one is marked Done (returned) or the
// connection ends. Non-final frames are handed to onFrame as they arrive.
func streamControlReplies(conn net.Conn, onFrame func(controlReply)) (*controlReply, error) {
	for {
		var reply controlReply
		if err := readControlFrame(conn, &reply); err != nil {
			if err == io.EOF {
				return &controlReply{Done: true}, nil
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return nil, fmt.Errorf("timed out waiting for the session daemon to respond")
			}
			return nil, err
		}
		if reply.Done {
			// A final frame may still carry payload (events/status/line).
			if onFrame != nil && (reply.Event != nil || len(reply.Events) > 0 || reply.Line != "") {
				onFrame(reply)
			}
			return &reply, nil
		}
		if onFrame != nil {
			onFrame(reply)
		}
	}
}

func printJSON(v any) error {
	enc := json.NewEncoder(out.ResultWriter())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Control protocol: a 4-byte big-endian length prefix + a JSON payload, mirroring
// pkg/ipc's framing but with JSON instead of protobuf (no new protobufs needed).
type controlRequest struct {
	Cmd        string `json:"cmd"`                  // say | listen | pending | history | status | logs | stop
	Text       string `json:"text,omitempty"`       // say: the user's turn
	TimeoutMs  int64  `json:"timeout_ms,omitempty"` // say: max wait for the reply; listen: max wait for activity to begin
	Logs       bool   `json:"logs,omitempty"`       // say: stream agent log lines during the turn
	Lines      int    `json:"lines,omitempty"`      // logs: number of trailing lines; stop: -1 dumps the whole log
	Follow     bool   `json:"follow,omitempty"`     // logs: keep streaming after the snapshot
	Transcript bool   `json:"transcript,omitempty"` // stop: include the conversation in the closing frame
}

type controlReply struct {
	Event      *turnEvent     `json:"event,omitempty"`  // say: one streamed event
	Events     []turnEvent    `json:"events,omitempty"` // history/pending: a batch
	Status     *sessionStatus `json:"status,omitempty"`
	Line       string         `json:"line,omitempty"` // logs: one log line
	Done       bool           `json:"done,omitempty"`
	Error      string         `json:"error,omitempty"`
	Reply      string         `json:"reply,omitempty"`  // say: concatenated assistant text
	Heard      string         `json:"heard,omitempty"`  // say: what the agent took the user to say
	Silent     bool           `json:"silent,omitempty"` // say: the agent produced no output
	DurationMs int64          `json:"duration_ms,omitempty"`
}

func writeControlFrame(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func readControlFrame(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(hdr[:])
	if length > 8<<20 {
		return fmt.Errorf("control frame too large: %d bytes", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
