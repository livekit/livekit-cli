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

func (p ProjectType) Lang() string {
	switch p {
	case ProjectTypePythonPip, ProjectTypePythonUV:
		return "Python"
	case ProjectTypeNode:
		return "Node.js"
	default:
		return ""
	}
}

func (p ProjectType) FileExt() string {
	switch p {
	case ProjectTypePythonPip, ProjectTypePythonUV:
		return ".py"
	case ProjectTypeNode:
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

func isPythonPip(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "requirements.txt"))
	return err == nil
}

func isPythonUV(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "pyproject.toml"))
	return err == nil
}

func isNode(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "package.json"))
	return err == nil
}

func DetectProjectType(dir string) (ProjectType, error) {
	if isNode(dir) {
		return ProjectTypeNode, nil
	}
	if isPythonPip(dir) {
		return ProjectTypePythonPip, nil
	}
	if isPythonUV(dir) {
		return ProjectTypePythonUV, nil
	}
	return ProjectTypeUnknown, errors.New("project type could not me identified, expect requirements.txt, pyproject.toml, or package.json")
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
