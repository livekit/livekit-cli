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
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/livekit/livekit-cli/pkg/config"
	"github.com/urfave/cli/v3"
)

type PackageManager string

const (
	NPM  PackageManager = "npm"
	PNPM PackageManager = "pnpm"
	Yarn PackageManager = "yarn"
)

const (
	templateBaseUrl = "https://github.com/livekit-examples"
)

var (
	template       string
	appName        string
	appNameRegex   = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)
	packageManager PackageManager
	AppCommands    = []*cli.Command{
		{
			Hidden:   true,
			Name:     "app",
			Category: "Core",
			Commands: []*cli.Command{
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
			},
		},
	}
)

func bootstrapApplication(ctx context.Context, cmd *cli.Command) error {
	cfg, err := loadProjectDetails(cmd)
	if err != nil {
		return err
	}

	var prompts []huh.Field

	appName = cmd.Args().First()
	if appName == "" {
		prompts = append(prompts, huh.NewInput().
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

	pm := cmd.String("package-manager")
	if pm == "" {
		pms, err := autodetectPackageManagers()
		if err != nil {
			return err
		}
		var opts []huh.Option[PackageManager]
		for _, p := range pms {
			opts = append(opts, huh.NewOption(string(p), p))
		}
		prompts = append(prompts, huh.NewSelect[PackageManager]().
			Title("Node Package Manager").
			Description("Some description").
			Options(opts...).
			Value(&packageManager).
			WithTheme(theme))
	} else {
		if !(pm == string(PNPM) || pm == string(NPM) || pm == string(Yarn)) {
			return errors.New("invalid package manager")
		}
	}

	if len(prompts) > 0 {
		var groups []*huh.Group
		for _, p := range prompts {
			groups = append(groups, huh.NewGroup(p))
		}
		if err := huh.NewForm(groups...).
			WithTheme(theme).
			RunWithContext(ctx); err != nil {
			return err
		}
	}

	if err := cloneRepository(template, appName); err != nil {
		return err
	}

	if err := installDependencies(appName, packageManager); err != nil {
		return err
	}

	if err := writeEnvironment(cfg, appName); err != nil {
		return err
	}

	if err := startDevServer(packageManager); err != nil {
		return err
	}

	return nil
}

func cloneRepository(templateName, appName string) error {
	url := templateBaseUrl + "/" + templateName
	cmd := exec.Command("git", "clone", url, appName)
	if o, err := cmd.CombinedOutput(); err != nil {
		return err
	} else {
		fmt.Println(string(o))
	}

	return nil
}

func writeEnvironment(cfg *config.ProjectConfig, appName string) error {
	env := strings.Builder{}
	if _, err := env.WriteString("LIVEKIT_API_KEY=" + cfg.APIKey); err != nil {
		return err
	}
	if _, err := env.WriteString("\nLIVEKIT_API_SECRET=" + cfg.APISecret); err != nil {
		return err
	}
	if _, err := env.WriteString("\nLIVEKIT_URL=" + cfg.URL); err != nil {
		return err
	}
	envPath := path.Join(appName, ".env.local")
	return os.WriteFile(envPath, []byte(env.String()), 0700)
}

func commandExists(command string) bool {
	_, err := exec.LookPath(command)
	return err == nil
}

func autodetectPackageManagers() ([]PackageManager, error) {
	var pms []PackageManager
	if commandExists(string(PNPM)) {
		pms = append(pms, PNPM)
	}
	if commandExists(string(NPM)) {
		pms = append(pms, NPM)
	}
	if commandExists(string(Yarn)) {
		pms = append(pms, Yarn)
	}
	if len(pms) == 0 {
		return pms, errors.New("must have one of pnpm, npm, or yarn installed")
	}
	return pms, nil
}

func installDependencies(appName string, pm PackageManager) error {
	if o, err := exec.Command("cd", appName).CombinedOutput(); err != nil {
		return err
	} else {
		fmt.Println(string(o))
	}
	if o, err := exec.Command(string(pm), "install").CombinedOutput(); err != nil {
		return err
	} else {
		fmt.Println(string(o))
	}

	return nil
}

func startDevServer(pm PackageManager) error {
	cmd := exec.Command(string(pm), "dev")
	if err := cmd.Run(); err != nil {
		return err
	}

	return nil
}
