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
	"regexp"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/huh/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/livekit/livekit-cli/v2/pkg/bootstrap"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/urfave/cli/v3"
)

var (
	template        *bootstrap.Template
	templateName    string
	templateURL     string
	sandboxID       string
	appName         string
	appNameRegex    = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)
	destinationFile string
	exampleFile     string
	project         *config.ProjectConfig
	AppCommands     = []*cli.Command{
		{
			Name:  "app",
			Usage: "Initialize and manage applications",
			Commands: []*cli.Command{
				{
					Name:      "create",
					Usage:     "Bootstrap a new application from a template or through guided creation",
					Before:    requireProject,
					Action:    setupTemplate,
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
						&cli.StringFlag{
							Name:        "sandbox",
							Usage:       "`NAME` of the sandbox, see your cloud dashboard",
							Destination: &sandboxID,
						},
						&cli.StringFlag{
							Name:        "server-url",
							Value:       cloudAPIServerURL,
							Destination: &serverURL,
							Hidden:      true,
						},
						&cli.BoolFlag{
							Name:    "install",
							Aliases: []string{"i"},
							Usage:   "Run installation tasks after creating the app",
							Hidden:  true,
						},
					},
				},
				{
					Name:   "list-templates",
					Usage:  "List available templates to bootstrap a new application",
					Flags:  []cli.Flag{jsonFlag},
					Action: listTemplates,
				},
				{
					Hidden:    true,
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
					Name:  "env",
					Usage: "Fill environment variables based on .env.example (optional) and project credentials",
					Flags: []cli.Flag{
						&cli.BoolFlag{
							Name:    "write",
							Aliases: []string{"w"},
							Usage:   "Write environment variables to file",
						},
						&cli.StringFlag{
							Name:        "destination",
							Aliases:     []string{"d"},
							Usage:       "Destination file path, when used with --write",
							Value:       ".env.local",
							TakesFile:   true,
							Destination: &destinationFile,
						},
						&cli.StringFlag{
							Name:        "example",
							Aliases:     []string{"e"},
							Usage:       "Example file path",
							Value:       ".env.example",
							TakesFile:   true,
							Destination: &exampleFile,
						},
					},
					ArgsUsage: "[DIR] location of the project directory (default: current directory)",
					Before:    requireProject,
					Action:    manageEnv,
				},
			},
		},
	}
)

func requireProject(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	var err error
	if project, err = loadProjectDetails(cmd); err != nil {
		if _, err = loadProjectConfig(ctx, cmd); err != nil {
			// something is wrong with config file
			return nil, err
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
				WithTheme(util.Theme).
				Run(); err != nil {
				return nil, err
			}
		} else {
			shouldAuth := true
			if err = huh.NewConfirm().
				Title("No local projects found. Authenticate one now?").
				Inline(true).
				Value(&shouldAuth).
				WithTheme(util.Theme).
				Run(); err != nil {
				return nil, err
			}
			if shouldAuth {
				initAuth(ctx, cmd)
				if err = tryAuthIfNeeded(ctx, cmd); err != nil {
					return nil, err
				}
				return requireProject(ctx, cmd)
			} else {
				return nil, errors.New("no project selected")
			}
		}
	}

	return nil, err
}

func listTemplates(ctx context.Context, cmd *cli.Command) error {
	templates, err := bootstrap.FetchTemplates(ctx)
	if err != nil {
		return err
	}

	if cmd.Bool("json") {
		util.PrintJSON(templates)
	} else {
		const maxDescLength = 64
		table := util.CreateTable().Headers("Template", "Description").BorderRow(true)
		for _, t := range templates {
			desc := strings.Join(util.WrapToLines(t.Desc, maxDescLength), "\n")
			url := util.Theme.Focused.Title.Render(t.URL)
			tags := util.Theme.Help.ShortDesc.Render("#" + strings.Join(t.Tags, " #"))
			table.Row(
				t.Name,
				desc+"\n\n"+url+"\n"+tags,
			)
		}
		fmt.Println(table)
	}
	return nil
}

func setupTemplate(ctx context.Context, cmd *cli.Command) error {
	verbose := cmd.Bool("verbose")
	install := cmd.Bool("install")
	isSandbox := sandboxID != ""

	var preinstallPrompts []huh.Field
	var templateOptions []bootstrap.Template

	if templateName != "" && templateURL != "" {
		return errors.New("only one of template or template-url can be specified")
	}

	if isSandbox {
		token, err := requireToken(ctx, cmd)
		if err != nil {
			return err
		}
		if templateURL == "" {
			details, err := bootstrap.FetchSandboxDetails(ctx, sandboxID, token, serverURL)
			if err != nil {
				return err
			}
			if len(details.ChildTemplates) == 0 {
				return errors.New("no child templates found for sandbox")
			}
			templateOptions = details.ChildTemplates
		}
	} else {
		var err error
		templateOptions, err = bootstrap.FetchTemplates(ctx)
		if err != nil {
			return err
		}
	}

	// if no template name or URL is specified, prompt user to choose from available templates
	if templateName == "" && templateURL == "" {
		templateSelect := huh.NewSelect[string]().
			Title("Select Template").
			Value(&templateURL).
			WithTheme(util.Theme)
		var options []huh.Option[string]
		for _, t := range templateOptions {
			descStyle := util.Theme.Help.ShortDesc
			optionText := t.Name + " " + descStyle.Render("#"+strings.Join(t.Tags, " #"))
			options = append(options, huh.NewOption(optionText, t.URL))
		}
		templateSelect.(*huh.Select[string]).Options(options...)
		preinstallPrompts = append(preinstallPrompts, templateSelect)
		// if templateName is specified, locate it in the list of templates
	} else if templateName != "" {
		for _, t := range templateOptions {
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

	appName = cmd.Args().First()
	if appName == "" {
		appName = sandboxID
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
			WithTheme(util.Theme))
	}

	if len(preinstallPrompts) > 0 {
		group := huh.NewGroup(preinstallPrompts...)
		if err := huh.NewForm(group).
			WithTheme(util.Theme).
			RunWithContext(ctx); err != nil {
			return err
		}
	}

	fmt.Println("Cloning template...")
	if err := cloneTemplate(ctx, cmd, templateURL, appName); err != nil {
		return err
	}

	tf, err := bootstrap.ParseTaskfile(appName)
	if err != nil {
		return err
	}

	fmt.Println("Instantiating environment...")
	addlEnv := &map[string]string{
		"LIVEKIT_SANDBOX_ID":             sandboxID,
		"NEXT_PUBLIC_LIVEKIT_SANDBOX_ID": sandboxID,
	}
	envOutputFile := ".env.local"
	envExampleFile := ".env.example"
	if tf != nil {
		if envFile, ok := tf.Vars.Get("env_file"); ok {
			if customOutput, ok := envFile.Value.(string); ok {
				envOutputFile = customOutput
			}
		}
		if envExample, ok := tf.Vars.Get("env_example"); ok {
			if customExample, ok := envExample.Value.(string); ok {
				envExampleFile = customExample
			}
		}
	}
	env, err := instantiateEnv(ctx, cmd, appName, addlEnv, envExampleFile)
	if err != nil {
		return err
	}

	bootstrap.WriteDotEnv(appName, envOutputFile, env)

	if install {
		fmt.Println("Installing template...")
		if err := doInstall(ctx, bootstrap.TaskInstall, appName, verbose); err != nil {
			return err
		}
	} else {
		if err := doPostCreate(ctx, cmd, appName, verbose); err != nil {
			return err
		}
	}

	return cleanupTemplate(ctx, cmd, appName)
}

func cloneTemplate(_ context.Context, cmd *cli.Command, url, appName string) error {
	var stdout string
	var stderr string
	var cmdErr error

	tempName, relocate, cleanup := util.UseTempPath(appName)
	defer cleanup()

	if err := spinner.New().
		Title("Cloning template from " + url).
		Action(func() {
			stdout, stderr, cmdErr = bootstrap.CloneTemplate(url, tempName)
		}).
		Style(util.Theme.Focused.Title).
		Run(); err != nil {
		return err
	}

	if len(stdout) > 0 && cmd.Bool("verbose") {
		fmt.Println(string(stdout))
	}
	if len(stderr) > 0 && cmd.Bool("verbose") {
		fmt.Fprintln(os.Stderr, string(stderr))
	}

	if cmdErr != nil {
		return cmdErr
	}
	return relocate()
}

func cleanupTemplate(ctx context.Context, cmd *cli.Command, appName string) error {
	return bootstrap.CleanupTemplate(appName)
}

func manageEnv(ctx context.Context, cmd *cli.Command) error {
	rootDir := cmd.Args().First()
	if rootDir == "" {
		rootDir = "."
	}

	env, err := instantiateEnv(ctx, cmd, rootDir, nil, exampleFile)
	if err != nil {
		return err
	}

	if cmd.Bool("write") {
		return bootstrap.WriteDotEnv(rootDir, destinationFile, env)
	} else {
		return bootstrap.PrintDotEnv(env)
	}
}

func instantiateEnv(ctx context.Context, cmd *cli.Command, rootPath string, addlEnv *map[string]string, exampleFile string) (map[string]string, error) {
	env := map[string]string{
		"LIVEKIT_API_KEY":         project.APIKey,
		"LIVEKIT_API_SECRET":      project.APISecret,
		"LIVEKIT_URL":             project.URL,
		"NEXT_PUBLIC_LIVEKIT_URL": project.URL,
	}
	if addlEnv != nil {
		for k, v := range *addlEnv {
			env[k] = v
		}
	}

	prompt := func(key, oldValue string) (string, error) {
		var newValue string
		if err := huh.NewInput().
			EchoMode(huh.EchoModePassword).
			Title("Enter " + key + "?").
			Placeholder(oldValue).
			Value(&newValue).
			WithTheme(util.Theme).
			Run(); err != nil || newValue == "" {
			return oldValue, err
		}
		return newValue, nil
	}

	return bootstrap.InstantiateDotEnv(ctx, rootPath, exampleFile, env, cmd.Bool("verbose"), prompt)
}

func installTemplate(ctx context.Context, cmd *cli.Command) error {
	verbose := cmd.Bool("verbose")
	rootPath := cmd.Args().First()
	if rootPath == "" {
		rootPath = "."
	}
	return doInstall(ctx, bootstrap.TaskInstall, rootPath, verbose)
}

func doPostCreate(ctx context.Context, _ *cli.Command, rootPath string, verbose bool) error {
	tf, err := bootstrap.ParseTaskfile(rootPath)
	if err != nil {
		return err
	}
	if tf == nil {
		return nil
	}

	task, err := bootstrap.NewTask(ctx, tf, rootPath, string(bootstrap.TaskPostCreate), verbose)
	if task == nil || err != nil {
		return nil
	}

	var cmdErr error
	if err := spinner.New().
		Title("Cleaning up...").
		TitleStyle(lipgloss.NewStyle()).
		Style(lipgloss.NewStyle()).
		Action(func() { cmdErr = task() }).
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
		Style(util.Theme.Focused.Title).
		Accessible(true).
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
			WithTheme(util.Theme).
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
		Style(util.Theme.Focused.Title).
		Accessible(verbose).
		Run(); err != nil {
		return err
	}
	return cmdErr
}
