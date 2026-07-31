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
	"path/filepath"
	"testing"
)

func writeVenv(t *testing.T, root, name string) string {
	t.Helper()
	bin := filepath.Join(root, name, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(bin, "python")
	if err := os.WriteFile(python, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return python
}

func TestFindPythonBinaryWalksUpToRepoVenv(t *testing.T) {
	t.Setenv("VIRTUAL_ENV", "")
	root := t.TempDir()
	want := writeVenv(t, root, ".venv")
	agentDir := filepath.Join(root, "examples", "myagent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, prefixArgs, err := FindPythonBinary(agentDir, ProjectTypePythonPip)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if len(prefixArgs) != 0 {
		t.Errorf("got prefix args %v, want none", prefixArgs)
	}
}

func TestFindPythonBinaryPrefersActivatedVenv(t *testing.T) {
	root := t.TempDir()
	writeVenv(t, root, ".venv")
	activated := filepath.Join(root, "activated")
	want := writeVenv(t, activated, "venv")
	t.Setenv("VIRTUAL_ENV", filepath.Join(activated, "venv"))

	got, _, err := FindPythonBinary(root, ProjectTypePythonPip)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
