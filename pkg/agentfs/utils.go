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
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

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
