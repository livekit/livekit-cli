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

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
)

func detectProject(cmd *cli.Command) (string, agentfs.ProjectType, string, error) {
	explicit := cmd.Args().First()

	detectFrom := "."
	if explicit != "" {
		absPath, err := filepath.Abs(explicit)
		if err != nil {
			return "", "", "", err
		}
		if _, err := os.Stat(absPath); err != nil {
			return "", "", "", fmt.Errorf("entrypoint file not found: %s", explicit)
		}
		detectFrom = filepath.Dir(absPath)
	}

	projectDir, projectType, err := agentfs.DetectProjectRoot(detectFrom)
	if err != nil {
		return "", "", "", noAgentError()
	}

	// TODO(node): support JS/Node agents here. DetectProjectRoot already
	// recognizes Node projects; once the session daemon can spawn a Node
	// agent in console mode, drop this gate and branch on projectType.
	if !projectType.IsPython() {
		return "", "", "", fmt.Errorf("currently only supports Python agents (detected: %s)", projectType)
	}

	if explicit != "" {
		absPath, _ := filepath.Abs(explicit)
		rel, err := filepath.Rel(projectDir, absPath)
		if err != nil {
			return "", "", "", fmt.Errorf("entrypoint %s is outside project root %s", explicit, projectDir)
		}
		return projectDir, projectType, rel, nil
	}

	entrypoint, err := findEntrypoint(projectDir, "", projectType)
	if err != nil {
		return "", "", "", err
	}
	return projectDir, projectType, entrypoint, nil
}

// buildConsoleArgs builds the agent subprocess argv for console mode, shared by
// `lk agent console` and the `lk session` daemon.
func buildConsoleArgs(addr string, record bool) []string {
	args := []string{"console", "--connect-addr", addr}
	if record {
		args = append(args, "--record")
	}
	return args
}
