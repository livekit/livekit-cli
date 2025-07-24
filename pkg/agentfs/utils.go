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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

func isPython(dir string) (bool, string) {
	pythonFiles := []string{
		"requirements.txt",
		"requirements.lock",
		"pyproject.toml",
	}

	for _, filename := range pythonFiles {
		if _, err := os.Stat(filepath.Join(dir, filename)); err == nil {
			return true, filename
		}
	}
	return false, ""
}

func isNode(dir string) (bool, string) {
	nodeFiles := []string{
		"package.json",
		"package-lock.json",
		"yarn.lock",
		"pnpm-lock.yaml",
	}

	for _, filename := range nodeFiles {
		if _, err := os.Stat(filepath.Join(dir, filename)); err == nil {
			return true, filename
		}
	}
	return false, ""
}

func getDependencyFile(dir string) (string, error) {
	if isPython, dependencyFile := isPython(dir); isPython {
		return filepath.Join(dir, dependencyFile), nil
	} else if isNode, dependencyFile := isNode(dir); isNode {
		return filepath.Join(dir, dependencyFile), nil
	}
	return "", fmt.Errorf("no dependency file found")
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
