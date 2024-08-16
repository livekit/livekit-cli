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

const (
	templateBaseUrl = "https://github.com/livekit-examples"
)

var (
	template     string
	appName      string
	appNameRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)
	AppCommands  = []*cli.Command{
		{
			Hidden:   true,
			Name:     "app",
			Category: "Core",
			Commands: []*cli.Command{
				{
					Hidden: true,
					Name:   "task",
					Action: func(ctx context.Context, c *cli.Command) error {
						rootPath := c.Args().First()
						tf, err := bootstrap.ParseTaskfile(rootPath)
						if err != nil {
							return err
						}
						if err := bootstrap.ExecuteInstallTask(ctx, tf, rootPath, c.Bool("verbose")); err != nil {
							return err
						}

						fmt.Println("Installed project at " + rootPath)
						return nil
					},
				},
				{
					Name:      "create",
					Usage:     "Bootstrap a new application from a template or through guided creation",
					Action:    bootstrapApplication,
					ArgsUsage: "`APP_NAME`",
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:        "template",
							Usage:       "`TEMPLATE` to instantiate, see " + templateBaseUrl,
							Destination: &template,
							Required:    true,
						},
						&cli.StringFlag{
							Name:     "package-manager",
							Usage:    "The package mangeer `PM` to use in this application",
							Required: false,
						},
					},
				},
				{
					Name:      "install",
					Usage:     "Execute installation defined in " + bootstrap.BootstrapPath(),
					ArgsUsage: "`DIR` location or the project directory",
					Action: func(ctx context.Context, cmd *cli.Command) error {
						appPath := cmd.Args().First()
						if appPath == "" {
							appPath = "."
						}

						cfg, err := loadProjectDetails(cmd)
						if err != nil {
							return err
						}

						return setupRepository(ctx, cmd, cfg, appPath)
					},
				},
			},
		},
	}
)

func bootstrapApplication(ctx context.Context, cmd *cli.Command) error {
	cfg, err := loadProjectDetails(cmd)
	if err != nil {
		return err
	}

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

	if err := cloneRepository(ctx, cmd, template, appName); err != nil {
		return err
	}

	if err := setupRepository(ctx, cmd, cfg, appName); err != nil {
		return err
	}

	return nil
}

func cloneRepository(_ context.Context, cmd *cli.Command, templateName, appName string) error {
	url := templateBaseUrl + "/" + templateName
	var cmdErr error
	if err := spinner.New().
		Title("Cloning template from " + url).
		Action(func() {
			c := exec.Command("git", "clone", url, appName)
			var out []byte
			if out, cmdErr = c.CombinedOutput(); len(out) > 0 && cmd.Bool("verbose") {
				fmt.Println(string(out))
			}
			cmdErr = c.Run()
		}).
		Run(); err != nil {
		return err
	}
	return cmdErr
}

func setupRepository(ctx context.Context, cmd *cli.Command, cfg *config.ProjectConfig, appPath string) error {
	verbose := cmd.Bool("verbose")

	bootstrapPath := path.Join(appPath, bootstrap.BootstrapPath())
	bc, err := bootstrap.ParseBootstrapConfig(bootstrapPath)
	if err != nil {
		return err
	}

	if err := bc.ExecuteInstall(ctx, "root", appPath, verbose); err != nil {
		return err
	}

	env := map[string]string{
		"$LIVEKIT_API_KEY":    cfg.APIKey,
		"$LIVEKIT_API_SECRET": cfg.APISecret,
		"$LIVEKIT_URL":        cfg.URL,
		"$LIVEKIT_SANDBOX":    "TODO",
	}
	return bc.WriteDotEnv(ctx, appPath, env, verbose)
}
