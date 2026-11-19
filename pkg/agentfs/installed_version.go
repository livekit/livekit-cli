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

package agentfs

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// This file resolves the agents SDK version that is actually installed, by
// asking the project's own runtime rather than parsing manifests: symlinked
// and workspace deps, loose version constraints, and hoisting all report what
// will really run. Static project-file parsing (sdk_version_check.go) is the
// fallback for when no runtime environment exists.

// venvPython returns the interpreter of the virtual environment at venv, or ""
// if there is none.
func venvPython(venv string) string {
	python := filepath.Join(venv, "bin", "python")
	if runtime.GOOS == "windows" {
		python = filepath.Join(venv, "Scripts", "python.exe")
	}
	if _, err := os.Stat(python); err != nil {
		return ""
	}
	return python
}

// projectVenvPython returns the interpreter of the project's environment:
// $UV_PROJECT_ENVIRONMENT, else the nearest .venv or venv from dir up, so an
// agent in a workspace member runs in the workspace's environment.
func projectVenvPython(dir string) string {
	if env := os.Getenv("UV_PROJECT_ENVIRONMENT"); env != "" {
		if !filepath.IsAbs(env) {
			env = filepath.Join(dir, env)
		}
		return venvPython(env)
	}
	for current := dir; ; current = filepath.Dir(current) {
		for _, name := range []string{".venv", "venv"} {
			if python := venvPython(filepath.Join(current, name)); python != "" {
				return python
			}
		}
		if filepath.Dir(current) == current {
			return ""
		}
	}
}

// FindPythonBinary locates the interpreter an agent in dir runs with: the
// activated environment, else the project's, else the system Python. It is run
// directly, never through a package manager: launching must not install
// anything, and `uv run` alone costs ~0.3 s per start.
func FindPythonBinary(dir string) (string, error) {
	if venv := os.Getenv("VIRTUAL_ENV"); venv != "" {
		if python := venvPython(venv); python != "" {
			return python, nil
		}
	}
	if python := projectVenvPython(dir); python != "" {
		return python, nil
	}
	for _, name := range []string{"python3", "python"} {
		if python, err := exec.LookPath(name); err == nil {
			return python, nil
		}
	}
	return "", fmt.Errorf("could not find Python binary; ensure a virtual environment exists or Python is on PATH")
}

// FindNodeBinary locates the Node binary used to run a JS/TS agent.
func FindNodeBinary() (string, error) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return "", fmt.Errorf("could not find Node binary; ensure node is on PATH")
	}
	return nodePath, nil
}

// nodeResolveVersionScript asks Node to report the installed @livekit/agents
// version using its own module resolution paths (so pnpm/workspace symlinks
// and hoisting resolve exactly as they will at runtime). See the source file
// for details.
//
//go:embed node_resolve_version.js
var nodeResolveVersionScript string

// ResolveNodeAgentVersion returns the installed @livekit/agents version as Node
// resolves it from fromDir, or "" if it can't be determined.
func ResolveNodeAgentVersion(nodeBin, fromDir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodeBin, "-e", nodeResolveVersionScript)
	cmd.Dir = fromDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// pythonResolveVersionScript prints the installed livekit-agents version, or a
// sentinel when the interpreter runs fine but the package isn't installed —
// distinguishing "dependencies not synced" from probe failures (no
// interpreter, timeout), which exit non-zero.
const pythonResolveVersionScript = `import importlib.metadata as m
try:
    print(m.version("livekit-agents"))
except m.PackageNotFoundError:
    print("` + pythonAgentNotInstalled + `")`

const pythonAgentNotInstalled = "__NOT_INSTALLED__"

// ResolvePythonAgentVersion returns the installed livekit-agents version read
// via the project's interpreter, so any installer (uv, pip, poetry) reports the
// version that will actually run. notInstalled reports that the interpreter ran
// but the package is missing from its environment; version is "" when it can't
// be determined at all (no interpreter, probe failure, etc.).
func ResolvePythonAgentVersion(dir string) (version string, notInstalled bool) {
	pythonBin, err := FindPythonBinary(dir)
	if err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, pythonBin, "-c", pythonResolveVersionScript)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	v := strings.TrimSpace(string(out))
	if v == pythonAgentNotInstalled {
		return "", true
	}
	return v, false
}

// ResolveInstalledSDKVersion returns the installed agents SDK version for the
// project as its own runtime resolves it, or "" when it can't be determined.
// For Node the resolution starts from the entrypoint's directory so monorepo
// and workspace layouts (where the dep is a workspace:* symlink, not a
// versioned entry in the root package.json) report the version that will
// actually run; entrypoint is relative to dir.
func ResolveInstalledSDKVersion(dir, entrypoint string, projectType ProjectType) string {
	if projectType.IsNode() {
		nodeBin, err := FindNodeBinary()
		if err != nil {
			return ""
		}
		fromDir := filepath.Dir(filepath.Join(dir, entrypoint))
		return ResolveNodeAgentVersion(nodeBin, fromDir)
	}
	version, _ := ResolvePythonAgentVersion(dir)
	return version
}
