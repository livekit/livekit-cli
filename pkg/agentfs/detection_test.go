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

package agentfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectProjectType(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, dir string)
		expected    ProjectType
		shouldError bool
	}{
		{
			name: "Node.js project with package.json",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name": "test"}`), 0644); err != nil {
					t.Fatal(err)
				}
			},
			expected: ProjectTypeNode,
		},
		{
			name: "Python UV project with uv.lock",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "uv.lock"), []byte("# uv lockfile"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			expected: ProjectTypePythonUV,
		},
		{
			name: "Python Poetry project with poetry.lock",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "poetry.lock"), []byte("# poetry lockfile"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			expected: ProjectTypePythonPip,
		},
		{
			name: "Python Pipenv project with Pipfile.lock",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "Pipfile.lock"), []byte("{}"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			expected: ProjectTypePythonPip,
		},
		{
			name: "Python PDM project with pdm.lock",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "pdm.lock"), []byte("# pdm lockfile"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			expected: ProjectTypePythonPip,
		},
		{
			name: "Python pip project with requirements.txt",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("flask==2.0.0"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			expected: ProjectTypePythonPip,
		},
		{
			name: "Python Poetry project with pyproject.toml",
			setup: func(t *testing.T, dir string) {
				content := `[tool.poetry]
name = "test"
version = "0.1.0"`
				if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			},
			expected: ProjectTypePythonPip,
		},
		{
			name: "Python PDM project with pyproject.toml",
			setup: func(t *testing.T, dir string) {
				content := `[tool.pdm]
version = "2.0.0"`
				if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			},
			expected: ProjectTypePythonPip,
		},
		{
			name: "Python Hatch project with pyproject.toml",
			setup: func(t *testing.T, dir string) {
				content := `[tool.hatch]
version = "1.0.0"`
				if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			},
			expected: ProjectTypePythonPip,
		},
		{
			name: "Python UV project with tool.uv in pyproject.toml",
			setup: func(t *testing.T, dir string) {
				content := `[tool.uv]
package = true`
				if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			},
			expected: ProjectTypePythonUV,
		},
		{
			name: "Python UV project with dependency-groups",
			setup: func(t *testing.T, dir string) {
				content := `[dependency-groups]
dev = ["pytest>=7.0.0"]`
				if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			},
			expected: ProjectTypePythonUV,
		},
		{
			name: "Python UV project with uv sync reference",
			setup: func(t *testing.T, dir string) {
				content := `[project]
name = "test"
# Run uv sync to install dependencies`
				if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			},
			expected: ProjectTypePythonUV,
		},
		{
			name: "Generic Python project with plain pyproject.toml",
			setup: func(t *testing.T, dir string) {
				content := `[project]
name = "test"
version = "0.1.0"`
				if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			},
			expected: ProjectTypePythonPip,
		},
		{
			name: "Priority: uv.lock over requirements.txt",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "uv.lock"), []byte("# uv lockfile"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("flask==2.0.0"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			expected: ProjectTypePythonUV,
		},
		{
			name: "Priority: poetry.lock over requirements.txt",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "poetry.lock"), []byte("# poetry lockfile"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("flask==2.0.0"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			expected: ProjectTypePythonPip,
		},
		{
			name: "Unknown project type",
			setup: func(t *testing.T, dir string) {
				// Empty directory
			},
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tt.setup(t, tmpDir)

			result, err := DetectProjectType(tmpDir)

			if tt.shouldError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				if result != ProjectTypeUnknown {
					t.Errorf("expected ProjectTypeUnknown, got %v", result)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if result != tt.expected {
					t.Errorf("expected %v, got %v", tt.expected, result)
				}
			}
		})
	}
}

func TestIsUVByContent(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected bool
	}{
		{
			name: "UV with dependency-groups",
			content: `[dependency-groups]
dev = ["pytest>=7.0.0"]`,
			expected: true,
		},
		{
			name: "UV with tool.uv section",
			content: `[tool.uv]
package = true`,
			expected: true,
		},
		{
			name: "UV with uv sync reference",
			content: `# Run uv sync to install
[project]
name = "test"`,
			expected: true,
		},
		{
			name: "Poetry project",
			content: `[tool.poetry]
name = "test"`,
			expected: false,
		},
		{
			name: "Plain setuptools project",
			content: `[project]
name = "test"
version = "1.0.0"`,
			expected: false,
		},
		{
			name: "PDM project",
			content: `[tool.pdm]
version = "2.0.0"`,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isUVByContent(tt.content)
			if result != tt.expected {
				t.Errorf("isUVByContent() = %v, want %v", result, tt.expected)
			}
		})
	}
}
