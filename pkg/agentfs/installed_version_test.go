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

package agentfs

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func requireUv(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv not installed")
	}
}

// venvOf returns the environment root a resolved interpreter belongs to, with
// symlinks evaluated so a /var vs /private/var temp dir compares equal.
func venvOf(t *testing.T, python string) string {
	t.Helper()
	root := filepath.Dir(filepath.Dir(python))
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return root
	}
	return resolved
}

func wantVenv(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return dir
	}
	return resolved
}

// makeVenv creates a real virtual environment at root/name.
func makeVenv(t *testing.T, root, name string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("uv", "venv", "--offline", name)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("uv venv: %v\n%s", err, out)
	}
	return filepath.Join(root, name)
}

func TestFindPythonBinaryResolvesVenvFromSubdirectory(t *testing.T) {
	requireUv(t)
	t.Setenv("VIRTUAL_ENV", "")
	root := t.TempDir()
	venv := makeVenv(t, root, ".venv")
	agentDir := filepath.Join(root, "examples", "myagent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, prefixArgs, err := FindPythonBinary(agentDir, ProjectTypePythonPip)
	if err != nil {
		t.Fatal(err)
	}
	if venvOf(t, got) != wantVenv(t, venv) {
		t.Errorf("got %s, want an interpreter inside %s", got, venv)
	}
	if len(prefixArgs) != 0 {
		t.Errorf("got prefix args %v, want none", prefixArgs)
	}
}

func TestFindPythonBinaryPrefersActivatedVenv(t *testing.T) {
	requireUv(t)
	root := t.TempDir()
	makeVenv(t, root, ".venv")
	activated := makeVenv(t, filepath.Join(root, "elsewhere"), "venv")
	t.Setenv("VIRTUAL_ENV", activated)

	got, _, err := FindPythonBinary(root, ProjectTypePythonPip)
	if err != nil {
		t.Fatal(err)
	}
	if venvOf(t, got) != wantVenv(t, activated) {
		t.Errorf("got %s, want an interpreter inside %s", got, activated)
	}
}

// $VIRTUAL_ENV is honored without uv, so an activated environment resolves for
// users who don't have it installed.
func TestFindPythonBinaryPrefersActivatedVenvWithoutUv(t *testing.T) {
	root := t.TempDir()
	activated := filepath.Join(root, "venv")
	if err := os.MkdirAll(filepath.Join(activated, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(activated, "bin", "python")
	if err := os.WriteFile(python, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIRTUAL_ENV", activated)

	got, _, err := FindPythonBinary(root, ProjectTypePythonPip)
	if err != nil {
		t.Fatal(err)
	}
	if got != python {
		t.Errorf("got %s, want %s", got, python)
	}
}

// With no venv anywhere, uv answers with a managed standalone interpreter that
// has no dependencies installed; resolution must reject it and keep searching.
func TestFindPythonBinaryRejectsNonVenvInterpreter(t *testing.T) {
	requireUv(t)
	t.Setenv("VIRTUAL_ENV", "")
	agentDir := filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, _, err := FindPythonBinary(agentDir, ProjectTypePythonPip)
	if err != nil {
		t.Skip("no system Python on PATH")
	}
	systemPython, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3 on PATH")
	}
	if got != systemPython {
		t.Errorf("got %s, want system python %s", got, systemPython)
	}
}
