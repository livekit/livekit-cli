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

package util

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIsEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if empty, err := IsEmptyDir(dir); err != nil || !empty {
		t.Fatalf("new dir: empty=%v err=%v, want empty", empty, err)
	}

	writeTestFile(t, filepath.Join(dir, ".DS_Store"), "")
	if empty, _ := IsEmptyDir(dir); !empty {
		t.Fatal("a dir with only .DS_Store should count as empty")
	}

	writeTestFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main")
	if empty, _ := IsEmptyDir(dir); empty {
		t.Fatal("a dir with .git shouldn't count as empty")
	}
}

func TestMoveDirInto(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(src, "README.md"), "hello")
	writeTestFile(t, filepath.Join(src, "src", "agent.py"), "print('hi')")
	writeTestFile(t, filepath.Join(dest, ".DS_Store"), "")

	if err := MoveDirInto(src, dest); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"README.md", filepath.Join("src", "agent.py")} {
		if _, err := os.Stat(filepath.Join(dest, f)); err != nil {
			t.Errorf("%s wasn't moved: %v", f, err)
		}
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source should be removed, got err=%v", err)
	}
}

func TestMoveDirIntoNonEmpty(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(src, "README.md"), "template")
	writeTestFile(t, filepath.Join(dest, "notes.txt"), "mine")

	if err := MoveDirInto(src, dest); err == nil {
		t.Fatal("moving into a non-empty dir should fail")
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "notes.txt")); string(b) != "mine" {
		t.Error("existing files must be left alone")
	}
}

func TestMoveDirExisting(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	if err := MoveDir(src, dest); err == nil {
		t.Fatal("MoveDir should refuse an existing destination")
	}
}
