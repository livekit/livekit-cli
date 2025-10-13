// Copyright 2021-2024 LiveKit, Inc.
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
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/joho/godotenv"
	"github.com/twitchtv/twirp"
	"github.com/urfave/cli/v3"

	"github.com/livekit/protocol/utils/interceptors"
	"github.com/livekit/server-sdk-go/v2/signalling"

	"github.com/livekit/livekit-cli/v2/pkg/bootstrap"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

const (
	cloudAPIServerURL = "https://cloud-api.livekit.io"
	cloudDashboardURL = "https://cloud.livekit.io"
)

var (
	printCurl    bool
	workingDir   string = "."
	tomlFilename string = config.LiveKitTOMLFile
	serverURL    string = cloudAPIServerURL
	dashboardURL string = cloudDashboardURL

	roomFlag = &TemplateStringFlag{
		Name:     "room",
		Aliases:  []string{"r"},
		Usage:    "`NAME` of the room (supports templates)",
		Required: true,
	}
	identityFlag = &TemplateStringFlag{
		Name:     "identity",
		Aliases:  []string{"i"},
		Usage:    "`ID` of participant (supports templates)",
		Required: true,
	}
	jsonFlag = &cli.BoolFlag{
		Name:    "json",
		Aliases: []string{"j"},
		Usage:   "Output as JSON",
	}
	silentFlag = &cli.BoolFlag{
		Name:     "silent",
		Usage:    "If set, will not prompt for confirmation",
		Required: false,
		Value:    false,
	}
	templateFlag = &cli.StringFlag{
		Name:        "template",
		Usage:       "`TEMPLATE` to instantiate, see " + bootstrap.TemplateBaseURL,
		Destination: &templateName,
	}
	templateURLFlag = &cli.StringFlag{
		Name:        "template-url",
		Usage:       "`URL` to instantiate, must contain a taskfile.yaml",
		Destination: &templateURL,
	}
	sandboxFlag = &cli.StringFlag{
		Name:        "sandbox",
		Usage:       "`NAME` of the sandbox, see your cloud dashboard",
		Destination: &sandboxID,
	}

	openFlag    = util.OpenFlag
	globalFlags = []cli.Flag{
		&cli.StringFlag{
			Name:    "url",
			Usage:   "`URL` to LiveKit instance",
			Sources: cli.EnvVars("LIVEKIT_URL"),
			Value:   "http://localhost:7880",
		},
		&cli.StringFlag{
			Name:    "api-key",
			Usage:   "Your `KEY`",
			Sources: cli.EnvVars("LIVEKIT_API_KEY"),
		},
		&cli.StringFlag{
			Name:    "api-secret",
			Usage:   "Your `SECRET`",
			Sources: cli.EnvVars("LIVEKIT_API_SECRET"),
		},
		&cli.BoolFlag{
			Name:  "dev",
			Usage: "Use developer credentials for local LiveKit server",
		},
		&cli.StringFlag{
			Name:  "project",
			Usage: "`NAME` of a configured project",
		},
		&cli.StringFlag{
			Name:  "subdomain",
			Usage: "`SUBDOMAIN` of a configured project",
		},
		&cli.StringFlag{
			Name:        "config",
			Usage:       "Config `TOML` to use in the working directory",
			Value:       config.LiveKitTOMLFile,
			Destination: &tomlFilename,
			Required:    false,
		},
		&cli.BoolFlag{
			Name:        "curl",
			Usage:       "Print curl commands for API actions",
			Destination: &printCurl,
			Required:    false,
		},
		&cli.BoolFlag{
			Name:     "verbose",
			Required: false,
		},
		&cli.StringFlag{
			Name:        "server-url",
			Value:       cloudAPIServerURL,
			Destination: &serverURL,
			Hidden:      true,
		},
		&cli.StringFlag{
			Name:        "dashboard-url",
			Value:       cloudDashboardURL,
			Destination: &dashboardURL,
			Hidden:      true,
		},
	}
)

func optional[T any, C any, VC cli.ValueCreator[T, C]](flag *cli.FlagBase[T, C, VC]) *cli.FlagBase[T, C, VC] {
	newFlag := *flag
	newFlag.Required = false
	return &newFlag
}

func hidden[T any, C any, VC cli.ValueCreator[T, C]](flag *cli.FlagBase[T, C, VC]) *cli.FlagBase[T, C, VC] {
	newFlag := *flag
	newFlag.Hidden = true
	return &newFlag
}

func withDefaultClientOpts(c *config.ProjectConfig) []twirp.ClientOption {
	var (
		opts []twirp.ClientOption
		ics  []twirp.Interceptor
	)
	if printCurl {
		ics = append(ics, interceptors.NewCurlPrinter(os.Stdout, signalling.ToHttpURL(c.URL)))
	}
	if len(ics) != 0 {
		opts = append(opts, twirp.WithClientInterceptors(ics...))
	}
	return opts
}

func extractArg(c *cli.Command) (string, error) {
	if !c.Args().Present() {
		return "", errors.New("no argument provided")
	}
	return c.Args().First(), nil
}

func extractArgs(c *cli.Command) ([]string, error) {
	if !c.Args().Present() {
		return nil, errors.New("no arguments provided")
	}
	return c.Args().Slice(), nil
}

func extractFlagOrArg(c *cli.Command, flag string) (string, error) {
	value := c.String(flag)
	if value == "" {
		argValue := c.Args().First()
		if argValue == "" {
			return "", fmt.Errorf("no option or argument found for \"--%s\"", flag)
		}
		value = argValue
	}
	return value, nil
}

func parseKeyValuePairs(c *cli.Command, flag string) (map[string]string, error) {
	pairs := c.StringSlice(flag)
	if len(pairs) == 0 {
		return nil, nil
	}

	result := make(map[string]string, len(pairs))

	for _, pair := range pairs {
		if m, err := godotenv.Unmarshal(pair); err != nil {
			return nil, fmt.Errorf("invalid key-value pair: %s: %w", pair, err)
		} else {
			maps.Copy(result, m)
		}
	}
	return result, nil
}

type loadParams struct {
	requireURL     bool
	confirmProject bool
}

type loadOption func(*loadParams)

var (
	ignoreURL = func(p *loadParams) {
		p.requireURL = false
	}
	confirmProject = func(p *loadParams) {
		p.confirmProject = true
	}
)

// attempt to load connection config, it'll prioritize
// 1. command line flags (or env var)
// 2. config file (by default, livekit.toml)
// 3. default project config
func loadProjectDetails(c *cli.Command, opts ...loadOption) (*config.ProjectConfig, error) {
	p := loadParams{requireURL: true, confirmProject: false}
	for _, opt := range opts {
		opt(&p)
	}
	logDetails := func(c *cli.Command, pc *config.ProjectConfig) {
		if c.Bool("verbose") {
			fmt.Printf("URL: %s, api-key: %s, api-secret: %s\n",
				pc.URL,
				pc.APIKey,
				"************",
			)
		}

	}

	// if explicit project is defined, then use it
	if c.String("project") != "" {
		if c.Bool("dev") {
			return nil, errors.New("both project and dev flags are set")
		}
		pc, err := config.LoadProject(c.String("project"))
		if err != nil {
			return nil, err
		}
		fmt.Println("Using project [" + util.Accented(c.String("project")) + "]")
		logDetails(c, pc)
		return pc, nil
	}

	// if explicit subdomain is provided, use it
	if c.String("subdomain") != "" {
		if c.Bool("dev") {
			return nil, errors.New("both subdomain and dev flags are set")
		}
		pc, err := config.LoadProjectBySubdomain(c.String("subdomain"))
		if err != nil {
			return nil, err
		}
		fmt.Println("Using project [" + util.Accented(pc.Name) + "]")
		logDetails(c, pc)
		return pc, nil
	}

	pc := &config.ProjectConfig{}
	if val := c.String("url"); val != "" {
		pc.URL = val
	}
	if val := c.String("api-key"); val != "" {
		if c.Bool("dev") {
			return nil, errors.New("both api-key and dev flags are set")
		}
		pc.APIKey = val
	}
	if val := c.String("api-secret"); val != "" {
		if c.Bool("dev") {
			return nil, errors.New("both api-secret and dev flags are set")
		}
		pc.APISecret = val
	}
	if pc.APIKey != "" && pc.APISecret != "" && (pc.URL != "" || !p.requireURL) {
		var envVars []string
		// if it's set via env, we should let users know
		if os.Getenv("LIVEKIT_URL") == pc.URL && pc.URL != "" {
			envVars = append(envVars, "url")
		}
		if os.Getenv("LIVEKIT_API_KEY") == pc.APIKey {
			envVars = append(envVars, "api-key")
		}
		if os.Getenv("LIVEKIT_API_SECRET") == pc.APISecret {
			envVars = append(envVars, "api-secret")
		}
		if len(envVars) > 0 {
			fmt.Printf("Using %s from environment\n", strings.Join(envVars, ", "))
			logDetails(c, pc)
		}
		return pc, nil
	}
	if c.Bool("dev") {
		pc.APIKey = "devkey"
		pc.APISecret = "secret"
		fmt.Println("Using dev credentials")
		return pc, nil
	}

	// load from config file
	_, err := requireConfig(workingDir, tomlFilename)
	if errors.Is(err, config.ErrInvalidConfig) {
		return nil, err
	}
	if lkConfig != nil {
		return config.LoadProjectBySubdomain(lkConfig.Project.Subdomain)
	}

	// load default project
	dp, err := config.LoadDefaultProject()
	if err == nil {
		if p.confirmProject {
			if dp != nil && len(cliConfig.Projects) > 1 && !c.Bool("silent") {
				useDefault := true
				if err := huh.NewForm(huh.NewGroup(huh.NewConfirm().
					Title(fmt.Sprintf("Use project [%s] (%s) to create agent?", dp.Name, dp.URL)).
					Value(&useDefault).
					Negative("Select another").
					Inline(false).
					WithTheme(util.Theme))).
					Run(); err != nil {
					return nil, fmt.Errorf("failed to confirm project: %w", err)
				}
				if !useDefault {
					if _, err = selectProject(context.Background(), c); err != nil {
						return nil, err
					}
					fmt.Printf("Using project [%s]\n", util.Accented(project.Name))
					return project, nil
				}
			}
		} else {
			if !c.Bool("silent") {
				fmt.Println("Using default project [" + util.Theme.Focused.Title.Render(dp.Name) + "]")
				logDetails(c, dp)
			}
		}
		return dp, nil
	}

	if p.requireURL && pc.URL == "" {
		return nil, errors.New("url is required")
	}
	if pc.APIKey == "" {
		return nil, errors.New("api-key is required")
	}
	if pc.APISecret == "" {
		return nil, errors.New("api-secret is required")
	}

	// cannot happen
	return pc, nil
}

type TemplateStringFlag = cli.FlagBase[string, cli.StringConfig, templateStringValue]

type templateStringValue struct {
	destination *string
	trimSpace   bool
}

func (s templateStringValue) Create(val string, p *string, c cli.StringConfig) cli.Value {
	*p = util.ExpandTemplate(val)
	return &templateStringValue{
		destination: p,
		trimSpace:   c.TrimSpace,
	}
}

func (s templateStringValue) ToString(val string) string {
	if val == "" {
		return val
	}
	return fmt.Sprintf("%q", val)
}

func (s *templateStringValue) Get() any { return util.ExpandTemplate(*s.destination) }

func (s *templateStringValue) Set(val string) error {
	if s.trimSpace {
		val = strings.TrimSpace(val)
	}
	*s.destination = util.ExpandTemplate(val)
	return nil
}

func (s *templateStringValue) String() string {
	if s.destination != nil {
		return *s.destination
	}
	return ""
}
