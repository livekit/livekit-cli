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

// TestSessionE2E drives the real `lk agent session` lifecycle end to end:
// build the binary, `start` the detached daemon, `say` to make the model echo
// a token (asserting the CLI→daemon→agent→LLM round-trip), `end`, then confirm
// the daemon exited (nothing answers on the port).
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

	run := func(timeout time.Duration, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = os.Environ()
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// Best-effort teardown so a mid-run failure doesn't leave the daemon alive.
	t.Cleanup(func() {
		_, _ = run(15*time.Second, "agent", "session", "end", "--port", port)
	})

	// start: launches the detached daemon and returns once the agent is ready.
	startOut, err := run(90*time.Second, "agent", "session", "start", "--port", port, entrypoint)
	require.NoError(t, err, "session start failed:\n%s", startOut)
	require.Contains(t, startOut, "Session started.", "start did not report readiness:\n%s", startOut)

	// say: the token appears once in the echoed prompt and again in the reply, so
	// >=2 occurrences proves the agent answered, not just the local echo.
	token := "PINEAPPLE7351"
	sayOut, err := run(90*time.Second, "agent", "session", "say", "--port", port,
		"Repeat this token back to me exactly and nothing else: "+token)
	require.NoError(t, err, "session say failed:\n%s", sayOut)
	require.GreaterOrEqualf(t, strings.Count(sayOut, token), 2,
		"agent did not echo the token back; say output:\n%s", sayOut)

	endOut, err := run(30*time.Second, "agent", "session", "end", "--port", port)
	require.NoError(t, err, "session end failed:\n%s", endOut)
	require.Contains(t, endOut, "Session ended.", "end did not confirm shutdown:\n%s", endOut)

	// The detached daemon should now be gone: nothing should answer on the port.
	require.Eventually(t, func() bool {
		conn, derr := net.DialTimeout("tcp", "127.0.0.1:"+port, 200*time.Millisecond)
		if derr != nil {
			return true // refused → daemon exited
		}
		conn.Close()
		return false
	}, 10*time.Second, 200*time.Millisecond, "session daemon still listening on port %s after end", port)
}

// buildLK compiles the lk binary into a temp dir and returns its path.
func buildLK(t *testing.T) string {
	t.Helper()
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
