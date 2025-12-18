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
	"io/fs"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/pelletier/go-toml"
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

func (p ProjectType) DefaultEntrypoint() string {
	switch {
	case p.IsPython():
		return "agent.py"
	case p.IsNode():
		return "agent.js"
	default:
		return ""
	}
}

func DetectProjectType(dir fs.FS) (ProjectType, error) {
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
		data, err := fs.ReadFile(dir, "pyproject.toml")
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

	return ProjectTypeUnknown, errors.New("expected package.json, requirements.txt, pyproject.toml, or lock files")
}
