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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func testDocsTree() *cli.Command {
	return &cli.Command{
		Name:  "lk",
		Flags: []cli.Flag{&cli.BoolFlag{Name: "quiet"}},
		Commands: []*cli.Command{
			{
				Name:      "generate-docs",
				Action:    generateDocs,
				Hidden:    true,
				Flags:     []cli.Flag{&cli.BoolFlag{Name: "list"}},
				ArgsUsage: "[command path...]",
			},
			{
				Name: "agent",
				Commands: []*cli.Command{
					{
						Name:      "deploy",
						Usage:     "Deploy the agent",
						ArgsUsage: "[working-dir]",
						Flags: []cli.Flag{
							&cli.StringFlag{Name: "deployment", Aliases: []string{"d"}, Usage: "`NAME` of the deployment", Required: true},
							&cli.DurationFlag{Name: "timeout", Value: 90 * time.Minute, Usage: "Give up after `DURATION`."},
							&cli.IntFlag{Name: "port", Value: 8080, Sources: cli.EnvVars("TEST_PORT"), Usage: "Local `PORT`"},
							&cli.BoolFlag{Name: "verbose", Usage: "Log more"},
							&cli.StringFlag{Name: "region", Hidden: true},
						},
					},
					{Name: "logs", Aliases: []string{"tail"}, Usage: "Stream logs"},
					{Name: "secret", Hidden: true},
				},
			},
		},
	}
}

func TestCommandDocs(t *testing.T) {
	root := testDocsTree()
	deploy := root.Command("agent").Command("deploy")

	want := "```shell\n" +
		"lk agent deploy [options] [working-dir]\n" +
		"```\n" +
		"\n" +
		"Options for `deploy`:\n" +
		"\n" +
		"- `--deployment NAME` (alias `-d`): NAME of the deployment. Required.\n" +
		"- `--timeout DURATION`: Give up after DURATION. Default: `1h30m`.\n" +
		"- `--port PORT`: Local PORT. Default: `8080`. Also read from `TEST_PORT`.\n" +
		"- `--verbose`: Log more.\n"
	assert.Equal(t, want, commandDocs(deploy, []string{"agent", "deploy"}))

	logs := root.Command("agent").Command("logs")
	assert.Equal(t, "```shell\nlk agent logs\n# or\nlk agent tail\n```\n", commandDocs(logs, []string{"agent", "logs"}))
}

func TestDocumentedPaths(t *testing.T) {
	root := testDocsTree()
	pruneHiddenCommands(root)
	// The root has (global) options but is never listed, so --list output has
	// no blank line for it.
	assert.Equal(t, []string{"agent deploy", "agent logs"}, documentedPaths(root, nil))
	assert.Equal(t, []string{"agent deploy"}, documentedPaths(root.Command("agent").Command("deploy"), []string{"agent", "deploy"}))
}

func TestGenerateDocsArgs(t *testing.T) {
	err := testDocsTree().Run(context.Background(), []string{"lk", "generate-docs"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "command path")

	err = testDocsTree().Run(context.Background(), []string{"lk", "generate-docs", "agent", "nope"})
	require.EqualError(t, err, "unknown command: lk agent nope")

	err = testDocsTree().Run(context.Background(), []string{"lk", "generate-docs", "agent", "secret"})
	require.EqualError(t, err, "unknown command: lk agent secret")

	require.NoError(t, testDocsTree().Run(context.Background(), []string{"lk", "generate-docs", "--list"}))
	require.NoError(t, testDocsTree().Run(context.Background(), []string{"lk", "generate-docs", "agent", "deploy"}))
}

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Minute:        "30m",
		2 * time.Minute:         "2m",
		2 * time.Hour:           "2h",
		90 * time.Minute:        "1h30m",
		90 * time.Second:        "1m30s",
		10 * time.Second:        "10s",
		500 * time.Millisecond:  "500ms",
		time.Hour + time.Second: "1h0m1s",
	} {
		assert.Equal(t, want, shortDuration(d), d.String())
	}
}

// Every command the real CLI lists must render without panicking, and with
// each marker the generator adds appearing once.
func TestCommandDocsRealTree(t *testing.T) {
	root := &cli.Command{Name: "lk", Flags: globalFlags}
	for _, group := range [][]*cli.Command{
		AppCommands, AgentCommands, AnalyticsCommands, CloudCommands, DocsCommands,
		SkillsCommands, ProjectCommands, WorkspaceCommands, UserCommands, ThemeCommands,
		RoomCommands, TokenCommands,
	} {
		root.Commands = append(root.Commands, group...)
	}

	paths := documentedPaths(root, nil)
	require.NotEmpty(t, paths)
	for _, p := range paths {
		require.NotEmpty(t, p)
		path := strings.Fields(p)
		target := root
		for _, name := range path {
			target = target.Command(name)
			require.NotNil(t, target, p)
		}
		docs := commandDocs(target, path)
		assert.NotContains(t, docs, "Required. Required.", p)
	}
}
