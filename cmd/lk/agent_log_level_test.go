// Copyright 2026 LiveKit, Inc.
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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
)

func findStringFlag(t *testing.T, cmd *cli.Command, name string) *cli.StringFlag {
	t.Helper()
	for _, flag := range cmd.Flags {
		if stringFlag, ok := flag.(*cli.StringFlag); ok && stringFlag.Name == name {
			return stringFlag
		}
	}
	t.Fatalf("flag %q not found on %q", name, cmd.Name)
	return nil
}

func runWithLogLevelFlag(
	t *testing.T,
	projectType agentfs.ProjectType,
	argv []string,
) []string {
	t.Helper()

	// urfave/cli flags retain parse state, so use a fresh copy of the production
	// flag for every test invocation.
	productionFlag := findStringFlag(t, startCommand, "log-level")
	flag := &cli.StringFlag{
		Name:    productionFlag.Name,
		Usage:   productionFlag.Usage,
		Sources: productionFlag.Sources,
	}

	var got []string
	app := &cli.Command{
		Name:  "lk",
		Flags: []cli.Flag{flag},
		Action: func(_ context.Context, cmd *cli.Command) error {
			got = buildCLIArgs(projectType, "start", cmd)
			return nil
		},
	}
	require.NoError(t, app.Run(context.Background(), argv))
	return got
}

func TestAgentCommandsExposeLogLevelEnvironmentVariable(t *testing.T) {
	for _, command := range []*cli.Command{startCommand, devCommand, consoleCommand} {
		t.Run(command.Name, func(t *testing.T) {
			flag := findStringFlag(t, command, "log-level")
			assert.Contains(t, flag.Sources.EnvKeys(), "LIVEKIT_LOG_LEVEL")
		})
	}
}

func TestAgentLogLevelEnvironmentVariableIsForwarded(t *testing.T) {
	t.Setenv("LIVEKIT_LOG_LEVEL", "info")

	assert.Equal(t,
		[]string{"start", "--log-level", "INFO"},
		runWithLogLevelFlag(t, agentfs.ProjectTypePythonUV, []string{"lk"}),
	)
	assert.Equal(t,
		[]string{"start", "--log-level", "info"},
		runWithLogLevelFlag(t, agentfs.ProjectTypeNode, []string{"lk"}),
	)
}

func TestAgentLogLevelFlagOverridesEnvironmentVariable(t *testing.T) {
	t.Setenv("LIVEKIT_LOG_LEVEL", "info")

	assert.Equal(t,
		[]string{"start", "--log-level", "ERROR"},
		runWithLogLevelFlag(
			t,
			agentfs.ProjectTypePythonUV,
			[]string{"lk", "--log-level", "error"},
		),
	)
}

func TestBuildConsoleArgsForwardsNormalizedLogLevel(t *testing.T) {
	assert.Equal(t,
		[]string{
			"console", "--connect-addr", "127.0.0.1:9876", "--record", "--log-level", "INFO",
		},
		buildConsoleArgs(agentfs.ProjectTypePythonUV, "127.0.0.1:9876", true, "info"),
	)
	assert.Equal(t,
		[]string{"console", "--connect-addr", "127.0.0.1:9876", "--log-level", "info"},
		buildConsoleArgs(agentfs.ProjectTypeNode, "127.0.0.1:9876", false, "INFO"),
	)
}

func TestBuildConsoleArgsOmitsUnsetLogLevel(t *testing.T) {
	assert.Equal(t,
		[]string{"console", "--connect-addr", "127.0.0.1:9876"},
		buildConsoleArgs(agentfs.ProjectTypePythonUV, "127.0.0.1:9876", false, ""),
	)
}
