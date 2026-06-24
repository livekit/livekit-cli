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

// TestSessionE2E drives the real `lk agent daemon` lifecycle end to end:
// build the binary, `start` the detached daemon, `say` to make the model echo
// a token (asserting the CLI→daemon→agent→LLM round-trip), `stop`, confirm a
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
		_, _ = run(sessionE2ETimeout, "agent", "daemon", "stop", "--port", port)
	})

	// start: launches the detached daemon and returns once the agent is ready.
	startOut, err := run(15*time.Second, "agent", "daemon", "start", "--port", port, entrypoint)
	require.NoError(t, err, "session start failed:\n%s", startOut)
	require.Contains(t, startOut, "Session started.", "start did not report readiness:\n%s", startOut)

	// say: the token appears once in the echoed prompt and again in the reply, so
	// >=2 occurrences proves the agent answered, not just the local echo.
	token := "PINEAPPLE7351"
	sayOut, err := run(sessionE2ETimeout, "agent", "daemon", "say", "--port", port,
		"Repeat this token back to me exactly and nothing else: "+token)
	require.NoError(t, err, "session say failed:\n%s", sayOut)
	require.GreaterOrEqualf(t, strings.Count(sayOut, token), 2,
		"agent did not echo the token back; say output:\n%s", sayOut)

	stopOut, err := run(sessionE2ETimeout, "agent", "daemon", "stop", "--port", port)
	require.NoError(t, err, "session stop failed:\n%s", stopOut)
	require.Contains(t, stopOut, "Session ended.", "stop did not confirm shutdown:\n%s", stopOut)

	require.Eventually(t, portIsFree, sessionE2ETimeout, 200*time.Millisecond,
		"session daemon still listening on port %s after stop", port)

	// After a successful match and shutdown, another say must not reach a live
	// agent or reproduce the token.
	afterStopSay, err := runCapture(sessionE2ETimeout, "agent", "daemon", "say", "--port", port,
		"Repeat this token back to me exactly and nothing else: "+token)
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
