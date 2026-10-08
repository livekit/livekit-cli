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
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const sessionE2ETimeout = 5 * time.Second

// TestSessionE2E drives the real `lk agent debugger` lifecycle end to end:
// build the binary, then in text mode and in audio mode: `start` the detached
// daemon, `say` a line the model echoes (asserting the CLI→daemon→agent→LLM
// round-trip, through TTS and the agent's STT in audio mode), `stop`, confirm a
// second `say` cannot still reach the agent, then confirm the daemon exited
// (nothing answers on the port).
//
// Opt-in: needs a prepared agent venv + live creds, so it skips unless
// LIVEKIT_API_KEY is set. Defaults to testdata/echo-agent; override with LK_SESSION_E2E_AGENT.
func TestSessionE2E(t *testing.T) {
	if os.Getenv("LIVEKIT_API_KEY") == "" {
		t.Skip("set LIVEKIT_API_KEY (and prepare the agent venv) to run the session e2e test")
	}
	entrypoint := os.Getenv("LK_SESSION_E2E_AGENT")
	if entrypoint == "" {
		entrypoint = filepath.Join("testdata", "echo-agent", "agent.py")
	}
	entrypoint, err := filepath.Abs(entrypoint)
	require.NoError(t, err)
	require.FileExists(t, entrypoint, "agent entrypoint not found (set LK_SESSION_E2E_AGENT to override)")

	// Dedicated port so the test can't collide with a real session on 8775.
	port := "18775"
	if p := os.Getenv("LK_SESSION_E2E_PORT"); p != "" {
		port = p
	}

	bin := buildLK(t)

	for _, audio := range []bool{false, true} {
		t.Run(sessionMode(audio), func(t *testing.T) {
			startArgs := []string{"agent", "debugger", "start", "--port", port}
			if audio {
				startArgs = append(startArgs, "--audio")
			}

			type runResult struct {
				stdout   string
				stderr   string
				exitCode int
			}

			runCapture := func(timeout time.Duration, args ...string) (runResult, error) {
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				cmd := exec.CommandContext(ctx, bin, args...)
				cmd.Env = os.Environ()
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				err := cmd.Run()
				require.NotNil(t, cmd.ProcessState, "command did not start: %v", err)

				return runResult{
					stdout:   stdout.String(),
					stderr:   stderr.String(),
					exitCode: cmd.ProcessState.ExitCode(),
				}, err
			}

			run := func(timeout time.Duration, args ...string) (string, error) {
				res, err := runCapture(timeout, args...)
				return res.stdout + res.stderr, err
			}

			portIsFree := func() bool {
				conn, derr := net.DialTimeout("tcp", "127.0.0.1:"+port, 200*time.Millisecond)
				if derr != nil {
					return true // refused -> daemon exited
				}
				conn.Close()
				return false
			}

			// Best-effort teardown so a mid-run failure doesn't leave the daemon alive.
			t.Cleanup(func() {
				_, _ = run(sessionE2ETimeout, "agent", "debugger", "stop", "--port", port)
			})

			// start: launches the detached daemon and returns once the agent is ready.
			// The CLI itself waits up to 65s for daemon readiness (awaitDaemonReady),
			// so give it just over that: on a cold Windows runner the chain of first
			// execs (lk.exe, uv, python) can alone eat a tighter budget, and killing
			// the command mid-wait loses the CLI's own error report.
			startOut, err := run(70*time.Second, append(startArgs, entrypoint)...)
			require.NoError(t, err, "session start failed:\n%s", startOut)
			require.Contains(t, startOut, "Session started in "+sessionMode(audio)+" mode.", "start did not report readiness:\n%s", startOut)

			// say: the token must reach the agent (as text, or through its STT in
			// audio mode) and come back in the reply.
			token := "pineapple"
			sayRes, err := runCapture(60*time.Second, "agent", "debugger", "say", "--port", port, "--json",
				"Repeat this word back to me: "+token)
			sayOut := sayRes.stdout + sayRes.stderr
			require.NoError(t, err, "session say failed:\n%s", sayOut)
			var turn sayJSON
			require.NoError(t, json.Unmarshal([]byte(sayRes.stdout), &turn), "say --json output:\n%s", sayOut)
			require.Containsf(t, strings.ToLower(turn.Heard), token, "agent did not hear the token; say output:\n%s", sayOut)
			require.Containsf(t, strings.ToLower(turn.Reply), token, "agent did not echo the token back; say output:\n%s", sayOut)

			// history: the agent's own record of the conversation must contain the turn.
			histOut, err := run(sessionE2ETimeout, "agent", "debugger", "chat-history", "--port", port)
			require.NoError(t, err, "session history failed:\n%s", histOut)
			require.GreaterOrEqualf(t, strings.Count(strings.ToLower(histOut), token), 2,
				"history did not contain both the prompt and the reply:\n%s", histOut)

			// status/logs: both must answer while the session is up.
			statusOut, err := run(sessionE2ETimeout, "agent", "debugger", "status", "--port", port)
			require.NoError(t, err, "session status failed:\n%s", statusOut)
			require.Contains(t, statusOut, "Session running", "status did not report a running session:\n%s", statusOut)
			logsOut, err := run(sessionE2ETimeout, "agent", "debugger", "logs", "-n", "5", "--port", port)
			require.NoError(t, err, "session logs failed:\n%s", logsOut)

			stopOut, err := run(sessionE2ETimeout, "agent", "debugger", "stop", "--port", port)
			require.NoError(t, err, "session stop failed:\n%s", stopOut)
			require.Contains(t, stopOut, "Session ended.", "stop did not confirm shutdown:\n%s", stopOut)

			require.Eventually(t, portIsFree, sessionE2ETimeout, 200*time.Millisecond,
				"session daemon still listening on port %s after stop", port)

			// After a successful match and shutdown, another say must not reach a live
			// agent or reproduce the token.
			afterStopSay, err := runCapture(sessionE2ETimeout, "agent", "debugger", "say", "--port", port,
				"Repeat this word back to me: "+token)
			afterStopSayOut := afterStopSay.stdout + afterStopSay.stderr
			require.Error(t, err, "session say unexpectedly succeeded after stop:\n%s", afterStopSayOut)
			require.Equal(t, 1, afterStopSay.exitCode,
				"session say after stop exited with wrong code; stdout:\n%s\nstderr:\n%s",
				afterStopSay.stdout, afterStopSay.stderr)
			require.Truef(t, strings.HasPrefix(afterStopSayOut, "no session running"),
				"session say after stop output did not start with no session running; stdout:\n%s\nstderr:\n%s",
				afterStopSay.stdout, afterStopSay.stderr)
			require.NotContains(t, afterStopSayOut, token,
				"session say after stop unexpectedly contained the matched token; stdout:\n%s\nstderr:\n%s",
				afterStopSay.stdout, afterStopSay.stderr)

			require.True(t, portIsFree(), "session daemon started listening again on port %s after failed say", port)
		})
	}
}

// buildLK returns the path to the lk binary under test. If LK_SESSION_E2E_BIN
// points at a prebuilt binary it's used as-is (the Windows CI arm cross-builds
// lk on Linux and ships it here, so the heavy cgo build never runs on the
// Windows runner); otherwise lk is compiled into a temp dir.
func buildLK(t *testing.T) string {
	t.Helper()
	if prebuilt := os.Getenv("LK_SESSION_E2E_BIN"); prebuilt != "" {
		abs, err := filepath.Abs(prebuilt)
		require.NoError(t, err)
		require.FileExists(t, abs, "LK_SESSION_E2E_BIN does not point at a binary")
		return abs
	}
	bin := filepath.Join(t.TempDir(), "lk")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
	out, err := build.CombinedOutput()
	require.NoErrorf(t, err, "failed to build lk binary:\n%s", out)
	return bin
}
