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
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
)

func TestAgentProcessFailSignal(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH")
	}

	// An agent whose job crashes logs a marker but keeps the process alive;
	// Failed() must fire without waiting for exit.
	dir := t.TempDir()
	script := `console.log('shutting down job task {"reason": "job crashed"}'); setTimeout(() => {}, 30000);`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.js"), []byte(script), 0o644))

	ap, err := startAgent(AgentStartConfig{
		Dir:         dir,
		Entrypoint:  "agent.js",
		ProjectType: agentfs.ProjectTypeNode,
		FailSignals: consoleCrashSignals,
	})
	require.NoError(t, err)
	defer ap.Kill()

	select {
	case <-ap.Failed():
	case <-time.After(10 * time.Second):
		t.Fatal("Failed() did not fire on crash marker")
	}
}
