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
	version, notInstalled := resolvePythonAgentVersion(dir, agentfs.ProjectTypePythonUV)
	require.Equal(t, "1.6.7", version)
	require.False(t, notInstalled)
}

func TestCheckPythonSDKVersion_TooOld(t *testing.T) {
	dir := setupUvAgentProject(t, "1.0.0", "livekit-agents", true)
	err := checkPythonSDKVersion(AgentStartConfig{Dir: dir, ProjectType: agentfs.ProjectTypePythonUV})
	require.Error(t, err)
	require.Contains(t, err.Error(), "too old")
}

func TestCheckPythonSDKVersion_InstalledBeatsLooseConstraint(t *testing.T) {
	// The pyproject floor >=1.0 would fail static parsing, but the installed
	// 1.6.7 is what gets used — proving the resolved version wins.
	dir := setupUvAgentProject(t, "1.6.7", "livekit-agents>=1.0", true)
	require.NoError(t, checkPythonSDKVersion(AgentStartConfig{Dir: dir, ProjectType: agentfs.ProjectTypePythonUV}))
}

func TestCheckPythonSDKVersion_UnsyncedUVProjectSuggestsSync(t *testing.T) {
	// Models a fresh clone where dependencies were never installed. The CLI is
	// a proxy for the local environment and never syncs it implicitly, so the
	// agent would die at launch with ModuleNotFoundError; the pre-flight must
	// fail fast and tell the user how to fix it instead.
	dir := setupUvAgentProject(t, "1.6.7", "livekit-agents>=1.6.0", false)
	err := checkPythonSDKVersion(AgentStartConfig{Dir: dir, ProjectType: agentfs.ProjectTypePythonUV})
	require.ErrorContains(t, err, "uv sync")
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

	bin, prefixArgs, err := findPythonBinary(dir, agentfs.ProjectTypePythonUV)
	require.NoError(t, err)
	cmd := exec.Command(bin, append(prefixArgs, "-c", `import importlib.metadata as m; print(m.version("livekit-agents"))`)...)
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err)
	require.Equal(t, "1.6.7", strings.TrimSpace(string(out)))
}
