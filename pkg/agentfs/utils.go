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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/livekit/livekit-cli/v2/pkg/util"
	"k8s.io/apimachinery/pkg/api/resource"
)

type ProjectType string

const (
	ProjectTypePythonPip    ProjectType = "python.pip"
	ProjectTypePythonUV     ProjectType = "python.uv"
	ProjectTypePythonPoetry ProjectType = "python.poetry"
	ProjectTypeNode         ProjectType = "node"
	ProjectTypeUnknown      ProjectType = "unknown"
)

func (p ProjectType) IsPython() bool {
	return p == ProjectTypePythonPip || p == ProjectTypePythonUV || p == ProjectTypePythonPoetry
}

func (p ProjectType) IsNode() bool {
	return p == ProjectTypeNode
}

func (p ProjectType) Lang() string {
	switch {
	case p.IsPython():
		return "Python"
	case p.IsNode():
		return "Node.js"
	default:
		return ""
	}
}

func (p ProjectType) FileExt() string {
	switch {
	case p.IsPython():
		return ".py"
	case p.IsNode():
		return ".js"
	default:
		return ""
	}
}

func LocateLockfile(dir string, p ProjectType) (bool, string) {
	// Define files to check based on project type
	// Prioritize actual lock files over dependency manifests
	var filesToCheck []string
	
	switch p {
	case ProjectTypePythonPip:
		filesToCheck = []string{
			"requirements.lock",    // Lock file (if exists)
			"pyproject.toml",       // Modern Python project file
			"requirements.txt",     // Legacy pip dependencies
		}
	case ProjectTypePythonUV:
		filesToCheck = []string{
			"uv.lock",              // UV lock file (highest priority)
			"pyproject.toml",       // UV uses pyproject.toml
			"requirements.txt",     // Fallback
		}
	case ProjectTypePythonPoetry:
		filesToCheck = []string{
			"poetry.lock",          // Poetry lock file (highest priority)
			"pyproject.toml",       // Poetry configuration
		}
	case ProjectTypeNode:
		filesToCheck = []string{
			"package-lock.json",    // npm lock file
			"yarn.lock",            // Yarn lock file
			"pnpm-lock.yaml",       // pnpm lock file
			"package.json",         // Package manifest (lowest priority)
		}
	default:
		return false, ""
	}

	// Check files in priority order
	for _, filename := range filesToCheck {
		if _, err := os.Stat(filepath.Join(dir, filename)); err == nil {
			return true, filename
		}
	}
	
	return false, ""
}

// DetectProjectType determines the project type by checking for specific configuration/lock files and their content
func DetectProjectType(dir string) (ProjectType, error) {
	// Node.js detection
	if util.FileExists(dir, "package.json") || util.FileExists(dir, "yarn.lock") || util.FileExists(dir, "package-lock.json") {
		return ProjectTypeNode, nil
	}

	// Python detection with priority order for most reliable indicators
	// 1. Check for uv.lock first (most definitive UV indicator)
	if util.FileExists(dir, "uv.lock") {
		return ProjectTypePythonUV, nil
	}

	// 2. Check for Poetry lock file first (most definitive Poetry indicator)
	if util.FileExists(dir, "poetry.lock") {
		return ProjectTypePythonPoetry, nil
	}
	
	// 3. Check for other lock files (Pipenv, PDM) - treat as pip-compatible for now
	if util.FileExists(dir, "Pipfile.lock") || util.FileExists(dir, "pdm.lock") {
		return ProjectTypePythonPip, nil
	}

	// 4. Check for requirements.txt (classic pip setup)
	if util.FileExists(dir, "requirements.txt") {
		return ProjectTypePythonPip, nil
	}

	// 5. Check pyproject.toml for specific tool configurations
	if util.FileExists(dir, "pyproject.toml") {
		tomlPath := filepath.Join(dir, "pyproject.toml")
		data, err := os.ReadFile(tomlPath)
		if err == nil {
			var doc map[string]any
			if err := toml.Unmarshal(data, &doc); err == nil {
				if tool, ok := doc["tool"].(map[string]any); ok {
					// Check for specific tool configurations
					if _, hasPoetry := tool["poetry"]; hasPoetry {
						return ProjectTypePythonPoetry, nil
					}
					if _, hasPdm := tool["pdm"]; hasPdm {
						return ProjectTypePythonPip, nil
					}
					if _, hasHatch := tool["hatch"]; hasHatch {
						return ProjectTypePythonPip, nil
					}
					if _, hasUv := tool["uv"]; hasUv {
						return ProjectTypePythonUV, nil
					}
				}

				// Try to detect UV projects by content
				if isUVByContent(string(data)) {
					return ProjectTypePythonUV, nil
				}
			}
		}
		// Default to pip if pyproject.toml is present but not informative
		return ProjectTypePythonPip, nil
	}

	return ProjectTypeUnknown, errors.New("project type could not be identified; expected package.json, requirements.txt, pyproject.toml, or lock files")
}

// isUVByContent performs UV detection through pyproject.toml content analysis
// This function specifically identifies UV-based Python projects without misclassifying
// setuptools, poetry, and other pyproject.toml-based projects as UV projects.
func isUVByContent(content string) bool {
	// Look for UV-specific patterns in pyproject.toml:
	// - [dependency-groups]: UV's dependency group syntax (not used by setuptools/poetry)
	// - "uv sync": UV command references in scripts or documentation
	// - [tool.uv]: UV-specific tool configuration section
	if strings.Contains(content, "[dependency-groups]") ||
		strings.Contains(content, "uv sync") ||
		strings.Contains(content, "[tool.uv]") {
		return true
	}
	return false
}

func ParseCpu(cpu string) (string, error) {
	cpuStr := strings.TrimSpace(cpu)
	cpuQuantity, err := resource.ParseQuantity(cpuStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse CPU quantity: %v", err)
	}

	// Convert to millicores
	cpuCores := fmt.Sprintf("%dm", cpuQuantity.MilliValue())

	return cpuCores, nil
}

func ParseMem(mem string, suffix bool) (string, error) {
	memStr := strings.TrimSpace(mem)
	memQuantity, err := resource.ParseQuantity(memStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse memory quantity: %v", err)
	}

	// Convert to GB
	memGB := float64(memQuantity.Value()) / (1024 * 1024 * 1024)
	if suffix {
		return fmt.Sprintf("%.2gGB", memGB), nil
	}
	return fmt.Sprintf("%.2g", memGB), nil
}

func validateSettingsMap(settingsMap map[string]string, keys []string) error {
	for _, key := range keys {
		if _, ok := settingsMap[key]; !ok {
			return fmt.Errorf("client setting %s is required, please try again later", key)
		}
	}
	return nil
}
