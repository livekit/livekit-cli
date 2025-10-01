// Copyright 2022-2024 LiveKit, Inc.
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
	"net/url"
	"regexp"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

var (
	ProjectCommands = []*cli.Command{
		{
			Name:   "project",
			Usage:  "Add or remove projects and view existing project properties",
			Before: loadProjectConfig,
			Commands: []*cli.Command{
				{
					Name:      "add",
					Usage:     "Add a new project (for LiveKit Cloud projects, also see `lk cloud auth`)",
					UsageText: "lk project add PROJECT_NAME",
					ArgsUsage: "PROJECT_NAME",
					Action:    addProject,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:  "url",
							Usage: "`URL` of the LiveKit server",
						},
						&cli.StringFlag{
							Name:  "api-key",
							Usage: "Project `KEY`",
						},
						&cli.StringFlag{
							Name:  "api-secret",
							Usage: "Project `SECRET`",
						},
						&cli.BoolFlag{
							Name:  "default",
							Usage: "Set this project as the default",
						},
					},
				},
				{
					Name:      "list",
					Usage:     "List all configured projects",
					UsageText: "lk project list",
					Action:    listProjects,
					Flags:     []cli.Flag{jsonFlag},
				},
				{
					Name:      "remove",
					Usage:     "Remove an existing project from config",
					UsageText: "lk project remove PROJECT_NAME",
					ArgsUsage: "PROJECT_NAME",
					Action:    removeProject,
				},
				{
					Name:      "set-default",
					Usage:     "Set a project as default to use with other commands",
					UsageText: "lk project set-default PROJECT_NAME",
					ArgsUsage: "PROJECT_NAME",
					Action:    setDefaultProject,
				},
			},
		},
	}

	cliConfig      *config.CLIConfig
	defaultProject *config.ProjectConfig
	nameRegex      = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)
	urlRegex       = regexp.MustCompile(`^(http|https|ws|wss)://[^\s/$.?#].[^\s]*$`)
)

func loadProjectConfig(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	conf, err := config.LoadOrCreate()
	if err != nil {
		return ctx, err
	}
	cliConfig = conf

	if cliConfig.DefaultProject != "" {
		for _, p := range cliConfig.Projects {
			if p.Name == cliConfig.DefaultProject {
				defaultProject = &p
				break
			}
		}
	}
	return ctx, nil
}

func addProject(ctx context.Context, cmd *cli.Command) error {
	p := config.ProjectConfig{}
	var err error
	var prompts []huh.Field

	// Name
	validateName := func(val string) error {
		if !nameRegex.MatchString(val) {
			return errors.New("name can only contain alphanumeric characters, dashes and underscores")
		}
		// cannot conflict with existing projects
		for _, p := range cliConfig.Projects {
			if p.Name == val {
				return errors.New("name already exists")
			}
		}
		return nil
	}

	if p.Name = cmd.Args().Get(0); p.Name != "" {
		if err = validateName(p.Name); err != nil {
			return err
		}
		fmt.Println("  Project Name:", p.Name)
	} else {
		prompts = append(prompts, huh.NewInput().
			Title("Project Name").
			Placeholder("my-project").
			Validate(validateName).
			Value(&p.Name))
	}

	// URL
	validateURL := func(val string) error {
		if !urlRegex.MatchString(val) {
			return errors.New("URL must start with http[s]:// or ws[s]://")
		}
		_, err := url.Parse(val)
		return err
	}
	if p.URL = cmd.String("url"); p.URL != "" {
		if err = validateURL(p.URL); err != nil {
			return err
		}
		fmt.Println("  URL:", p.URL)
	} else {
		prompts = append(prompts, huh.NewInput().
			Title("Project URL").
			Placeholder("wss://my-project.livekit.cloud").
			Validate(validateURL).
			Value(&p.URL))
	}

	// API key
	validateKey := func(val string) error {
		if len(val) < 3 {
			return errors.New("value must be at least 3 characters")
		}
		return nil
	}
	if p.APIKey = cmd.String("api-key"); p.APIKey != "" {
		if err = validateKey(p.APIKey); err != nil {
			return err
		}
		fmt.Println("  API Key:", p.APIKey)
	} else {
		prompts = append(prompts, huh.NewInput().
			Title("API Key").
			Placeholder("APIxxxxxxxxxxxx").
			Validate(validateKey).
			Value(&p.APIKey))
	}

	// API Secret
	if p.APISecret = cmd.String("api-secret"); p.APISecret != "" {
		if err = validateKey(p.APISecret); err != nil {
			return err
		}
		fmt.Println("  API Secret:", p.APISecret)
	} else {
		prompts = append(prompts, huh.NewInput().
			Title("API Secret").
			Placeholder("****************************").
			Validate(validateKey).
			Value(&p.APISecret))
	}

	// if it's first project, make it default
	isDefault := false
	if cmd.Bool("default") || defaultProject == nil {
		cliConfig.DefaultProject = p.Name
	} else if !cmd.IsSet("default") {
		prompts = append(prompts, huh.NewConfirm().
			Title("Make this project default?").
			Value(&isDefault).
			Inline(true).
			WithTheme(util.Theme))
	}

	if len(prompts) > 0 {
		var groups []*huh.Group
		for _, p := range prompts {
			groups = append(groups, huh.NewGroup(p))
		}
		err = huh.NewForm(groups...).
			WithTheme(util.Theme).
			RunWithContext(ctx)
		if err != nil {
			return err
		}
		if isDefault {
			cliConfig.DefaultProject = p.Name
		}
	}

	cliConfig.Projects = append(cliConfig.Projects, p)

	// save config
	if err = cliConfig.PersistIfNeeded(); err != nil {
		return err
	}

	listProjects(ctx, cmd)

	return nil
}

func listProjects(ctx context.Context, cmd *cli.Command) error {
	if len(cliConfig.Projects) == 0 {
		fmt.Println("No projects configured, use `lk cloud auth` to authenticate a new project.")
		return nil
	}

	baseStyle := util.Theme.Form.Base.Foreground(util.Fg).Padding(0, 1)
	headerStyle := baseStyle.Bold(true)
	selectedStyle := util.Theme.Focused.Title.Padding(0, 1)

	if cmd.Bool("json") {
		util.PrintJSON(cliConfig.Projects)
	} else {
		table := util.CreateTable().
			StyleFunc(func(row, col int) lipgloss.Style {
				switch {
				case row == table.HeaderRow:
					return headerStyle
				case cliConfig.Projects[row].Name == cliConfig.DefaultProject:
					return selectedStyle
				default:
					return baseStyle
				}
			}).
			Headers("Name", "URL", "API Key")
		for _, p := range cliConfig.Projects {
			var pName string
			if p.Name == cliConfig.DefaultProject {
				pName = "* " + p.Name
			} else {
				pName = "  " + p.Name
			}
			table.Row(pName, p.URL, p.APIKey)
		}
		fmt.Println(table)
	}

	return nil
}

func removeProject(ctx context.Context, cmd *cli.Command) error {
	if cmd.NArg() == 0 {
		_ = cli.ShowSubcommandHelp(cmd)
		return errors.New("project name is required")
	}
	name := cmd.Args().First()
	return cliConfig.RemoveProject(name)
}

func setDefaultProject(ctx context.Context, cmd *cli.Command) error {
	if cmd.NArg() == 0 {
		_ = cli.ShowSubcommandHelp(cmd)
		return errors.New("project name is required")
	}
	name := cmd.Args().First()

	for _, p := range cliConfig.Projects {
		if p.Name != name {
			continue
		}

		cliConfig.DefaultProject = p.Name
		if err := cliConfig.PersistIfNeeded(); err != nil {
			return err
		}
		fmt.Println("Default project set to [" + util.Theme.Focused.Title.Render(p.Name) + "]")
		return nil
	}

	return errors.New("project not found")
}
