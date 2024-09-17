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
	"regexp"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/huh/spinner"
	"github.com/livekit/livekit-cli/pkg/bootstrap"
	"github.com/livekit/livekit-cli/pkg/config"
	"github.com/urfave/cli/v3"
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
							Usage:       "`TEMPLATE` to instantiate, see " + bootstrap.TemplateBaseURL,
							Destination: &templateName,
						},
						&cli.StringFlag{
							Name:        "template-url",
							Usage:       "`URL` to instantiate, must contain a taskfile.yaml",
							Destination: &templateURL,
						},
						&cli.BoolFlag{
							Name:   "install",
							Usage:  "Run installation tasks after creating the app",
							Hidden: true,
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
						&cli.StringFlag{
							Name:        "server-url",
							Value:       cloudAPIServerURL,
							Destination: &serverURL,
							Hidden:      true,
						},
						&cli.BoolFlag{
							Name:   "install",
							Usage:  "Run installation tasks after creating the app",
							Hidden: true,
						},
					},
				},
				{
					Name:      "install",
					Usage:     "Execute installation defined in " + bootstrap.TaskFile,
					ArgsUsage: "[DIR] location of the project directory (default: current directory)",
					Before:    requireProject,
					Action:    installTemplate,
				},
				{
					Hidden:    true,
					Name:      "run",
					Usage:     "Execute a task defined in " + bootstrap.TaskFile,
					ArgsUsage: "[TASK] to run in the project's taskfile.yaml",
					Action:    runTask,
				},
				{
					Name:   "env",
					Usage:  "Manage environment variables",
					Before: requireProject,
					Action: func(ctx context.Context, cmd *cli.Command) error {
						return instantiateEnv(ctx, cmd, ".")
					},
				},
			},
		},
	}
)

func requireProject(ctx context.Context, cmd *cli.Command) error {
	var err error
	if project, err = loadProjectDetails(cmd); err != nil {
		if err = loadProjectConfig(ctx, cmd); err != nil {
			// something is wrong with config file
			return err
		}

		// choose from existing credentials or authenticate
		if len(cliConfig.Projects) > 0 {
			var options []huh.Option[*config.ProjectConfig]
			for _, p := range cliConfig.Projects {
				options = append(options, huh.NewOption(p.Name+" ["+p.APIKey+"]", &p))
			}
			if err = huh.NewSelect[*config.ProjectConfig]().
				Title("Select a project to use for this app").
				Description("If you'd like to use a different project, run `lk cloud auth` to add credentials").
				Options(options...).
				Value(&project).
				WithTheme(theme).
				Run(); err != nil {
				return err
			}
		} else {
			shouldAuth := true
			if err = huh.NewConfirm().
				Title("No local projects found. Authenticate one now?").
				Inline(true).
				Value(&shouldAuth).
				WithTheme(theme).
				Run(); err != nil {
				return err
			}
			if shouldAuth {
				initAuth(ctx, cmd)
				if err = tryAuthIfNeeded(ctx, cmd); err != nil {
					return err
				}
				return requireProject(ctx, cmd)
			} else {
				return errors.New("no project selected")
			}
		}
	}

	return err
}

func setupBootstrapTemplate(ctx context.Context, cmd *cli.Command) error {
	verbose := cmd.Bool("verbose")
	install := cmd.Bool("install")

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
			if t.IsSandbox {
				options = append(options, huh.NewOption(t.Name, t.URL))
			}
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

	if install {
		fmt.Println("Installing template...")
		return doInstall(ctx, bootstrap.TaskInstall, appName, verbose)
	} else {
		return doPostCreate(ctx, cmd, appName, false)
	}
}

func setupSandboxTemplate(ctx context.Context, cmd *cli.Command) error {
	verbose := cmd.Bool("verbose")
	install := cmd.Bool("install")

	if sandboxID == "" {
		return errors.New("sandbox ID is required")
	}

	token, err := requireToken(ctx, cmd)
	if err != nil {
		return err
	}

	details, err := bootstrap.FetchSandboxDetails(ctx, sandboxID, token, serverURL)
	if err != nil {
		return err
	}
	template = &details.Template

	fmt.Println("Cloning template...")
	if err := cloneTemplate(ctx, cmd, template.URL, details.Name); err != nil {
		return err
	}

	fmt.Println("Instantiating environment...")
	if err := instantiateEnv(ctx, cmd, details.Name); err != nil {
		return err
	}

	if install {
		fmt.Println("Installing template...")
		return doInstall(ctx, bootstrap.TaskInstallSandbox, details.Name, verbose)
	} else {
		return doPostCreate(ctx, cmd, details.Name, false)
	}
}

func cloneTemplate(_ context.Context, cmd *cli.Command, url, appName string) error {
	var cmdErr error
	if err := spinner.New().
		Title("Cloning template from " + url).
		Action(func() {
			c := exec.Command("git", "clone", "--depth=1", url, appName)
			var out []byte
			if out, cmdErr = c.CombinedOutput(); len(out) > 0 && cmd.Bool("verbose") {
				fmt.Println(string(out))
			}
			os.RemoveAll(path.Join(appName, ".git"))
		}).
		Style(theme.Focused.Title).
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
	return doInstall(ctx, bootstrap.TaskInstall, rootPath, verbose)
}

func doPostCreate(ctx context.Context, _ *cli.Command, rootPath string, isBootstrap bool) error {
	tf, err := bootstrap.ParseTaskfile(rootPath)
	if err != nil {
		return err
	}

	taskName := string(bootstrap.TaskPostCreateSandbox)
	if isBootstrap {
		taskName = string(bootstrap.TaskPostCreate)
	}

	task, err := bootstrap.NewTask(ctx, tf, rootPath, taskName, true)
	if err != nil {
		return err
	}
	var cmdErr error
	if err := spinner.New().
		Title("Running task " + taskName + "...").
		Action(func() { cmdErr = task() }).
		Style(theme.Focused.Title).
		Accessible(true).
		Run(); err != nil {
		return err
	}
	return cmdErr
}

func doInstall(ctx context.Context, task bootstrap.KnownTask, rootPath string, verbose bool) error {
	tf, err := bootstrap.ParseTaskfile(rootPath)
	if err != nil {
		return err
	}

	install, err := bootstrap.NewTask(ctx, tf, rootPath, string(task), verbose)
	if err != nil {
		return err
	}

	var cmdErr error
	if err := spinner.New().
		Title("Installing...").
		Action(func() { cmdErr = install() }).
		Style(theme.Focused.Title).
		Accessible(verbose).
		Run(); err != nil {
		return err
	}
	return cmdErr
}

func runTask(ctx context.Context, cmd *cli.Command) error {
	verbose := cmd.Bool("verbose")
	rootDir := "."
	tf, err := bootstrap.ParseTaskfile(rootDir)
	if err != nil {
		return err
	}

	taskName := cmd.Args().First()
	if taskName == "" {
		var options []huh.Option[string]
		for _, name := range tf.Tasks.Keys() {
			options = append(options, huh.NewOption(name, name))
		}

		if err := huh.NewSelect[string]().
			Title("Select Task").
			Options(options...).
			Value(&taskName).
			WithTheme(theme).
			Run(); err != nil {
			return err
		}
	}

	task, err := bootstrap.NewTask(ctx, tf, rootDir, taskName, verbose)
	if err != nil {
		return err
	}
	var cmdErr error
	if err := spinner.New().
		Title("Running task " + taskName + "...").
		Action(func() { cmdErr = task() }).
		Style(theme.Focused.Title).
		Accessible(verbose).
		Run(); err != nil {
		return err
	}
	return cmdErr
}
