package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/manifoldco/promptui"
	"github.com/olekukonko/tablewriter"
	"github.com/urfave/cli/v2"

	"github.com/livekit/livekit-cli/pkg/config"
)

var (
	ProjectCommands = []*cli.Command{
		{
			Name:     "project",
			Usage:    "subcommand for project management",
			Category: "Project Management",
			Before:   loadProjectConfig,
			Subcommands: []*cli.Command{
				{
					Name:   "add",
					Usage:  "add a new project",
					Action: addProject,
				},
				{
					Name:   "list",
					Usage:  "list all configured projects",
					Action: listProjects,
				},
				{
					Name:      "remove",
					Usage:     "remove an existing project from config",
					UsageText: "livekit-cli project remove <project-name>",
					Action:    removeProject,
				},
				{
					Name:      "set-default",
					Usage:     "set a project as default to use with other commands",
					UsageText: "livekit-cli project set-default <project-name>",
					Action:    setDefaultProject,
				},
			},
		},
	}

	cliConfig      *config.CLIConfig
	defaultProject *config.ProjectConfig
	nameRegex      = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)
)

func loadProjectConfig(c *cli.Context) error {
	conf, err := config.LoadOrCreate()
	if err != nil {
		return err
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
	return nil
}

func addProject(c *cli.Context) error {
	p := config.ProjectConfig{}
	fmt.Println("Enter project details")
	prompt := promptui.Prompt{
		Label: "URL",
		Validate: func(val string) error {
			if !strings.HasPrefix(val, "http") && !strings.HasPrefix(val, "ws") {
				return errors.New("scheme must be http(s) or ws(s)")
			}
			_, err := url.Parse(val)
			return err
		},
	}
	var err error
	if p.URL, err = prompt.Run(); err != nil {
		return err
	}

	prompt = promptui.Prompt{
		Label: "API Key",
		Validate: func(val string) error {
			if len(val) < 3 {
				return errors.New("API key must be at least 3 characters")
			}
			return nil
		},
	}
	if p.APIKey, err = prompt.Run(); err != nil {
		return err
	}

	prompt = promptui.Prompt{
		Label: "API APISecret",
		Validate: func(val string) error {
			if len(val) < 3 {
				return errors.New("API secret must be at least 3 characters")
			}
			return nil
		},
	}
	if p.APISecret, err = prompt.Run(); err != nil {
		return err
	}

	prompt = promptui.Prompt{
		Label: "Name",
		Validate: func(val string) error {
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
		},
	}
	if p.Name, err = prompt.Run(); err != nil {
		return err
	}

	// if it's first project, make it default
	if defaultProject != nil {
		prompt = promptui.Prompt{
			Label:     "Make this project default?",
			IsConfirm: true,
		}
		if _, err = prompt.Run(); err != nil && err != promptui.ErrAbort {
			return err
		}
		if err == nil {
			cliConfig.DefaultProject = p.Name
		}
	} else {
		cliConfig.DefaultProject = p.Name
	}
	cliConfig.Projects = append(cliConfig.Projects, p)

	// save config
	if err = cliConfig.PersistIfNeeded(); err != nil {
		return err
	}

	fmt.Println("Added project", p.Name)

	return nil
}

func listProjects(c *cli.Context) error {
	if len(cliConfig.Projects) == 0 {
		fmt.Println("No projects configured, use `livekit-cli project add` to add a new project.")
		return nil
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetHeader([]string{"Name", "URL", "API Key", "Default"})
	for _, p := range cliConfig.Projects {
		table.Append([]string{p.Name, p.URL, p.APIKey, fmt.Sprint(p.Name == cliConfig.DefaultProject)})
	}
	table.Render()
	return nil
}

func removeProject(c *cli.Context) error {
	if c.NArg() == 0 {
		return errors.New("project name is required")
	}
	name := c.Args().First()

	var newProjects []config.ProjectConfig
	for _, p := range cliConfig.Projects {
		if p.Name == name {
			continue
		}
		newProjects = append(newProjects, p)
	}
	cliConfig.Projects = newProjects

	if cliConfig.DefaultProject == name {
		cliConfig.DefaultProject = ""
	}

	if err := cliConfig.PersistIfNeeded(); err != nil {
		return err
	}

	fmt.Println("Removed project", name)

	return nil
}

func setDefaultProject(c *cli.Context) error {
	if c.NArg() == 0 {
		return errors.New("project name is required")
	}
	name := c.Args().First()

	for _, p := range cliConfig.Projects {
		if p.Name == name {
			cliConfig.DefaultProject = name
			if err := cliConfig.PersistIfNeeded(); err != nil {
				return err
			}
			fmt.Println("Default project set to", name)
			return nil
		}
	}

	return errors.New("project not found")
}
