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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
)

func TestNormalizeLogLevel(t *testing.T) {
	assert.Equal(t, "debug", normalizeLogLevel(agentfs.ProjectTypeNode, "DEBUG"))
	assert.Equal(t, "warn", normalizeLogLevel(agentfs.ProjectTypeNode, "warn"))
	assert.Equal(t, "DEBUG", normalizeLogLevel(agentfs.ProjectTypePythonUV, "debug"))
	assert.Equal(t, "INFO", normalizeLogLevel(agentfs.ProjectTypePythonPip, "INFO"))
}

func TestDefaultEntrypoints(t *testing.T) {
	assert.Equal(t, []string{"agent.ts", "agent.js"}, defaultEntrypoints(agentfs.ProjectTypeNode))
	assert.Equal(t, []string{"agent.py"}, defaultEntrypoints(agentfs.ProjectTypePythonUV))
	assert.Equal(t, []string{"src/agent.ts", "src/agent.js"}, fallbackEntrypoints(agentfs.ProjectTypeNode))
	assert.Equal(t, []string{"src/agent.py"}, fallbackEntrypoints(agentfs.ProjectTypePythonPip))
}

func TestFindEntrypointPrecedence(t *testing.T) {
	touch := func(path string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, nil, 0o644))
	}

	// A root src/ layout must not shadow an agent next to the user's cwd.
	root := t.TempDir()
	touch(filepath.Join(root, "src", "agent.py"))
	touch(filepath.Join(root, "examples", "foo", "agent.py"))
	t.Chdir(filepath.Join(root, "examples", "foo"))

	entry, err := findEntrypoint(root, "", agentfs.ProjectTypePythonUV)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("examples", "foo", "agent.py"), entry)

	// With nothing cwd-relative, the root src/ fallback is found.
	root2 := t.TempDir()
	touch(filepath.Join(root2, "src", "agent.ts"))
	t.Chdir(root2)

	entry, err = findEntrypoint(root2, "", agentfs.ProjectTypeNode)
	require.NoError(t, err)
	assert.Equal(t, "src/agent.ts", entry)

	// A bare root agent file still wins over everything.
	touch(filepath.Join(root2, "agent.js"))
	entry, err = findEntrypoint(root2, "", agentfs.ProjectTypeNode)
	require.NoError(t, err)
	assert.Equal(t, "agent.js", entry)
}

func TestSplitForwardedArgs(t *testing.T) {
	// No separator: everything is an entrypoint arg.
	entry, fwd := splitForwardedArgs(
		[]string{"lk", "agent", "dev", "agent.ts"},
		[]string{"agent.ts"})
	assert.Equal(t, []string{"agent.ts"}, entry)
	assert.Empty(t, fwd)

	// Entrypoint plus forwarded runtime args.
	entry, fwd = splitForwardedArgs(
		[]string{"lk", "agent", "console", "agent.ts", "--", "--env-file=.env", "--inspect"},
		[]string{"agent.ts", "--env-file=.env", "--inspect"})
	assert.Equal(t, []string{"agent.ts"}, entry)
	assert.Equal(t, []string{"--env-file=.env", "--inspect"}, fwd)

	// Forwarded args only — nothing is mistaken for an entrypoint.
	entry, fwd = splitForwardedArgs(
		[]string{"lk", "agent", "dev", "--", "--env-file=.env"},
		[]string{"--env-file=.env"})
	assert.Empty(t, entry)
	assert.Equal(t, []string{"--env-file=.env"}, fwd)

	// A "--" consumed as a flag's value is not a separator: the args after
	// it were parsed as flags, so nothing is forwarded.
	entry, fwd = splitForwardedArgs(
		[]string{"lk", "agent", "dev", "--log-level", "--", "--url", "x"},
		[]string{})
	assert.Empty(t, entry)
	assert.Empty(t, fwd)
}

func TestBuildAgentCommandNode(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH")
	}

	// TypeScript entrypoints run via Node's type-stripping loader.
	bin, args, err := buildAgentCommand(AgentStartConfig{
		Dir:         t.TempDir(),
		Entrypoint:  "agent.ts",
		ProjectType: agentfs.ProjectTypeNode,
		CLIArgs:     []string{"dev", "--url", "wss://example.com"},
	})
	require.NoError(t, err)
	assert.Contains(t, bin, "node")
	assert.Equal(t, []string{"--experimental-strip-types", "agent.ts", "dev", "--url", "wss://example.com"}, args)

	// Plain JS entrypoints don't need the flag.
	_, args, err = buildAgentCommand(AgentStartConfig{
		Dir:         t.TempDir(),
		Entrypoint:  "agent.js",
		ProjectType: agentfs.ProjectTypeNode,
		CLIArgs:     []string{"console", "--connect-addr", "127.0.0.1:9999"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"agent.js", "console", "--connect-addr", "127.0.0.1:9999"}, args)

	// Forwarded runtime args land between node's own flags and the entrypoint.
	_, args, err = buildAgentCommand(AgentStartConfig{
		Dir:         t.TempDir(),
		Entrypoint:  "agent.ts",
		ProjectType: agentfs.ProjectTypeNode,
		RuntimeArgs: []string{"--env-file=.env"},
		CLIArgs:     []string{"dev"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"--experimental-strip-types", "--env-file=.env", "agent.ts", "dev"}, args)
}

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

func TestBuildAgentCommandPython(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}

	// Pip project with no venv falls back to system python; argv ordering is
	// `<python> <runtime args> <entry> <cli args>`.
	bin, args, err := buildAgentCommand(AgentStartConfig{
		Dir:         t.TempDir(),
		Entrypoint:  "agent.py",
		ProjectType: agentfs.ProjectTypePythonPip,
		RuntimeArgs: []string{"-u"},
		CLIArgs:     []string{"start", "--log-level", "DEBUG", "--dev"},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, bin)
	assert.Equal(t, []string{"-u", "agent.py", "start", "--log-level", "DEBUG", "--dev"}, args)
}
