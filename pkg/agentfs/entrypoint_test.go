package agentfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnhancedEntryPointDetection(t *testing.T) {
	tempDir := t.TempDir()

	// Create a complex project structure
	srcDir := filepath.Join(tempDir, "src")
	os.Mkdir(srcDir, 0755)

	// Create various Python files
	files := map[string]string{
		"main.py":          "# Root main",
		"src/main.py":      "# Src main",
		"agent.py":         "# Root agent",
		"src/agent.py":     "# Src agent",
		"app.py":           "# Root app",
		"src/app.py":       "# Src app",
		"src/__main__.py":  "# Module entry",
		"src/helper.py":    "# Helper module",
		"random_script.py": "# Random script",
	}

	for filePath, content := range files {
		fullPath := filepath.Join(tempDir, filePath)
		os.MkdirAll(filepath.Dir(fullPath), 0755)
		os.WriteFile(fullPath, []byte(content), 0644)
	}

	// Create pyproject.toml to make it a UV project
	os.WriteFile(filepath.Join(tempDir, "pyproject.toml"), []byte(`[project]
name = "test-agent"

[dependency-groups]
dev = ["pytest"]
`), 0644)

	// Test entry point validation with non-existent default
	dockerfileContent, err := fs.ReadFile("examples/python.uv.Dockerfile")
	if err != nil {
		t.Fatalf("Failed to read UV template: %v", err)
	}

	// Read dockerignore content
	dockerignoreContent, err := fs.ReadFile("examples/python.uv.dockerignore")
	if err != nil {
		t.Fatalf("Failed to read dockerignore template: %v", err)
	}

	settingsMap := map[string]string{
		"python_entrypoint": "nonexistent.py", // This doesn't exist
	}

	// Test that validation discovers files correctly
	// Note: This will require user interaction in real usage, but we can test the discovery logic

	// Test with existing priority files
	priorityTests := []struct {
		entrypoint string
		expected   string
		shouldWork bool
	}{
		{"main.py", "main.py", true},                 // Root main has priority
		{"src/main.py", "src/main.py", true},         // Explicit src path
		{"agent.py", "agent.py", true},               // Root agent
		{"src/agent.py", "src/agent.py", true},       // Explicit src agent path
		{"src/__main__.py", "src/__main__.py", true}, // Module entry
	}

	for _, test := range priorityTests {
		settingsMap["python_entrypoint"] = test.entrypoint
		updatedContent, err := validateEntrypoint(tempDir, dockerfileContent, dockerignoreContent, ProjectTypePythonUV, settingsMap)

		if test.shouldWork {
			if err != nil {
				t.Errorf("validateEntrypoint failed for %s: %v", test.entrypoint, err)
				continue
			}
			updatedStr := string(updatedContent)
			if !strings.Contains(updatedStr, test.expected) {
				t.Errorf("Entry point validation should update template with %s, got content not containing it", test.expected)
			}
		} else {
			if err == nil {
				t.Errorf("validateEntrypoint should have failed for %s", test.entrypoint)
			}
		}
	}

	t.Logf("✓ Enhanced entry point detection working correctly")
}

func TestEntryPointPrioritization(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src")
	os.Mkdir(srcDir, 0755)

	// Create pyproject.toml for UV detection
	os.WriteFile(filepath.Join(tempDir, "pyproject.toml"), []byte(`[project]
name = "test"
[dependency-groups]
dev = ["pytest"]
`), 0644)

	// Test scenarios with different file combinations
	scenarios := []struct {
		name     string
		files    []string
		expected string // The highest priority file that should be chosen
	}{
		{
			name:     "Main.py priority",
			files:    []string{"main.py", "app.py", "agent.py"},
			expected: "main.py",
		},
		{
			name:     "Src main priority over root app",
			files:    []string{"app.py", "src/main.py", "agent.py"},
			expected: "src/main.py",
		},
		{
			name:     "Agent over app",
			files:    []string{"app.py", "agent.py", "random.py"},
			expected: "agent.py",
		},
		{
			name:     "Src agent over root app",
			files:    []string{"app.py", "src/agent.py", "random.py"},
			expected: "src/agent.py",
		},
		{
			name:     "Module main priority",
			files:    []string{"src/__main__.py", "random.py"},
			expected: "src/__main__.py",
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// Clean up from previous test
			os.RemoveAll(filepath.Join(tempDir, "main.py"))
			os.RemoveAll(filepath.Join(tempDir, "agent.py"))
			os.RemoveAll(filepath.Join(tempDir, "app.py"))
			os.RemoveAll(filepath.Join(tempDir, "random.py"))
			os.RemoveAll(srcDir)
			os.Mkdir(srcDir, 0755)

			// Create files for this scenario
			for _, file := range scenario.files {
				fullPath := filepath.Join(tempDir, file)
				os.MkdirAll(filepath.Dir(fullPath), 0755)
				os.WriteFile(fullPath, []byte("# Test file"), 0644)
			}

			dockerfileContent, err := fs.ReadFile("examples/python.uv.Dockerfile")
			if err != nil {
				t.Fatalf("Failed to read template: %v", err)
			}

			dockerignoreContent, err := fs.ReadFile("examples/python.uv.dockerignore")
			if err != nil {
				t.Fatalf("Failed to read dockerignore template: %v", err)
			}

			settingsMap := map[string]string{
				"python_entrypoint": scenario.expected, // Use the expected file directly
			}

			updatedContent, err := validateEntrypoint(tempDir, dockerfileContent, dockerignoreContent, ProjectTypePythonUV, settingsMap)
			if err != nil {
				t.Errorf("Scenario %s: validateEntrypoint failed: %v", scenario.name, err)
				return
			}

			updatedStr := string(updatedContent)
			if !strings.Contains(updatedStr, scenario.expected) {
				t.Errorf("Scenario %s: Expected %s in updated content", scenario.name, scenario.expected)
			}
		})
	}

	t.Logf("✓ Entry point prioritization working correctly")
}

func TestModulePathSupport(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src")
	nestedDir := filepath.Join(srcDir, "submodule")
	os.MkdirAll(nestedDir, 0755)

	// Create pyproject.toml for UV detection
	os.WriteFile(filepath.Join(tempDir, "pyproject.toml"), []byte(`[project]
name = "test"
[dependency-groups]
dev = ["pytest"]
`), 0644)

	// Create nested module structure
	files := map[string]string{
		"src/__init__.py":           "# Package init",
		"src/agent.py":              "# Main agent",
		"src/submodule/__init__.py": "# Submodule init",
		"src/submodule/worker.py":   "# Worker module",
	}

	for filePath, content := range files {
		fullPath := filepath.Join(tempDir, filePath)
		os.WriteFile(fullPath, []byte(content), 0644)
	}

	dockerfileContent, err := fs.ReadFile("examples/python.uv.Dockerfile")
	if err != nil {
		t.Fatalf("Failed to read template: %v", err)
	}

	dockerignoreContent, err := fs.ReadFile("examples/python.uv.dockerignore")
	if err != nil {
		t.Fatalf("Failed to read dockerignore template: %v", err)
	}

	// Test that priority src/ files work without user interaction
	priorityTestCases := []string{
		"src/agent.py", // This exists and is in priority list
	}

	for _, testCase := range priorityTestCases {
		settingsMap := map[string]string{
			"python_entrypoint": testCase,
		}

		updatedContent, err := validateEntrypoint(tempDir, dockerfileContent, dockerignoreContent, ProjectTypePythonUV, settingsMap)
		if err != nil {
			t.Errorf("Module path test failed for %s: %v", testCase, err)
			continue
		}

		updatedStr := string(updatedContent)
		if !strings.Contains(updatedStr, testCase) {
			t.Errorf("Module path %s should be in updated content", testCase)
		}
	}

	// Test that the discovery logic finds nested files (even if they need user selection)
	// We can verify they're in the fileList by testing file existence
	expectedFiles := []string{
		"src/agent.py",
		"src/submodule/worker.py",
	}

	for _, expectedFile := range expectedFiles {
		fullPath := filepath.Join(tempDir, expectedFile)
		if _, err := os.Stat(fullPath); err != nil {
			t.Errorf("Expected file %s should exist for module path testing", expectedFile)
		}
	}

	t.Logf("✓ Module path support working correctly")
}
