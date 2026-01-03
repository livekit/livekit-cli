package agentfs

import (
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
				"uv.lock": `livekit-agents = "1.5.0"`,
			},
			expectError: false,
		},
		{
			name:        "Python requirements.txt with prerelease version",
			projectType: ProjectTypePythonPip,
			setupFiles: map[string]string{
				"requirements.txt": "livekit-agents~=1.3rc",
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
		expected   bool
		expectErr  bool
	}{
		{"1.5.0", "1.0.0", true, false},
		{"1.0.0", "1.0.0", true, false},
		{"0.9.0", "1.0.0", false, false},
		{"latest", "1.0.0", true, false},
		{"*", "1.0.0", true, false},
		{"", "1.0.0", true, false},
		{"^1.5.0", "1.0.0", true, false},
		{"~1.5.0", "1.0.0", true, false},
		{">=1.5.0", "1.0.0", true, false},
		{"==1.5.0", "1.0.0", true, false},
		{">=1.3.0rc1", "1.2.0", true, false},    // prerelease should satisfy lower base version
		{"1.3.0.rc1", "1.3.0", true, false},     // prerelease should satisfy same base version
		{"1.3rc", "1.3.0", true, false},         // short prerelease should satisfy same base version
		{"1.3.0rc1", "1.4.0", false, false},     // prerelease should not satisfy higher version
		{"1.0.0.rc2", "1.0.0", true, false},     // dot prerelease should satisfy same base version
		{"1.3.0.beta1", "1.3.0", true, false},   // dot prerelease should satisfy same base version
		{"1.2.0.alpha1", "1.3.0", false, false}, // dot prerelease should not satisfy higher version
		{"invalid", "1.0.0", false, true},
		{"1.5.0", "invalid", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.version+"_vs_"+tt.minVersion, func(t *testing.T) {
			result, err := isVersionSatisfied(tt.version, tt.minVersion)

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
		input    string
		expected string
	}{
		{"1.5.0", "1.5.0"},
		{"^1.5.0", "1.5.0"},
		{"~1.5.0", "1.5.0"},
		{">=1.5.0", "1.5.0"},
		{"==1.5.0", "1.5.0"},
		{" 1.5.0 ", "1.5.0"},
		{`"1.5.0"`, "1.5.0"},
		{`'1.5.0'`, "1.5.0"},
		{"*", "*"},
		{"latest", "latest"},
		{"1.3.0rc1", "1.3.0-rc1"},
		{"1.3.0beta2", "1.3.0-beta2"},
		{"1.3.0alpha1", "1.3.0-alpha1"},
		{"1.3rc", "1.3.0-rc"},
		{"1.3rc1", "1.3.0-rc1"},
		{"~=1.3rc", "1.3.0-rc"},
		{"1.0.0.rc2", "1.0.0-rc2"},
		{"1.3.0.beta1", "1.3.0-beta1"},
		{"1.5.2.alpha3", "1.5.2-alpha3"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeVersion(tt.input)
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
			result := getTargetPackageName(tt.projectType)
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
		name           string
		input          string
		expectedOutput string
		expectedFound  bool
	}{
		{
			name:           "Simple version",
			input:          "livekit-agents==1.5.0",
			expectedOutput: "==1.5.0",
			expectedFound:  true,
		},
		{
			name:           "Version with extras",
			input:          "livekit-agents[extra]==1.5.0",
			expectedOutput: "==1.5.0",
			expectedFound:  true,
		},
		{
			name:           "extra with compatible version",
			input:          "livekit-agents[extra1,extra2]~=1.5.0.rc2",
			expectedOutput: "~=1.5.0.rc2",
			expectedFound:  true,
		},
		{
			name:           "Version with no specifier",
			input:          "livekit-agents",
			expectedOutput: "latest",
			expectedFound:  true,
		},
		{
			name:           "Version with greater than",
			input:          "livekit-agents>=1.5.0",
			expectedOutput: ">=1.5.0",
			expectedFound:  true,
		},
		{
			name:           "Comma-separated constraints",
			input:          "livekit-agents>=1.2.5,<2",
			expectedOutput: ">=1.2.5",
			expectedFound:  true,
		},
		{
			name:           "Space-separated constraints",
			input:          "livekit-agents>=1.2.5 <2",
			expectedOutput: ">=1.2.5",
			expectedFound:  true,
		},
		{
			name:           "Not livekit-agents",
			input:          "some-other-package==1.0.0",
			expectedOutput: "",
			expectedFound:  false,
		},
		{
			name:           "Git URL format",
			input:          "livekit-agents[openai,turn-detector,silero,cartesia,deepgram] @ git+https://github.com/livekit/agents.git@load-debug#subdirectory=livekit-agents",
			expectedOutput: "latest",
			expectedFound:  true,
		},
		{
			name:           "Git URL format with extras",
			input:          "livekit-agents[voice] @ git+https://github.com/livekit/agents.git@main",
			expectedOutput: "latest",
			expectedFound:  true,
		},
		{
			name:           "Git URL format simple",
			input:          "livekit-agents @ git+https://github.com/livekit/agents.git",
			expectedOutput: "latest",
			expectedFound:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, found := parsePythonPackageVersion(tt.input)
			if found != tt.expectedFound {
				t.Errorf("Expected found=%v, got %v", tt.expectedFound, found)
			}
			if output != tt.expectedOutput {
				t.Errorf("Expected output=%q, got %q", tt.expectedOutput, output)
			}
		})
	}
}
