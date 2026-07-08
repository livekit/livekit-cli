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
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// cloudConsoleURL returns the LiveKit Cloud agents-console URL for a worker that
// registered against wsURL with the given agent name, or "" when wsURL is not a
// LiveKit Cloud project (e.g. a self-hosted or localhost server), in which case
// no console link is shown.
func cloudConsoleURL(wsURL, agentName string) string {
	consoleHost, sub := cloudProject(wsURL)
	if consoleHost == "" {
		return ""
	}
	return fmt.Sprintf(
		"https://%s/projects/d_%s/agents/console?agentName=%s&autoStart=false",
		consoleHost, sub, url.QueryEscape(agentName),
	)
}

// cloudProject maps a LiveKit Cloud project URL to its console host and project
// subdomain, or ("", "") if the host is not a recognized cloud project (self-
// hosted, localhost, or any other domain). Both production and staging are
// supported:
//
//	wss://my-proj.livekit.cloud         -> cloud.livekit.io,         my-proj
//	wss://my-proj.staging.livekit.cloud -> cloud.staging.livekit.io, my-proj
func cloudProject(wsURL string) (consoleHost, subdomain string) {
	u, err := url.Parse(wsURL)
	if err != nil {
		return "", ""
	}
	sub := util.ExtractSubdomain(wsURL)
	if sub == "" {
		return "", ""
	}
	host := u.Hostname()
	switch {
	case strings.EqualFold(host, sub+".staging.livekit.cloud"):
		return "cloud.staging.livekit.io", sub
	case strings.EqualFold(host, sub+".livekit.cloud"):
		return "cloud.livekit.io", sub
	}
	return "", ""
}

func init() {
	AgentCommands[0].Commands = append(AgentCommands[0].Commands, startCommand, devCommand)
}

var agentRunFlags = []cli.Flag{
	&cli.StringFlag{
		Name:  "log-level",
		Usage: "Log level (TRACE, DEBUG, INFO, WARN, ERROR)",
	},
}

var startCommand = &cli.Command{
	Name:      "start",
	Usage:     "Run an agent in production mode",
	ArgsUsage: "[entrypoint] [-- node/python-args...]",
	Flags:     agentRunFlags,
	Action:    runAgentStart,
}

var devCommand = &cli.Command{
	Name:      "dev",
	Usage:     "Run an agent in development mode with auto-reload",
	ArgsUsage: "[entrypoint] [-- node/python-args...]",
	Flags: append(agentRunFlags, &cli.BoolFlag{
		Name:  "no-reload",
		Usage: "Disable auto-reload on file changes",
	}),
	Action: runAgentDev,
}

// agentCredentials holds the connection details forwarded to the agent subprocess.
type agentCredentials struct {
	url       string
	apiKey    string
	apiSecret string
}

// complete reports whether every field is populated, meaning no project lookup
// is needed to fill in the gaps.
func (c agentCredentials) complete() bool {
	return c.url != "" && c.apiKey != "" && c.apiSecret != ""
}

// args renders the credentials as agent subprocess CLI flags, skipping any empty
// field so the agent can fall back to its own defaults / environment.
func (c agentCredentials) args() []string {
	var args []string
	if c.url != "" {
		args = append(args, "--url", c.url)
	}
	if c.apiKey != "" {
		args = append(args, "--api-key", c.apiKey)
	}
	if c.apiSecret != "" {
		args = append(args, "--api-secret", c.apiSecret)
	}
	return args
}

// explicitCredentials returns only the credentials the user explicitly provided,
// via command-line flags or the LIVEKIT_* environment variables.
//
// The global --url flag carries a localhost default (so commands targeting a
// local server work out of the box). That default must NOT count as an explicit
// value here: if it did, it would mask the URL of the configured/default project
// and the agent would dial localhost with the project's cloud credentials. We use
// IsSet, which is true only when the flag was set from the command line or from
// LIVEKIT_URL — not when it falls back to its static default.
func explicitCredentials(cmd *cli.Command) agentCredentials {
	var c agentCredentials
	if cmd.IsSet("url") {
		c.url = cmd.String("url")
	}
	if cmd.IsSet("api-key") {
		c.apiKey = cmd.String("api-key")
	}
	if cmd.IsSet("api-secret") {
		c.apiSecret = cmd.String("api-secret")
	}
	return c
}

// mergeCredentials fills empty explicit fields from the project config. Explicit
// values (an intentional override) always win; the project supplies whatever the
// user did not provide — including the URL, so the configured project overrides
// the --url localhost default.
func mergeCredentials(explicit, project agentCredentials) agentCredentials {
	merged := explicit
	if merged.url == "" {
		merged.url = project.url
	}
	if merged.apiKey == "" {
		merged.apiKey = project.apiKey
	}
	if merged.apiSecret == "" {
		merged.apiSecret = project.apiSecret
	}
	return merged
}

// resolveCredentials returns CLI args (--url, --api-key, --api-secret) for the agent subprocess.
func resolveCredentials(cmd *cli.Command, loadOpts ...loadOption) ([]string, error) {
	explicit := explicitCredentials(cmd)
	merged := explicit

	// Only consult the project config when the user didn't fully specify the
	// connection on the command line / environment.
	if !explicit.complete() {
		opts := append([]loadOption{ignoreURL}, loadOpts...)
		pc, err := loadProjectDetails(cmd, opts...)
		if err != nil {
			return nil, err
		}
		if pc != nil {
			merged = mergeCredentials(explicit, agentCredentials{
				url:       pc.URL,
				apiKey:    pc.APIKey,
				apiSecret: pc.APISecret,
			})
		}
	}

	return merged.args(), nil
}

func buildCLIArgs(projectType agentfs.ProjectType, subcmd string, cmd *cli.Command, loadOpts ...loadOption) ([]string, error) {
	args := []string{subcmd}
	if logLevel := cmd.String("log-level"); logLevel != "" {
		args = append(args, "--log-level", normalizeLogLevel(projectType, logLevel))
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
	out.Statusf("Detected %s agent (%s in %s)", projectType.Lang(), entrypoint, projectDir)

	cliArgs, err := buildCLIArgs(projectType, "start", cmd)
	if err != nil {
		return err
	}

	agent, err := startAgent(AgentStartConfig{
		Dir:           projectDir,
		Entrypoint:    entrypoint,
		ProjectType:   projectType,
		RuntimeArgs:   forwardedArgs(cmd),
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

	// Forward every signal to the agent — the agent decides:
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

	// Python has no dedicated dev subcommand: dev mode is `start --dev`.
	// agents-js has a `dev` subcommand that already defaults to debug logs.
	subcmd := "dev"
	if projectType.IsPython() {
		subcmd = "start"
	}
	cliArgs, err := buildCLIArgs(projectType, subcmd, cmd)
	if err != nil {
		return err
	}
	if projectType.IsPython() {
		cliArgs = append(cliArgs, "--dev")
		if cmd.String("log-level") == "" {
			cliArgs = append(cliArgs, "--log-level", "DEBUG")
		}
	}

	cfg := AgentStartConfig{
		Dir:           projectDir,
		Entrypoint:    entrypoint,
		ProjectType:   projectType,
		RuntimeArgs:   forwardedArgs(cmd),
		CLIArgs:       cliArgs,
		ForwardOutput: os.Stdout,
	}

	// When the agent reports its ServerInfo over the dev channel, print a console
	// link (LiveKit Cloud projects only) the user can open to drive/debug the
	// agent in the browser. Printed once, even across hot reloads (link stays valid).
	var consoleLinkOnce sync.Once
	cfg.OnServerInfo = func(agentName, wsURL string) {
		if link := cloudConsoleURL(wsURL, agentName); link != "" {
			consoleLinkOnce.Do(func() {
				// Delay briefly so the link prints after the agent's own startup
				// logs rather than getting buried in them.
				time.AfterFunc(time.Second, func() {
					// Accent-colored, and a clickable OSC 8 hyperlink on terminals
					// that support it (gated so the escape never leaks into pipes).
					label := util.Accented(link)
					if out.Interactive() {
						label = util.Hyperlink(link, label)
					}
					out.Status("")
					out.Statusf("Agent console: %s", label)
				})
			})
		}
	}

	out.Statusf("Detected %s agent (%s in %s)", projectType.Lang(), entrypoint, projectDir)

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
