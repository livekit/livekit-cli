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
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/mattn/go-isatty"
	"github.com/twitchtv/twirp"
	"github.com/urfave/cli/v3"

	"github.com/livekit/protocol/logger"
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
	// quietFlag is global. "silent" is kept as an alias for backwards compatibility with
	// the former per-command --silent flag; both resolve to the same value and feed the
	// Printer's Quiet gating.
	quietFlag = &cli.BoolFlag{
		Name:    "quiet",
		Aliases: []string{"q", "silent"},
		Usage:   "Suppress informational output to stderr (warnings and errors still print)",
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
		Hidden:      true,
	}
	installFlag = &cli.BoolFlag{
		Name:  "install",
		Usage: "Run installation after creating the application",
	}

	// out is the process-wide sink for human-facing CLI output. It is initialized
	// in main.go's root Before hook from the parsed root command, so all status,
	// warning, and result lines share consistent streams (stderr/stdout) and
	// --quiet gating. Before init it is nil and all methods no-op safely.
	out *util.Printer

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
		&cli.BoolFlag{
			Name:    "yes",
			Aliases: []string{"y"},
			Usage:   "Assume yes for confirmations; fail or use default for other prompts (use in CI/non-interactive)",
		},
		quietFlag,
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

// SkipPrompts returns true when the CLI should not prompt (e.g. --yes or non-interactive terminal).
// When true, confirmations are treated as accepted; selects/inputs should use a default or return an error.
func SkipPrompts(cmd *cli.Command) bool {
	return cmd.Bool("yes") || !isatty.IsTerminal(os.Stdin.Fd())
}

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

// projectSource records how a project's credentials were resolved. It feeds two things:
// the confirm-default prompt (only triggers for sourceDefault) and the notice string
// composed in resolveProject and emitted by callers via out.Status.
type projectSource int

const (
	sourceFlag        projectSource = iota // --project NAME
	sourceSubdomain                        // --subdomain SUBDOMAIN
	sourceEnv                              // credentials from LIVEKIT_* environment variables
	sourceInlineFlags                      // credentials from --url/--api-key/--api-secret (name-less; silent)
	sourceDev                              // --dev
	sourceTOML                             // livekit.toml in the working directory
	sourceDefault                          // configured default project
	sourceSelected                         // interactively picked, or added via `lk cloud auth`
)

// resolvedProject is the outcome of project resolution: the chosen credentials, how they
// were resolved, and (for sourceEnv) which env vars came through. Call rp.announce() to
// surface the outcome to the user — that is the single hand-off from resolution to output.
type resolvedProject struct {
	project *config.ProjectConfig
	source  projectSource
	envVars []string // which LIVEKIT_* vars were used, for sourceEnv
}

// announce surfaces how the project was resolved: a one-line breadcrumb through the
// package-level Printer (stderr, suppressed by --quiet), plus a structured debug log via
// protocol/logger (gated to --verbose). Centralizing the source→message mapping here keeps
// the resolver pure and gives us one place to evolve wording or wire color/decoration.
func (rp *resolvedProject) announce() {
	if rp == nil {
		return
	}
	switch rp.source {
	case sourceEnv:
		out.Statusf("Using %s from environment", strings.Join(rp.envVars, ", "))
	case sourceDev:
		out.Status("Using dev credentials")
	case sourceInlineFlags:
		// name-less credentials supplied directly via flags; nothing to surface
	default: // sourceFlag, sourceSubdomain, sourceTOML, sourceDefault, sourceSelected
		out.Statusf("Using project [%s]", util.Accented(rp.project.Name))
	}
	if rp.project != nil && rp.source != sourceDev {
		logger.Debugw("project resolved",
			"source", rp.source,
			"url", rp.project.URL,
			"api-key", rp.project.APIKey,
		)
	}
}

// resolveProject determines which project's credentials to use, in priority order:
//  1. --project flag
//  2. --subdomain flag
//  3. --url/--api-key/--api-secret flags or LIVEKIT_* env vars
//  4. --dev credentials
//  5. livekit.toml in the working directory
//  6. the configured default project
//
// It performs no prompting and no printing: callers print rp.notice via out.Status, and
// handle the no-project case (a returned error) by offering interactive selection.
func resolveProject(c *cli.Command, p loadParams) (*resolvedProject, error) {
	// 1. explicit project
	if c.String("project") != "" {
		if c.Bool("dev") {
			return nil, errors.New("both project and dev flags are set")
		}
		pc, err := config.LoadProject(c.String("project"))
		if err != nil {
			return nil, err
		}
		return &resolvedProject{project: pc, source: sourceFlag}, nil
	}

	// 2. explicit subdomain
	if c.String("subdomain") != "" {
		if c.Bool("dev") {
			return nil, errors.New("both subdomain and dev flags are set")
		}
		pc, err := config.LoadProjectBySubdomain(c.String("subdomain"))
		if err != nil {
			return nil, err
		}
		return &resolvedProject{project: pc, source: sourceSubdomain}, nil
	}

	// 3. inline credentials (flags or environment)
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
			return &resolvedProject{project: pc, source: sourceEnv, envVars: envVars}, nil
		}
		return &resolvedProject{project: pc, source: sourceInlineFlags}, nil
	}

	// 4. dev credentials
	if c.Bool("dev") {
		pc.APIKey = "devkey"
		pc.APISecret = "secret"
		return &resolvedProject{project: pc, source: sourceDev}, nil
	}

	// 5. livekit.toml in the working directory
	if _, err := requireConfig(workingDir, tomlFilename); errors.Is(err, config.ErrInvalidConfig) {
		return nil, err
	}
	if lkConfig != nil {
		pc, err := config.LoadProjectBySubdomain(lkConfig.Project.Subdomain)
		if err != nil {
			return nil, err
		}
		return &resolvedProject{project: pc, source: sourceTOML}, nil
	}

	// 6. configured default project
	if dp, err := config.LoadDefaultProject(); err == nil {
		return &resolvedProject{project: dp, source: sourceDefault}, nil
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
	return &resolvedProject{project: pc, source: sourceInlineFlags}, nil
}

// loadProjectDetails resolves project credentials for commands that consume the result
// directly (egress, ingress, token, …) and announces the resolution. Commands that rely on
// the package-level `project` (app/agent) go through requireProject instead, which layers
// interactive selection on top of the same resolver before announcing.
func loadProjectDetails(c *cli.Command, opts ...loadOption) (*config.ProjectConfig, error) {
	p := loadParams{requireURL: true}
	for _, opt := range opts {
		opt(&p)
	}
	rp, err := resolveProject(c, p)
	if err != nil {
		return nil, err
	}
	rp.announce()
	return rp.project, nil
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

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "--"
	}
	return t.Format(time.RFC3339)
}
