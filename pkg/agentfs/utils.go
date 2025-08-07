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
	ProjectTypePythonPip ProjectType = "python.pip"
	ProjectTypePythonUV  ProjectType = "python.uv"
	ProjectTypeNode      ProjectType = "node"
	ProjectTypeUnknown   ProjectType = "unknown"
)

func (p ProjectType) IsPython() bool {
	return p == ProjectTypePythonPip || p == ProjectTypePythonUV
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
	pythonFiles := []string{
		"requirements.txt",
		"requirements.lock",
		"pyproject.toml",
	}

	nodeFiles := []string{
		"package.json",
		"package-lock.json",
		"yarn.lock",
		"pnpm-lock.yaml",
	}

	switch p {
	case ProjectTypePythonPip:
	case ProjectTypePythonUV:
		for _, filename := range pythonFiles {
			if _, err := os.Stat(filepath.Join(dir, filename)); err == nil {
				return true, filename
			}
		}
	case ProjectTypeNode:
		for _, filename := range nodeFiles {
			if _, err := os.Stat(filepath.Join(dir, filename)); err == nil {
				return true, filename
			}
		}
	default:
		return false, ""
	}
	return false, ""
}

func DetectProjectType(dir string) (ProjectType, error) {
	// Node.js detection
	if util.FileExists(dir, "package.json") {
		return ProjectTypeNode, nil
	}

	// Python detection
	if util.FileExists(dir, "uv.lock") {
		return ProjectTypePythonUV, nil
	}
	if util.FileExists(dir, "poetry.lock") || util.FileExists(dir, "Pipfile.lock") {
		return ProjectTypePythonPip, nil // We can treat as pip-compatible
	}
	if util.FileExists(dir, "requirements.txt") {
		return ProjectTypePythonPip, nil
	}
	if util.FileExists(dir, "pyproject.toml") {
		tomlPath := filepath.Join(dir, "pyproject.toml")
		data, err := os.ReadFile(tomlPath)
		if err == nil {
			var doc map[string]any
			if err := toml.Unmarshal(data, &doc); err == nil {
				if tool, ok := doc["tool"].(map[string]any); ok {
					if _, hasPoetry := tool["poetry"]; hasPoetry {
						return ProjectTypePythonPip, nil
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
			}
		}
		// Default to pip if pyproject.toml is present but not informative
		return ProjectTypePythonPip, nil
	}

	return ProjectTypeUnknown, errors.New("project type could not be identified; expected package.json, requirements.txt, pyproject.toml, or lock files")
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
