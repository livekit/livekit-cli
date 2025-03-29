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
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/huh"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/util"
	lkproto "github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type AgentTOML struct {
	ProjectSubdomain string `toml:"project_subdomain"`
	Name             string `toml:"name"`
	CPU              string `toml:"cpu"`
	Replicas         int    `toml:"replicas"`
	MaxReplicas      int    `toml:"max_replicas"`

	Regions []string `toml:"regions"`
}

const (
	AgentTOMLFile = "livekit.toml"

	clientDefaults_CPU         = "1"
	clientDefaults_Replicas    = 1
	clientDefaults_MaxReplicas = 10
)

var (
	tomlFlag = &cli.StringFlag{
		Name:     "toml",
		Usage:    fmt.Sprintf("TOML file to use in the working directory. Defaults to %s", AgentTOMLFile),
		Required: false,
	}

	nameFlag = func(required bool) *cli.StringFlag {
		return &cli.StringFlag{
			Name:     "name",
			Usage:    fmt.Sprintf("Name of the agent. If unset, and the %s file is present, will use the name in the %s file.", AgentTOMLFile, AgentTOMLFile),
			Required: required,
		}
	}

	nameSliceFlag = &cli.StringSliceFlag{
		Name:     "name",
		Usage:    "Name(s) of the agent",
		Required: false,
	}

	secretsFileFlag = &cli.StringFlag{
		Name:     "secrets-file",
		Usage:    "File containing secret KEY=VALUE pairs, one per line. These will be injected as environment variables into the agent.",
		Required: false,
	}

	secretsFlag = &cli.StringSliceFlag{
		Name:     "secrets",
		Usage:    "KEY=VALUE comma separated secrets. These will be injected as environment variables into the agent. These take precedence over secrets-file.",
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
						tomlFlag,
					},
					ArgsUsage: "[working-dir]",
				},
				{
					Name:   "config",
					Usage:  fmt.Sprintf("Creates a %s in the working directory for an existing agent.", AgentTOMLFile),
					Before: createAgentClient,
					Action: createAgentConfig,
					Flags: []cli.Flag{
						nameFlag(true),
						tomlFlag,
					},
					ArgsUsage: "[working-dir]",
				},
				{
					Name:   "deploy",
					Usage:  "Deploy a new version of the agent",
					Before: createAgentClient,
					Action: deployAgent,
					Flags: []cli.Flag{
						tomlFlag,
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
						tomlFlag,
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
						tomlFlag,
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
						tomlFlag,
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
						tomlFlag,
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
						tomlFlag,
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
						tomlFlag,
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
						tomlFlag,
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
						tomlFlag,
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

	agentsClient *lksdk.AgentClient
)

func createAgentClient(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	var err error
	var agentConfig *AgentTOML

	if _, err := requireProject(ctx, cmd); err != nil {
		return ctx, err
	}

	if cmd.String("toml") != "" {
		workingDir := "."
		if cmd.NArg() > 0 {
			workingDir = cmd.Args().First()
		}
		agentConfig, err = loadTomlFile(workingDir, cmd.String("toml"))
		if err != nil {
			return nil, err
		}
		cmd.Set("subdomain", agentConfig.ProjectSubdomain)
	}

	agentsClient, err = lksdk.NewAgentClient(project.URL, project.APIKey, project.APISecret)
	return nil, err
}

func loadTomlFile(dir string, tomlFileName string) (*AgentTOML, error) {
	logger.Debugw(fmt.Sprintf("loading %s file", tomlFileName))
	var agentConfig AgentTOML
	var err error
	tomlFile := filepath.Join(dir, tomlFileName)
	if _, err = os.Stat(tomlFile); err == nil {
		_, err = toml.DecodeFile(tomlFile, &agentConfig)
		if err != nil {
			return nil, err
		}
	}

	return &agentConfig, err
}

func createAgent(ctx context.Context, cmd *cli.Command) error {
	subdomainPattern := regexp.MustCompile(`^(?:https?|wss?)://([^.]+)\.`)
	subdomainMatches := subdomainPattern.FindStringSubmatch(project.URL)
	if len(subdomainMatches) < 1 {
		return fmt.Errorf("invalid project URL [%s]", project.URL)
	}

	useProject := true
	if err := huh.NewConfirm().
		Title(fmt.Sprintf("Use project [%s] with subdomain [%s] to create agent?", project.Name, subdomainMatches[1])).
		Value(&useProject).
		Inline(false).
		WithTheme(util.Theme).
		Run(); err != nil {
		return err
	}

	if !useProject {
		return fmt.Errorf("cancelled")
	}

	workingDir := "."
	if cmd.NArg() > 0 {
		workingDir = cmd.Args().First()
	}

	logger.Debugw("Creating agent", "working-dir", workingDir)
	tomlFilename := cmd.String("toml")
	if tomlFilename == "" {
		tomlFilename = AgentTOMLFile
	}
	agentConfig, err := loadTomlFile(workingDir, tomlFilename)
	var exists bool
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		exists = false
	} else {
		exists = true
	}

	name := cmd.String("name")

	if exists && !cmd.Bool("silent") {
		useExisting := true
		if err := huh.NewConfirm().
			Title(fmt.Sprintf("[%s] already exists. Would you like to use it?", tomlFilename)).
			Value(&useExisting).
			Inline(false).
			WithTheme(util.Theme).
			Run(); err != nil {
			return err
		}

		if !useExisting {
			return fmt.Errorf("[%s] already exists", tomlFilename)
		}

		if name != "" && agentConfig.Name != name {
			return fmt.Errorf("agent name passed in command line: [%s] does not match name in [%s]: [%s]", name, tomlFilename, agentConfig.Name)
		}

		logger.Debugw("using existing agent toml")
	} else if !exists && !cmd.Bool("silent") {
		if cmd.String("name") == "" {
			if err := huh.NewInput().
				Title("Agent name").
				Value(&name).
				Run(); err != nil {
				return err
			}
		}

		var createFile bool
		if err := huh.NewConfirm().
			Title(fmt.Sprintf("Config file [%s] required. Create one?", tomlFilename)).
			Description(fmt.Sprintf("Project [%s]", project.Name)).
			Value(&createFile).
			Inline(false).
			WithTheme(util.Theme).
			Run(); err != nil {
			return err
		}

		if !createFile {
			return fmt.Errorf("config file [%s] required to create agent", tomlFilename)
		}

		f, err := os.Create(filepath.Join(workingDir, tomlFilename))
		if err != nil {
			return err
		}
		defer f.Close()

		agentConfig = &AgentTOML{
			ProjectSubdomain: subdomainMatches[1],
			Name:             name,
			CPU:              clientDefaults_CPU,
			Replicas:         clientDefaults_Replicas,
			MaxReplicas:      clientDefaults_MaxReplicas,
		}

		encoder := toml.NewEncoder(f)
		if err := encoder.Encode(agentConfig); err != nil {
			return fmt.Errorf("error encoding TOML: %w", err)
		}
	}

	if name == "" {
		if agentConfig.Name == "" {
			return fmt.Errorf("name is required")
		}
	} else {
		agentConfig.Name = name
	}

	var livekitApiKey, livekitApiSecret bool
	secrets := make(map[string]*lkproto.AgentSecret)
	for _, secret := range cmd.StringSlice("secrets") {
		secret := strings.Split(secret, "=")
		agentSecret := &lkproto.AgentSecret{
			Name:  secret[0],
			Value: []byte(secret[1]),
		}
		secrets[secret[0]] = agentSecret
		if secret[0] == "LIVEKIT_API_KEY" {
			livekitApiKey = true
		}
		if secret[0] == "LIVEKIT_API_SECRET" {
			livekitApiSecret = true
		}
	}

	if cmd.String("secrets-file") != "" {
		env, err := agentfs.ParseEnvFile(cmd.String("secrets-file"))
		if err != nil {
			return err
		}
		selected, err := selectSecrets(env)
		if err != nil {
			return err
		}
		for k, v := range selected {
			if _, exists := secrets[k]; exists {
				continue
			}

			secret := &lkproto.AgentSecret{
				Name:  k,
				Value: []byte(v),
			}
			secrets[k] = secret
			if k == "LIVEKIT_API_KEY" {
				livekitApiKey = true
			}
			if k == "LIVEKIT_API_SECRET" {
				livekitApiSecret = true
			}
		}
	}

	if len(secrets) == 0 {
		logger.Warnw("No secrets provided. You can update your agents later with the update-secrets command.", nil)
	}

	if !livekitApiKey || !livekitApiSecret {
		useAutoGeneratedKeys := true
		if err := huh.NewConfirm().
			Title("LIVEKIT_API_KEY and LIVEKIT_API_SECRET are unset, would you like to use auto-generated keys?").
			Value(&useAutoGeneratedKeys).
			Inline(false).
			WithTheme(util.Theme).
			Run(); err != nil {
			return err
		}

		if !useAutoGeneratedKeys {
			return fmt.Errorf("LIVEKIT_API_KEY and LIVEKIT_API_SECRET are required")
		}
	}

	dockerfileExists, err := agentfs.FindDockerfile(workingDir)
	if err != nil {
		return err
	}

	if !dockerfileExists {
		createDockerfile := true
		if err := huh.NewConfirm().
			Title("No Dockerfile found in current directory. Would you like to create one?").
			Value(&createDockerfile).
			Inline(false).
			WithTheme(util.Theme).
			Run(); err != nil {
			return err
		}

		if !createDockerfile {
			return fmt.Errorf("dockerfile is required to create agent")
		}

		clientSettingsResponse, err := agentsClient.GetClientSettings(ctx, &lkproto.ClientSettingsRequest{})
		if err != nil {
			return err
		}

		settingsMap := make(map[string]string)
		for _, setting := range clientSettingsResponse.Params {
			settingsMap[setting.Name] = setting.Value
		}

		if err := agentfs.CreateDockerfile(workingDir, settingsMap); err != nil {
			return err
		}
	}

	secretsSlice := make([]*lkproto.AgentSecret, 0, len(secrets))
	for _, secret := range secrets {
		secretsSlice = append(secretsSlice, secret)
	}
	req := &lkproto.CreateAgentRequest{
		AgentName:   agentConfig.Name,
		Secrets:     secretsSlice,
		Replicas:    int32(agentConfig.Replicas),
		MaxReplicas: int32(agentConfig.MaxReplicas),
		CpuReq:      agentConfig.CPU,
	}

	resp, err := agentsClient.CreateAgent(ctx, req)
	if err != nil {
		return err
	}

	err = agentfs.UploadTarball(workingDir, resp.PresignedUrl, []string{AgentTOMLFile})
	if err != nil {
		return err
	}

	fmt.Printf("Created agent [%s]\n", resp.AgentId)
	err = agentfs.Build(ctx, resp.AgentId, agentConfig.Name, "deploy", project)
	if err != nil {
		return err
	}

	fmt.Println("Build completed")
	fmt.Println("Deploying agent...")
	err = agentfs.LogHelper(ctx, "", agentConfig.Name, "deploy", project)
	return err
}

func createAgentConfig(ctx context.Context, cmd *cli.Command) error {
	tomlFilename := cmd.String("toml")
	if tomlFilename == "" {
		tomlFilename = AgentTOMLFile
	}
	if _, err := os.Stat(tomlFilename); err == nil {
		var overwrite bool
		if err := huh.NewConfirm().
			Title(
				fmt.Sprintf("Config file [%s] file already exists. Overwrite?", tomlFilename),
			).
			Value(&overwrite).
			WithTheme(util.Theme).
			Run(); err != nil {
			return err
		}
		if !overwrite {
			return fmt.Errorf("config file [%s] already exists", tomlFilename)
		}
	}

	response, err := agentsClient.ListAgents(ctx, &lkproto.ListAgentsRequest{
		AgentName: cmd.String("name"),
	})
	if err != nil {
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
	agentConfig := &AgentTOML{
		ProjectSubdomain: matches[1],
		Name:             agent.AgentName,
		CPU:              regionAgent.CpuReq,
		Replicas:         int(regionAgent.Replicas),
		MaxReplicas:      int(regionAgent.MaxReplicas),
	}

	f, err := os.Create(cmd.String("toml"))
	if err != nil {
		return err
	}
	defer f.Close()

	if err := toml.NewEncoder(f).Encode(agentConfig); err != nil {
		return fmt.Errorf("error encoding TOML: %w", err)
	}

	fmt.Printf("Created config file [%s]\n", tomlFilename)
	return nil
}

func deployAgent(ctx context.Context, cmd *cli.Command) error {
	workingDir := "."
	if cmd.NArg() > 0 {
		workingDir = cmd.Args().First()
	}

	tomlFilename := cmd.String("toml")
	if tomlFilename == "" {
		tomlFilename = AgentTOMLFile
	}

	agentConfig, err := loadTomlFile(workingDir, tomlFilename)
	tomlExists := true
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		tomlExists = false
	}

	if !tomlExists {
		return fmt.Errorf("config file [%s] required to update agent", tomlFilename)
	}

	secrets := make(map[string]*lkproto.AgentSecret)
	for _, secret := range cmd.StringSlice("secrets") {
		secret := strings.Split(secret, "=")
		agentSecret := &lkproto.AgentSecret{
			Name:  secret[0],
			Value: []byte(secret[1]),
		}
		secrets[secret[0]] = agentSecret
	}

	if cmd.String("secrets-file") != "" {
		env, err := agentfs.ParseEnvFile(cmd.String("secrets-file"))
		if err != nil {
			return err
		}
		selected, err := selectSecrets(env)
		if err != nil {
			return err
		}
		for k, v := range selected {
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

	secretsSlice := make([]*lkproto.AgentSecret, 0, len(secrets))
	for _, secret := range secrets {
		secretsSlice = append(secretsSlice, secret)
	}
	req := &lkproto.DeployAgentRequest{
		AgentName:   agentConfig.Name,
		Secrets:     secretsSlice,
		Replicas:    int32(agentConfig.Replicas),
		CpuReq:      agentConfig.CPU,
		MaxReplicas: int32(agentConfig.MaxReplicas),
	}

	resp, err := agentsClient.DeployAgent(ctx, req)
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to deploy agent: %s", resp.Message)
	}

	presignedUrl := resp.PresignedUrl
	err = agentfs.UploadTarball(workingDir, presignedUrl, []string{AgentTOMLFile})
	if err != nil {
		return err
	}

	fmt.Printf("Updated agent [%s]\n", resp.AgentId)
	err = agentfs.Build(ctx, resp.AgentId, agentConfig.Name, "update", project)
	if err != nil {
		return err
	}

	fmt.Println("Deployed agent")
	return nil
}

func getAgentStatus(ctx context.Context, cmd *cli.Command) error {
	workingDir := "."
	if cmd.NArg() > 0 {
		workingDir = cmd.Args().First()
	}
	tomlFilename := cmd.String("toml")
	if tomlFilename == "" {
		tomlFilename = AgentTOMLFile
	}
	agentName, err := getAgentName(cmd, workingDir, tomlFilename)
	if err != nil {
		return err
	}

	res, err := agentsClient.ListAgents(ctx, &lkproto.ListAgentsRequest{
		AgentName: agentName,
	})
	if err != nil {
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
				fmt.Sprintf("%d", regionalAgent.Replicas),
				fmt.Sprintf("%d/%d", regionalAgent.MinReplicas, regionalAgent.MaxReplicas),
				agent.DeployedAt.AsTime().Format(time.RFC3339),
			})
		}
	}

	t := util.CreateTable().
		Headers("Region", "Status", "CPU", "Mem", "Repl.", "Repl. min/max", "Deployed").
		Rows(rows...)

	fmt.Println(t)
	return nil
}

func updateAgent(ctx context.Context, cmd *cli.Command) error {
	workingDir := "."
	if cmd.NArg() > 0 {
		workingDir = cmd.Args().First()
	}

	tomlExists := true
	tomlFilename := cmd.String("toml")
	if tomlFilename == "" {
		tomlFilename = AgentTOMLFile
	}
	agentConfig, err := loadTomlFile(workingDir, tomlFilename)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		tomlExists = false
	}

	if !tomlExists {
		return fmt.Errorf("config file [%s] required to update agent", tomlFilename)
	}

	req := &lkproto.UpdateAgentRequest{
		AgentName:   agentConfig.Name,
		Replicas:    int32(agentConfig.Replicas),
		CpuReq:      agentConfig.CPU,
		MaxReplicas: int32(agentConfig.MaxReplicas),
	}

	resp, err := agentsClient.UpdateAgent(ctx, req)
	if err != nil {
		return err
	}

	if resp.Success {
		fmt.Println("Updated agent")
		return nil
	}

	return fmt.Errorf("failed to update agent: %s", resp.Message)
}

func rollbackAgent(ctx context.Context, cmd *cli.Command) error {
	workingDir := "."
	if cmd.NArg() > 0 {
		workingDir = cmd.Args().First()
	}

	tomlFilename := cmd.String("toml")
	if tomlFilename == "" {
		tomlFilename = AgentTOMLFile
	}

	agentName, err := getAgentName(cmd, workingDir, tomlFilename)
	if err != nil {
		return err
	}

	resp, err := agentsClient.RollbackAgent(ctx, &lkproto.RollbackAgentRequest{
		AgentName: agentName,
		Version:   cmd.String("version"),
	})

	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to rollback agent %s", resp.Message)
	}

	fmt.Printf("Rolled back agent [%s] to version %s\n", agentName, cmd.String("version"))

	return nil
}

func getLogs(ctx context.Context, cmd *cli.Command) error {
	workingDir := "."
	if cmd.NArg() > 0 {
		workingDir = cmd.Args().First()
	}
	tomlFilename := cmd.String("toml")
	if tomlFilename == "" {
		tomlFilename = AgentTOMLFile
	}
	agentName, err := getAgentName(cmd, workingDir, tomlFilename)
	if err != nil {
		return err
	}
	err = agentfs.LogHelper(ctx, "", agentName, cmd.String("log_type"), project)
	return err
}

func deleteAgent(ctx context.Context, cmd *cli.Command) error {
	workingDir := "."
	if cmd.NArg() > 0 {
		workingDir = cmd.Args().First()
	}
	tomlFilename := cmd.String("toml")
	if tomlFilename == "" {
		tomlFilename = AgentTOMLFile
	}
	agentName, err := getAgentName(cmd, workingDir, tomlFilename)
	if err != nil {
		return err
	}

	var confirmDelete bool
	if err := huh.NewConfirm().
		Title(fmt.Sprintf("Are you sure you want to delete agent %s?", agentName)).
		Value(&confirmDelete).
		Inline(false).
		WithTheme(util.Theme).
		Run(); err != nil {
		return err
	}

	if !confirmDelete {
		return nil
	}

	resp, err := agentsClient.DeleteAgent(ctx, &lkproto.DeleteAgentRequest{
		AgentName: agentName,
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to delete agent %s", resp.Message)
	}

	fmt.Println("Deleted agent", cmd.String("id"))
	return nil
}

func listAgentVersions(ctx context.Context, cmd *cli.Command) error {
	workingDir := "."
	if cmd.NArg() > 0 {
		workingDir = cmd.Args().First()
	}
	tomlFilename := cmd.String("toml")
	if tomlFilename == "" {
		tomlFilename = AgentTOMLFile
	}
	agentName, err := getAgentName(cmd, workingDir, tomlFilename)
	if err != nil {
		return err
	}

	req := &lkproto.ListAgentVersionsRequest{
		AgentName: agentName,
	}

	versions, err := agentsClient.ListAgentVersions(ctx, req)
	if err != nil {
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
				return err
			}
			items = append(items, res.Agents...)
		}
	} else {
		agents, err := agentsClient.ListAgents(ctx, req)
		if err != nil {
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
	workingDir := "."
	if cmd.NArg() > 0 {
		workingDir = cmd.Args().First()
	}
	tomlFilename := cmd.String("toml")
	if tomlFilename == "" {
		tomlFilename = AgentTOMLFile
	}
	agentName, err := getAgentName(cmd, workingDir, tomlFilename)
	if err != nil {
		return err
	}

	req := &lkproto.ListAgentSecretsRequest{
		AgentName: agentName,
	}

	secrets, err := agentsClient.ListAgentSecrets(ctx, req)
	if err != nil {
		return err
	}

	table := util.CreateTable().
		Headers("Name", "Created At", "Updated At")

	for _, secret := range secrets.Secrets {
		table.Row(secret.Name, secret.CreatedAt.AsTime().Format(time.RFC3339), secret.UpdatedAt.AsTime().Format(time.RFC3339))
	}

	fmt.Println(table)
	return nil
}

func updateAgentSecrets(ctx context.Context, cmd *cli.Command) error {
	workingDir := "."
	if cmd.NArg() > 0 {
		workingDir = cmd.Args().First()
	}
	tomlFilename := cmd.String("toml")
	if tomlFilename == "" {
		tomlFilename = AgentTOMLFile
	}
	agentName, err := getAgentName(cmd, workingDir, tomlFilename)
	if err != nil {
		return err
	}

	if len(cmd.StringSlice("secrets")) == 0 && cmd.String("secrets-file") == "" {
		return fmt.Errorf("no secrets provided")
	}

	secrets := make(map[string]*lkproto.AgentSecret)
	for _, secret := range cmd.StringSlice("secrets") {
		secret := strings.Split(secret, "=")
		agentSecret := &lkproto.AgentSecret{
			Name:  secret[0],
			Value: []byte(secret[1]),
		}
		secrets[secret[0]] = agentSecret
	}

	if cmd.String("secrets-file") != "" {
		env, err := agentfs.ParseEnvFile(cmd.String("secrets-file"))
		if err != nil {
			return err
		}
		selected, err := selectSecrets(env)
		if err != nil {
			return err
		}
		for k, v := range selected {
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

	secretsSlice := make([]*lkproto.AgentSecret, 0, len(secrets))
	for _, secret := range secrets {
		secretsSlice = append(secretsSlice, secret)
	}

	req := &lkproto.UpdateAgentSecretsRequest{
		AgentName: agentName,
		Secrets:   secretsSlice,
		Overwrite: cmd.Bool("overwrite"),
	}

	resp, err := agentsClient.UpdateAgentSecrets(ctx, req)
	if err != nil {
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
		agentConfig, err := loadTomlFile(agentDir, tomlFileName)
		tomlExists := true
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
			tomlExists = false
		}

		if !tomlExists {
			return "", fmt.Errorf("agent name or %s required", tomlFileName)
		}

		agentName = agentConfig.Name
	}

	if agentName == "" {
		// shouldn't happen, but check to ensure we have a name
		return "", fmt.Errorf("agent name or %s required", tomlFileName)
	}

	return agentName, nil
}

func selectSecrets(secrets map[string]string) (map[string]string, error) {
	keys := make([]string, 0, len(secrets))
	options := make([]huh.Option[string], 0, len(secrets))
	for k := range secrets {
		keys = append(keys, k)
		options = append(options, huh.NewOption(k, k))
	}
	sort.Slice(options, func(i, j int) bool {
		return options[i].Key < options[j].Key
	})

	if err := huh.NewMultiSelect[string]().
		Title("Select secrets to include").
		Description("Press space to toggle, enter to confirm").
		Options(options...).
		Value(&keys).
		Height(6).
		WithTheme(util.Theme).
		Run(); err != nil {
		return nil, fmt.Errorf("error running TUI: %w", err)
	}

	selected := make(map[string]string)
	for _, k := range keys {
		selected[k] = secrets[k]
	}

	return selected, nil
}
