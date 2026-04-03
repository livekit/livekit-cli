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
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
)

func init() {
	AgentCommands[0].Commands = append(AgentCommands[0].Commands, startCommand, devCommand)
}

var agentRunFlags = []cli.Flag{
	&cli.StringFlag{
		Name:  "entrypoint",
		Usage: "Agent entrypoint `FILE` (default: auto-detect)",
	},
	&cli.StringFlag{
		Name:    "url",
		Usage:   "LiveKit server `URL`",
		Sources: cli.EnvVars("LIVEKIT_URL"),
	},
	&cli.StringFlag{
		Name:    "api-key",
		Usage:   "LiveKit API `KEY`",
		Sources: cli.EnvVars("LIVEKIT_API_KEY"),
	},
	&cli.StringFlag{
		Name:    "api-secret",
		Usage:   "LiveKit API `SECRET`",
		Sources: cli.EnvVars("LIVEKIT_API_SECRET"),
	},
	&cli.StringFlag{
		Name:  "log-level",
		Usage: "Log level (TRACE, DEBUG, INFO, WARN, ERROR)",
	},
}

var startCommand = &cli.Command{
	Name:   "start",
	Usage:  "Run an agent in production mode",
	Flags:  agentRunFlags,
	Action: runAgentStart,
}

var devCommand = &cli.Command{
	Name:  "dev",
	Usage: "Run an agent in development mode with auto-reload",
	Flags: append(agentRunFlags, &cli.BoolFlag{
		Name:  "no-reload",
		Usage: "Disable auto-reload on file changes",
	}),
	Action: runAgentDev,
}

// resolveCredentials returns CLI args (--url, --api-key, --api-secret) for the agent subprocess.
func resolveCredentials(cmd *cli.Command, loadOpts ...loadOption) ([]string, error) {
	url := cmd.String("url")
	apiKey := cmd.String("api-key")
	apiSecret := cmd.String("api-secret")

	// Try project config if any are missing
	if url == "" || apiKey == "" || apiSecret == "" {
		opts := append([]loadOption{ignoreURL}, loadOpts...)
		pc, err := loadProjectDetails(cmd, opts...)
		if err != nil {
			return nil, err
		}
		if pc != nil {
			if url == "" {
				url = pc.URL
			}
			if apiKey == "" {
				apiKey = pc.APIKey
			}
			if apiSecret == "" {
				apiSecret = pc.APISecret
			}
		}
	}

	var args []string
	if url != "" {
		args = append(args, "--url", url)
	}
	if apiKey != "" {
		args = append(args, "--api-key", apiKey)
	}
	if apiSecret != "" {
		args = append(args, "--api-secret", apiSecret)
	}
	return args, nil
}

func noAgentError() error {
	return fmt.Errorf("no agent project detected in the current directory\n\n" +
		"Make sure you are running this command from an agent project directory\n" +
		"containing one of: pyproject.toml, requirements.txt, uv.lock, package.json, or lock files.\n\n" +
		"To get started, see: https://docs.livekit.io/agents/quickstart")
}

func detectProject(cmd *cli.Command) (string, agentfs.ProjectType, string, error) {
	explicit := cmd.String("entrypoint")

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

func buildCLIArgs(subcmd string, cmd *cli.Command, loadOpts ...loadOption) ([]string, error) {
	args := []string{subcmd}
	if logLevel := cmd.String("log-level"); logLevel != "" {
		args = append(args, "--log-level", logLevel)
	}
	creds, err := resolveCredentials(cmd, loadOpts...)
	if err != nil {
		return nil, err
	}
	args = append(args, creds...)
	return args, nil
}

func runAgentStart(ctx context.Context, cmd *cli.Command) error {
	projectDir, projectType, entrypoint, err := detectProject(cmd)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Detected %s agent (%s in %s)\n", projectType.Lang(), entrypoint, projectDir)

	cliArgs, err := buildCLIArgs("start", cmd, quietOutput)
	if err != nil {
		return err
	}

	agent, err := startAgent(AgentStartConfig{
		Dir:           projectDir,
		Entrypoint:    entrypoint,
		ProjectType:   projectType,
		CLIArgs:       cliArgs,
		ForwardOutput: os.Stdout,
	})
	if err != nil {
		return err
	}

	// Take over signal handling from the global NotifyContext.
	signal.Reset(syscall.SIGINT, syscall.SIGTERM)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Forward every signal to the agent — Python decides
	// first = graceful shutdown, second = force exit.
	go func() {
		for range sigCh {
			agent.Shutdown()
		}
	}()

	// Wait for agent to exit
	<-agent.exitCh
	signal.Stop(sigCh)
	return nil
}

func runAgentDev(ctx context.Context, cmd *cli.Command) error {
	projectDir, projectType, entrypoint, err := detectProject(cmd)
	if err != nil {
		return err
	}

	cliArgs, err := buildCLIArgs("start", cmd, outputToStderr)
	if err != nil {
		return err
	}
	if cmd.String("log-level") == "" {
		cliArgs = append(cliArgs, "--log-level", "DEBUG")
	}

	cfg := AgentStartConfig{
		Dir:           projectDir,
		Entrypoint:    entrypoint,
		ProjectType:   projectType,
		CLIArgs:       cliArgs,
		ForwardOutput: os.Stdout,
	}

	fmt.Fprintf(os.Stderr, "Detected %s agent (%s in %s)\n", projectType.Lang(), entrypoint, projectDir)

	// Take over signal handling from the global NotifyContext.
	signal.Reset(syscall.SIGINT, syscall.SIGTERM)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	if cmd.Bool("no-reload") {
		// No reload — just run like start
		agent, err := startAgent(cfg)
		if err != nil {
			return err
		}

		go func() {
			for range sigCh {
				agent.Shutdown()
			}
		}()

		<-agent.exitCh
		signal.Stop(sigCh)
		return nil
	}

	// Dev mode with file watching
	watcher, err := newAgentWatcher(cfg)
	if err != nil {
		return err
	}

	done := make(chan struct{})
	doneOnce := sync.Once{}

	// Forward signals to the current agent, and stop the watcher on first signal.
	go func() {
		for range sigCh {
			doneOnce.Do(func() { close(done) })
			if watcher.agent != nil {
				watcher.agent.Shutdown()
			}
		}
	}()

	err = watcher.Run(done)
	signal.Stop(sigCh)
	return err
}
