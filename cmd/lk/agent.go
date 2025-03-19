package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	lkproto "github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type AgentTOML struct {
	LocalProjectName string `toml:"local_project_name"`
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
	formWidth                  = 120
)

var (
	tomlFlag = &cli.StringFlag{
		Name:     "toml",
		Usage:    fmt.Sprintf("TOML file to use in the working directory. Defaults to %s", AgentTOMLFile),
		Required: false,
		Value:    AgentTOMLFile,
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
					Usage:  fmt.Sprintf("Creates a %s in the current directory for an existing agent.", AgentTOMLFile),
					Before: createAgentClient,
					Action: createAgentConfig,
					Flags: []cli.Flag{
						nameFlag(true),
						tomlFlag,
					},
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
				},
			},
		},
	}

	agentsClient        *lksdk.AgentClient
	globalProjectConfig *config.ProjectConfig
)

func createAgentClient(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	var err error
	globalProjectConfig, err = loadProjectDetails(cmd)
	if err != nil {
		return nil, err
	}

	agentsClient, err = lksdk.NewAgentClient(globalProjectConfig.URL, globalProjectConfig.APIKey, globalProjectConfig.APISecret)
	return ctx, err
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
	subdomainMatches := subdomainPattern.FindStringSubmatch(globalProjectConfig.URL)
	if len(subdomainMatches) < 1 {
		return fmt.Errorf("invalid project URL: %s", globalProjectConfig.URL)
	}

	var useProject bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Use project: %s with subdomain: %s to create agent?", globalProjectConfig.Name, subdomainMatches[1])).
				Value(&useProject).
				Inline(true),
		),
	).WithTheme(getFormTheme()).
		WithWidth(formWidth)
	err := form.Run()
	if err != nil {
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
	agentConfig, err := loadTomlFile(workingDir, cmd.String("toml"))
	var exists bool
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		exists = false
	} else {
		exists = true
	}

	if exists && !cmd.Bool("silent") {
		var useExisting bool
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("%s already exists. Would you like to use it?", cmd.String("toml"))).
					Value(&useExisting).Inline(true),
			),
		).WithTheme(getFormTheme()).
			WithWidth(formWidth)
		err = form.Run()
		if err != nil {
			return err
		}

		if !useExisting {
			return fmt.Errorf("%s already exists", cmd.String("toml"))
		}

		if cmd.String("name") != "" && agentConfig.Name != cmd.String("name") {
			return fmt.Errorf("agent name passed in command line: %s does not match name in %s: %s", cmd.String("name"), cmd.String("toml"), agentConfig.Name)
		}

		logger.Debugw("using existing agent toml")
	} else if !exists && !cmd.Bool("silent") {
		if cmd.String("name") == "" {
			return fmt.Errorf("name is required")
		}

		var createFile bool
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("%s required. create one?", cmd.String("toml"))).
					Description(fmt.Sprintf(" project: %s", globalProjectConfig.Name)).
					Value(&createFile).Inline(true),
			),
		).WithTheme(getFormTheme()).
			WithWidth(formWidth)

		err = form.Run()
		if err != nil {
			return err
		}

		if !createFile {
			return fmt.Errorf("%s required to create agent", cmd.String("toml"))
		}

		f, err := os.Create(filepath.Join(workingDir, cmd.String("toml")))
		if err != nil {
			return err
		}
		defer f.Close()

		agentConfig = &AgentTOML{
			LocalProjectName: globalProjectConfig.Name,
			ProjectSubdomain: subdomainMatches[1],
			Name:             cmd.String("name"),
			CPU:              clientDefaults_CPU,
			Replicas:         clientDefaults_Replicas,
			MaxReplicas:      clientDefaults_MaxReplicas,
		}

		encoder := toml.NewEncoder(f)
		if err := encoder.Encode(agentConfig); err != nil {
			fmt.Println("Error encoding TOML:", err)
		}
	}

	if cmd.String("name") == "" {
		if agentConfig.Name == "" {
			return fmt.Errorf("name is required")
		}
	} else {
		agentConfig.Name = cmd.String("name")
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
		env, err := agentfs.ParseEnvFile(filepath.Join(workingDir, cmd.String("secrets-file")))
		if err != nil {
			return err
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
		var useAutoGeneratedKeys bool
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("LIVEKIT_API_KEY and LIVEKIT_API_SECRET are unset, would you like to use auto-generated keys?").
					Value(&useAutoGeneratedKeys).Inline(true),
			),
		).WithTheme(getFormTheme()).
			WithWidth(formWidth)
		err = form.Run()
		if err != nil {
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
		var createDockerfile bool

		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("No Dockerfile found in current directory. Would you like to create one?").
					Value(&createDockerfile).
					Inline(true),
			),
		).WithTheme(getFormTheme()).
			WithWidth(formWidth)

		err = form.Run()
		if err != nil {
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

		err = agentfs.CreateDockerfile(workingDir, settingsMap)
		if err != nil {
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

	fmt.Printf("created agent %s\n", resp.AgentId)
	err = agentfs.Build(ctx, resp.AgentId, agentConfig.Name, "deploy", globalProjectConfig)
	if err != nil {
		return err
	}

	fmt.Println("build completed")
	fmt.Println("deploying agent...")
	err = agentfs.LogHelper(ctx, "", agentConfig.Name, "deploy", globalProjectConfig)
	return err
}

func createAgentConfig(ctx context.Context, cmd *cli.Command) error {
	if _, err := os.Stat(cmd.String("toml")); err == nil {
		var overwrite bool
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("%s file already exists. Overwrite?", cmd.String("toml"))).
					Value(&overwrite),
			),
		).WithTheme(getFormTheme()).
			WithWidth(formWidth)

		if err := form.Run(); err != nil {
			return err
		}
		if !overwrite {
			return fmt.Errorf("%s already exists", cmd.String("toml"))
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
	matches := subdomainPattern.FindStringSubmatch(globalProjectConfig.URL)
	if len(matches) < 1 {
		return fmt.Errorf("invalid project URL: %s", globalProjectConfig.URL)
	}

	agent := response.Agents[0]
	regionAgent := agent.AgentDeployments[0]
	agentConfig := &AgentTOML{
		LocalProjectName: globalProjectConfig.Name,
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

	fmt.Printf("Created %s file\n", cmd.String("toml"))
	return nil
}

func deployAgent(ctx context.Context, cmd *cli.Command) error {
	workingDir := "."
	if cmd.NArg() > 0 {
		workingDir = cmd.Args().First()
	}

	agentConfig, err := loadTomlFile(workingDir, cmd.String("toml"))
	tomlExists := true
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		tomlExists = false
	}

	if !tomlExists {
		return fmt.Errorf("%s required to update agent", cmd.String("toml"))
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

	fmt.Printf("updated agent %s\n", resp.AgentId)
	err = agentfs.Build(ctx, resp.AgentId, agentConfig.Name, "update", globalProjectConfig)
	if err != nil {
		return err
	}

	fmt.Println("deployed agent")
	return nil
}

func getAgentStatus(ctx context.Context, cmd *cli.Command) error {
	agentName, err := getAgentName(cmd, ".", cmd.String("toml"))
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
				agent.DeployedAt.AsTime().Local().Format("Jan 2, 2006 15:04:05"),
			})
		}
	}

	t := CreateAgentTable().
		Headers("Region", "Status", "CPU", "Mem", "Replicas current", "Replicas min/max", "Deployed At").
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
	agentConfig, err := loadTomlFile(workingDir, cmd.String("toml"))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		tomlExists = false
	}

	if !tomlExists {
		return fmt.Errorf("%s required to update agent", cmd.String("toml"))
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
		fmt.Println("updated agent")
		return nil
	}

	return fmt.Errorf("failed to update agent: %s", resp.Message)
}

func rollbackAgent(ctx context.Context, cmd *cli.Command) error {
	agentName, err := getAgentName(cmd, ".", cmd.String("toml"))
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

	fmt.Println("rolled back agent", agentName, "to version", cmd.String("version"))

	return nil
}

func getLogs(ctx context.Context, cmd *cli.Command) error {
	agentName, err := getAgentName(cmd, ".", cmd.String("toml"))
	if err != nil {
		return err
	}
	err = agentfs.LogHelper(ctx, "", agentName, cmd.String("log_type"), globalProjectConfig)
	return err
}

func deleteAgent(ctx context.Context, cmd *cli.Command) error {
	agentName, err := getAgentName(cmd, ".", cmd.String("toml"))
	if err != nil {
		return err
	}

	var confirmDelete bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Are you sure you want to delete agent %s?", agentName)).
				Value(&confirmDelete).
				Inline(true),
		),
	).WithTheme(getFormTheme()).
		WithWidth(formWidth)

	err = form.Run()
	if err != nil {
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

	fmt.Println("deleted agent", cmd.String("id"))
	return nil
}

func listAgentVersions(ctx context.Context, cmd *cli.Command) error {
	agentName, err := getAgentName(cmd, ".", cmd.String("toml"))
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

	table := CreateAgentTable().
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
		fmt.Println("no agents found")
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

	t := CreateAgentTable().
		Headers("ID", "Name", "Regions").
		Rows(rows...)

	fmt.Println(t)
	return nil
}

func listAgentSecrets(ctx context.Context, cmd *cli.Command) error {
	agentName, err := getAgentName(cmd, ".", cmd.String("toml"))
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

	table := CreateAgentTable().
		Headers("Name", "Created At", "Updated At")

	for _, secret := range secrets.Secrets {
		table.Row(secret.Name, secret.CreatedAt.AsTime().Format(time.RFC3339), secret.UpdatedAt.AsTime().Format(time.RFC3339))
	}

	fmt.Println(table)
	return nil
}

func updateAgentSecrets(ctx context.Context, cmd *cli.Command) error {
	agentName, err := getAgentName(cmd, ".", cmd.String("toml"))
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
		fmt.Println("updated agent secrets")
		return nil
	}

	return fmt.Errorf("failed to update agent secrets: %s", resp.Message)
}

// helper functions
func getFormTheme() *huh.Theme {
	return huh.ThemeBase16()
}

func CreateAgentTable() *table.Table {
	return table.New().
		Border(lipgloss.ThickBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("36"))).
		StyleFunc(func(row, col int) lipgloss.Style {
			switch {
			case row == -1:
				return lipgloss.NewStyle().
					Foreground(lipgloss.Color("63")).
					Bold(true)
			default:
				return lipgloss.NewStyle()
			}
		})
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
