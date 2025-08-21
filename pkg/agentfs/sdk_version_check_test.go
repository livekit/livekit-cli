package agentfs

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckSDKVersion(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "sdk-version-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	settingsMap := map[string]string{
		"python-min-sdk-version": "1.0.0",
		"node-min-sdk-version":   "1.0.0",
	}

	tests := []struct {
		name        string
		projectType ProjectType
		setupFiles  map[string]string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "Python setup.py with valid version",
			projectType: ProjectTypePythonPip,
			setupFiles: map[string]string{
				"setup.py": `setup(
    name="my-project",
    version="1.0.0",
    install_requires=[
        "livekit-agents>=1.5.0",
        "requests==2.25.1",
    ],
)`,
			},
			expectError: false,
		},
		{
			name:        "Python requirements.txt with valid version",
			projectType: ProjectTypePythonPip,
			setupFiles: map[string]string{
				"requirements.txt": "livekit-agents==1.5.0",
			},
			expectError: false,
		},
		{
			name:        "Python requirements.txt with old version",
			projectType: ProjectTypePythonPip,
			setupFiles: map[string]string{
				"requirements.txt": "livekit-agents==0.5.0",
			},
			expectError: true,
			errorMsg:    "too old",
		},
		{
			name:        "Python pyproject.toml with valid version",
			projectType: ProjectTypePythonPip,
			setupFiles: map[string]string{
				"pyproject.toml": `[project]
dependencies = ["livekit-agents>=1.0.0"]`,
			},
			expectError: false,
		},
		{
			name:        "Python pyproject.toml with comma-separated constraints",
			projectType: ProjectTypePythonPip,
			setupFiles: map[string]string{
				"pyproject.toml": `[project]
dependencies = ["livekit-agents>=1.2.5,<2"]`,
			},
			expectError: false,
		},
		{
			name:        "Python Pipfile with valid version",
			projectType: ProjectTypePythonPip,
			setupFiles: map[string]string{
				"Pipfile": `[packages]
livekit-agents = ">=1.0.0"`,
			},
			expectError: false,
		},
		{
			name:        "Node package.json with valid version",
			projectType: ProjectTypeNode,
			setupFiles: map[string]string{
				"package.json": `{
  "dependencies": {
    "@livekit/agents": "^1.5.0"
  }
}`,
			},
			expectError: false,
		},
		{
			name:        "Node package.json with old version",
			projectType: ProjectTypeNode,
			setupFiles: map[string]string{
				"package.json": `{
  "dependencies": {
    "@livekit/agents": "^0.5.0"
  }
}`,
			},
			expectError: true,
			errorMsg:    "too old",
		},
		{
			name:        "Node package.json with good version",
			projectType: ProjectTypeNode,
			setupFiles: map[string]string{
				"package.json": `{
  "dependencies": {
    "@livekit/agents": "^1.1.1"
  }
}`,
			},
			expectError: false,
		},

		{
			name:        "Node package-lock.json with valid version",
			projectType: ProjectTypeNode,
			setupFiles: map[string]string{
				"package-lock.json": `{
  "dependencies": {
    "@livekit/agents": {
      "version": "1.5.0"
    }
  }
}`,
			},
			expectError: false,
		},
		{
			name:        "Python poetry.lock with valid version",
			projectType: ProjectTypePythonPip,
			setupFiles: map[string]string{
				"poetry.lock": `[[package]]
name = "livekit-agents"
version = "1.5.0"`,
			},
			expectError: false,
		},
		{
			name:        "Python uv.lock with valid version",
			projectType: ProjectTypePythonUV,
			setupFiles: map[string]string{
				"uv.lock": `[[package]]
name = "livekit-agents"
version = "1.2.5"
source = { registry = "https://pypi.org/simple" }
dependencies = [
    { name = "aiohttp" },
    { name = "watchfiles" },
]
`,
			},
			expectError: false,
		},
		{
			name:        "No package found",
			projectType: ProjectTypePythonPip,
			setupFiles: map[string]string{
				"requirements.txt": "requests==2.25.1",
			},
			expectError: true,
			errorMsg:    "not found",
		},
		{
			name:        "No project files",
			projectType: ProjectTypePythonPip,
			setupFiles:  map[string]string{},
			expectError: true,
			errorMsg:    "unable to locate project files",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up temp dir
			os.RemoveAll(tempDir)
			os.MkdirAll(tempDir, 0755)

			// Create test files
			for filename, content := range tt.setupFiles {
				filePath := filepath.Join(tempDir, filename)
				if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
					t.Fatalf("Failed to create test file %s: %v", filename, err)
				}
			}

			// Run the test
			err := CheckSDKVersion(tempDir, tt.projectType, settingsMap)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				} else if tt.errorMsg != "" && !contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error to contain '%s', got: %v", tt.errorMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
			}
		})
	}
}

func TestIsVersionSatisfied(t *testing.T) {
	tests := []struct {
		version    string
		minVersion string
		sourceType SourceType
		expected   bool
		expectErr  bool
	}{
		// Lock file tests (exact version matching)
		{"1.5.0", "1.0.0", SourceTypeLock, true, false},
		{"1.0.0", "1.0.0", SourceTypeLock, true, false},
		{"0.9.0", "1.0.0", SourceTypeLock, false, false},
		{"1.5.0", "2.0.0", SourceTypeLock, false, false},
		{"2.0.0", "2.0.0", SourceTypeLock, true, false},

		// Package file tests (constraint satisfaction)
		{">=1.5.0", "1.0.0", SourceTypePackage, true, false},
		{"<2.0.0", "1.0.0", SourceTypePackage, true, false},
		{">=2.0.0", "1.0.0", SourceTypePackage, true, false},
		{"~1.2.0", "1.0.0", SourceTypePackage, true, false},
		{"^1.0.0", "1.0.0", SourceTypePackage, true, false},
		// Test the specific case that was failing: ^0.7.9 should satisfy minimum 0.0.7
		{"^0.7.9", "0.0.7", SourceTypePackage, true, false},
		// Test other caret scenarios
		{"^0.5.0", "0.0.7", SourceTypePackage, true, false}, // ^0.5.0 allows 0.5.0+ which >= 0.0.7
		{"^1.0.0", "0.0.7", SourceTypePackage, true, false}, // 1.0.0+ >= 0.0.7

		// Special cases
		{"latest", "1.0.0", SourceTypeLock, true, false},
		{"*", "1.0.0", SourceTypeLock, true, false},
		{"", "1.0.0", SourceTypeLock, true, false},

		// Error cases
		{"invalid", "1.0.0", SourceTypeLock, false, true},
		{"1.5.0", "invalid", SourceTypeLock, false, true},
	}

	for _, tt := range tests {
		sourceTypeStr := "Package"
		if tt.sourceType == SourceTypeLock {
			sourceTypeStr = "Lock"
		}
		t.Run(fmt.Sprintf("%s_vs_%s_%s", tt.version, tt.minVersion, sourceTypeStr), func(t *testing.T) {
			result, err := isVersionSatisfied(tt.version, tt.minVersion, tt.sourceType)

			if tt.expectErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
				if result != tt.expected {
					t.Errorf("Expected %v but got %v", tt.expected, result)
				}
			}
		})
	}
}

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		input       string
		sourceType  SourceType
		expected    string
		description string
	}{
		// Lock file tests (should remove ^ and ~)
		{"1.5.0", SourceTypeLock, "1.5.0", "exact version"},
		{"^1.5.0", SourceTypeLock, "1.5.0", "npm caret removed for lock file"},
		{"~1.5.0", SourceTypeLock, "1.5.0", "npm tilde removed for lock file"},
		{">=1.5.0", SourceTypeLock, "1.5.0", "semver operators removed for lock file"},
		{"<2.0.0", SourceTypeLock, "2.0.0", "semver operators removed for lock file"},
		{"==1.5.0", SourceTypeLock, "1.5.0", "semver operators removed for lock file"},
		{" 1.5.0 ", SourceTypeLock, "1.5.0", "whitespace removed"},
		{`"1.5.0"`, SourceTypeLock, "1.5.0", "quotes removed"},
		{`'1.5.0'`, SourceTypeLock, "1.5.0", "quotes removed"},
		{"*", SourceTypeLock, "*", "wildcard preserved"},
		{"latest", SourceTypeLock, "latest", "latest preserved"},

		// Package file tests (should preserve ^ and ~)
		{"1.5.0", SourceTypePackage, "1.5.0", "exact version"},
		{"^1.5.0", SourceTypePackage, "1.5.0", "npm caret removed for package file"},
		{"~1.5.0", SourceTypePackage, "1.5.0", "npm tilde removed for package file"},
		{">=1.5.0", SourceTypePackage, "1.5.0", "semver operators removed for package file"},
		{"<2.0.0", SourceTypePackage, "2.0.0", "semver operators removed for package file"},
		{"==1.5.0", SourceTypePackage, "1.5.0", "semver operators removed for package file"},
		{" 1.5.0 ", SourceTypePackage, "1.5.0", "whitespace removed"},
		{`"1.5.0"`, SourceTypePackage, "1.5.0", "quotes removed"},
		{`'1.5.0'`, SourceTypePackage, "1.5.0", "quotes removed"},
		{"*", SourceTypePackage, "*", "wildcard preserved"},
		{"latest", SourceTypePackage, "latest", "latest preserved"},
	}

	for _, tt := range tests {
		sourceTypeStr := "Package"
		if tt.sourceType == SourceTypeLock {
			sourceTypeStr = "Lock"
		}
		t.Run(fmt.Sprintf("%s_%s_%s", tt.input, sourceTypeStr, tt.description), func(t *testing.T) {
			result := normalizeVersion(tt.input, tt.sourceType)
			if result != tt.expected {
				t.Errorf("Expected %s but got %s", tt.expected, result)
			}
		})
	}
}

func TestDetectProjectFiles(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "project-files-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create some test files
	testFiles := []string{
		"requirements.txt",
		"pyproject.toml",
		"package.json",
		"package-lock.json",
		"poetry.lock",
	}

	for _, filename := range testFiles {
		filePath := filepath.Join(tempDir, filename)
		if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create test file %s: %v", filename, err)
		}
	}

	tests := []struct {
		projectType ProjectType
		expected    int // expected number of files
	}{
		{ProjectTypePythonPip, 3}, // requirements.txt, pyproject.toml, poetry.lock
		{ProjectTypePythonUV, 3},  // same as pip
		{ProjectTypeNode, 2},      // package.json, package-lock.json
	}

	for _, tt := range tests {
		t.Run(string(tt.projectType), func(t *testing.T) {
			files := detectProjectFiles(tempDir, tt.projectType)
			if len(files) != tt.expected {
				t.Errorf("Expected %d files but got %d: %v", tt.expected, len(files), files)
			}
		})
	}
}

func TestFindBestResult(t *testing.T) {
	results := []VersionCheckResult{
		{
			PackageInfo: PackageInfo{
				Name:        "livekit-agents",
				Version:     "1.5.0",
				FoundInFile: "requirements.txt",
			},
			Satisfied: true,
		},
		{
			PackageInfo: PackageInfo{
				Name:        "livekit-agents",
				Version:     "1.5.0",
				FoundInFile: "poetry.lock",
			},
			Satisfied: true,
		},
		{
			PackageInfo: PackageInfo{
				Name:        "livekit-agents",
				Version:     "1.5.0",
				FoundInFile: "pyproject.toml",
			},
			Satisfied: true,
		},
	}

	best := findBestResult(results)
	if best == nil {
		t.Fatal("Expected to find a best result")
	}

	// Should prefer poetry.lock over other files
	if filepath.Base(best.FoundInFile) != "poetry.lock" {
		t.Errorf("Expected poetry.lock to be preferred, got: %s", best.FoundInFile)
	}
}

func TestGetTargetPackageName(t *testing.T) {
	tests := []struct {
		projectType ProjectType
		expected    string
	}{
		{ProjectTypePythonPip, "livekit-agents"},
		{ProjectTypePythonUV, "livekit-agents"},
		{ProjectTypeNode, "@livekit/agents"},
		{ProjectTypeUnknown, ""},
	}

	for _, tt := range tests {
		t.Run(string(tt.projectType), func(t *testing.T) {
			result := tt.projectType.TargetPackageName()
			if result != tt.expected {
				t.Errorf("Expected %s but got %s", tt.expected, result)
			}
		})
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			func() bool {
				for i := 0; i <= len(s)-len(substr); i++ {
					if s[i:i+len(substr)] == substr {
						return true
					}
				}
				return false
			}())))
}

func TestParsePythonPackageVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"livekit-agents==1.5.0", "=1.5.0"},
		{"livekit-agents[extra]==1.5.0", "=1.5.0"},
		{"livekit-agents", "latest"},
		{"livekit-agents>=1.5.0", ">=1.5.0"},
		{"livekit-agents>=1.2.5,<2", ">=1.2.5"},
		{"livekit-agents 1.5.0", "1.5.0"},
		{"requests==2.25.1", ""},
		{"livekit-agents@git+https://github.com/livekit/livekit-agents.git", "latest"},
		{"livekit-agents[extra]@git+https://github.com/livekit/livekit-agents.git", "latest"},
		{"livekit-agents@git+https://github.com/livekit/livekit-agents.git", "latest"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			output, found := parsePythonPackageVersion(tt.input)
			if found != (tt.expected != "") { // Expect found to be true if expected is not empty
				t.Errorf("Expected found=%v, got %v", tt.expected != "", found)
			}
			if output != tt.expected {
				t.Errorf("Expected output=%q, got %q", tt.expected, output)
			}
		})
	}
}
