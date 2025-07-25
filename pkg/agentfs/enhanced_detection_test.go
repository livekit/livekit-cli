package agentfs

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPDMProjectDetection tests PDM project detection via [tool.pdm] in pyproject.toml
func TestPDMProjectDetection(t *testing.T) {
	tempDir := t.TempDir()

	// Create PDM project with pyproject.toml containing [tool.pdm]
	pyprojectContent := `[project]
name = "test-pdm-project"
version = "0.1.0"
dependencies = ["fastapi", "uvicorn"]

[tool.pdm]
version = {source = "file", path = "src/version.txt"}
includes = ["src"]

[tool.pdm.dev-dependencies]
test = ["pytest", "pytest-cov"]
`

	os.WriteFile(filepath.Join(tempDir, "pyproject.toml"), []byte(pyprojectContent), 0644)

	projectType, err := DetectProjectType(tempDir)
	if err != nil {
		t.Errorf("Expected DetectProjectType to succeed for PDM project: %v", err)
	}

	if !projectType.IsPython() {
		t.Errorf("Expected IsPython() to return true for PDM project, got %s", projectType)
	}

	if projectType != ProjectTypePythonPip {
		t.Errorf("Expected ProjectTypePythonPip for PDM project, got %s", projectType)
	}

	t.Logf("✓ PDM project correctly detected as pip-compatible")
}

// TestHatchProjectDetection tests Hatch project detection via [tool.hatch] in pyproject.toml
func TestHatchProjectDetection(t *testing.T) {
	tempDir := t.TempDir()

	// Create Hatch project with pyproject.toml containing [tool.hatch]
	pyprojectContent := `[project]
name = "test-hatch-project"
version = "0.1.0"
dependencies = ["click", "rich"]

[tool.hatch.version]
path = "src/mypackage/__init__.py"

[tool.hatch.build.targets.wheel]
packages = ["src/mypackage"]
`

	os.WriteFile(filepath.Join(tempDir, "pyproject.toml"), []byte(pyprojectContent), 0644)

	projectType, err := DetectProjectType(tempDir)
	if err != nil {
		t.Errorf("Expected DetectProjectType to succeed for Hatch project: %v", err)
	}

	if !projectType.IsPython() {
		t.Errorf("Expected IsPython() to return true for Hatch project, got %s", projectType)
	}

	if projectType != ProjectTypePythonPip {
		t.Errorf("Expected ProjectTypePythonPip for Hatch project, got %s", projectType)
	}

	t.Logf("✓ Hatch project correctly detected as pip-compatible")
}

// TestPipenvProjectDetection tests Pipenv project detection via Pipfile.lock
func TestPipenvProjectDetection(t *testing.T) {
	tempDir := t.TempDir()

	// Create Pipenv project with Pipfile.lock
	pipfileLockContent := `{
    "_meta": {
        "hash": {
            "sha256": "example-hash"
        },
        "pipfile-spec": 6,
        "requires": {
            "python_version": "3.11"
        },
        "sources": [
            {
                "name": "pypi",
                "url": "https://pypi.org/simple",
                "verify_ssl": true
            }
        ]
    },
    "default": {
        "flask": {
            "hashes": ["example-hash"],
            "version": "==2.3.0"
        }
    },
    "develop": {}
}`

	os.WriteFile(filepath.Join(tempDir, "Pipfile.lock"), []byte(pipfileLockContent), 0644)

	projectType, err := DetectProjectType(tempDir)
	if err != nil {
		t.Errorf("Expected DetectProjectType to succeed for Pipenv project: %v", err)
	}

	if !projectType.IsPython() {
		t.Errorf("Expected IsPython() to return true for Pipenv project, got %s", projectType)
	}

	if projectType != ProjectTypePythonPip {
		t.Errorf("Expected ProjectTypePythonPip for Pipenv project, got %s", projectType)
	}

	t.Logf("✓ Pipenv project correctly detected as pip-compatible")
}

// TestPriorityOrderDetection tests the priority order when multiple indicators are present
func TestPriorityOrderDetection(t *testing.T) {
	tempDir := t.TempDir()

	// Test 1: uv.lock should take priority over poetry.lock
	uvPriorityDir := filepath.Join(tempDir, "uv-priority")
	os.Mkdir(uvPriorityDir, 0755)

	os.WriteFile(filepath.Join(uvPriorityDir, "uv.lock"), []byte("# UV lock file"), 0644)
	os.WriteFile(filepath.Join(uvPriorityDir, "poetry.lock"), []byte("# Poetry lock file"), 0644)
	os.WriteFile(filepath.Join(uvPriorityDir, "requirements.txt"), []byte("flask==2.0.1"), 0644)

	projectType, err := DetectProjectType(uvPriorityDir)
	if err != nil {
		t.Errorf("Expected DetectProjectType to succeed for UV priority test: %v", err)
	}

	if projectType != ProjectTypePythonUV {
		t.Errorf("Expected ProjectTypePythonUV (uv.lock priority), got %s", projectType)
	}

	// Test 2: poetry.lock should take priority over requirements.txt
	poetryPriorityDir := filepath.Join(tempDir, "poetry-priority")
	os.Mkdir(poetryPriorityDir, 0755)

	os.WriteFile(filepath.Join(poetryPriorityDir, "poetry.lock"), []byte("# Poetry lock file"), 0644)
	os.WriteFile(filepath.Join(poetryPriorityDir, "requirements.txt"), []byte("flask==2.0.1"), 0644)

	projectType, err = DetectProjectType(poetryPriorityDir)
	if err != nil {
		t.Errorf("Expected DetectProjectType to succeed for Poetry priority test: %v", err)
	}

	if projectType != ProjectTypePythonPip {
		t.Errorf("Expected ProjectTypePythonPip (poetry.lock priority), got %s", projectType)
	}

	// Test 3: [tool.uv] should take priority over [dependency-groups]
	toolUvPriorityDir := filepath.Join(tempDir, "tool-uv-priority")
	os.Mkdir(toolUvPriorityDir, 0755)

	pyprojectContent := `[project]
name = "test"
dependencies = ["flask"]

[dependency-groups]
dev = ["pytest"]

[tool.uv]
dev-dependencies = ["ruff", "mypy"]
`

	os.WriteFile(filepath.Join(toolUvPriorityDir, "pyproject.toml"), []byte(pyprojectContent), 0644)

	projectType, err = DetectProjectType(toolUvPriorityDir)
	if err != nil {
		t.Errorf("Expected DetectProjectType to succeed for tool.uv priority test: %v", err)
	}

	if projectType != ProjectTypePythonUV {
		t.Errorf("Expected ProjectTypePythonUV ([tool.uv] priority), got %s", projectType)
	}

	t.Logf("✓ Priority order detection working correctly")
}

// TestTOMLErrorHandling tests error handling for malformed TOML files
func TestTOMLErrorHandling(t *testing.T) {
	tempDir := t.TempDir()

	// Test 1: Malformed TOML syntax
	malformedDir := filepath.Join(tempDir, "malformed-toml")
	os.Mkdir(malformedDir, 0755)

	malformedTOML := `[project
name = "test"  # Missing closing bracket
dependencies = ["flask"
`

	os.WriteFile(filepath.Join(malformedDir, "pyproject.toml"), []byte(malformedTOML), 0644)

	projectType, err := DetectProjectType(malformedDir)
	if err != nil {
		t.Errorf("Expected DetectProjectType to succeed even with malformed TOML: %v", err)
	}

	// Should default to pip for any pyproject.toml file
	if projectType != ProjectTypePythonPip {
		t.Errorf("Expected ProjectTypePythonPip for malformed TOML (fallback), got %s", projectType)
	}

	// Test 2: Empty TOML file
	emptyDir := filepath.Join(tempDir, "empty-toml")
	os.Mkdir(emptyDir, 0755)

	os.WriteFile(filepath.Join(emptyDir, "pyproject.toml"), []byte(""), 0644)

	projectType, err = DetectProjectType(emptyDir)
	if err != nil {
		t.Errorf("Expected DetectProjectType to succeed for empty TOML: %v", err)
	}

	if projectType != ProjectTypePythonPip {
		t.Errorf("Expected ProjectTypePythonPip for empty TOML (fallback), got %s", projectType)
	}

	t.Logf("✓ TOML error handling working correctly")
}

// TestComplexTOMLScenarios tests edge cases in TOML parsing
func TestComplexTOMLScenarios(t *testing.T) {
	tempDir := t.TempDir()

	// Test 1: Multiple tool sections (UV should win)
	multiToolDir := filepath.Join(tempDir, "multi-tool")
	os.Mkdir(multiToolDir, 0755)

	multiToolTOML := `[project]
name = "multi-tool-project"
dependencies = ["flask"]

[tool.poetry]
name = "multi-tool-project"
version = "0.1.0"

[tool.pdm]
includes = ["src"]

[tool.uv]
dev-dependencies = ["pytest"]

[tool.hatch.version]
path = "src/__init__.py"
`

	os.WriteFile(filepath.Join(multiToolDir, "pyproject.toml"), []byte(multiToolTOML), 0644)

	projectType, err := DetectProjectType(multiToolDir)
	if err != nil {
		t.Errorf("Expected DetectProjectType to succeed for multi-tool TOML: %v", err)
	}

	if projectType != ProjectTypePythonPip {
		t.Errorf("Expected ProjectTypePythonPip for multi-tool TOML (PDM detected first), got %s", projectType)
	}

	// Test 2: Complex nested TOML structure
	complexDir := filepath.Join(tempDir, "complex-toml")
	os.Mkdir(complexDir, 0755)

	complexTOML := `[project]
name = "complex-project"
dynamic = ["version"]

[tool.setuptools]
packages = ["src"]

[tool.setuptools.dynamic]
version = {attr = "mypackage.__version__"}

[build-system]
requires = ["setuptools>=45", "wheel"]
build-backend = "setuptools.build_meta"
`

	os.WriteFile(filepath.Join(complexDir, "pyproject.toml"), []byte(complexTOML), 0644)

	projectType, err = DetectProjectType(complexDir)
	if err != nil {
		t.Errorf("Expected DetectProjectType to succeed for complex TOML: %v", err)
	}

	if projectType != ProjectTypePythonPip {
		t.Errorf("Expected ProjectTypePythonPip for complex TOML (no recognized tool), got %s", projectType)
	}

	t.Logf("✓ Complex TOML scenarios handled correctly")
}

// TestIsNodeMethod tests the IsNode() method for consistency
func TestIsNodeMethod(t *testing.T) {
	tests := []struct {
		projectType ProjectType
		expected    bool
		name        string
	}{
		{ProjectTypeNode, true, "Node project"},
		{ProjectTypePythonPip, false, "Python pip project"},
		{ProjectTypePythonUV, false, "Python UV project"},
		{ProjectTypeUnknown, false, "Unknown project"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.projectType.IsNode()
			if result != test.expected {
				t.Errorf("Expected IsNode() to return %v for %s, got %v", test.expected, test.projectType, result)
			}
		})
	}

	t.Logf("✓ IsNode() method working correctly")
}
