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
	"strings"

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

	if !projectType.IsPython() && !projectType.IsNode() {
		return "", "", "", fmt.Errorf("only Python and Node agents are supported (detected: %s)", projectType)
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

// consoleCrashSignals are output markers meaning the console job died even
// though the worker process may stay alive: the Python SDK keeps the worker
// running after the job task crashes (logging `"reason": "job crashed"`), and
// agents-js logs FATAL `console mode failed:` before exiting. Without these,
// a pre-connect crash leaves the user waiting out the full connect timeout.
var consoleCrashSignals = []string{
	`"job crashed"`,
	"console mode failed:",
}

// buildConsoleArgs builds the agent subprocess argv for console mode, shared by
// `lk agent console` and the `lk agent session` daemon.
func buildConsoleArgs(addr string, record bool) []string {
	args := []string{"console", "--connect-addr", addr}
	if record {
		args = append(args, "--record")
	}
	return args
}

// normalizeLogLevel adapts the log level to the agent runtime's convention:
// agents-js accepts only lowercase levels, Python expects uppercase.
func normalizeLogLevel(projectType agentfs.ProjectType, level string) string {
	if projectType.IsNode() {
		return strings.ToLower(level)
	}
	return strings.ToUpper(level)
}
