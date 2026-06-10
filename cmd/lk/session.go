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
	"bufio"
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

	"github.com/urfave/cli/v3"
)

// Single-session model: the fixed loopback port is the singleton registry.
// The daemon binds it; whoever wins the bind() is "the session". start, say,
// and end all rendezvous on this one port. No session id, manifest, or dir.
const (
	sessionMagic       = "LKCP" // 4-byte preamble that marks a control connection
	sessionHost        = "127.0.0.1"
	defaultSessionPort = 8775

	envSessionPort    = "LK_SESSION_PORT"  // fixed port
	envSessionDir     = "LK_SESSION_DIR"   // resolved project dir
	envSessionEntry   = "LK_SESSION_ENTRY" // resolved entrypoint (project-relative)
	envSessionPType   = "LK_SESSION_PTYPE" // agentfs.ProjectType string
	envSessionFwd     = "LK_SESSION_FWD"   // JSON array of runtime (node/python) args forwarded after "--"
	envSessionReadyFD = "LK_SESSION_READY_FD"

	// sessionDaemonSubcommand is the hidden entrypoint `start` re-execs into.
	sessionDaemonSubcommand = "daemon"
)

var sessionPortFlag = &cli.IntFlag{
	Name:    "port",
	Sources: cli.EnvVars(envSessionPort),
	Value:   defaultSessionPort,
	Usage:   "Fixed loopback port shared by the agent and control connections",
}

func init() {
	// Register under the `agent` group as `lk agent session`, mirroring how
	// `lk agent console` attaches itself. Unlike console, this command is not
	// gated behind the `console` build tag: it is CGO-free and ships in the
	// default binary.
	AgentCommands[0].Commands = append(AgentCommands[0].Commands, agentSessionCommand)
}

var agentSessionCommand = &cli.Command{
	Name:     "session",
	Usage:    "Drive a single local agent session in text mode (start/say/end)",
	Category: "Core",
	Commands: []*cli.Command{
		{
			Name:      "start",
			Usage:     "Start a detached agent session daemon",
			ArgsUsage: "[entrypoint] [-- node/python-args...]",
			Flags:     []cli.Flag{sessionPortFlag},
			Action:    runSessionStart,
		},
		{
			Name:      "say",
			Usage:     "Send a text turn to the running session and print the reply",
			ArgsUsage: "<text>",
			Flags:     []cli.Flag{sessionPortFlag},
			Action:    runSessionSay,
		},
		{
			Name:   "end",
			Usage:  "Stop the running session and its agent",
			Flags:  []cli.Flag{sessionPortFlag},
			Action: runSessionEnd,
		},
		{
			Name:   sessionDaemonSubcommand,
			Hidden: true,
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if os.Getenv(envSessionReadyFD) == "" {
					return fmt.Errorf("`session daemon` is an internal entrypoint; run `lk agent session start <entrypoint>` instead")
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

// sessionFwdEnv encodes the forwarded runtime args as the LK_SESSION_FWD env
// entry handed to the daemon, or "" when there is nothing to forward. JSON
// keeps args with spaces or quotes unambiguous in a single env var;
// sessionFwdArgs is the daemon-side inverse.
func sessionFwdEnv(fwd []string) string {
	if len(fwd) == 0 {
		return ""
	}
	encoded, _ := json.Marshal(fwd) // marshaling []string cannot fail
	return envSessionFwd + "=" + string(encoded)
}

// sessionFwdArgs decodes the LK_SESSION_FWD value set by `session start`.
func sessionFwdArgs(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var fwd []string
	if err := json.Unmarshal([]byte(raw), &fwd); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", envSessionFwd, err)
	}
	return fwd, nil
}

func runSessionStart(ctx context.Context, cmd *cli.Command) error {
	projectDir, projectType, entrypoint, err := detectProject(cmd)
	if err != nil {
		return err
	}
	port := int(cmd.Int("port"))

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not resolve own binary: %w", err)
	}

	// Pipe the daemon uses to report readiness (or a startup error) before we
	// return. This avoids racing a TCP probe against the agent's own connect.
	readyR, readyW, err := os.Pipe()
	if err != nil {
		return err
	}
	defer readyR.Close()

	// The daemon is detached, so its own stdout/stderr (panics etc.) go to a
	// temp log rather than the user's terminal.
	logFile, err := os.CreateTemp("", "lk-session-daemon-*.log")
	if err != nil {
		readyW.Close()
		return err
	}

	daemon := exec.Command(exe, "agent", "session", sessionDaemonSubcommand)
	daemon.Env = append(os.Environ(),
		envSessionPort+"="+strconv.Itoa(port),
		envSessionDir+"="+projectDir,
		envSessionEntry+"="+entrypoint,
		envSessionPType+"="+string(projectType),
		envSessionReadyFD+"=3", // ExtraFiles[0] is fd 3 in the child
	)
	if env := sessionFwdEnv(forwardedArgs(cmd)); env != "" {
		daemon.Env = append(daemon.Env, env)
	}
	daemon.ExtraFiles = []*os.File{readyW}
	daemon.Stdout = logFile
	daemon.Stderr = logFile
	setDetachedProcAttr(daemon)

	if err := daemon.Start(); err != nil {
		readyW.Close()
		logFile.Close()
		return fmt.Errorf("failed to start session daemon: %w", err)
	}
	// Close our copy of the write end so the read below sees EOF if the daemon dies.
	readyW.Close()
	logFile.Close()

	status, _ := bufio.NewReader(readyR).ReadString('\n')
	status = strings.TrimSpace(status)
	switch {
	case status == "ready":
		fmt.Fprintf(os.Stderr, "Detected %s agent (%s in %s)\n", projectType.Lang(), entrypoint, projectDir)
		fmt.Printf("Session started. Use `lk agent session say \"...\"` to talk, `lk agent session end` to stop.\n")
		return nil
	case strings.HasPrefix(status, "error:"):
		return fmt.Errorf("%s", strings.TrimSpace(strings.TrimPrefix(status, "error:")))
	default:
		return fmt.Errorf("session daemon exited before becoming ready (see %s)", logFile.Name())
	}
}

func runSessionSay(ctx context.Context, cmd *cli.Command) error {
	text := strings.TrimSpace(strings.Join(cmd.Args().Slice(), " "))
	if text == "" {
		return fmt.Errorf("usage: lk agent session say <text>")
	}
	conn, err := dialControl(int(cmd.Int("port")))
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := writeControlFrame(conn, controlRequest{Cmd: "say", Text: text}); err != nil {
		return err
	}
	return streamControlReplies(conn)
}

func runSessionEnd(ctx context.Context, cmd *cli.Command) error {
	conn, err := dialControl(int(cmd.Int("port")))
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := writeControlFrame(conn, controlRequest{Cmd: "end"}); err != nil {
		return err
	}
	if err := streamControlReplies(conn); err != nil {
		return err
	}
	fmt.Println("Session ended.")
	return nil
}

// dialControl connects to the session daemon and sends the control preamble.
func dialControl(port int) (net.Conn, error) {
	conn, err := net.Dial("tcp", sessionAddr(port))
	if err != nil {
		return nil, fmt.Errorf("no session running on %s (run `lk agent session start` first)", sessionAddr(port))
	}
	if _, err := conn.Write([]byte(sessionMagic)); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func streamControlReplies(conn net.Conn) error {
	for {
		var reply controlReply
		if err := readControlFrame(conn, &reply); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if reply.Line != "" {
			fmt.Println(reply.Line)
		}
		if reply.Done {
			if reply.Error != "" {
				return fmt.Errorf("%s", reply.Error)
			}
			return nil
		}
	}
}

// Control protocol: a 4-byte big-endian length prefix + a JSON payload, mirroring
// pkg/ipc's framing but with JSON instead of protobuf (no new protobufs needed).
type controlRequest struct {
	Cmd  string `json:"cmd"`
	Text string `json:"text,omitempty"`
}

type controlReply struct {
	Line  string `json:"line,omitempty"`
	Done  bool   `json:"done,omitempty"`
	Error string `json:"error,omitempty"`
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
	if length > 1<<20 {
		return fmt.Errorf("control frame too large: %d bytes", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
