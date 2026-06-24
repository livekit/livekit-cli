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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/twitchtv/twirp"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/bootstrap"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/util"
	lkproto "github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	"github.com/livekit/server-sdk-go/v2/pkg/cloudagents"
)

const (
	maxSecretFileSize = 1024 * 1024 // 1MB
	buildTimeout      = 15 * time.Minute
)

var (
	idFlag = func(required bool) *cli.StringFlag {
		return &cli.StringFlag{
			Name:     "id",
			Usage:    fmt.Sprintf("`ID` of the agent. If unset, and the %s file is present, will use the id found there.", config.LiveKitTOMLFile),
			Required: required,
		}
	}

	idSliceFlag = &cli.StringSliceFlag{
		Name:     "id",
		Usage:    "`IDs` of agent(s)",
		Required: false,
	}

	attributesFlag = &cli.StringFlag{
		Name:     "attributes",
		Usage:    "`JSON` literal or file path containing an object of string key-value pairs. Use \"-\" to read from stdin.",
		Required: false,
	}

	attributeFlag = &cli.StringSliceFlag{
		Name:     "attribute",
		Usage:    "`KEY=VALUE` attribute pair, may be repeated. Merged with --attributes, taking precedence on conflicting keys.",
		Required: false,
	}

	secretsFileFlag = &cli.StringFlag{
		Name:      "secrets-file",
		Usage:     "`FILE` containing secret KEY=VALUE pairs, one per line. These will be injected as environment variables into the agent.",
		TakesFile: true,
		Required:  false,
	}

	secretsFlag = &cli.StringSliceFlag{
		Name:     "secrets",
		Usage:    "KEY=VALUE comma separated secrets. These will be injected as environment variables into the agent. These take precedence over secrets-file.",
		Required: false,
	}

	secretsMountFlag = &cli.StringSliceFlag{
		Name:     "secret-mount",
		Usage:    "Local path to a secret file to be mounted on agent environment",
		Required: false,
	}

	ignoreEmptySecretsFlag = &cli.BoolFlag{
		Name:     "ignore-empty-secrets",
		Usage:    "If set, will skip environment variables with empty values from secrets files instead of failing",
		Required: false,
		Value:    false,
	}

	logTypeFlag = &cli.StringFlag{
		Name:     "log-type",
		Usage:    "Type of logs to retrieve. Valid values are 'deploy' and 'build'",
		Value:    "deploy",
		Required: false,
	}

	regionFlag = &cli.StringFlag{
		Name:     "region",
		Usage:    "Region to deploy the agent to. If unset, will deploy to the nearest region.",
		Required: false,
	}

	deploymentFlag = &cli.StringFlag{
		Name:     "deployment",
		Usage:    "Agent deployment",
		Required: false,
		Aliases:  []string{"d"},
	}

	agentPrebuiltImageFlag = &cli.StringFlag{
		Name:  "image",
		Usage: "Pre-built image from the local Docker daemon (e.g. myimage:latest). Requires Docker.",
	}

	agentPrebuiltImageTarFlag = &cli.StringFlag{
		Name:  "image-tar",
		Usage: "Pre-built image from an OCI tar file (e.g. ./image.tar). No Docker daemon required.",
	}

	skipSDKCheckFlag = &cli.BoolFlag{
		Name:     "skip-sdk-check",
		Required: false,
		Hidden:   true,
	}

	AgentCommands = []*cli.Command{
		{
			Name:    "agent",
			Aliases: []string{"a"},
			Usage:   "Manage LiveKit Cloud Agents",
			Commands: []*cli.Command{
				{
					Name:  "init",
					Usage: "Initialize a new LiveKit Cloud agent project",
					Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
						return createAgentClientWithOpts(ctx, cmd, confirmProject)
					},
					Action: initAgent,
					MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{{
						Flags: [][]cli.Flag{{
							&cli.StringFlag{
								Name:  "lang",
								Usage: "`LANGUAGE` of the project, one of \"node\", \"python\"",
								Action: func(ctx context.Context, cmd *cli.Command, l string) error {
									if l == "" {
										return nil
									}
									if !slices.Contains([]string{"node", "python"}, l) {
										return fmt.Errorf("unsupported language: %s", l)
									}
									return nil
								},
								Hidden: true,
							},
							&cli.BoolFlag{
								Name:  "deploy",
								Usage: "If set, automatically deploys the agent to LiveKit Cloud after initialization.",
								Value: false,
							},
							templateFlag,
							templateURLFlag,
						}, {
							sandboxFlag,
							&cli.BoolFlag{
								Name:   "no-sandbox",
								Usage:  "If set, will not create a sandbox for the project. ",
								Value:  true,
								Hidden: true,
							},
						}},
					}},
					Flags: []cli.Flag{
						regionFlag,
						installFlag,
					},
					ArgsUsage:                 "[AGENT-NAME]",
					DisableSliceFlagSeparator: true,
				},
				{
					Name:   "create",
					Usage:  "Create a new LiveKit Cloud Agent",
					Action: createAgent,
					Before: createAgentClient,
					Flags: []cli.Flag{
						secretsFlag,
						secretsFileFlag,
						secretsMountFlag,
						ignoreEmptySecretsFlag,
						regionFlag,
						deploymentFlag,
						skipSDKCheckFlag,
						agentPrebuiltImageFlag,
						agentPrebuiltImageTarFlag,
					},
					// NOTE: since secrets may contain commas, or indeed any special character we might want to treat as a flag separator,
					// we disable it entirely here and require multiple --secrets flags to be used.
					DisableSliceFlagSeparator: true,
					ArgsUsage:                 "[working-dir]",
				},
				{
					Name:   "dockerfile",
					Usage:  "Generate Dockerfile and .dockerignore for your project",
					Before: createAgentClient,
					Action: generateAgentDockerfile,
					Flags: []cli.Flag{
						&cli.BoolFlag{
							Name:     "overwrite",
							Usage:    "Overwrite existing Dockerfile and/or .dockerignore if they exist",
							Required: false,
							Value:    false,
						},
					},
					ArgsUsage: "[working-dir]",
				},
				{
					Name:   "config",
					Usage:  fmt.Sprintf("Creates a %s in the working directory for an existing agent.", config.LiveKitTOMLFile),
					Before: createAgentClient,
					Action: createAgentConfig,
					Flags: []cli.Flag{
						idFlag(false),
					},
					ArgsUsage: "[working-dir]",
				},
				{
					Name:   "deploy",
					Usage:  "Deploy a new version of the agent",
					Before: createAgentClient,
					Action: deployAgent,
					Flags: []cli.Flag{
						attributesFlag,
						attributeFlag,
						secretsFlag,
						secretsFileFlag,
						secretsMountFlag,
						regionFlag,
						deploymentFlag,
						ignoreEmptySecretsFlag,
						skipSDKCheckFlag,
						agentPrebuiltImageFlag,
						agentPrebuiltImageTarFlag,
					},
					// NOTE: since secrets may contain commas, or indeed any special character we might want to treat as a flag separator,
					// we disable it entirely here and require multiple --secrets flags to be used.
					DisableSliceFlagSeparator: true,
					ArgsUsage:                 "[working-dir]",
				},
				{
					Name:   "promote",
					Usage:  "Promote an agent to a new deployment",
					Before: createAgentClient,
					Action: promoteAgent,
					Flags: []cli.Flag{
						idFlag(false),
						deploymentFlag,
					},
					ArgsUsage: "[working-dir]",
				},
				{
					Name:   "status",
					Usage:  "Get the status of an agent",
					Before: createAgentClient,
					Action: getAgentStatus,
					Flags: []cli.Flag{
						idFlag(false),
						jsonFlag,
					},
					ArgsUsage: "[working-dir]",
				},
				{
					Name:   "update",
					Usage:  "Update an agent metadata and secrets. This will restart the agent.",
					Before: createAgentClient,
					Action: updateAgent,
					Flags: []cli.Flag{
						secretsFlag,
						secretsFileFlag,
						secretsMountFlag,
						ignoreEmptySecretsFlag,
					},
					// NOTE: since secrets may contain commas, or indeed any special character we might want to treat as a flag separator,
					// we disable it entirely here and require multiple --secrets flags to be used.
					DisableSliceFlagSeparator: true,
					ArgsUsage:                 "[working-dir]",
				},
				{
					Name:   "restart",
					Usage:  "Restart an agent",
					Before: createAgentClient,
					Action: restartAgent,
					Flags: []cli.Flag{
						idFlag(false),
					},
					ArgsUsage: "[working-dir]",
				},
				{
					Name:   "rollback",
					Usage:  "Rollback an agent to a previous version",
					Before: createAgentClient,
					Action: rollbackAgent,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:     "version",
							Usage:    "Version to rollback to, defaults to most recent previous to current.",
							Value:    "latest",
							Required: true,
						},
						idFlag(false),
					},
					ArgsUsage: "[working-dir]",
				},
				{
					Name:    "logs",
					Aliases: []string{"tail"},
					Usage:   "Tail logs from agent",
					Before:  createAgentClient,
					Action:  getLogs,
					Flags: []cli.Flag{
						idFlag(false),
						logTypeFlag,
						deploymentFlag,
					},
					ArgsUsage: "[working-dir]",
				},
				{
					Name:    "delete",
					Usage:   "Delete an agent",
					Before:  createAgentClient,
					Action:  deleteAgent,
					Aliases: []string{"destroy"},
					Flags: []cli.Flag{
						idFlag(false),
						deploymentFlag,
					},
					ArgsUsage: "[working-dir]",
				},
				{
					Name:   "versions",
					Usage:  "List versions of an agent",
					Before: createAgentClient,
					Action: listAgentVersions,
					Flags: []cli.Flag{
						idFlag(false),
						attributeFlag,
						attributesFlag,
						jsonFlag,
					},
					ArgsUsage: "[working-dir]",
				},
				{
					Name:   "list",
					Usage:  "List all LiveKit Cloud Agents",
					Action: listAgents,
					Before: createAgentClient,
					Flags: []cli.Flag{
						idSliceFlag,
						jsonFlag,
					},
				},
				{
					Name:   "secrets",
					Usage:  "List secrets for an agent",
					Before: createAgentClient,
					Action: listAgentSecrets,
					Flags: []cli.Flag{
						idFlag(false),
						jsonFlag,
					},
					ArgsUsage: "[working-dir]",
				},
				{
					Name:   "update-secrets",
					Usage:  "Update secrets for an agent, will cause a re-start of the agent.",
					Before: createAgentClient,
					Action: updateAgentSecrets,
					Flags: []cli.Flag{
						secretsFlag,
						secretsFileFlag,
						secretsMountFlag,
						ignoreEmptySecretsFlag,
						idFlag(false),
						&cli.BoolFlag{
							Name:     "overwrite",
							Usage:    "If set, will overwrite existing secrets",
							Required: false,
							Value:    false,
						},
					},
					// NOTE: since secrets may contain commas, or indeed any special character we might want to treat as a flag separator,
					// we disable it entirely here and require multiple --secrets flags to be used.
					DisableSliceFlagSeparator: true,
					ArgsUsage:                 "[working-dir]",
				},
				privateLinkCommands,
			},
		},
	}
	subdomainPattern = regexp.MustCompile(`^(?:https?|wss?)://([^.]+)\.`)
	agentsClient     *cloudagents.Client
	ignoredSecrets   = []string{
		"LIVEKIT_API_KEY",
		"LIVEKIT_API_SECRET",
		"LIVEKIT_URL",
	}
)

func noAgentError() error {
	return fmt.Errorf("no agent project detected in the current directory\n\n" +
		"Make sure you are running this command from an agent project directory\n" +
		"containing one of: pyproject.toml, requirements.txt, uv.lock, package.json, or lock files.\n\n" +
		"To get started, see: https://docs.livekit.io/agents/quickstart")
}

func createAgentClient(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	return createAgentClientWithOpts(ctx, cmd)
}

func createAgentClientWithOpts(ctx context.Context, cmd *cli.Command, opts ...loadOption) (context.Context, error) {
	var err error

	if _, err := requireProjectWithOpts(ctx, cmd, opts...); err != nil {
		return ctx, err
	}

	if cmd.NArg() > 0 {
		workingDir = cmd.Args().First()
	}

	// If a project has been manually selected that conflicts with the agent's config,
	// or if the config file is malformed, this is an error. If the config does not exist,
	// we assume it gets created later.
	configExists, err := requireConfig(workingDir, tomlFilename)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return ctx, err
	}
	if configExists {
		projectSubdomainMatch := subdomainPattern.FindStringSubmatch(project.URL)
		if len(projectSubdomainMatch) < 2 {
			return ctx, fmt.Errorf("invalid project URL [%s]", project.URL)
		}
		if projectSubdomainMatch[1] != lkConfig.Project.Subdomain {
			return ctx, fmt.Errorf("project does not match agent subdomain [%s]", lkConfig.Project.Subdomain)
		}
	}

	agentsClient, err = cloudagents.New(cloudagents.WithProject(project.URL, project.APIKey, project.APISecret))
	if err != nil {
		return ctx, err
	}
	return ctx, nil
}

func initAgent(ctx context.Context, cmd *cli.Command) error {
	// TODO: (@rektdeckard) move compatibility flag into template index,
	// then show template picker containing only compatible templates
	if !cmd.IsSet("lang") && !cmd.IsSet("template") && !cmd.IsSet("template-url") {
		if SkipPrompts(cmd) {
			templateURL = "https://github.com/livekit-examples/agent-starter-python"
		} else {
			var lang string
			// Prompt for language
			if err := huh.NewSelect[string]().
				Title("Select the language for your agent project").
				Options(
					huh.NewOption("Python", "python"),
					huh.NewOption("Node.js", "node"),
				).
				Value(&lang).
				WithTheme(util.Theme).
				Run(); err != nil {
				return err
			}

			switch lang {
			case "node":
				templateURL = "https://github.com/livekit-examples/agent-starter-node"
			case "python":
				templateURL = "https://github.com/livekit-examples/agent-starter-python"
			default:
				return fmt.Errorf("unsupported language: %s", lang)
			}
		}
	}

	logger.Debugw("Initializing agent project", "working-dir", workingDir)

	appName = cmd.Args().First()
	if appName == "" {
		appName = project.Name
	}

	// Create sandbox only when not disabled by flag and we don't already have one
	if !cmd.Bool("no-sandbox") && sandboxID == "" {
		if err := out.Await("Creating sandbox app...", ctx, func(ctx context.Context) error {
			token, err := requireToken(ctx, cmd)
			if err != nil {
				return err
			}

			// TODO: (@rektdeckard) figure out why AccessKeyProvider does not immediately
			// have access to newly-created API keys, then remove this sleep
			time.Sleep(4 * time.Second)
			sandboxID, err = bootstrap.CreateSandbox(
				ctx,
				appName,
				// NOTE: we may want to support embed sandbox in the future
				"https://github.com/livekit-examples/agent-starter-react",
				token,
				serverURL,
			)

			// We set sandbox ID in env for use in template tasks
			os.Setenv("LIVEKIT_SANDBOX_ID", sandboxID)

			return err
		}); err != nil {
			return fmt.Errorf("failed to create sandbox: %w", err)
		} else {
			out.Status("Creating sandbox app...")
			out.Statusf("Created sandbox app [%s]", util.Accented(sandboxID))
		}
	}

	// Run template bootstrap
	shouldDeploy := cmd.Bool("deploy")
	if shouldDeploy {
		cmd.Set("install", "true")
	}
	if err := setupTemplate(ctx, cmd); err != nil {
		return err
	}
	// Deploy if requested
	if shouldDeploy {
		out.Status("Deploying agent...")
		if err := createAgent(ctx, cmd); err != nil {
			return fmt.Errorf("failed to deploy agent: %w", err)
		}
	}

	return nil
}

func createAgent(ctx context.Context, cmd *cli.Command) error {
	subdomainMatches := subdomainPattern.FindStringSubmatch(project.URL)
	if len(subdomainMatches) < 2 {
		return fmt.Errorf("invalid project URL [%s]", project.URL)
	}

	// We have a configured project, but don't need to double-confirm if it was
	// set via a command line flag, because intent is clear.
	if !cmd.IsSet("project") {
		useProject := true
		if !SkipPrompts(cmd) {
			if err := huh.NewForm(huh.NewGroup(util.Confirm().
				Title(fmt.Sprintf("Use project [%s] (%s) to create agent deployment?", project.Name, project.URL)).
				Value(&useProject).
				Options(
					huh.NewOption("Yes", true),
					huh.NewOption("No, select another...", false),
				).
				WithTheme(util.Theme))).
				Run(); err != nil {
				return err
			}
		}
		if !useProject {
			if _, err := selectProject(ctx, cmd); err != nil {
				return err
			}
			(&resolvedProject{project: project, source: sourceSelected}).announce()
			var err error
			// Recreate the client with the new project
			agentsClient, err = cloudagents.New(cloudagents.WithProject(project.URL, project.APIKey, project.APISecret))
			if err != nil {
				return err
			}

			// Re-parse the project URL to get the subdomain
			subdomainMatches = subdomainPattern.FindStringSubmatch(project.URL)
			if len(subdomainMatches) < 2 {
				return fmt.Errorf("invalid project URL [%s]", project.URL)
			}
		}
	}

	logger.Debugw("Creating agent", "working-dir", workingDir)
	configExists, err := requireConfig(workingDir, tomlFilename)
	if err != nil && configExists {
		return err
	}

	if configExists && lkConfig.Agent != nil {
		out.Statusf("Using agent configuration [%s]", util.Accented(tomlFilename))
	} else {
		lkConfig = config.NewLiveKitTOML(subdomainMatches[1]).WithDefaultAgent()
	}
	out.Status("Creating new agent deployment")

	secrets, err := requireSecrets(ctx, cmd, false, false)
	if err != nil {
		return err
	}

	settingsMap, err := getClientSettings(ctx)
	if err != nil {
		return err
	}

	imageRef := cmd.String("image")
	imageTar := cmd.String("image-tar")
	// Prebuilt image: no local project layout is required; skip language/dockerfile/sdk checks.
	if imageRef != "" || imageTar != "" {
		region, err := resolveRegion(cmd, settingsMap, "Select region for agent deployment")
		if err != nil {
			return err
		}
		buildContext, cancel := context.WithTimeout(ctx, buildTimeout)
		defer cancel()
		regions := []string{region}
		agentID, err := agentsClient.RegisterAgent(buildContext, secrets, regions)
		if err != nil {
			if twerr, ok := err.(twirp.Error); ok {
				return fmt.Errorf("unable to create agent: %s", twerr.Msg())
			}
			return fmt.Errorf("unable to create agent: %w", err)
		}
		lkConfig.Agent.ID = agentID
		if err := lkConfig.SaveTOMLFile(workingDir, tomlFilename); err != nil {
			return err
		}
		out.Statusf("Created agent with ID [%s]", util.Accented(agentID))
		return deployPrebuiltImage(buildContext, agentID, imageRef, imageTar)
	}

	projectType, err := agentfs.DetectProjectType(os.DirFS(workingDir))
	if err != nil {
		return noAgentError()
	}
	out.Statusf("Detected agent language [%s]", util.Accented(string(projectType)))

	if err := requireDockerfile(ctx, cmd, workingDir, projectType, settingsMap); err != nil {
		return err
	}

	if err := agentfs.CheckSDKVersion(workingDir, projectType, settingsMap); err != nil {
		if cmd.Bool("skip-sdk-check") {
			out.Warnf("Error checking SDK version: %v, skipping...", err)
		} else {
			return err
		}
	}

	region, err := resolveRegion(cmd, settingsMap, "Select region for agent deployment")
	if err != nil {
		return err
	}

	buildContext, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()
	regions := []string{region}

	excludeFiles := []string{fmt.Sprintf("**/%s", config.LiveKitTOMLFile)}
	resp, err := agentsClient.CreateAgent(buildContext, os.DirFS(workingDir), secrets, regions, excludeFiles, os.Stderr)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("build timed out possibly due to large image size")
		}
		if twerr, ok := err.(twirp.Error); ok {
			return fmt.Errorf("unable to create agent: %s", twerr.Msg())
		}
		return fmt.Errorf("unable to create agent: %w", err)
	}

	lkConfig.Agent.ID = resp.AgentId
	if err := lkConfig.SaveTOMLFile(workingDir, tomlFilename); err != nil {
		return err
	}

	out.Statusf("Created agent with ID [%s]", util.Accented(resp.AgentId))

	out.Status("Build completed - You can view build logs later with `lk agent logs --log-type=build`")

	if !SkipPrompts(cmd) {
		viewLogs := true
		if err := huh.NewForm(
			huh.NewGroup(
				util.Confirm().
					Title("Agent deploying. Would you like to view logs?").
					Description("You can view logs later with `lk agent logs`").
					Value(&viewLogs).
					WithTheme(util.Theme),
			),
		).Run(); err != nil {
			return err
		} else if viewLogs {
			out.Status("Tailing runtime logs...safe to exit at any time")
			return agentsClient.StreamLogs(ctx, "deploy", lkConfig.Agent.ID, "", os.Stdout, resp.ServerRegions[0])
		}
	}
	return nil
}

func createAgentConfig(ctx context.Context, cmd *cli.Command) error {
	if _, err := os.Stat(tomlFilename); err == nil {
		overwrite := false
		if SkipPrompts(cmd) {
			overwrite = true
		} else {
			var overwriteVal bool
			if err := huh.NewForm(
				huh.NewGroup(
					util.Confirm().
						Title(
							fmt.Sprintf("Config file [%s] file already exists. Overwrite?", tomlFilename),
						).
						Value(&overwriteVal).
						WithTheme(util.Theme),
				),
			).
				Run(); err != nil {
				return err
			}
			overwrite = overwriteVal
		}
		if !overwrite {
			return fmt.Errorf("config file [%s] already exists", util.Accented(tomlFilename))
		}
	}

	agentID := cmd.String("id")
	if agentID == "" {
		configExists, err := requireConfig(workingDir, tomlFilename)
		if err != nil && configExists {
			return err
		}

		if configExists && lkConfig.HasAgent() {
			agentID = lkConfig.Agent.ID
		} else {
			agentID, err = selectAgent(ctx, cmd, false)
			if err != nil {
				return err
			}
		}
	}

	response, err := agentsClient.ListAgents(ctx, &lkproto.ListAgentsRequest{
		AgentId: agentID,
	})
	if err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			return fmt.Errorf("unable to list agents: %s", twerr.Msg())
		}
		return fmt.Errorf("unable to list agents: %w", err)
	}
	if len(response.Agents) == 0 {
		return fmt.Errorf("agent not found")
	}

	subdomainPattern := regexp.MustCompile(`^(?:https?|wss?)://([^.]+)\.`)
	matches := subdomainPattern.FindStringSubmatch(project.URL)
	if len(matches) < 1 {
		return fmt.Errorf("invalid project URL: %s", project.URL)
	}

	agent := response.Agents[0]
	lkConfig := config.NewLiveKitTOML(matches[1])
	lkConfig.Agent = &config.LiveKitTOMLAgentConfig{
		ID: agent.AgentId,
	}

	if err := lkConfig.SaveTOMLFile(workingDir, tomlFilename); err != nil {
		return err
	}
	return nil
}

func deployAgent(ctx context.Context, cmd *cli.Command) error {
	agentId, err := getAgentID(ctx, cmd, workingDir, tomlFilename, false)
	if err != nil {
		return err
	}

	buildContext, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()

	attrs, err := resolveAttributes(cmd)
	if err != nil {
		return err
	}

	secrets, err := requireSecrets(ctx, cmd, false, true)
	if err != nil {
		return err
	}

	// --image or --image-tar: skip source build and push a prebuilt image via the OCI proxy.
	imageRef := cmd.String("image")
	imageTar := cmd.String("image-tar")
	if imageRef != "" || imageTar != "" {
		if len(secrets) > 0 {
			resp, err := agentsClient.UpdateAgentSecrets(buildContext, &lkproto.UpdateAgentSecretsRequest{
				AgentId: agentId,
				Secrets: secrets,
			})
			if err != nil {
				if twerr, ok := err.(twirp.Error); ok {
					return fmt.Errorf("unable to update agent secrets: %s", twerr.Msg())
				}
				return fmt.Errorf("unable to update agent secrets: %w", err)
			}
			if !resp.Success {
				return fmt.Errorf("failed to update agent secrets: %s", resp.Message)
			}
		}
		if err := deployPrebuiltImage(buildContext, agentId, imageRef, imageTar); err != nil {
			return fmt.Errorf("unable to deploy prebuilt image: %w", err)
		}
		out.Status("Deployed agent")
		return nil
	}

	projectType, err := agentfs.DetectProjectType(os.DirFS(workingDir))
	if err != nil {
		return noAgentError()
	}
	out.Statusf("Detected agent language [%s]", util.Accented(string(projectType)))

	settingsMap, err := getClientSettings(ctx)
	if err != nil {
		return err
	}

	if err := agentfs.CheckSDKVersion(workingDir, projectType, settingsMap); err != nil {
		if cmd.Bool("skip-sdk-check") {
			out.Warnf("Error checking SDK version: %v, skipping...", err)
		} else {
			return err
		}
	}

	agentDeployment := ""
	if cmd.IsSet("deployment") {
		agentDeployment = cmd.String("deployment")
		fmt.Printf("Using deployment [%s]\n", util.Accented(agentDeployment))
	}

	excludeFiles := []string{fmt.Sprintf("**/%s", config.LiveKitTOMLFile)}
	if err := agentsClient.DeployAgentV2(buildContext, agentId, os.DirFS(workingDir), secrets, attrs, agentDeployment, excludeFiles, os.Stderr); err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			return fmt.Errorf("unable to deploy agent: %s", twerr.Msg())
		}
		return fmt.Errorf("unable to deploy agent: %w", err)
	}

	out.Status("Deployed agent")
	return nil
}

func promoteAgent(ctx context.Context, cmd *cli.Command) error {
	agentID, err := getAgentID(ctx, cmd, workingDir, tomlFilename, false)
	if err != nil {
		return err
	}
	agentDeployment := cmd.String("deployment")
	if agentDeployment == "" {
		return fmt.Errorf("cannot promote production deployment")
	}
	if err := agentsClient.PromoteAgent(ctx, agentID, agentDeployment, ""); err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			return fmt.Errorf("unable to promote agent: %s", twerr.Msg())
		}
		return fmt.Errorf("unable to promote agent: %w", err)
	}
	out.Statusf("Promoted agent from deployment [%s] to production", util.Accented(agentDeployment))
	return nil
}

// deployPrebuiltImage pushes a locally-built image through the cloud-agents OCI proxy.
// Exactly one of imageRef (Docker daemon via the Docker API) or imageTar must be non-empty.
func deployPrebuiltImage(ctx context.Context, agentID, imageRef, imageTar string) error {
	target, err := agentsClient.GetPushTarget(ctx, agentID)
	if err != nil {
		return fmt.Errorf("failed to get push target: %w", err)
	}

	var img v1.Image
	if imageRef != "" {
		imageRef = strings.TrimSpace(imageRef)
		out.Statusf("Loading image from Docker daemon [%s]", util.Accented(imageRef))
		var dockerCloser io.Closer
		img, dockerCloser, err = agentfs.LoadDockerDaemonImage(ctx, imageRef)
		if err != nil {
			return err
		}
		defer dockerCloser.Close()
	} else {
		out.Statusf("Loading image from [%s]", util.Accented(imageTar))
		img, err = crane.Load(imageTar)
		if err != nil {
			return fmt.Errorf("failed to load image: %w", err)
		}
	}

	proxyRef := fmt.Sprintf("%s/%s:%s", target.ProxyHost, target.Name, target.Tag)
	out.Statusf("Pushing image [%s]", util.Accented(proxyRef))

	rt := agentsClient.NewRegistryTransport()
	if err := crane.Push(img, proxyRef,
		crane.WithTransport(rt),
		crane.WithAuth(authn.Anonymous),
	); err != nil {
		return fmt.Errorf("failed to push image: %w", err)
	}
	return nil
}

func getAgentStatus(ctx context.Context, cmd *cli.Command) error {
	agentID, err := getAgentID(ctx, cmd, workingDir, tomlFilename, false)
	if err != nil {
		return err
	}

	res, err := agentsClient.ListAgents(ctx, &lkproto.ListAgentsRequest{
		AgentId: agentID,
	})
	if err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			return fmt.Errorf("unable to list agents: %s", twerr.Msg())
		}
		return fmt.Errorf("unable to list agents: %w", err)
	}

	if len(res.Agents) == 0 {
		return fmt.Errorf("no agents found")
	}

	if cmd.Bool("json") {
		util.PrintJSON(res)
		return nil
	}

	var rows [][]string
	for _, agent := range res.Agents {
		for _, regionalAgent := range agent.AgentDeployments {
			curCPU, err := agentfs.ParseCpu(regionalAgent.CurCpu)
			if err != nil {
				logger.Errorw("error parsing cpu", err)
			}

			curMem, err := agentfs.ParseMem(regionalAgent.CurMem, false)
			if err != nil {
				logger.Errorw("error parsing mem", err)
			}

			memLimit, err := agentfs.ParseMem(regionalAgent.MemLimit, true)
			if err != nil {
				logger.Errorw("error parsing mem req", err)
			}

			version := regionalAgent.Version
			if version == "" {
				version = agent.Version
			}

			deployment := "---"
			if regionalAgent.DeploymentEnabled {
				deployment = regionalAgent.Deployment
				if deployment == "" {
					deployment = "production"
				}
			}

			name := regionalAgent.AgentName
			if name == "" {
				name = "---"
			}

			rows = append(rows, []string{
				agent.AgentId,
				name,
				version,
				regionalAgent.Region,
				deployment,
				regionalAgent.Status,
				fmt.Sprintf("%s / %s", curCPU, regionalAgent.CpuLimit),
				fmt.Sprintf("%s / %s", curMem, memLimit),
				fmt.Sprintf("%d / %d / %d", regionalAgent.Replicas, regionalAgent.MinReplicas, regionalAgent.MaxReplicas),
				formatTime(agent.DeployedAt.AsTime()),
			})
		}
	}

	t := util.CreateTable().
		Headers(
			"ID",
			"Name",
			"Version",
			"Region",
			"Deployment",
			"Status",
			"CPU",
			"Mem",
			"Replicas",
			"Deployed At",
		).
		Rows(rows...)

	out.Result(t)
	return nil
}

func restartAgent(ctx context.Context, cmd *cli.Command) error {
	agentID, err := getAgentID(ctx, cmd, workingDir, tomlFilename, false)
	if err != nil {
		return err
	}

	resp, err := agentsClient.RestartAgent(ctx, &lkproto.RestartAgentRequest{
		AgentId: agentID,
	})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("failed to restart agent: %s", resp.Message)
	}

	out.Statusf("Restarted agent [%s]", util.Accented(agentID))
	return nil
}

func updateAgent(ctx context.Context, cmd *cli.Command) error {
	configExists, err := requireConfig(workingDir, tomlFilename)
	if err != nil && configExists {
		return err
	}
	if !configExists {
		return fmt.Errorf("config file [%s] required to update agent", tomlFilename)
	}
	if !lkConfig.HasAgent() {
		return fmt.Errorf("no agent config found in [%s]", tomlFilename)
	}

	req := &lkproto.UpdateAgentRequest{
		AgentId: lkConfig.Agent.ID,
	}

	secrets, err := requireSecrets(ctx, cmd, false, true)
	if err != nil {
		return err
	}
	if len(secrets) > 0 {
		req.Secrets = secrets
	}

	var resp *lkproto.UpdateAgentResponse
	err = out.Await("Updating agent ["+util.Accented(lkConfig.Agent.ID)+"]", ctx, func(ctx context.Context) error {
		var clientErr error
		resp, clientErr = agentsClient.UpdateAgent(ctx, req)
		return clientErr
	})
	if err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			return fmt.Errorf("unable to update agent: %s", twerr.Msg())
		}
		return fmt.Errorf("unable to update agent: %w", err)
	}

	if resp.Success {
		out.Statusf("Updated agent [%s]", util.Accented(lkConfig.Agent.ID))
		err = lkConfig.SaveTOMLFile("", tomlFilename)
		return err
	}

	return fmt.Errorf("failed to update agent: %s", resp.Message)
}

func rollbackAgent(ctx context.Context, cmd *cli.Command) error {
	agentID, err := getAgentID(ctx, cmd, workingDir, tomlFilename, false)
	if err != nil {
		return err
	}

	var resp *lkproto.RollbackAgentResponse
	err = out.Await("Rolling back agent ["+util.Accented(agentID)+"]", ctx, func(ctx context.Context) error {
		var clientErr error
		resp, clientErr = agentsClient.RollbackAgent(ctx, &lkproto.RollbackAgentRequest{
			AgentId: agentID,
			Version: cmd.String("version"),
		})
		return clientErr
	})

	if err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			return fmt.Errorf("unable to rollback agent: %s", twerr.Msg())
		}
		return fmt.Errorf("unable to rollback agent: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("failed to rollback agent %s", resp.Message)
	}

	out.Statusf("Rolled back agent [%s] to version [%s]", util.Accented(agentID), util.Accented(cmd.String("version")))

	return nil
}

func getLogs(ctx context.Context, cmd *cli.Command) error {
	agentID, err := getAgentID(ctx, cmd, workingDir, tomlFilename, true)
	if err != nil {
		return err
	}

	response, err := agentsClient.ListAgents(ctx, &lkproto.ListAgentsRequest{
		AgentId: agentID,
	})
	if err != nil {
		return err
	}

	if len(response.Agents) == 0 {
		return fmt.Errorf("no agent deployments found")
	}

	return agentsClient.StreamLogs(ctx, cmd.String("log-type"), agentID, cmd.String("deployment"), os.Stdout, response.Agents[0].AgentDeployments[0].ServerRegion)
}

func deleteAgent(ctx context.Context, cmd *cli.Command) error {
	agentID, err := getAgentID(ctx, cmd, workingDir, tomlFilename, false)
	if err != nil {
		return err
	}

	agentDeployment := cmd.String("deployment")
	agentMsg := fmt.Sprintf("agent [%s]", util.Accented(agentID))
	if agentDeployment != "" {
		agentMsg += fmt.Sprintf(" deployment [%s]", util.Accented(agentDeployment))
	}

	if !SkipPrompts(cmd) {
		var confirmDelete bool
		if err := huh.NewForm(
			huh.NewGroup(
				util.Confirm().
					Title(fmt.Sprintf("Are you sure you want to delete agent %s?", agentMsg)).
					Value(&confirmDelete).
					WithTheme(util.Theme),
			),
		).Run(); err != nil {
			return err
		}

		if !confirmDelete {
			return nil
		}
	}

	var res *lkproto.DeleteAgentResponse
	if err = out.Await(
		fmt.Sprintf("Deleting agent %s", agentMsg),
		ctx,
		func(ctx context.Context) error {
			var clientErr error
			res, clientErr = agentsClient.DeleteAgent(ctx, &lkproto.DeleteAgentRequest{
				AgentId:    agentID,
				Deployment: agentDeployment,
			})
			return clientErr
		},
	); err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			return fmt.Errorf("unable to delete agent: %s", twerr.Msg())
		}
		return fmt.Errorf("unable to delete agent: %w", err)
	}

	if !res.Success {
		return fmt.Errorf("failed to delete agent %s", res.Message)
	}

	out.Statusf("Deleted agent %s", agentMsg)
	return nil
}

// resolveAttributes merges attribute inputs from the --attributes JSON flag
// (literal, file path, or "-" for stdin) and the repeatable --attribute
// key=value flag. The key=value pairs take precedence over the JSON object on
// conflicting keys. Returns nil when neither flag is set.
func resolveAttributes(cmd *cli.Command) (map[string]string, error) {
	attrs := map[string]string{}
	if cmd.IsSet(attributesFlag.Name) {
		if _, err := ReadJSONFileOrLiteral(cmd.String(attributesFlag.Name), &attrs); err != nil {
			return nil, err
		}
	}
	pairs, err := parseKeyValuePairs(cmd, attributeFlag.Name)
	if err != nil {
		return nil, err
	}
	maps.Copy(attrs, pairs)
	if len(attrs) == 0 {
		return nil, nil
	}
	return attrs, nil
}

// attributesMatch reports whether attrs contains every key-value pair in
// filter. Extra keys in attrs are allowed, so the match is inclusive.
func attributesMatch(attrs, filter map[string]string) bool {
	for k, want := range filter {
		if got, ok := attrs[k]; !ok || got != want {
			return false
		}
	}
	return true
}

func listAgentVersions(ctx context.Context, cmd *cli.Command) error {
	agentID, err := getAgentID(ctx, cmd, workingDir, tomlFilename, false)
	if err != nil {
		return err
	}

	req := &lkproto.ListAgentVersionsRequest{
		AgentId: agentID,
	}

	versions, err := agentsClient.ListAgentVersions(ctx, req)
	if err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			return fmt.Errorf("unable to list agent versions: %s", twerr.Msg())
		}
		return fmt.Errorf("unable to list agent versions: %w", err)
	}

	// Filter to versions containing all requested attributes. Extra attributes
	// on a version are allowed; the filter is inclusive, not exclusive.
	attrFilter, err := resolveAttributes(cmd)
	if err != nil {
		return err
	}
	if len(attrFilter) > 0 {
		versions.Versions = slices.DeleteFunc(versions.Versions, func(v *lkproto.AgentVersion) bool {
			return !attributesMatch(v.Attributes, attrFilter)
		})
	}

	// Sort versions by created date descending
	slices.SortFunc(versions.Versions, func(a, b *lkproto.AgentVersion) int {
		return b.CreatedAt.AsTime().Compare(a.CreatedAt.AsTime())
	})

	if cmd.Bool("json") {
		util.PrintJSON(versions)
		return nil
	}

	showDigest := false
	for _, v := range versions.Versions {
		if v.Attributes["image_digest"] != "" {
			showDigest = true
			break
		}
	}

	flag := func(b bool) string {
		if b {
			return "✓"
		}
		return "---"
	}
	headers := []string{"Version", "Production", "Draining", "Active", "Status", "Attributes", "Created At", "Deployed At"}
	if showDigest {
		headers = append(headers, "Digest")
	}
	table := util.CreateTable().Headers(headers...)

	for _, version := range versions.Versions {
		attrs, err := json.Marshal(version.Attributes)
		if err != nil || len(version.Attributes) == 0 {
			attrs = []byte("--")
		}
		row := []string{
			version.Version,
			flag(version.Current),
			flag(version.Draining),
			flag(version.Active),
			version.Status,
			string(attrs),
			formatTime(version.CreatedAt.AsTime()),
			formatTime(version.DeployedAt.AsTime()),
		}
		if showDigest {
			row = append(row, version.Attributes["image_digest"])
		}
		table.Row(row...)
	}

	out.Result(table)
	return nil
}

func listAgents(ctx context.Context, cmd *cli.Command) error {
	var items []*lkproto.AgentInfo
	if cmd.IsSet("id") {
		for _, agentID := range cmd.StringSlice("id") {
			if agentID == "" {
				continue
			}
			res, err := agentsClient.ListAgents(ctx, &lkproto.ListAgentsRequest{
				AgentId: agentID,
			})
			if err != nil {
				if twerr, ok := err.(twirp.Error); ok {
					return fmt.Errorf("unable to list agents: %s", twerr.Msg())
				}
				return fmt.Errorf("unable to list agents: %w", err)
			}
			items = append(items, res.Agents...)
		}
	} else {
		agents, err := agentsClient.ListAgents(ctx, &lkproto.ListAgentsRequest{})
		if err != nil {
			if twerr, ok := err.(twirp.Error); ok {
				return fmt.Errorf("unable to list agents: %s", twerr.Msg())
			}
			return fmt.Errorf("unable to list agents: %w", err)
		}
		items = agents.Agents
	}

	slices.SortFunc(items, func(a, b *lkproto.AgentInfo) int {
		return b.DeployedAt.AsTime().Compare(a.DeployedAt.AsTime())
	})

	if cmd.Bool("json") {
		util.PrintJSON(&lkproto.ListAgentsResponse{Agents: items})
		return nil
	}

	if len(items) == 0 {
		out.Status("No agents found")
		return nil
	}

	var rows [][]string
	for _, agent := range items {
		regionSet := map[string]struct{}{}
		deploymentSet := map[string]struct{}{}
		for _, regionalAgent := range agent.AgentDeployments {
			regionSet[regionalAgent.Region] = struct{}{}
			deploymentSet[regionalAgent.Deployment] = struct{}{}
		}
		regions := make([]string, 0, len(regionSet))
		for region := range regionSet {
			regions = append(regions, region)
		}
		deployments := make([]string, 0, len(deploymentSet))
		for deployment := range deploymentSet {
			if deployment == "" {
				deployment = "production"
			}
			deployments = append(deployments, deployment)
		}
		// Always sort "production" first, then the rest alphabetically.
		slices.SortFunc(deployments, func(a, b string) int {
			if a == "production" {
				return -1
			}
			if b == "production" {
				return 1
			}
			return strings.Compare(a, b)
		})
		rows = append(rows, []string{
			agent.AgentId,
			agent.AgentName,
			strings.Join(regions, ","),
			strings.Join(deployments, ","),
			agent.Version,
			formatTime(agent.DeployedAt.AsTime()),
		})
	}

	t := util.CreateTable().
		Headers("ID", "Dispatch Name", "Regions", "Deployments", "Version", "Deployed At").
		Rows(rows...)

	out.Result(t)
	return nil
}

func listAgentSecrets(ctx context.Context, cmd *cli.Command) error {
	agentID, err := getAgentID(ctx, cmd, workingDir, tomlFilename, false)
	if err != nil {
		return err
	}

	req := &lkproto.ListAgentSecretsRequest{
		AgentId: agentID,
	}

	secrets, err := agentsClient.ListAgentSecrets(ctx, req)
	if err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			return fmt.Errorf("unable to list agent secrets: %s", twerr.Msg())
		}
		return fmt.Errorf("unable to list agent secrets: %w", err)
	}

	// NOTE: Maybe these should be omitted on the server side?
	visible := make([]*lkproto.AgentSecret, 0, len(secrets.Secrets))
	for _, secret := range secrets.Secrets {
		if slices.Contains(ignoredSecrets, secret.Name) {
			continue
		}
		visible = append(visible, secret)
	}

	if cmd.Bool("json") {
		util.PrintJSON(&lkproto.ListAgentSecretsResponse{Secrets: visible})
		return nil
	}

	// TODO (steveyoon): show secret.Kind.String() once cloud-agents is released
	table := util.CreateTable().
		Headers("Name", "Created At", "Updated At")

	for _, secret := range visible {
		table.Row(secret.Name, secret.CreatedAt.AsTime().Format(time.RFC3339), secret.UpdatedAt.AsTime().Format(time.RFC3339))
	}

	out.Result(table)
	return nil
}

func updateAgentSecrets(ctx context.Context, cmd *cli.Command) error {
	agentID, err := getAgentID(ctx, cmd, workingDir, tomlFilename, false)
	if err != nil {
		return err
	}

	secrets, err := requireSecrets(ctx, cmd, true, true)
	if err != nil {
		return err
	}

	var confirmOverwrite bool
	if cmd.Bool("overwrite") {
		confirmOverwrite = SkipPrompts(cmd)
		if !SkipPrompts(cmd) {
			if err := huh.NewForm(
				huh.NewGroup(
					util.Confirm().
						Title(fmt.Sprintf("This will remove all existing secrets. Are you sure you want to proceed [%s]?", agentID)).
						Value(&confirmOverwrite).
						WithTheme(util.Theme),
				),
			).Run(); err != nil {
				return err
			}
		}
		if !confirmOverwrite {
			return nil
		}
	}

	req := &lkproto.UpdateAgentSecretsRequest{
		AgentId:   agentID,
		Secrets:   secrets,
		Overwrite: cmd.Bool("overwrite"),
	}

	resp, err := agentsClient.UpdateAgentSecrets(ctx, req)
	if err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			return fmt.Errorf("unable to update agent secrets: %s", twerr.Msg())
		}
		return fmt.Errorf("unable to update agent secrets: %w", err)
	}

	if resp.Success {
		out.Status("Updated agent secrets")
		return nil
	}

	return fmt.Errorf("failed to update agent secrets: %s", resp.Message)
}

func getAgentID(ctx context.Context, cmd *cli.Command, agentDir string, tomlFileName string, excludeEmptyVersion bool) (string, error) {
	agentID := cmd.String("id")
	if agentID == "" {
		configExists, err := requireConfig(agentDir, tomlFileName)
		if err != nil && configExists {
			return "", err
		}

		if configExists {
			if !lkConfig.HasAgent() {
				return "", fmt.Errorf("no agent config found in [%s]", tomlFilename)
			}
			agentID = lkConfig.Agent.ID
		} else {
			agentID, err = selectAgent(ctx, cmd, excludeEmptyVersion)
			if err != nil {
				return "", err
			}
		}
	}

	if agentID == "" {
		// shouldn't happen, but check to ensure we have a name
		return "", fmt.Errorf("agent ID or [%s] required", util.Accented(tomlFileName))
	}

	out.Statusf("Using agent [%s]", util.Accented(agentID))

	return agentID, nil
}

func selectAgent(ctx context.Context, cmd *cli.Command, excludeEmptyVersion bool) (string, error) {
	var agents *lkproto.ListAgentsResponse

	err := out.Await("No agent ID provided, selecting from available agents...", ctx, func(ctx context.Context) error {
		var clientErr error
		agents, clientErr = agentsClient.ListAgents(ctx, &lkproto.ListAgentsRequest{})
		return clientErr
	})
	if err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			return "", fmt.Errorf("unable to list agents: %s", twerr.Msg())
		}
		return "", fmt.Errorf("unable to list agents: %w", err)
	}

	if len(agents.Agents) == 0 {
		return "", fmt.Errorf("no agents found")
	}

	var agentNames []huh.Option[string]
	for _, agent := range agents.Agents {
		if excludeEmptyVersion && agent.Version == "---" {
			continue
		}
		var deployedStr string
		if deployedAt := agent.DeployedAt.AsTime(); deployedAt.IsZero() {
			deployedStr = "never deployed"
		} else {
			deployedStr = "deployed " + deployedAt.Format(time.RFC3339)
		}
		name := agent.AgentId + " " + util.Dimmed(deployedStr)
		agentNames = append(agentNames, huh.Option[string]{Key: name, Value: agent.AgentId})
	}

	if SkipPrompts(cmd) {
		if len(agentNames) != 1 {
			return "", fmt.Errorf("non-interactive mode: set --id when multiple agents exist")
		}
		return agentNames[0].Value, nil
	}

	var selectedAgent string
	if err := huh.NewSelect[string]().
		Title("Select an agent").
		Options(agentNames...).
		Value(&selectedAgent).
		WithTheme(util.Theme).
		Run(); err != nil {
		return "", err
	}

	return selectedAgent, nil
}

func requireSecrets(_ context.Context, cmd *cli.Command, required, lazy bool) ([]*lkproto.AgentSecret, error) {
	secrets := make(map[string]*lkproto.AgentSecret)

	mountableSecretFiles := cmd.StringSlice("secret-mount")
	for _, filePath := range mountableSecretFiles {
		fileInfo, err := os.Stat(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to get secret file: %w", err)
		}
		if fileInfo.Size() > maxSecretFileSize {
			return nil, fmt.Errorf("secret file size is too large (must be under %d MB): %s", maxSecretFileSize/(1024*1024), filePath)
		}
		fileContent, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read secret file: %w", err)
		}
		name := fileInfo.Name()
		agentSecret := &lkproto.AgentSecret{
			Name:  name,
			Value: []byte(fileContent),
			Kind:  lkproto.AgentSecretKind_AGENT_SECRET_KIND_FILE,
		}
		secrets[name] = agentSecret
	}

	if values, err := parseKeyValuePairs(cmd, "secrets"); err != nil {
		return nil, fmt.Errorf("failed to parse secrets: %w", err)
	} else {
		for key, val := range values {
			agentSecret := &lkproto.AgentSecret{
				Name:  key,
				Value: []byte(val),
				Kind:  lkproto.AgentSecretKind_AGENT_SECRET_KIND_ENVIRONMENT,
			}
			secrets[key] = agentSecret
		}
	}

	shouldReadFromDisk := cmd.IsSet("secrets-file") || !lazy || (required && len(secrets) == 0)
	if shouldReadFromDisk {
		file, env, err := agentfs.DetectEnvFile(cmd.String("secrets-file"), SkipPrompts(cmd))
		if err != nil {
			return nil, err
		}
		if file != "" {
			out.Statusf("Using secrets file [%s]", util.Accented(file))
		}

		ignoreEmpty := cmd.Bool("ignore-empty-secrets")
		var skippedEmpty []string

		for k, v := range env {
			if _, exists := secrets[k]; exists {
				continue
			}

			if v == "" {
				if ignoreEmpty {
					skippedEmpty = append(skippedEmpty, k)
					continue
				}
				return nil, fmt.Errorf("failed to parse secrets file: secret %s is empty, either remove it or provide a value, or use --ignore-empty-secrets to skip empty values", k)
			}

			secret := &lkproto.AgentSecret{
				Name:  k,
				Value: []byte(v),
				Kind:  lkproto.AgentSecretKind_AGENT_SECRET_KIND_ENVIRONMENT,
			}
			secrets[k] = secret
		}

		// Note any empty secrets that were skipped (suppressed by --quiet via the Printer)
		if len(skippedEmpty) > 0 {
			skippedNames := strings.Join(skippedEmpty, ", ")
			out.Statusf("Skipped %d empty secret(s): %s", len(skippedEmpty), util.Dimmed(skippedNames))
		}
	}

	var secretsSlice []*lkproto.AgentSecret
	var secretsIgnored bool
	for _, secret := range secrets {
		if slices.Contains(ignoredSecrets, secret.Name) {
			secretsIgnored = true
			continue
		}
		secretsSlice = append(secretsSlice, secret)
	}

	if required && len(secretsSlice) == 0 {
		msg := "no secrets provided"
		if secretsIgnored {
			msg = "no valid secrets provided, LIVEKIT_ secrets are ignored and injected automatically to your agent"
		}
		return nil, errors.New(msg)
	}

	return secretsSlice, nil
}

func requireDockerfile(ctx context.Context, cmd *cli.Command, workingDir string, projectType agentfs.ProjectType, settingsMap map[string]string) error {
	dockerfileExists, err := agentfs.HasDockerfile(workingDir)
	if err != nil {
		return err
	}

	dockerIgnoreExists, err := agentfs.HasDockerIgnore(workingDir)
	if err != nil {
		return err
	}

	if !dockerfileExists {
		// out.Await handles spinner suppression (--quiet / non-interactive) internally.
		if err := out.Await(
			"Creating Dockerfile...",
			ctx,
			func(ctx context.Context) error {
				return agentfs.CreateDockerfile(workingDir, projectType, settingsMap, SkipPrompts(cmd))
			},
		); err != nil {
			return err
		}
		out.Statusf("Created [%s]", util.Accented("Dockerfile"))
	} else {
		out.Status("Using existing Dockerfile")
	}

	if !dockerIgnoreExists {
		out.Status("Creating .dockerignore...")
		if err := agentfs.CreateDockerIgnoreFile(workingDir, projectType); err != nil {
			return err
		}
		out.Statusf("Created [%s]", util.Accented(".dockerignore"))
	} else {
		out.Status("Using existing .dockerignore")
	}

	return nil
}

func getClientSettings(ctx context.Context) (map[string]string, error) {
	var clientSettingsResponse *lkproto.ClientSettingsResponse
	// out.Await handles spinner suppression (--quiet / non-interactive) internally.
	err := out.Await(
		"Loading client settings...",
		ctx,
		func(ctx context.Context) error {
			var e error
			clientSettingsResponse, e = agentsClient.GetClientSettings(ctx, &lkproto.ClientSettingsRequest{})
			return e
		},
	)

	if err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			return nil, fmt.Errorf("unable to get client settings: %s", twerr.Msg())
		}
		return nil, fmt.Errorf("unable to get client settings: %w", err)
	}

	if clientSettingsResponse == nil {
		return nil, fmt.Errorf("unable to contact server; please try again later")
	}

	settingsMap := make(map[string]string)
	for _, setting := range clientSettingsResponse.Params {
		settingsMap[setting.Name] = setting.Value
	}

	return settingsMap, nil
}

// resolveRegion returns the LiveKit region to use, prompting the user with a
// picker populated from server-reported available_regions when --region is
// unset and the CLI is interactive. In non-interactive mode an unset --region
// is an error so invocations fail loudly instead of silently defaulting.
func resolveRegion(cmd *cli.Command, settingsMap map[string]string, title string) (string, error) {
	if region := cmd.String("region"); region != "" {
		return region, nil
	}

	availableRegionsStr, ok := settingsMap["available_regions"]
	if !ok || availableRegionsStr == "" {
		// we shouldn't ever get here, but if we do, just default to us-east
		logger.Debugw("no available regions found, defaulting to us-east. please contact LiveKit support if this is unexpected.")
		return "us-east", nil
	}

	regionOptions := strings.Split(availableRegionsStr, ",")
	for i, r := range regionOptions {
		regionOptions[i] = strings.TrimSpace(r)
	}
	slices.Sort(regionOptions)
	slices.Reverse(regionOptions)

	if SkipPrompts(cmd) {
		return "", fmt.Errorf("non-interactive mode: --region flag must be specified, available regions: %v", regionOptions)
	}

	var region string
	if err := huh.NewSelect[string]().
		Title(title).
		Options(huh.NewOptions(regionOptions...)...).
		Value(&region).
		WithTheme(util.Theme).
		Run(); err != nil {
		return "", err
	}
	out.Statusf("Using region [%s]", util.Accented(region))
	return region, nil
}

func requireConfig(workingDir, tomlFilename string) (bool, error) {
	if lkConfig != nil {
		return true, nil
	}

	var exists bool
	var err error
	lkConfig, exists, err = config.LoadTOMLFile(workingDir, tomlFilename)
	return exists, err
}

func generateAgentDockerfile(ctx context.Context, cmd *cli.Command) error {
	if cmd.NArg() > 0 {
		workingDir = cmd.Args().First()
	}

	if stat, err := os.Stat(workingDir); err != nil || !stat.IsDir() {
		return fmt.Errorf("invalid working directory: %s", workingDir)
	}

	settingsMap, err := getClientSettings(ctx)
	if err != nil {
		return err
	}

	projectType, err := agentfs.DetectProjectType(os.DirFS(workingDir))
	if err != nil {
		return noAgentError()
	}
	out.Statusf("Detected agent language [%s]", util.Accented(string(projectType)))

	dockerfilePath := filepath.Join(workingDir, "Dockerfile")
	dockerignorePath := filepath.Join(workingDir, ".dockerignore")
	overwrite := cmd.Bool("overwrite")

	writeDockerfile := true
	writeDockerignore := true
	if !overwrite {
		if _, err := os.Stat(dockerfilePath); err == nil {
			out.Statusf("%s already exists; skipping. Use --overwrite to replace.", util.Accented("Dockerfile"))
			writeDockerfile = false
		}
		if _, err := os.Stat(dockerignorePath); err == nil {
			out.Statusf("%s already exists; skipping. Use --overwrite to replace.", util.Accented(".dockerignore"))
			writeDockerignore = false
		}
	}

	if !writeDockerfile && !writeDockerignore {
		return nil
	}

	// Generate contents without writing
	dockerfileContent, dockerignoreContent, err := agentfs.GenerateDockerArtifacts(workingDir, projectType, settingsMap, SkipPrompts(cmd))
	if err != nil {
		return err
	}

	if writeDockerfile {
		if err := os.WriteFile(dockerfilePath, dockerfileContent, 0644); err != nil {
			return err
		}

		out.Statusf("Wrote new %s", util.Accented("Dockerfile"))
	}

	if writeDockerignore {
		if err := os.WriteFile(dockerignorePath, dockerignoreContent, 0644); err != nil {
			return err
		}
		out.Statusf("Wrote new %s", util.Accented(".dockerignore"))
	}

	return nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "---"
	}
	return t.Format(time.RFC3339)
}
