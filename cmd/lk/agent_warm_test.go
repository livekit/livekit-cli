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
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// A spare preloads recorded installed modules, but never the project's own,
// holds its output, and runs the agent only once activated. A run records the
// installed modules it imported for the next spare.
func TestWarmPythonSparePreloadsInstalledModulesAndWaitsForActivation(t *testing.T) {
	dir := setupUvAgentProject(t, "1.8.0", "livekit-agents", true)
	source := filepath.Join(dir, "sdk-source")
	module := filepath.Join(source, "livekit", "agents")
	require.NoError(t, os.MkdirAll(module, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(module, "__init__.py"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(module, "__main__.py"), []byte(
		"import sys, uuid\n"+
			"print('RAN', sys.argv[1:], 'json.tool' in sys.modules, 'mymod' in sys.modules, flush=True)\n"), 0o644))
	// The project provides mymod; a stale installed copy must not be preloaded.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mymod.py"), nil, 0o644))
	preload := filepath.Join(t.TempDir(), "preload.txt")
	require.NoError(t, os.WriteFile(preload, []byte("json.tool\nmymod\n"), 0o644))

	cfg := AgentStartConfig{
		Dir: dir, Entrypoint: "agent.py", ProjectType: agentfs.ProjectTypePythonUV,
		CLIArgs: []string{"start", "--dev"}, Env: []string{"PYTHONPATH=" + source},
		PythonPreloadFile: preload, Warm: true,
	}
	var output syncBuffer
	cfg.ForwardOutput = &output
	spare, err := startAgent(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { spare.Kill() })

	time.Sleep(time.Second)
	require.False(t, spare.exited(), "spare must wait for activation")
	require.NotContains(t, output.String(), "RAN")

	require.NoError(t, spare.Activate())
	require.NoError(t, <-spare.Done())
	require.Contains(t, output.String(), "RAN ['start', 'agent.py', '--dev'] True False")

	// The run recorded installed modules it imported (uuid), not the project's.
	recorded, err := os.ReadFile(preload)
	require.NoError(t, err)
	names := strings.Fields(string(recorded))
	require.Contains(t, names, "uuid")
	require.NotContains(t, names, "mymod")
	require.NotContains(t, names, "livekit.agents.__main__") // from sdk-source, outside the environment
}
