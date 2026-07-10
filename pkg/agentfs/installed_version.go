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
	"strings"
	"time"
)

// This file resolves the agents SDK version that is actually installed, by
// asking the project's own runtime rather than parsing manifests: symlinked
// and workspace deps, loose version constraints, and hoisting all report what
// will really run. Static project-file parsing (sdk_version_check.go) is the
// fallback for when no runtime environment exists.

// FindPythonBinary locates a Python binary for the given project type.
func FindPythonBinary(dir string, projectType ProjectType) (string, []string, error) {
	if projectType == ProjectTypePythonUV {
		uvPath, err := exec.LookPath("uv")
		if err == nil {
			// --no-sync: the CLI proxies the environment as it exists on disk
			// and must never install or upgrade packages as a side effect.
			return uvPath, []string{"run", "--no-sync", "python"}, nil
		}
	}

	// Check common venv locations
	for _, venvDir := range []string{".venv", "venv"} {
		candidate := filepath.Join(dir, venvDir, "bin", "python")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil, nil
		}
	}

	// Fall back to system python
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		pythonPath, err = exec.LookPath("python")
		if err != nil {
			return "", nil, fmt.Errorf("could not find Python binary; ensure a virtual environment exists or Python is on PATH")
		}
	}
	return pythonPath, nil, nil
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
func ResolvePythonAgentVersion(dir string, projectType ProjectType) (version string, notInstalled bool) {
	pythonBin, prefixArgs, err := FindPythonBinary(dir, projectType)
	if err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := append(append([]string{}, prefixArgs...), "-c", pythonResolveVersionScript)
	cmd := exec.CommandContext(ctx, pythonBin, args...)
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
	version, _ := ResolvePythonAgentVersion(dir, projectType)
	return version
}
