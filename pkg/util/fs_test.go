// Copyright 2024 LiveKit, Inc.
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

func TestFileExists(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T) string
		dir      func(tmpDir string) string
		filename string
		expected bool
	}{
		{
			name: "regular file exists",
			setup: func(t *testing.T) string {
				tmpDir := t.TempDir()
				file := filepath.Join(tmpDir, "test.txt")
				if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
					t.Fatal(err)
				}
				return tmpDir
			},
			dir: func(tmpDir string) string {
				return tmpDir
			},
			filename: "test.txt",
			expected: true,
		},
		{
			name: "directory exists but should return false",
			setup: func(t *testing.T) string {
				tmpDir := t.TempDir()
				subDir := filepath.Join(tmpDir, "subdir")
				if err := os.Mkdir(subDir, 0755); err != nil {
					t.Fatal(err)
				}
				return tmpDir
			},
			dir: func(tmpDir string) string {
				return tmpDir
			},
			filename: "subdir",
			expected: false,
		},
		{
			name: "non-existent file",
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
			dir: func(tmpDir string) string {
				return tmpDir
			},
			filename: "non-existent.txt",
			expected: false,
		},
		{
			name: "file in subdirectory",
			setup: func(t *testing.T) string {
				tmpDir := t.TempDir()
				subDir := filepath.Join(tmpDir, "subdir")
				if err := os.Mkdir(subDir, 0755); err != nil {
					t.Fatal(err)
				}
				file := filepath.Join(subDir, "test.txt")
				if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
					t.Fatal(err)
				}
				return tmpDir
			},
			dir: func(tmpDir string) string {
				return filepath.Join(tmpDir, "subdir")
			},
			filename: "test.txt",
			expected: true,
		},
		{
			name: "symlink to regular file",
			setup: func(t *testing.T) string {
				tmpDir := t.TempDir()
				file := filepath.Join(tmpDir, "test.txt")
				symlink := filepath.Join(tmpDir, "link.txt")
				if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(file, symlink); err != nil {
					t.Fatal(err)
				}
				return tmpDir
			},
			dir: func(tmpDir string) string {
				return tmpDir
			},
			filename: "link.txt",
			expected: true, // Symlink to a regular file should return true
		},
		{
			name: "symlink to directory",
			setup: func(t *testing.T) string {
				tmpDir := t.TempDir()
				subDir := filepath.Join(tmpDir, "subdir")
				symlink := filepath.Join(tmpDir, "dirlink")
				if err := os.Mkdir(subDir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(subDir, symlink); err != nil {
					t.Fatal(err)
				}
				return tmpDir
			},
			dir: func(tmpDir string) string {
				return tmpDir
			},
			filename: "dirlink",
			expected: false, // Symlink to a directory should return false
		},
		{
			name: "broken symlink",
			setup: func(t *testing.T) string {
				tmpDir := t.TempDir()
				symlink := filepath.Join(tmpDir, "broken")
				if err := os.Symlink("/non/existent/path", symlink); err != nil {
					t.Fatal(err)
				}
				return tmpDir
			},
			dir: func(tmpDir string) string {
				return tmpDir
			},
			filename: "broken",
			expected: false,
		},
		{
			name: "empty filename",
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
			dir: func(tmpDir string) string {
				return tmpDir
			},
			filename: "",
			expected: false,
		},
		{
			name: "empty directory",
			setup: func(t *testing.T) string {
				tmpDir := t.TempDir()
				file := filepath.Join(tmpDir, "test.txt")
				if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
					t.Fatal(err)
				}
				return tmpDir
			},
			dir: func(tmpDir string) string {
				return ""
			},
			filename: "test.txt",
			expected: false,
		},
		{
			name: "file with unusual permissions",
			setup: func(t *testing.T) string {
				tmpDir := t.TempDir()
				file := filepath.Join(tmpDir, "readonly.txt")
				if err := os.WriteFile(file, []byte("test"), 0000); err != nil {
					t.Fatal(err)
				}
				return tmpDir
			},
			dir: func(tmpDir string) string {
				return tmpDir
			},
			filename: "readonly.txt",
			expected: true, // File exists even with no permissions
		},
		{
			name: "hidden file",
			setup: func(t *testing.T) string {
				tmpDir := t.TempDir()
				file := filepath.Join(tmpDir, ".hidden")
				if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
					t.Fatal(err)
				}
				return tmpDir
			},
			dir: func(tmpDir string) string {
				return tmpDir
			},
			filename: ".hidden",
			expected: true,
		},
		{
			name: "file with spaces in name",
			setup: func(t *testing.T) string {
				tmpDir := t.TempDir()
				file := filepath.Join(tmpDir, "file with spaces.txt")
				if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
					t.Fatal(err)
				}
				return tmpDir
			},
			dir: func(tmpDir string) string {
				return tmpDir
			},
			filename: "file with spaces.txt",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := tt.setup(t)
			dir := tt.dir(tmpDir)

			result := FileExists(dir, tt.filename)
			if result != tt.expected {
				t.Errorf("FileExists(%q, %q) = %v, want %v", dir, tt.filename, result, tt.expected)
			}
		})
	}
}
