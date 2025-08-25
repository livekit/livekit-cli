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
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/twitchtv/twirp"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/util"
	lkproto "github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

const (
	cloudAgentsBetaSignupURL = "https://forms.gle/GkGNNTiMt2qyfnu78"
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

	logTypeFlag = &cli.StringFlag{
		Name:     "log-type",
		Usage:    "Type of logs to retrieve. Valid values are 'deploy' and 'build'",
		Value:    "deploy",
		Required: false,
	}

	regionFlag = &cli.StringSliceFlag{
		Name:     "regions",
		Usage:    "Region(s) to deploy the agent to. If unset, will deploy to the nearest region.",
		Required: false,
		Hidden:   true,
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
					Name:   "create",
					Usage:  "Create a new LiveKit Cloud Agent",
					Action: createAgent,
					Before: createAgentClient,
					Flags: []cli.Flag{
						secretsFlag,
						secretsFileFlag,
						silentFlag,
						regionFlag,
						skipSDKCheckFlag,
					},
					// NOTE: since secrets may contain commas, or indeed any special character we might want to treat as a flag separator,
					// we disable it entirely here and require multiple --secrets flags to be used.
					DisableSliceFlagSeparator: true,
					ArgsUsage:                 "[working-dir]",
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
						secretsFlag,
						secretsFileFlag,
						skipSDKCheckFlag,
					},
					// NOTE: since secrets may contain commas, or indeed any special character we might want to treat as a flag separator,
					// we disable it entirely here and require multiple --secrets flags to be used.
					DisableSliceFlagSeparator: true,
					ArgsUsage:                 "[working-dir]",
				},
				{
					Name:   "status",
					Usage:  "Get the status of an agent",
					Before: createAgentClient,
					Action: getAgentStatus,
					Flags: []cli.Flag{
						idFlag(false),
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
					},
				},
				{
					Name:   "secrets",
					Usage:  "List secrets for an agent",
					Before: createAgentClient,
					Action: listAgentSecrets,
					Flags: []cli.Flag{
						idFlag(false),
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
			},
		},
	}
	subdomainPattern = regexp.MustCompile(`^(?:https?|wss?)://([^.]+)\.`)
	agentsClient     *lksdk.AgentClient
	ignoredSecrets   = []string{
		"LIVEKIT_API_KEY",
		"LIVEKIT_API_SECRET",
		"LIVEKIT_URL",
	}
)

func createAgentClient(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	var err error

	if _, err := requireProject(ctx, cmd); err != nil {
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

	agentsClient, err = lksdk.NewAgentClient(project.URL, project.APIKey, project.APISecret)
	if err != nil {
		return ctx, err
	}
	return ctx, nil
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
		if err := huh.NewForm(huh.NewGroup(huh.NewConfirm().
			Title(fmt.Sprintf("Use [%s] (%s) to create agent deployment?", project.Name, project.URL)).
			Value(&useProject).
			Inline(false).
			WithTheme(util.Theme))).
			Run(); err != nil {
			return err
		}
		if !useProject {
			if _, err := selectProject(ctx, cmd); err != nil {
				return err
			}
			var err error
			// Recreate the client with the new project
			agentsClient, err = lksdk.NewAgentClient(project.URL, project.APIKey, project.APISecret)
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

	silent := cmd.Bool("silent")

	if configExists && lkConfig.Agent != nil {
		if !silent {
			fmt.Printf("Using agent configuration [%s]\n", util.Accented(tomlFilename))
		}
	} else {
		lkConfig = config.NewLiveKitTOML(subdomainMatches[1]).WithDefaultAgent()
	}
	if !silent {
		fmt.Printf("Creating new agent\n")
	}

	regions := cmd.StringSlice("regions")
	if len(regions) != 0 {
		lkConfig.Agent.Regions = regions
	}

	secrets, err := requireSecrets(ctx, cmd, false, false)
	if err != nil {
		return err
	}

	settingsMap, err := getClientSettings(ctx, cmd.Bool("silent"))
	if err != nil {
		return err
	}

	projectType, err := agentfs.DetectProjectType(workingDir)
	if err != nil {
		return fmt.Errorf("unable to determine project type: %w, please use a supported project type, or create your own Dockerfile in the current directory", err)
	}

	if err := requireDockerfile(ctx, cmd, workingDir, projectType, settingsMap); err != nil {
		return err
	}

	if err := agentfs.CheckSDKVersion(workingDir, projectType, settingsMap); err != nil {
		if cmd.Bool("skip-sdk-check") {
			fmt.Printf("Error checking SDK version: %v, skipping...\n", err)
		} else {
			return err
		}
	}

	req := &lkproto.CreateAgentRequest{
		Secrets: secrets,
		Regions: lkConfig.Agent.Regions,
	}

	resp, err := agentsClient.CreateAgent(ctx, req)
	if err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			if twerr.Code() == twirp.PermissionDenied {
				return fmt.Errorf("agent hosting is disabled for this project -- join the beta program here [%s]", cloudAgentsBetaSignupURL)
			}
		}
		return err
	}

	lkConfig.Agent.ID = resp.AgentId
	if err := lkConfig.SaveTOMLFile(workingDir, tomlFilename); err != nil {
		return err
	}

	err = agentfs.UploadTarball(workingDir, resp.PresignedUrl, []string{config.LiveKitTOMLFile})
	if err != nil {
		return err
	}

	fmt.Printf("Created agent with ID [%s]\n", util.Accented(resp.AgentId))
	err = agentfs.Build(ctx, resp.AgentId, project)
	if err != nil {
		return err
	}

	fmt.Println("Build completed - You can view build logs later with `lk agent logs --log-type=build`")

	if !silent {
		var viewLogs bool = true
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("Agent deploying. Would you like to view logs?").
					Description("You can view logs later with `lk agent logs`").
					Value(&viewLogs).
					WithTheme(util.Theme),
			),
		).Run(); err != nil {
			return err
		} else if viewLogs {
			fmt.Println("Tailing runtime logs...safe to exit at any time")
			return agentfs.LogHelper(ctx, lkConfig.Agent.ID, "deploy", project)
		}
	}
	return nil
}

func createAgentConfig(ctx context.Context, cmd *cli.Command) error {
	if _, err := os.Stat(tomlFilename); err == nil {
		var overwrite bool
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(
						fmt.Sprintf("Config file [%s] file already exists. Overwrite?", tomlFilename),
					).
					Value(&overwrite).
					WithTheme(util.Theme),
			),
		).
			Run(); err != nil {
			return err
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
			if twerr.Code() == twirp.PermissionDenied {
				return fmt.Errorf("agent hosting is disabled for this project -- join the beta program here [%s]", cloudAgentsBetaSignupURL)
			}
		}
		return err
	}
	if len(response.Agents) == 0 {
		return fmt.Errorf("agent not found")
	}

	subdomainPattern := regexp.MustCompile(`^(?:https?|wss?)://([^.]+)\.`)
	matches := subdomainPattern.FindStringSubmatch(project.URL)
	if len(matches) < 1 {
		return fmt.Errorf("invalid project URL: %s", project.URL)
	}

	var regions []string
	for _, regionalAgent := range response.Agents[0].AgentDeployments {
		regions = append(regions, regionalAgent.Region)
	}

	agent := response.Agents[0]
	lkConfig := config.NewLiveKitTOML(matches[1])
	lkConfig.Agent = &config.LiveKitTOMLAgentConfig{
		ID:      agent.AgentId,
		Regions: regions,
	}

	if err := lkConfig.SaveTOMLFile(workingDir, tomlFilename); err != nil {
		return err
	}
	return nil
}

func deployAgent(ctx context.Context, cmd *cli.Command) error {
	var req *lkproto.DeployAgentRequest

	agentId, err := getAgentID(ctx, cmd, workingDir, tomlFilename, false)
	if err != nil {
		return err
	}

	req = &lkproto.DeployAgentRequest{
		AgentId: agentId,
	}

	secrets, err := requireSecrets(ctx, cmd, false, true)
	if err != nil {
		return err
	}
	if len(secrets) > 0 {
		req.Secrets = secrets
	}

	projectType, err := agentfs.DetectProjectType(workingDir)
	if err != nil {
		return fmt.Errorf("unable to determine project type: %w, please use a supported project type, or create your own Dockerfile in the current directory", err)
	}

	settingsMap, err := getClientSettings(ctx, cmd.Bool("silent"))
	if err != nil {
		return err
	}

	if err := agentfs.CheckSDKVersion(workingDir, projectType, settingsMap); err != nil {
		if cmd.Bool("skip-sdk-check") {
			fmt.Printf("Error checking SDK version: %v, skipping...\n", err)
		} else {
			return err
		}
	}

	resp, err := agentsClient.DeployAgent(ctx, req)
	if err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			if twerr.Code() == twirp.PermissionDenied {
				return fmt.Errorf("agent hosting is disabled for this project -- join the beta program here [%s]", cloudAgentsBetaSignupURL)
			}
		}
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to deploy agent: %s", resp.Message)
	}

	presignedUrl := resp.PresignedUrl
	err = agentfs.UploadTarball(workingDir, presignedUrl, []string{config.LiveKitTOMLFile})
	if err != nil {
		return err
	}

	fmt.Printf("Updated agent [%s]\n", util.Accented(resp.AgentId))
	err = agentfs.Build(ctx, resp.AgentId, project)
	if err != nil {
		return err
	}

	fmt.Println("Deployed agent")
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
			if twerr.Code() == twirp.PermissionDenied {
				return fmt.Errorf("agent hosting is disabled for this project -- join the beta program here [%s]", cloudAgentsBetaSignupURL)
			}
		}
		return err
	}

	if len(res.Agents) == 0 {
		return fmt.Errorf("no agents found")
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

			rows = append(rows, []string{
				agent.AgentId,
				agent.Version,
				regionalAgent.Region,
				regionalAgent.Status,
				fmt.Sprintf("%s / %s", curCPU, regionalAgent.CpuLimit),
				fmt.Sprintf("%s / %s", curMem, memLimit),
				fmt.Sprintf("%d / %d / %d", regionalAgent.Replicas, regionalAgent.MinReplicas, regionalAgent.MaxReplicas),
				agent.DeployedAt.AsTime().Format(time.RFC3339),
			})
		}
	}

	t := util.CreateTable().
		Headers("ID", "Version", "Region", "Status", "CPU", "Mem", "Replicas", "Deployed At").
		Rows(rows...)

	fmt.Println(t)
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

	fmt.Printf("Restarted agent [%s]\n", util.Accented(agentID))
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

	regions := cmd.StringSlice("regions")
	if len(regions) != 0 {
		lkConfig.Agent.Regions = regions
	}

	req := &lkproto.UpdateAgentRequest{
		AgentId: lkConfig.Agent.ID,
		Regions: lkConfig.Agent.Regions,
	}

	secrets, err := requireSecrets(ctx, cmd, false, true)
	if err != nil {
		return err
	}
	if len(secrets) > 0 {
		req.Secrets = secrets
	}

	var resp *lkproto.UpdateAgentResponse
	err = util.Await("Updating agent ["+util.Accented(lkConfig.Agent.ID)+"]", ctx, func(ctx context.Context) error {
		var clientErr error
		resp, clientErr = agentsClient.UpdateAgent(ctx, req)
		return clientErr
	})
	if err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			if twerr.Code() == twirp.PermissionDenied {
				return fmt.Errorf("agent hosting is disabled for this project -- join the beta program here [%s]", cloudAgentsBetaSignupURL)
			}
		}
		return err
	}

	if resp.Success {
		fmt.Printf("Updated agent [%s]\n", util.Accented(lkConfig.Agent.ID))
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
	err = util.Await("Rolling back agent ["+util.Accented(agentID)+"]", ctx, func(ctx context.Context) error {
		var clientErr error
		resp, clientErr = agentsClient.RollbackAgent(ctx, &lkproto.RollbackAgentRequest{
			AgentId: agentID,
			Version: cmd.String("version"),
		})
		return clientErr
	})

	if err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			if twerr.Code() == twirp.PermissionDenied {
				return fmt.Errorf("agent hosting is disabled for this project -- join the beta program here [%s]", cloudAgentsBetaSignupURL)
			}
		}
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to rollback agent %s", resp.Message)
	}

	fmt.Printf("Rolled back agent [%s] to version [%s]\n", util.Accented(agentID), util.Accented(cmd.String("version")))

	return nil
}

func getLogs(ctx context.Context, cmd *cli.Command) error {
	agentID, err := getAgentID(ctx, cmd, workingDir, tomlFilename, true)
	if err != nil {
		return err
	}
	err = agentfs.LogHelper(ctx, agentID, cmd.String("log-type"), project)
	return err
}

func deleteAgent(ctx context.Context, cmd *cli.Command) error {
	agentID, err := getAgentID(ctx, cmd, workingDir, tomlFilename, false)
	if err != nil {
		return err
	}

	var confirmDelete bool
	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Are you sure you want to delete agent [%s]?", agentID)).
				Value(&confirmDelete).
				Inline(false).
				WithTheme(util.Theme),
		),
	).Run(); err != nil {
		return err
	}

	if !confirmDelete {
		return nil
	}

	var res *lkproto.DeleteAgentResponse
	err = util.Await(
		"Deleting agent ["+util.Accented(agentID)+"]",
		ctx,
		func(ctx context.Context) error {
			var clientErr error
			res, clientErr = agentsClient.DeleteAgent(ctx, &lkproto.DeleteAgentRequest{
				AgentId: agentID,
			})
			return clientErr
		},
	)

	if err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			if twerr.Code() == twirp.PermissionDenied {
				return fmt.Errorf("agent hosting is disabled for this project -- join the beta program here [%s]", cloudAgentsBetaSignupURL)
			}
		}
		return err
	}

	if !res.Success {
		return fmt.Errorf("failed to delete agent %s", res.Message)
	}

	fmt.Printf("Deleted agent [%s]\n", util.Accented(agentID))
	return nil
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
			if twerr.Code() == twirp.PermissionDenied {
				return fmt.Errorf("agent hosting is disabled for this project -- join the beta program here [%s]", cloudAgentsBetaSignupURL)
			}
		}
		return err
	}

	table := util.CreateTable().
		Headers("Version", "Current", "Created At", "Deployed At")

	// Sort versions by created date descending
	slices.SortFunc(versions.Versions, func(a, b *lkproto.AgentVersion) int {
		return b.CreatedAt.AsTime().Compare(a.CreatedAt.AsTime())
	})
	for _, version := range versions.Versions {
		table.Row(
			version.Version,
			fmt.Sprintf("%t", version.Current),
			version.CreatedAt.AsTime().Format(time.RFC3339),
			version.DeployedAt.AsTime().Format(time.RFC3339),
		)
	}

	fmt.Println(table)
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
					if twerr.Code() == twirp.PermissionDenied {
						return fmt.Errorf("agent hosting is disabled for this project -- join the beta program here [%s]", cloudAgentsBetaSignupURL)
					}
				}
				return err
			}
			items = append(items, res.Agents...)
		}
	} else {
		agents, err := agentsClient.ListAgents(ctx, &lkproto.ListAgentsRequest{})
		if err != nil {
			if twerr, ok := err.(twirp.Error); ok {
				if twerr.Code() == twirp.PermissionDenied {
					return fmt.Errorf("agent hosting is disabled for this project -- join the beta program here [%s]", cloudAgentsBetaSignupURL)
				}
			}
			return err
		}
		items = agents.Agents
	}

	if len(items) == 0 {
		fmt.Println("No agents found")
		return nil
	}

	slices.SortFunc(items, func(a, b *lkproto.AgentInfo) int {
		return b.DeployedAt.AsTime().Compare(a.DeployedAt.AsTime())
	})

	var rows [][]string
	for _, agent := range items {
		var regions []string
		for _, regionalAgent := range agent.AgentDeployments {
			regions = append(regions, regionalAgent.Region)
		}
		rows = append(rows, []string{
			agent.AgentId,
			strings.Join(regions, ","),
			agent.Version,
			agent.DeployedAt.AsTime().Format(time.RFC3339),
		})
	}

	t := util.CreateTable().
		Headers("ID", "Regions", "Version", "Deployed At").
		Rows(rows...)

	fmt.Println(t)
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
			if twerr.Code() == twirp.PermissionDenied {
				return fmt.Errorf("agent hosting is disabled for this project -- join the beta program here [%s]", cloudAgentsBetaSignupURL)
			}
		}
		return err
	}

	table := util.CreateTable().
		Headers("Name", "Created At", "Updated At")

	for _, secret := range secrets.Secrets {
		// NOTE: Maybe these should be omitted on the server side?
		if slices.Contains(ignoredSecrets, secret.Name) {
			continue
		}
		table.Row(secret.Name, secret.CreatedAt.AsTime().Format(time.RFC3339), secret.UpdatedAt.AsTime().Format(time.RFC3339))
	}

	fmt.Println(table)
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
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("This will remove all existing secrets. Are you sure you want to proceed [%s]?", agentID)).
					Value(&confirmOverwrite).
					Inline(false).
					WithTheme(util.Theme),
			),
		).Run(); err != nil {
			return err
		}
	}

	if !confirmOverwrite {
		return nil
	}

	req := &lkproto.UpdateAgentSecretsRequest{
		AgentId:   agentID,
		Secrets:   secrets,
		Overwrite: cmd.Bool("overwrite"),
	}

	resp, err := agentsClient.UpdateAgentSecrets(ctx, req)
	if err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			if twerr.Code() == twirp.PermissionDenied {
				return fmt.Errorf("agent hosting is disabled for this project -- join the beta program here [%s]", cloudAgentsBetaSignupURL)
			}
		}
		return err
	}

	if resp.Success {
		fmt.Println("Updated agent secrets")
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

	fmt.Printf("Using agent [%s]\n", util.Accented(agentID))

	return agentID, nil
}

func selectAgent(ctx context.Context, _ *cli.Command, excludeEmptyVersion bool) (string, error) {
	var agents *lkproto.ListAgentsResponse

	err := util.Await("No agent ID provided, selecting from available agents...", ctx, func(ctx context.Context) error {
		var clientErr error
		agents, clientErr = agentsClient.ListAgents(ctx, &lkproto.ListAgentsRequest{})
		return clientErr
	})
	if err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			if twerr.Code() == twirp.PermissionDenied {
				return "", fmt.Errorf("agent hosting is disabled for this project -- join the beta program here [%s]", cloudAgentsBetaSignupURL)
			}
		}
		return "", err
	}

	if len(agents.Agents) == 0 {
		return "", fmt.Errorf("no agents found")
	}

	var agentNames []huh.Option[string]
	for _, agent := range agents.Agents {
		if excludeEmptyVersion && agent.Version == "---" {
			continue
		}
		name := agent.AgentId + " " + util.Dimmed("deployed "+agent.DeployedAt.AsTime().Format(time.RFC3339))
		agentNames = append(agentNames, huh.Option[string]{Key: name, Value: agent.AgentId})
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
	silent := cmd.Bool("silent")
	secrets := make(map[string]*lkproto.AgentSecret)

	if values, err := parseKeyValuePairs(cmd, "secrets"); err != nil {
		return nil, fmt.Errorf("failed to parse secrets: %w", err)
	} else {
		for key, val := range values {
			agentSecret := &lkproto.AgentSecret{
				Name:  key,
				Value: []byte(val),
			}
			secrets[key] = agentSecret
		}

	}

	shouldReadFromDisk := cmd.IsSet("secrets-file") || !lazy || (required && len(secrets) == 0)
	if shouldReadFromDisk {
		file, env, err := agentfs.DetectEnvFile(cmd.String("secrets-file"))
		if err != nil {
			return nil, err
		}
		if file != "" && !silent {
			fmt.Printf("Using secrets file [%s]\n", util.Accented(file))
		}

		for k, v := range env {
			if _, exists := secrets[k]; exists {
				continue
			}

			secret := &lkproto.AgentSecret{
				Name:  k,
				Value: []byte(v),
			}
			secrets[k] = secret
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

	if !dockerfileExists {
		if !cmd.Bool("silent") {
			err := util.Await(
				"Creating Dockerfile...",
				ctx,
				func(ctx context.Context) error {
					return agentfs.CreateDockerfile(workingDir, projectType, settingsMap)
				},
			)
			if err != nil {
				return err
			}
			fmt.Println("Created [" + util.Accented("Dockerfile") + "]")
		} else {
			if err := agentfs.CreateDockerfile(workingDir, projectType, settingsMap); err != nil {
				return err
			}
		}
	} else {
		if !cmd.Bool("silent") {
			fmt.Println("Using existing Dockerfile")
		}
	}

	return nil
}

func getClientSettings(ctx context.Context, silent bool) (map[string]string, error) {
	var clientSettingsResponse *lkproto.ClientSettingsResponse
	var err error

	if !silent {
		err = util.Await(
			"Loading client settings...",
			ctx,
			func(ctx context.Context) error {
				clientSettingsResponse, err = agentsClient.GetClientSettings(ctx, &lkproto.ClientSettingsRequest{})
				return err
			},
		)
	} else {
		clientSettingsResponse, err = agentsClient.GetClientSettings(ctx, &lkproto.ClientSettingsRequest{})
	}

	if err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			if twerr.Code() == twirp.PermissionDenied {
				return nil, fmt.Errorf("agent hosting is disabled for this project -- join the beta program here [%s]", cloudAgentsBetaSignupURL)
			}
		}
		return nil, err
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

func requireConfig(workingDir, tomlFilename string) (bool, error) {
	if lkConfig != nil {
		return true, nil
	}

	var exists bool
	var err error
	lkConfig, exists, err = config.LoadTOMLFile(workingDir, tomlFilename)
	return exists, err
}
