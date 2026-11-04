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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
)

// setupUvAgentProject builds a real uv project whose only dependency is a local
// stub package named "livekit-agents" pinned to stubVersion. This resolves a
// known installed version through real uv — no network, no real SDK — and
// works on every platform uv supports. depSpec is the dependency string written
// to the project's pyproject.toml (e.g. "livekit-agents" or
// "livekit-agents>=1.0"); when sync is true the stub is installed into the env.
func setupUvAgentProject(t *testing.T, stubVersion, depSpec string, sync bool) string {
	t.Helper()
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv not on PATH")
	}
	dir := t.TempDir()

	stubMod := filepath.Join(dir, "stub", "src", "livekit_agents")
	require.NoError(t, os.MkdirAll(stubMod, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stubMod, "__init__.py"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stub", "pyproject.toml"), []byte(
		"[project]\n"+
			"name = \"livekit-agents\"\n"+
			"version = \""+stubVersion+"\"\n"+
			"requires-python = \">=3.8\"\n"+
			"[build-system]\n"+
			"requires = [\"uv_build>=0.5,<10\"]\n"+
			"build-backend = \"uv_build\"\n"), 0o644))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(
		"[project]\n"+
			"name = \"test-agent\"\n"+
			"version = \"0.0.0\"\n"+
			"requires-python = \">=3.8\"\n"+
			"dependencies = [\""+depSpec+"\"]\n"+
			"[tool.uv.sources]\n"+
			"livekit-agents = { path = \"stub\" }\n"), 0o644))

	if sync {
		cmd := exec.Command("uv", "sync")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("uv sync failed: %v\n%s", err, out)
		}
	}
	return dir
}

func TestResolvePythonAgentVersion_ReadsInstalledVersion(t *testing.T) {
	dir := setupUvAgentProject(t, "1.6.7", "livekit-agents", true)
	version, notInstalled := agentfs.ResolvePythonAgentVersion(dir, agentfs.ProjectTypePythonUV)
	require.Equal(t, "1.6.7", version)
	require.False(t, notInstalled)
}

// A missing module gets an actionable error; an importable module from an
// arbitrary source directory launches without distribution metadata.
func TestStartPythonAgentImportsOnceAndReportsMissingDependencies(t *testing.T) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv not on PATH")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(
		"[project]\nname = \"test-agent\"\nversion = \"0.0.0\"\n"), 0o644))
	venv := exec.Command("uv", "venv", "--offline", ".venv")
	venv.Dir = dir
	out, err := venv.CombinedOutput()
	require.NoError(t, err, "%s", out)
	source := filepath.Join(dir, "custom-sdk-source")
	cfg := AgentStartConfig{
		Dir: dir, Entrypoint: "agent.py", ProjectType: agentfs.ProjectTypePythonUV,
		CLIArgs: []string{"start", "--dev"}, ReadySignal: "module launched",
		Env: []string{"PYTHONPATH=" + source},
	}
	missing, err := startAgent(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { missing.Kill() })
	require.Error(t, <-missing.Done())
	require.Contains(t, strings.Join(missing.RecentLogs(0), "\n"), "run `uv sync`")

	module := filepath.Join(source, "livekit", "agents")
	require.NoError(t, os.MkdirAll(module, 0o755))
	count := filepath.Join(dir, "imports.txt")
	cfg.Env = append(cfg.Env, "IMPORT_COUNT="+count)
	require.NoError(t, os.WriteFile(filepath.Join(module, "__init__.py"),
		[]byte("import os\nwith open(os.environ['IMPORT_COUNT'], 'a', newline='') as f: f.write('import\\n')\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(module, "__main__.py"),
		[]byte("print('module launched', flush=True)\n"), 0o644))
	ap, err := startAgent(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { ap.Kill() })
	select {
	case <-ap.readyCh:
	case <-time.After(10 * time.Second):
		t.Fatal("Python module did not launch")
	}
	require.NoError(t, <-ap.doneCh)
	imports, err := os.ReadFile(count)
	require.NoError(t, err)
	require.Equal(t, "import\n", string(imports))
}

// An SDK older than the thin CLI has no livekit.agents.__main__. Its failure
// must name the version floor, not suggest syncing dependencies already there.
func TestStartPythonAgentReportsSDKOlderThanThinCLI(t *testing.T) {
	dir := setupUvAgentProject(t, "1.5.0", "livekit-agents", true)
	source := filepath.Join(dir, "old-sdk-source")
	require.NoError(t, os.MkdirAll(filepath.Join(source, "livekit", "agents"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(source, "livekit", "agents", "__init__.py"), nil, 0o644))
	ap, err := startAgent(AgentStartConfig{
		Dir: dir, Entrypoint: "agent.py", ProjectType: agentfs.ProjectTypePythonUV,
		CLIArgs: []string{"start", "--dev"}, Env: []string{"PYTHONPATH=" + source},
	})
	require.NoError(t, err)
	t.Cleanup(func() { ap.Kill() })
	require.Error(t, <-ap.Done())
	logs := strings.Join(ap.RecentLogs(0), "\n")
	require.Contains(t, logs, "No module named livekit.agents.__main__")
	require.Contains(t, logs, "livekit-agents version 1.5.0 is too old, please upgrade to 1.6.0 or newer")
	require.NotContains(t, logs, "sync or install")
}

func TestFindPythonBinary_UVRunDoesNotSyncEnvironment(t *testing.T) {
	// Models a user who synced their env, then edited a dependency's version
	// without re-syncing. Running through the CLI-resolved interpreter must
	// execute against the environment as it exists on disk — a plain `uv run`
	// would re-lock and install 9.9.9 as a side effect of launching.
	dir := setupUvAgentProject(t, "1.6.7", "livekit-agents", true)
	stubPyproject := filepath.Join(dir, "stub", "pyproject.toml")
	orig, err := os.ReadFile(stubPyproject)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(stubPyproject, []byte(strings.Replace(string(orig), "1.6.7", "9.9.9", 1)), 0o644))

	bin, prefixArgs, err := agentfs.FindPythonBinary(dir, agentfs.ProjectTypePythonUV)
	require.NoError(t, err)
	cmd := exec.Command(bin, append(prefixArgs, "-c", `import importlib.metadata as m; print(m.version("livekit-agents"))`)...)
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err)
	require.Equal(t, "1.6.7", strings.TrimSpace(string(out)))
}
