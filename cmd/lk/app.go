// Copyright 2024 LiveKit, Inc.
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
	"os/exec"
	"path"
	"path/filepath"
	"regexp"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/huh/spinner"
	"github.com/livekit/livekit-cli/pkg/bootstrap"
	"github.com/livekit/livekit-cli/pkg/config"
	"github.com/urfave/cli/v3"
)

const (
	templateBaseUrl = "https://github.com/livekit-examples"
)

var (
	template     *bootstrap.Template
	templateName string
	templateURL  string
	sandboxID    string
	appName      string
	appNameRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)
	project      *config.ProjectConfig
	AppCommands  = []*cli.Command{
		{
			Hidden:   true,
			Name:     "app",
			Category: "Core",
			Commands: []*cli.Command{
				{
					Name:      "create",
					Usage:     "Bootstrap a new application from a template or through guided creation",
					Before:    requireProject,
					Action:    setupBootstrapTemplate,
					ArgsUsage: "`APP_NAME`",
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:        "template",
							Usage:       "`TEMPLATE` to instantiate, see " + templateBaseUrl,
							Destination: &templateName,
						},
						&cli.StringFlag{
							Name:        "template-url",
							Usage:       "`URL` to instantiate, must contain a taskfile.yaml",
							Destination: &templateURL,
						},
					},
				},
				{
					Name:      "sandbox",
					Usage:     "Bootstrap a sandbox application created on your cloud dashboard",
					Before:    requireProject,
					Action:    setupSandboxTemplate,
					ArgsUsage: "`APP_NAME`",
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:        "id",
							Usage:       "`ID` of the sandbox, see your cloud dashboard",
							Destination: &sandboxID,
							Required:    true,
						},
					},
				},
				{
					Name:      "install",
					Usage:     "Execute installation defined in " + bootstrap.TaskFile,
					ArgsUsage: "`DIR` location or the project directory (default: current directory)",
					Before:    requireProject,
					Action:    installTemplate,
				},
				{
					Name:      "run",
					Usage:     "Execute a task defined in " + bootstrap.TaskFile,
					ArgsUsage: "`DIR` location or the project directory (default: current directory)",
					Action:    runTask,
				},
				{
					Name: "test",
					Action: func(ctx context.Context, c *cli.Command) error {
						templates, err := bootstrap.FetchTemplates(ctx)
						if err != nil {
							return err
						}

						fmt.Printf("Fetched templates: %+v\n", templates)
						return nil
					},
				},
			},
		},
	}
)

func requireProject(_ context.Context, cmd *cli.Command) error {
	var err error
	project, err = loadProjectDetails(cmd)
	return err
}

func setupBootstrapTemplate(ctx context.Context, cmd *cli.Command) error {
	var preinstallPrompts []huh.Field

	appName = cmd.Args().First()
	if appName == "" {
		preinstallPrompts = append(preinstallPrompts, huh.NewInput().
			Title("Application Name").
			Placeholder("my-app").
			Value(&appName).
			Validate(func(s string) error {
				if len(s) < 3 {
					return errors.New("name is too short")
				}
				if !appNameRegex.MatchString(s) {
					return errors.New("try a simpler name")
				}
				if s, _ := os.Stat(s); s != nil {
					return errors.New("that name is in use")
				}
				return nil
			}).
			WithTheme(theme))
	}

	if templateName != "" && templateURL != "" {
		return errors.New("only one of template or template-url can be specified")
	}

	// if no template name or URL is specified, fetch template index and prompt user
	if templateName == "" && templateURL == "" {
		templates, err := bootstrap.FetchTemplates(ctx)
		if err != nil {
			return err
		}
		templateSelect := huh.NewSelect[string]().
			Title("Select Template").
			Value(&templateURL).
			WithTheme(theme)
		var options []huh.Option[string]
		for _, t := range templates {
			options = append(options, huh.NewOption(t.Name, t.URL))
		}
		templateSelect.(*huh.Select[string]).Options(options...)
		preinstallPrompts = append(preinstallPrompts, templateSelect)
		// if templateName is specified, fetch template index and find it
	} else if templateName != "" {
		templates, err := bootstrap.FetchTemplates(ctx)
		if err != nil {
			return err
		}
		for _, t := range templates {
			if t.Name == templateName {
				template = &t
				templateURL = t.URL
				break
			}
		}
		if template == nil {
			return errors.New("template not found: " + templateName)
		}
	}

	if len(preinstallPrompts) > 0 {
		group := huh.NewGroup(preinstallPrompts...)
		if err := huh.NewForm(group).
			WithTheme(theme).
			RunWithContext(ctx); err != nil {
			return err
		}
	}

	fmt.Println("Cloning template...")
	if err := cloneTemplate(ctx, cmd, templateURL, appName); err != nil {
		return err
	}

	fmt.Println("Instantiating environment...")
	if err := instantiateEnv(ctx, cmd, appName); err != nil {
		return err
	}

	fmt.Println("Installing template...")
	if err := installTemplate(ctx, cmd); err != nil {
		return err
	}

	return nil
}

func setupSandboxTemplate(ctx context.Context, cmd *cli.Command) error {
	var preinstallPrompts []huh.Field

	appName = cmd.Args().First()
	if appName == "" {
		preinstallPrompts = append(preinstallPrompts, huh.NewInput().
			Title("Application Name").
			Placeholder("my-app").
			Value(&appName).
			Validate(func(s string) error {
				if len(s) < 3 {
					return errors.New("name is too short")
				}
				if !appNameRegex.MatchString(s) {
					return errors.New("try a simpler name")
				}
				if s, _ := os.Stat(s); s != nil {
					return errors.New("that name is in use")
				}
				return nil
			}).
			WithTheme(theme))
	}

	if len(preinstallPrompts) > 0 {
		var groups []*huh.Group
		for _, p := range preinstallPrompts {
			groups = append(groups, huh.NewGroup(p))
		}
		if err := huh.NewForm(groups...).
			WithTheme(theme).
			RunWithContext(ctx); err != nil {
			return err
		}
	}

	if err := cloneTemplate(ctx, cmd, templateName, appName); err != nil {
		return err
	}

	if err := instantiateEnv(ctx, cmd, appName); err != nil {
		return err
	}

	if err := installSandboxTemplate(ctx, cmd); err != nil {
		return err
	}

	return nil
}

func cloneTemplate(_ context.Context, cmd *cli.Command, templateURL, appName string) error {
	var cmdErr error
	if err := spinner.New().
		Title("Cloning template from " + templateURL).
		Action(func() {
			c := exec.Command("git", "clone", "--depth=1", templateURL, appName)
			var out []byte
			if out, cmdErr = c.CombinedOutput(); len(out) > 0 && cmd.Bool("verbose") {
				fmt.Println(string(out))
			}
			os.RemoveAll(path.Join(appName, ".git"))
		}).
		Run(); err != nil {
		return err
	}
	return cmdErr
}

func instantiateEnv(ctx context.Context, cmd *cli.Command, rootPath string) error {
	env := map[string]string{
		"LIVEKIT_API_KEY":    project.APIKey,
		"LIVEKIT_API_SECRET": project.APISecret,
		"LIVEKIT_URL":        project.URL,
		"LIVEKIT_SANDBOX":    "TODO",
	}

	prompt := func(key, oldValue string) (string, error) {
		var newValue string
		if err := huh.NewInput().
			Title("Enter " + key + "?").
			Placeholder(oldValue).
			Value(&newValue).
			WithTheme(theme).
			Run(); err != nil || newValue == "" {
			return oldValue, err
		}
		return newValue, nil
	}

	return bootstrap.InstantiateDotEnv(ctx, rootPath, env, cmd.Bool("verbose"), prompt)
}

func installTemplate(ctx context.Context, cmd *cli.Command) error {
	verbose := cmd.Bool("verbose")
	rootPath := cmd.Args().First()
	if rootPath == "" {
		rootPath = "."
	}

	tf, err := bootstrap.ParseTaskfile(rootPath)
	if err != nil {
		return err
	}

	install, err := bootstrap.NewTask(ctx, tf, rootPath, bootstrap.TaskInstall, verbose)
	if err != nil {
		return err
	}

	if verbose {
		if err := install(); err != nil {
			return err
		}
	} else {
		var cmdErr error
		if err := spinner.New().
			Title("Installing...").
			Action(func() { cmdErr = install() }).
			Type(spinner.Dots).
			Run(); err != nil {
			return err
		}
		if cmdErr != nil {
			return cmdErr
		}
	}

	fullPath, err := filepath.Abs(rootPath)
	if fullPath != "" {
		fmt.Println("Installed project at " + fullPath)
	}

	return err
}

func installSandboxTemplate(ctx context.Context, cmd *cli.Command) error {
	verbose := cmd.Bool("verbose")
	rootPath := cmd.Args().First()
	if rootPath == "" {
		rootPath = "."
	}

	tf, err := bootstrap.ParseTaskfile(rootPath)
	if err != nil {
		return err
	}

	install, err := bootstrap.NewTask(ctx, tf, rootPath, bootstrap.TaskInstallSandbox, verbose)
	if err != nil {
		return err
	}

	if verbose {
		if err := install(); err != nil {
			return err
		}
	} else {
		var cmdErr error
		if err := spinner.New().
			Title("Installing Sandbox...").
			Action(func() { cmdErr = install() }).
			Type(spinner.Dots).
			Run(); err != nil {
			return err
		}
		if cmdErr != nil {
			return cmdErr
		}
	}

	fullPath, err := filepath.Abs(rootPath)
	if fullPath != "" {
		fmt.Println("Installed sandbox at " + fullPath)
	}

	return err
}

func runTask(ctx context.Context, cmd *cli.Command) error {
	taskName := cmd.Args().First()
	if taskName == "" {
		return errors.New("task name is required")
	}

	tf, err := bootstrap.ParseTaskfile(".")
	if err != nil {
		return err
	}

	task, err := bootstrap.NewTask(ctx, tf, ".", taskName, cmd.Bool("verbose"))
	if err != nil {
		return err
	}
	return task()
}
