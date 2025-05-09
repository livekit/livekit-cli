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
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
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
	nameFlag = func(required bool) *cli.StringFlag {
		return &cli.StringFlag{
			Name:     "name",
			Usage:    fmt.Sprintf("`NAME` of the agent. If unset, and the %s file is present, will use the name found there.", config.LiveKitTOMLFile),
			Required: required,
		}
	}

	nameSliceFlag = &cli.StringSliceFlag{
		Name:     "name",
		Usage:    "`NAMES` of agent(s)",
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
						nameFlag(false),
						secretsFlag,
						secretsFileFlag,
						&cli.BoolFlag{
							Name:     "silent",
							Usage:    "If set, will not prompt for confirmation",
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
						nameFlag(false),
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
					},
					ArgsUsage: "[working-dir]",
				},
				{
					Name:   "status",
					Usage:  "Get the status of an agent",
					Before: createAgentClient,
					Action: getAgentStatus,
					Flags: []cli.Flag{
						nameFlag(false),
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
						nameFlag(false),
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
						nameFlag(false),
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
						nameFlag(false),
					},
					ArgsUsage: "[working-dir]",
				},
				{
					Name:   "versions",
					Usage:  "List versions of an agent",
					Before: createAgentClient,
					Action: listAgentVersions,
					Flags: []cli.Flag{
						nameFlag(false),
					},
					ArgsUsage: "[working-dir]",
				},
				{
					Name:   "list",
					Usage:  "List all LiveKit Cloud Agents",
					Action: listAgents,
					Before: createAgentClient,
					Flags: []cli.Flag{
						nameSliceFlag,
					},
				},
				{
					Name:   "secrets",
					Usage:  "List secrets for an agent",
					Before: createAgentClient,
					Action: listAgentSecrets,
					Flags: []cli.Flag{
						nameFlag(false),
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
						nameFlag(false),
						&cli.BoolFlag{
							Name:     "overwrite",
							Usage:    "If set, will overwrite existing secrets",
							Required: false,
							Value:    false,
						},
					},
					ArgsUsage: "[working-dir]",
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
	var lkConfig *config.LiveKitTOML

	if _, err := requireProject(ctx, cmd); err != nil {
		return ctx, err
	}

	if cmd.NArg() > 0 {
		workingDir = cmd.Args().First()
	}

	// Verify that the project and agent config match, if it exists.
	lkConfig, configExists, err := config.LoadTomlFile(workingDir, tomlFilename)
	if err != nil {
		fmt.Println(err.Error())
	}
	if configExists {
		projectSubdomainMatch := subdomainPattern.FindStringSubmatch(project.URL)
		if len(projectSubdomainMatch) < 2 {
			return nil, fmt.Errorf("invalid project URL [%s]", project.URL)
		}
		if projectSubdomainMatch[1] != lkConfig.Project.Subdomain {
			return nil, fmt.Errorf("project does not match agent subdomain [%s]", lkConfig.Project.Subdomain)
		}
	} else {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}

	agentsClient, err = lksdk.NewAgentClient(project.URL, project.APIKey, project.APISecret)
	return nil, err
}

func createAgent(ctx context.Context, cmd *cli.Command) error {
	subdomainMatches := subdomainPattern.FindStringSubmatch(project.URL)
	if len(subdomainMatches) < 2 {
		return fmt.Errorf("invalid project URL [%s]", project.URL)
	}

	// We have a configured project, but don't need to double-confirm if it was
	// set via a command line flag, because intent it clear.
	if !cmd.IsSet("project") {
		useProject := true
		if err := huh.NewForm(huh.NewGroup(huh.NewConfirm().
			Title(fmt.Sprintf("Use project [%s] with subdomain [%s] to create agent?", project.Name, subdomainMatches[1])).
			Value(&useProject).
			Inline(false).
			WithTheme(util.Theme))).
			Run(); err != nil {
			return err
		}
		if !useProject {
			return fmt.Errorf("cancelled")
		}
	}

	logger.Debugw("Creating agent", "working-dir", workingDir)
	lkConfig, configExists, err := config.LoadTomlFile(workingDir, tomlFilename)
	if err != nil && configExists {
		return err
	}

	name := cmd.String("name")
	silent := cmd.Bool("silent")

	if configExists && lkConfig.Agent != nil {
		// If name was set via command line, it must match the name in the config.
		if name != "" && lkConfig.Agent.Name != name {
			return fmt.Errorf("agent name passed in command line: [%s] does not match name in [%s]: [%s]", name, tomlFilename, lkConfig.Agent.Name)
		}

		if !silent {
			fmt.Printf("Using agent configuration [%s]\n", util.Accented(tomlFilename))
		}
	} else {
		// If name was not set via command line, prompt for it.
		if name == "" {
			if silent {
				return fmt.Errorf("agent name is required")
			} else {
				if err := huh.NewInput().
					Title("Agent name").
					Value(&name).
					WithTheme(util.Theme).
					Run(); err != nil {
					return err
				}
			}
		}

		f, err := os.Create(filepath.Join(workingDir, tomlFilename))
		if err != nil {
			return err
		}
		defer f.Close()

		lkConfig = config.NewLiveKitTOML(subdomainMatches[1]).
			WithDefaultAgent(name)

		encoder := toml.NewEncoder(f)
		if err := encoder.Encode(lkConfig); err != nil {
			return fmt.Errorf("error encoding TOML: %w", err)
		}
		fmt.Printf("Creating config file [%s]\n", util.Accented(tomlFilename))
	}

	if name == "" {
		if lkConfig.Agent.Name == "" {
			return fmt.Errorf("name is required")
		}
	} else {
		lkConfig.Agent.Name = name
	}
	if !silent {
		fmt.Printf("Creating agent [%s]\n", util.Accented(lkConfig.Agent.Name))
	}

	secrets, err := requireSecrets(ctx, cmd, true, false)
	if err != nil {
		return err
	}

	if err := requireDockerfile(ctx, cmd, workingDir); err != nil {
		return err
	}

	req := &lkproto.CreateAgentRequest{
		AgentName:   lkConfig.Agent.Name,
		Secrets:     secrets,
		Replicas:    int32(lkConfig.Agent.Replicas),
		MaxReplicas: int32(lkConfig.Agent.MaxReplicas),
		CpuReq:      string(lkConfig.Agent.CPU),
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

	err = agentfs.UploadTarball(workingDir, resp.PresignedUrl, []string{config.LiveKitTOMLFile})
	if err != nil {
		return err
	}

	fmt.Printf("Created agent [%s] with ID [%s]\n", util.Accented(resp.AgentName), util.Accented(resp.AgentId))
	err = agentfs.Build(ctx, resp.AgentId, lkConfig.Agent.Name, "deploy", project)
	if err != nil {
		return err
	}

	fmt.Println("Build completed")

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
			fmt.Println("Tailing logs...safe to exit at any time")
			return agentfs.LogHelper(ctx, "", lkConfig.Agent.Name, "deploy", project)
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
			return fmt.Errorf("config file [%s] already exists", tomlFilename)
		}
	}

	name := cmd.String("name")
	if name == "" {
		if err := huh.NewInput().
			Title("Agent name").
			Value(&name).
			WithTheme(util.Theme).
			Run(); err != nil {
			return err
		} else if name == "" {
			return fmt.Errorf("name is required")
		}
	}

	response, err := agentsClient.ListAgents(ctx, &lkproto.ListAgentsRequest{
		AgentName: name,
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

	agent := response.Agents[0]
	regionAgent := agent.AgentDeployments[0]
	lkConfig := config.NewLiveKitTOML(matches[1])
	lkConfig.Agent = &config.LiveKitTOMLAgentConfig{
		Name:        agent.AgentName,
		CPU:         config.CPUString(regionAgent.CpuReq),
		Replicas:    int(regionAgent.Replicas),
		MaxReplicas: int(regionAgent.MaxReplicas),
	}

	f, err := os.Create(tomlFilename)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := toml.NewEncoder(f).Encode(lkConfig); err != nil {
		return fmt.Errorf("error encoding TOML: %w", err)
	}

	fmt.Printf("Created config file [%s]\n", util.Accented(tomlFilename))
	return nil
}

func deployAgent(ctx context.Context, cmd *cli.Command) error {
	lkConfig, configExists, err := config.LoadTomlFile(workingDir, tomlFilename)
	if err != nil && configExists {
		return err
	}
	if !configExists {
		return fmt.Errorf("config file [%s] required to update agent", util.Accented(tomlFilename))
	}
	if !lkConfig.HasAgent() {
		return fmt.Errorf("no agent config found in [%s]", tomlFilename)
	}

	req := &lkproto.DeployAgentRequest{
		AgentName:   lkConfig.Agent.Name,
		Replicas:    int32(lkConfig.Agent.Replicas),
		CpuReq:      string(lkConfig.Agent.CPU),
		MaxReplicas: int32(lkConfig.Agent.MaxReplicas),
	}

	secrets, err := requireSecrets(ctx, cmd, false, true)
	if err != nil {
		return err
	}
	if len(secrets) > 0 {
		req.Secrets = secrets
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
	err = agentfs.Build(ctx, resp.AgentId, lkConfig.Agent.Name, "update", project)
	if err != nil {
		return err
	}

	fmt.Println("Deployed agent")
	return nil
}

func getAgentStatus(ctx context.Context, cmd *cli.Command) error {
	agentName, err := getAgentName(cmd, workingDir, tomlFilename)
	if err != nil {
		return err
	}

	res, err := agentsClient.ListAgents(ctx, &lkproto.ListAgentsRequest{
		AgentName: agentName,
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

			memReq, err := agentfs.ParseMem(regionalAgent.MemReq, true)
			if err != nil {
				logger.Errorw("error parsing mem req", err)
			}

			rows = append(rows, []string{
				regionalAgent.Region,
				regionalAgent.Status,
				fmt.Sprintf("%.4g / %s", curCPU, regionalAgent.CpuReq),
				fmt.Sprintf("%s / %s", curMem, memReq),
				fmt.Sprintf("%d / %d / %d", regionalAgent.Replicas, regionalAgent.MinReplicas, regionalAgent.MaxReplicas),
				agent.DeployedAt.AsTime().Format(time.RFC3339),
			})
		}
	}

	t := util.CreateTable().
		Headers("Region", "Status", "CPU", "Mem", "Replicas/Min/Max", "Deployed At").
		Rows(rows...)

	fmt.Println(t)
	return nil
}

func updateAgent(ctx context.Context, cmd *cli.Command) error {
	lkConfig, configExists, err := config.LoadTomlFile(workingDir, tomlFilename)
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
		AgentName:   lkConfig.Agent.Name,
		Replicas:    int32(lkConfig.Agent.Replicas),
		CpuReq:      string(lkConfig.Agent.CPU),
		MaxReplicas: int32(lkConfig.Agent.MaxReplicas),
	}

	secrets, err := requireSecrets(ctx, cmd, false, true)
	if err != nil {
		return err
	}
	if len(secrets) > 0 {
		req.Secrets = secrets
	}

	resp, err := agentsClient.UpdateAgent(ctx, req)
	if err != nil {
		if twerr, ok := err.(twirp.Error); ok {
			if twerr.Code() == twirp.PermissionDenied {
				return fmt.Errorf("agent hosting is disabled for this project -- join the beta program here [%s]", cloudAgentsBetaSignupURL)
			}
		}
		return err
	}

	if resp.Success {
		fmt.Printf("Updated agent [%s]\n", util.Accented(lkConfig.Agent.Name))
		return nil
	}

	return fmt.Errorf("failed to update agent: %s", resp.Message)
}

func rollbackAgent(ctx context.Context, cmd *cli.Command) error {
	agentName, err := getAgentName(cmd, workingDir, tomlFilename)
	if err != nil {
		return err
	}

	resp, err := agentsClient.RollbackAgent(ctx, &lkproto.RollbackAgentRequest{
		AgentName: agentName,
		Version:   cmd.String("version"),
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

	fmt.Printf("Rolled back agent [%s] to version %s\n", util.Accented(agentName), cmd.String("version"))

	return nil
}

func getLogs(ctx context.Context, cmd *cli.Command) error {
	agentName, err := getAgentName(cmd, workingDir, tomlFilename)
	if err != nil {
		return err
	}
	err = agentfs.LogHelper(ctx, "", agentName, cmd.String("log-type"), project)
	return err
}

func deleteAgent(ctx context.Context, cmd *cli.Command) error {
	agentName, err := getAgentName(cmd, workingDir, tomlFilename)
	if err != nil {
		return err
	}

	var confirmDelete bool
	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Are you sure you want to delete agent [%s]?", agentName)).
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

	resp, err := agentsClient.DeleteAgent(ctx, &lkproto.DeleteAgentRequest{
		AgentName: agentName,
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
		return fmt.Errorf("failed to delete agent %s", resp.Message)
	}

	fmt.Printf("Deleted agent [%s]\n", util.Accented(agentName))
	return nil
}

func listAgentVersions(ctx context.Context, cmd *cli.Command) error {
	agentName, err := getAgentName(cmd, workingDir, tomlFilename)
	if err != nil {
		return err
	}

	req := &lkproto.ListAgentVersionsRequest{
		AgentName: agentName,
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
		Headers("Version", "Current", "Created At")

	for _, version := range versions.Versions {
		table.Row(version.Version, fmt.Sprintf("%t", version.Current), fmt.Sprintf("%v", version.CreatedAt.AsTime().Format(time.RFC3339)))
	}

	fmt.Println(table)
	return nil
}

func listAgents(ctx context.Context, cmd *cli.Command) error {
	var items []*lkproto.AgentInfo
	req := &lkproto.ListAgentsRequest{}
	if cmd.IsSet("name") {
		for _, n := range cmd.StringSlice("name") {
			if n == "" {
				continue
			}
			res, err := agentsClient.ListAgents(ctx, &lkproto.ListAgentsRequest{
				AgentName: n,
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
		agents, err := agentsClient.ListAgents(ctx, req)
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

	var rows [][]string
	for _, agent := range items {
		var regions []string
		for _, regionalAgent := range agent.AgentDeployments {
			regions = append(regions, regionalAgent.Region)
		}
		rows = append(rows, []string{agent.AgentId, agent.AgentName, strings.Join(regions, ",")})
	}

	t := util.CreateTable().
		Headers("ID", "Name", "Regions").
		Rows(rows...)

	fmt.Println(t)
	return nil
}

func listAgentSecrets(ctx context.Context, cmd *cli.Command) error {
	agentName, err := getAgentName(cmd, workingDir, tomlFilename)
	if err != nil {
		return err
	}

	req := &lkproto.ListAgentSecretsRequest{
		AgentName: agentName,
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
	agentName, err := getAgentName(cmd, workingDir, tomlFilename)
	if err != nil {
		return err
	}

	secrets, err := requireSecrets(ctx, cmd, true, true)
	if err != nil {
		return err
	}

	req := &lkproto.UpdateAgentSecretsRequest{
		AgentName: agentName,
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

func getAgentName(cmd *cli.Command, agentDir string, tomlFileName string) (string, error) {
	agentName := cmd.String("name")
	if agentName == "" {
		lkConfig, configExists, err := config.LoadTomlFile(agentDir, tomlFileName)
		if err != nil && configExists {
			return "", err
		}
		if !configExists {
			return "", fmt.Errorf("config file [%s] required to update agent", tomlFileName)
		}
		if !lkConfig.HasAgent() {
			return "", fmt.Errorf("no agent config found in [%s]", tomlFileName)
		}

		agentName = lkConfig.Agent.Name
	}

	if agentName == "" {
		// shouldn't happen, but check to ensure we have a name
		return "", fmt.Errorf("agent name or %s required", tomlFileName)
	}

	return agentName, nil
}

func requireSecrets(_ context.Context, cmd *cli.Command, required, lazy bool) ([]*lkproto.AgentSecret, error) {
	silent := cmd.Bool("silent")
	secrets := make(map[string]*lkproto.AgentSecret)
	for _, secret := range cmd.StringSlice("secrets") {
		secret := strings.Split(secret, "=")
		agentSecret := &lkproto.AgentSecret{
			Name:  secret[0],
			Value: []byte(secret[1]),
		}
		secrets[secret[0]] = agentSecret
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

func requireDockerfile(ctx context.Context, cmd *cli.Command, workingDir string) error {
	dockerfileExists, err := agentfs.HasDockerfile(workingDir)
	if err != nil {
		return err
	}

	if !dockerfileExists {
		if !cmd.Bool("silent") {
			fmt.Println("Creating Dockerfile")
		}

		clientSettingsResponse, err := agentsClient.GetClientSettings(ctx, &lkproto.ClientSettingsRequest{})
		if err != nil {
			if twerr, ok := err.(twirp.Error); ok {
				if twerr.Code() == twirp.PermissionDenied {
					return fmt.Errorf("agent hosting is disabled for this project -- join the beta program here [%s]", cloudAgentsBetaSignupURL)
				}
			}
			return err
		}

		settingsMap := make(map[string]string)
		for _, setting := range clientSettingsResponse.Params {
			settingsMap[setting.Name] = setting.Value
		}

		if err := agentfs.CreateDockerfile(workingDir, settingsMap); err != nil {
			return err
		}
	} else {
		if !cmd.Bool("silent") {
			fmt.Println("Using existing Dockerfile")
		}
	}

	return nil
}
