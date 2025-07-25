package agentfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectDetection(t *testing.T) {
	// Test UV project detection with the actual agent-starter-python example
	uvProjectPath := "/Volumes/Code/workspaces/cloud-agents/livekit-examples/agent-starter-python"
	if _, err := os.Stat(uvProjectPath); err == nil {
		projectType, err := DetectProjectType(uvProjectPath)
		if err != nil {
			t.Errorf("Expected DetectProjectType to succeed for UV project at %s: %v", uvProjectPath, err)
		}
		if !projectType.IsPython() {
			t.Errorf("Expected IsPython() to return true for UV project at %s, got %s", uvProjectPath, projectType)
		}
		if projectType != ProjectTypePythonUV {
			t.Errorf("Expected ProjectTypePythonUV for UV project at %s, got %s", uvProjectPath, projectType)
		}
		t.Logf("✓ Successfully detected UV project at %s", uvProjectPath)
	} else {
		t.Logf("Skipping UV project test - path not available: %s", uvProjectPath)
	}

	// Test pip project detection with the actual agent-deployment example
	pipProjectPath := "/Volumes/Code/workspaces/cloud-agents/livekit-examples/agent-deployment/python-agent-example-app"
	if _, err := os.Stat(pipProjectPath); err == nil {
		projectType, err := DetectProjectType(pipProjectPath)
		if err != nil {
			t.Errorf("Expected DetectProjectType to succeed for pip project at %s: %v", pipProjectPath, err)
		}
		if !projectType.IsPython() {
			t.Errorf("Expected IsPython() to return true for pip project at %s, got %s", pipProjectPath, projectType)
		}
		if projectType == ProjectTypePythonUV {
			t.Errorf("Expected pip project (not UV) at %s, got %s", pipProjectPath, projectType)
		}
		t.Logf("✓ Successfully detected pip project at %s", pipProjectPath)
	} else {
		t.Logf("Skipping pip project test - path not available: %s", pipProjectPath)
	}
}

func TestProjectDetectionWithTempDirs(t *testing.T) {
	// Create temporary directories for testing
	tempDir := t.TempDir()

	// Test with requirements.txt only
	pipDir := filepath.Join(tempDir, "pip-project")
	os.Mkdir(pipDir, 0755)
	os.WriteFile(filepath.Join(pipDir, "requirements.txt"), []byte("flask==2.0.1\n"), 0644)

	projectType, err := DetectProjectType(pipDir)
	if err != nil {
		t.Errorf("Expected DetectProjectType to succeed for requirements.txt directory: %v", err)
	}
	if !projectType.IsPython() {
		t.Errorf("Expected IsPython() to return true for directory with requirements.txt, got %s", projectType)
	}
	if projectType != ProjectTypePythonPip {
		t.Errorf("Expected ProjectTypePythonPip for directory with only requirements.txt, got %s", projectType)
	}

	// Test with pyproject.toml only (non-UV)
	pyprojectDir := filepath.Join(tempDir, "pyproject-project")
	os.Mkdir(pyprojectDir, 0755)
	os.WriteFile(filepath.Join(pyprojectDir, "pyproject.toml"), []byte(`[project]
name = "test-project"
dependencies = ["flask"]
`), 0644)

	projectType, err = DetectProjectType(pyprojectDir)
	if err != nil {
		t.Errorf("Expected DetectProjectType to succeed for pyproject.toml directory: %v", err)
	}
	if !projectType.IsPython() {
		t.Errorf("Expected IsPython() to return true for directory with pyproject.toml, got %s", projectType)
	}
	if projectType != ProjectTypePythonPip {
		t.Errorf("Expected ProjectTypePythonPip for directory with basic pyproject.toml, got %s", projectType)
	}

	// Test with UV indicators in pyproject.toml content
	uvDir := filepath.Join(tempDir, "uv-project")
	os.Mkdir(uvDir, 0755)
	os.WriteFile(filepath.Join(uvDir, "pyproject.toml"), []byte(`[project]
name = "test-project"
dependencies = ["flask"]

[dependency-groups]
dev = ["pytest"]
`), 0644)

	projectType, err = DetectProjectType(uvDir)
	if err != nil {
		t.Errorf("Expected DetectProjectType to succeed for UV directory: %v", err)
	}
	if !projectType.IsPython() {
		t.Errorf("Expected IsPython() to return true for UV directory, got %s", projectType)
	}
	if projectType != ProjectTypePythonUV {
		t.Errorf("Expected ProjectTypePythonUV for directory with dependency-groups, got %s", projectType)
	}

	// Test with uv.lock file (highest priority)
	uvLockDir := filepath.Join(tempDir, "uv-lock-project")
	os.Mkdir(uvLockDir, 0755)
	os.WriteFile(filepath.Join(uvLockDir, "pyproject.toml"), []byte(`[project]
name = "test-project"
`), 0644)
	os.WriteFile(filepath.Join(uvLockDir, "uv.lock"), []byte("# UV lock file\n"), 0644)

	projectType, err = DetectProjectType(uvLockDir)
	if err != nil {
		t.Errorf("Expected DetectProjectType to succeed for UV lock directory: %v", err)
	}
	if !projectType.IsPython() {
		t.Errorf("Expected IsPython() to return true for UV lock directory, got %s", projectType)
	}
	if projectType != ProjectTypePythonUV {
		t.Errorf("Expected ProjectTypePythonUV for directory with uv.lock, got %s", projectType)
	}

	// Test Poetry project
	poetryDir := filepath.Join(tempDir, "poetry-project")
	os.Mkdir(poetryDir, 0755)
	os.WriteFile(filepath.Join(poetryDir, "poetry.lock"), []byte("# Poetry lock file\n"), 0644)
	os.WriteFile(filepath.Join(poetryDir, "pyproject.toml"), []byte(`[tool.poetry]
name = "test-project"
version = "0.1.0"
`), 0644)

	projectType, err = DetectProjectType(poetryDir)
	if err != nil {
		t.Errorf("Expected DetectProjectType to succeed for Poetry directory: %v", err)
	}
	if !projectType.IsPython() {
		t.Errorf("Expected IsPython() to return true for Poetry directory, got %s", projectType)
	}
	if projectType != ProjectTypePythonPip {
		t.Errorf("Expected ProjectTypePythonPip for Poetry project, got %s", projectType)
	}

	// Test Node.js project
	nodeDir := filepath.Join(tempDir, "node-project")
	os.Mkdir(nodeDir, 0755)
	os.WriteFile(filepath.Join(nodeDir, "package.json"), []byte(`{"name": "test-app"}`), 0644)

	projectType, err = DetectProjectType(nodeDir)
	if err != nil {
		t.Errorf("Expected DetectProjectType to succeed for Node.js directory: %v", err)
	}
	if !projectType.IsNode() {
		t.Errorf("Expected IsNode() to return true for Node.js directory, got %s", projectType)
	}
	if projectType != ProjectTypeNode {
		t.Errorf("Expected ProjectTypeNode for Node.js directory, got %s", projectType)
	}

	// Test non-supported project directory
	nonSupportedDir := filepath.Join(tempDir, "non-supported")
	os.Mkdir(nonSupportedDir, 0755)
	os.WriteFile(filepath.Join(nonSupportedDir, "README.md"), []byte("# Not a supported project\n"), 0644)

	projectType, err = DetectProjectType(nonSupportedDir)
	if err == nil {
		t.Errorf("Expected DetectProjectType to fail for non-supported directory")
	}
	if projectType != ProjectTypeUnknown {
		t.Errorf("Expected ProjectTypeUnknown for non-supported directory, got %s", projectType)
	}
}
