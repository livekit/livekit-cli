package agentfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsPython(t *testing.T) {
	tests := []struct {
		name     string
		files    map[string]string
		expected bool
		filename string
	}{
		{
			name: "requirements.txt",
			files: map[string]string{
				"requirements.txt": "livekit-agents==1.1.6",
			},
			expected: true,
			filename: "requirements.txt",
		},
		{
			name: "pyproject.toml",
			files: map[string]string{
				"pyproject.toml": `[project]
dependencies = ["livekit-agents>=1.1.6"]`,
			},
			expected: true,
			filename: "pyproject.toml",
		},
		{
			name: "requirements.lock",
			files: map[string]string{
				"requirements.lock": `{"dependencies": {"livekit-agents": "1.1.6"}}`,
			},
			expected: true,
			filename: "requirements.lock",
		},
		{
			name: "no python files",
			files: map[string]string{
				"package.json": `{"dependencies": {"livekit-agents": "1.1.6"}}`,
			},
			expected: false,
			filename: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			for filename, content := range tt.files {
				filePath := filepath.Join(tmpDir, filename)
				err := os.WriteFile(filePath, []byte(content), 0644)
				if err != nil {
					t.Fatalf("failed to create test file %s: %v", filename, err)
				}
			}

			isPython, filename := isPython(tmpDir)
			if isPython != tt.expected {
				t.Errorf("isPython() = %v, expected %v", isPython, tt.expected)
			}
			if filename != tt.filename {
				t.Errorf("isPython() filename = %v, expected %v", filename, tt.filename)
			}
		})
	}
}

func TestIsNode(t *testing.T) {
	tests := []struct {
		name     string
		files    map[string]string
		expected bool
		filename string
	}{
		{
			name: "package.json",
			files: map[string]string{
				"package.json": `{"dependencies": {"livekit-agents": "1.1.6"}}`,
			},
			expected: true,
			filename: "package.json",
		},
		{
			name: "package-lock.json",
			files: map[string]string{
				"package-lock.json": `{"dependencies": {"livekit-agents": {"version": "1.1.6"}}}`,
			},
			expected: true,
			filename: "package-lock.json",
		},
		{
			name: "yarn.lock",
			files: map[string]string{
				"yarn.lock": `livekit-agents@1.1.6:`,
			},
			expected: true,
			filename: "yarn.lock",
		},
		{
			name: "pnpm-lock.yaml",
			files: map[string]string{
				"pnpm-lock.yaml": `dependencies:
  livekit-agents: 1.1.6`,
			},
			expected: true,
			filename: "pnpm-lock.yaml",
		},
		{
			name: "no node files",
			files: map[string]string{
				"requirements.txt": "livekit-agents==1.1.6",
			},
			expected: false,
			filename: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			for filename, content := range tt.files {
				filePath := filepath.Join(tmpDir, filename)
				err := os.WriteFile(filePath, []byte(content), 0644)
				if err != nil {
					t.Fatalf("failed to create test file %s: %v", filename, err)
				}
			}

			isNode, filename := isNode(tmpDir)
			if isNode != tt.expected {
				t.Errorf("isNode() = %v, expected %v", isNode, tt.expected)
			}
			if filename != tt.filename {
				t.Errorf("isNode() filename = %v, expected %v", filename, tt.filename)
			}
		})
	}
}

func TestGetDependencyFile(t *testing.T) {
	tests := []struct {
		name        string
		files       map[string]string
		expected    string
		expectError bool
	}{
		{
			name: "python requirements.txt",
			files: map[string]string{
				"requirements.txt": "livekit-agents==1.1.6",
			},
			expected:    "requirements.txt",
			expectError: false,
		},
		{
			name: "node package.json",
			files: map[string]string{
				"package.json": `{"dependencies": {"livekit-agents": "1.1.6"}}`,
			},
			expected:    "package.json",
			expectError: false,
		},
		{
			name: "no dependency files",
			files: map[string]string{
				"README.md": "# Test Project",
			},
			expected:    "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			for filename, content := range tt.files {
				filePath := filepath.Join(tmpDir, filename)
				err := os.WriteFile(filePath, []byte(content), 0644)
				if err != nil {
					t.Fatalf("failed to create test file %s: %v", filename, err)
				}
			}

			result, err := getDependencyFile(tmpDir)
			if tt.expectError {
				if err == nil {
					t.Errorf("getDependencyFile() expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("getDependencyFile() unexpected error: %v", err)
				}
				expectedPath := filepath.Join(tmpDir, tt.expected)
				if result != expectedPath {
					t.Errorf("getDependencyFile() = %v, expected %v", result, expectedPath)
				}
			}
		})
	}
}

func TestValidateVersion(t *testing.T) {
	tests := []struct {
		name           string
		currentVersion string
		minVersion     string
		packageName    string
		expectError    bool
		errorContains  string
	}{
		{
			name:           "valid version newer than minimum",
			currentVersion: "1.2.0",
			minVersion:     "1.1.6",
			packageName:    "livekit-agents",
			expectError:    false,
		},
		{
			name:           "valid version equal to minimum",
			currentVersion: "1.1.6",
			minVersion:     "1.1.6",
			packageName:    "livekit-agents",
			expectError:    false,
		},
		{
			name:           "version too old",
			currentVersion: "1.1.5",
			minVersion:     "1.1.6",
			packageName:    "livekit-agents",
			expectError:    true,
			errorContains:  "too old",
		},
		{
			name:           "invalid current version format",
			currentVersion: "invalid-version",
			minVersion:     "1.1.6",
			packageName:    "livekit-agents",
			expectError:    true,
			errorContains:  "invalid current version format",
		},
		{
			name:           "invalid minimum version format",
			currentVersion: "1.1.6",
			minVersion:     "invalid-version",
			packageName:    "livekit-agents",
			expectError:    true,
			errorContains:  "invalid minimum version format",
		},
		{
			name:           "prerelease version handling",
			currentVersion: "1.2.0-alpha.1",
			minVersion:     "1.1.6",
			packageName:    "livekit-agents",
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVersion(tt.currentVersion, tt.minVersion, tt.packageName)
			if tt.expectError {
				if err == nil {
					t.Errorf("validateVersion() expected error but got none")
				} else if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("validateVersion() error = %v, expected to contain %v", err, tt.errorContains)
				}
			} else {
				if err != nil {
					t.Errorf("validateVersion() unexpected error: %v", err)
				}
			}
		})
	}
}

func TestScanDependencyFile(t *testing.T) {
	tests := []struct {
		name          string
		fileContent   string
		fileName      string
		targetPackage string
		minVersion    string
		expectError   bool
		errorContains string
	}{
		{
			name: "python requirements.txt with livekit-agents",
			fileContent: `livekit-agents==1.2.0
requests>=2.25.0`,
			fileName:      "requirements.txt",
			targetPackage: "livekit-agents",
			minVersion:    "1.1.6",
			expectError:   false,
		},
		{
			name: "python requirements.txt with livekit-agents version too old",
			fileContent: `livekit-agents==1.1.5
requests>=2.25.0`,
			fileName:      "requirements.txt",
			targetPackage: "livekit-agents",
			minVersion:    "1.1.6",
			expectError:   true,
			errorContains: "too old",
		},
		{
			name: "python requirements.txt with livekit-agents compatible release too low",
			fileContent: `livekit-agents~=1.1.4
requests>=2.25.0`,
			fileName:      "requirements.txt",
			targetPackage: "livekit-agents",
			minVersion:    "1.1.6",
			expectError:   true,
			errorContains: "too old",
		},
		{
			name: "python requirements.txt with livekit-agents compatible release success",
			fileContent: `livekit-agents~=1.1.6
requests>=2.25.0`,
			fileName:      "requirements.txt",
			targetPackage: "livekit-agents",
			minVersion:    "1.1.6",
			expectError:   false,
		},
		{
			name: "python requirements.txt without version specified",
			fileContent: `livekit-agents
requests>=2.25.0`,
			fileName:      "requirements.txt",
			targetPackage: "livekit-agents",
			minVersion:    "1.1.6",
			expectError:   false,
		},
		{
			name: "python requirements.txt without livekit-agents",
			fileContent: `requests>=2.25.0
numpy>=1.20.0`,
			fileName:      "requirements.txt",
			targetPackage: "livekit-agents",
			minVersion:    "1.1.6",
			expectError:   true,
			errorContains: "not found",
		},
		{
			name: "node package-lock.json with livekit-agents",
			fileContent: `{
  "name": "test-project",
  "version": "1.0.0",
  "lockfileVersion": 2,
  "dependencies": {
    "livekit-agents": {
      "version": "1.2.0",
      "resolved": "https://registry.npmjs.org/livekit-agents/-/livekit-agents-1.2.0.tgz"
    },
    "express": {
      "version": "4.17.1",
      "resolved": "https://registry.npmjs.org/express/-/express-4.17.1.tgz"
    }
  }
}`,
			fileName:      "package-lock.json",
			targetPackage: "livekit-agents",
			minVersion:    "1.1.6",
			expectError:   false,
		},
		{
			name: "node package-lock.json with livekit-agents version too old",
			fileContent: `{
  "name": "test-project",
  "version": "1.0.0",
  "lockfileVersion": 2,
  "dependencies": {
    "livekit-agents": {
      "version": "1.1.5",
      "resolved": "https://registry.npmjs.org/livekit-agents/-/livekit-agents-1.1.5.tgz"
    },
    "express": {
      "version": "4.17.1",
      "resolved": "https://registry.npmjs.org/express/-/express-4.17.1.tgz"
    }
  }
}`,
			fileName:      "package-lock.json",
			targetPackage: "livekit-agents",
			minVersion:    "1.1.6",
			expectError:   true,
			errorContains: "too old",
		},
		{
			name: "node package-lock.json with livekit-agents compatible release too low",
			fileContent: `{
  "name": "test-project",
  "version": "1.0.0",
  "lockfileVersion": 2,
  "dependencies": {
    "livekit-agents": {
      "version": "1.1.4",
      "resolved": "https://registry.npmjs.org/livekit-agents/-/livekit-agents-1.1.4.tgz"
    },
    "express": {
      "version": "4.17.1",
      "resolved": "https://registry.npmjs.org/express/-/express-4.17.1.tgz"
    }
  }
}`,
			fileName:      "package-lock.json",
			targetPackage: "livekit-agents",
			minVersion:    "1.1.6",
			expectError:   true,
			errorContains: "too old",
		},
		{
			name: "node package-lock.json with livekit-agents compatible release success",
			fileContent: `{
  "name": "test-project",
  "version": "1.0.0",
  "lockfileVersion": 2,
  "dependencies": {
    "livekit-agents": {
      "version": "1.1.6",
      "resolved": "https://registry.npmjs.org/livekit-agents/-/livekit-agents-1.1.6.tgz"
    },
    "express": {
      "version": "4.17.1",
      "resolved": "https://registry.npmjs.org/express/-/express-4.17.1.tgz"
    }
  }
}`,
			fileName:      "package-lock.json",
			targetPackage: "livekit-agents",
			minVersion:    "1.1.6",
			expectError:   false,
		},
		{
			name: "node yarn.lock with livekit-agents",
			fileContent: `# THIS IS AN AUTOGENERATED FILE. DO NOT EDIT THIS FILE DIRECTLY.
# yarn lockfile v1

livekit-agents@1.2.0:
  version "1.2.0"
  resolved "https://registry.npmjs.org/livekit-agents/-/livekit-agents-1.2.0.tgz"
  integrity sha512-abc123

express@^4.17.1:
  version "4.17.1"
  resolved "https://registry.npmjs.org/express/-/express-4.17.1.tgz"
  integrity sha512-def456`,
			fileName:      "yarn.lock",
			targetPackage: "livekit-agents",
			minVersion:    "1.1.6",
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile := filepath.Join(t.TempDir(), tt.fileName)
			err := os.WriteFile(tmpFile, []byte(tt.fileContent), 0644)
			if err != nil {
				t.Fatalf("failed to create test file: %v", err)
			}

			err = scanDependencyFile(tmpFile, tt.targetPackage, tt.minVersion)
			if tt.expectError {
				if err == nil {
					t.Errorf("scanDependencyFile() expected error but got none")
				} else if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("scanDependencyFile() error = %v, expected to contain %v", err, tt.errorContains)
				}
			} else {
				if err != nil {
					t.Errorf("scanDependencyFile() unexpected error: %v", err)
				}
			}
		})
	}
}
