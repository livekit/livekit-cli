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
	"maps"
	"os"
	"regexp"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/bootstrap"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

var (
	ErrNoProjectSelected = errors.New("no project selected")
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
	lkConfig        *config.LiveKitTOML
	AppCommands     = []*cli.Command{
		{
			Name:  "app",
			Usage: "Initialize and manage applications",
			Commands: []*cli.Command{
				{
					Name:      "create",
					Usage:     "Bootstrap a new application from a template or through guided creation",
					Action:    setupTemplate,
					ArgsUsage: "`APP_NAME`",
					Flags: []cli.Flag{
						templateFlag,
						templateURLFlag,
						sandboxFlag,
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
	return requireProjectWithOpts(ctx, cmd)
}

func requireProjectWithOpts(ctx context.Context, cmd *cli.Command, opts ...loadOption) (context.Context, error) {
	var err error
	if project != nil {
		return ctx, nil
	}
	if ctx, err = loadProjectConfig(ctx, cmd); err != nil {
		// something is wrong with CLI config file
		return ctx, err
	}
	if project, err = loadProjectDetails(cmd, opts...); err != nil {
		// something is wrong with project config file
		if errors.Is(err, config.ErrInvalidConfig) {
			return ctx, err
		}
		// choose from existing credentials or authenticate
		return selectProject(ctx, cmd)
	}

	return ctx, err
}

func selectProject(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	var err error

	if cliConfig != nil && len(cliConfig.Projects) > 0 {
		var options []huh.Option[*config.ProjectConfig]
		for _, p := range cliConfig.Projects {
			options = append(options, huh.NewOption(p.Name+" "+util.Dimmed(util.ExtractSubdomain(p.URL)), &p))
		}
		if err = huh.NewForm(
			huh.NewGroup(huh.NewSelect[*config.ProjectConfig]().
				Title("Select a project to use for this action").
				Description("To use a different project, run `lk cloud auth` to add credentials").
				Options(options...).
				Value(&project).
				WithTheme(util.Theme))).
			Run(); err != nil {
			return nil, fmt.Errorf("no project selected: %w", err)
		}
		fmt.Println("Using project [" + util.Accented(project.Name) + "]")
	} else {
		shouldAuth := true
		if err = huh.NewForm(huh.NewGroup(huh.NewConfirm().
			Title("No local projects found. Authenticate one?").
			Inline(true).
			Value(&shouldAuth).
			WithTheme(util.Theme))).
			Run(); err != nil {
			return nil, fmt.Errorf("no project selected: %w", err)
		}
		if shouldAuth {
			initAuth(ctx, cmd)
			if err = tryAuthIfNeeded(ctx, cmd); err != nil {
				return nil, fmt.Errorf("authentication failed: %w", err)
			}
			return requireProject(ctx, cmd)
		} else {
			return nil, ErrNoProjectSelected
		}
	}

	return ctx, nil
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
		_, err := requireProject(ctx, cmd)
		if err != nil {
			return err
		}
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
			if t.IsHidden {
				continue
			}
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

	if appName == "" {
		arg := cmd.Args().First()
		if arg != "" {
			appName = arg
		} else {
			preinstallPrompts = append(preinstallPrompts, huh.NewInput().
				Title("Application Name").
				Placeholder("my-app").
				Value(&appName).
				Validate(func(s string) error {
					if len(s) < 2 {
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
	}
	if err := doPostCreate(ctx, cmd, appName, verbose); err != nil {
		return err
	}

	return cleanupTemplate(ctx, cmd, appName)
}

func cloneTemplate(ctx context.Context, cmd *cli.Command, url, appName string) error {
	var stdout string
	var stderr string

	tempName, relocate, cleanup := util.UseTempPath(appName)
	defer cleanup()

	err := util.Await(
		"Cloning template from "+url,
		ctx,
		func(ctx context.Context) error {
			var cmdErr error
			stdout, stderr, cmdErr = bootstrap.CloneTemplate(url, tempName)
			return cmdErr
		},
	)

	// err is handled after checking stdout and stderr
	if len(stdout) > 0 && cmd.Bool("verbose") {
		fmt.Println(string(stdout))
	}
	if len(stderr) > 0 && cmd.Bool("verbose") {
		fmt.Fprintln(os.Stderr, string(stderr))
	}

	if err != nil {
		return err
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
	env := map[string]string{}
	if _, err := requireProject(ctx, cmd); err != nil {
		if !errors.Is(err, ErrNoProjectSelected) {
			return nil, err
		}
		// if no project is selected, we prompt for all environment variables including LIVEKIT_ ones
	} else {
		env = map[string]string{
			"LIVEKIT_API_KEY":         project.APIKey,
			"LIVEKIT_API_SECRET":      project.APISecret,
			"LIVEKIT_URL":             project.URL,
			"NEXT_PUBLIC_LIVEKIT_URL": project.URL,
		}
	}

	if addlEnv != nil {
		maps.Copy(env, *addlEnv)
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

	fmt.Println("Cleaning up...")
	return task()
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

	err = util.Await(
		"Installing...",
		ctx,
		func(ctx context.Context) error {
			return install()
		},
	)
	if err != nil {
		return err
	}
	return nil
}

func runTask(ctx context.Context, cmd *cli.Command) error {
	verbose := cmd.Bool("verbose")
	rootDir := "."

	args := cmd.Args().Tail()
	if len(args) > 0 {
		dirPath := args[0]
		if stat, err := os.Stat(dirPath); os.IsNotExist(err) {
			return fmt.Errorf("directory does not exist: %s", dirPath)
		} else if err != nil {
			return fmt.Errorf("error accessing directory %s: %v", dirPath, err)
		} else {
			if !stat.IsDir() {
				return fmt.Errorf("path is not a directory: %s", dirPath)
			}
			rootDir = dirPath
		}
	}

	_, err := bootstrap.ParseTaskfile(rootDir)
	if err != nil {
		return err
	}

	exe := bootstrap.NewTaskExecutor(rootDir, verbose)
	if err := exe.Setup(); err != nil {
		return fmt.Errorf("could not initialize task executor: %w", err)
	}

	taskName := cmd.Args().First()
	if taskName == "" {
		tasks, err := exe.GetTaskList()
		if err != nil {
			return err
		}

		var options []huh.Option[string]
		for _, t := range tasks {
			options = append(options, huh.NewOption(t.Name(), t.Name()))
		}

		if err := huh.NewForm(
			huh.NewGroup(huh.NewSelect[string]().
				Title("Select Task").
				Options(options...).
				Value(&taskName).
				WithTheme(util.Theme))).
			Run(); err != nil {
			return err
		}
	}

	task, err := bootstrap.NewTaskWithExecutor(ctx, exe, taskName, verbose)
	if err != nil {
		return err
	}
	return task()
}
