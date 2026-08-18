// Copyright 2022-2026 LiveKit, Inc.
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
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/public"
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
					Name:      "get",
					Usage:     "Get a LiveKit Cloud project by ID (requires --experimental-auth)",
					UsageText: "lk project get PROJECT_ID --experimental-auth",
					ArgsUsage: "PROJECT_ID",
					Action:    getUserProject,
					Flags:     []cli.Flag{jsonFlag},
				},
				{
					Name:      "create",
					Usage:     "Create a new LiveKit Cloud project (requires --experimental-auth)",
					UsageText: "lk project create PROJECT_NAME --experimental-auth",
					ArgsUsage: "PROJECT_NAME",
					Action:    createUserProject,
					Flags:     []cli.Flag{jsonFlag},
				},
				{
					Name:      "update",
					Usage:     "Rename a LiveKit Cloud project (requires --experimental-auth)",
					UsageText: "lk project update PROJECT_ID --name NEW_NAME --experimental-auth",
					ArgsUsage: "PROJECT_ID",
					Action:    updateUserProject,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:     "name",
							Usage:    "New project `NAME`",
							Required: true,
						},
						jsonFlag,
					},
				},
				{
					Name:      "delete",
					Usage:     "Delete a LiveKit Cloud project (requires --experimental-auth)",
					UsageText: "lk project delete PROJECT_ID --experimental-auth",
					ArgsUsage: "PROJECT_ID",
					Action:    deleteUserProject,
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

	if SkipPrompts(cmd) {
		p.Name = cmd.Args().Get(0)
		p.URL = cmd.String("url")
		p.APIKey = cmd.String("api-key")
		p.APISecret = cmd.String("api-secret")
		if p.Name == "" || p.URL == "" || p.APIKey == "" || p.APISecret == "" {
			return errors.New("non-interactive mode: provide project name as argument and --url, --api-key, --api-secret")
		}
		if err = validateName(p.Name); err != nil {
			return err
		}
		if _, err := url.Parse(p.URL); err != nil {
			return err
		}
		if len(p.APIKey) < 3 || len(p.APISecret) < 3 {
			return errors.New("api-key and api-secret must be at least 3 characters")
		}
		if cmd.Bool("default") || defaultProject == nil {
			cliConfig.DefaultProject = p.Name
		}
		cliConfig.Projects = append(cliConfig.Projects, p)
		if err = cliConfig.PersistIfNeeded(); err != nil {
			return err
		}
		listProjects(ctx, cmd)
		return nil
	}

	if p.Name = cmd.Args().Get(0); p.Name != "" {
		if err = validateName(p.Name); err != nil {
			return err
		}
		out.Statusf("  Project Name: %s", p.Name)
	} else {
		prompts = append(prompts, huh.NewInput().
			Title("Project Name").
			Placeholder("my-project").
			Prompt("").
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
		out.Statusf("  URL: %s", p.URL)
	} else {
		prompts = append(prompts, huh.NewInput().
			Title("Project URL").
			Placeholder("wss://my-project.livekit.cloud").
			Prompt("").
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
		out.Statusf("  API Key: %s", p.APIKey)
	} else {
		prompts = append(prompts, huh.NewInput().
			Title("API Key").
			Placeholder("APIxxxxxxxxxxxx").
			Prompt("").
			Validate(validateKey).
			Value(&p.APIKey))
	}

	// API Secret
	if p.APISecret = cmd.String("api-secret"); p.APISecret != "" {
		if err = validateKey(p.APISecret); err != nil {
			return err
		}
		out.Statusf("  API Secret: %s", p.APISecret)
	} else {
		prompts = append(prompts, huh.NewInput().
			Title("API Secret").
			Placeholder("****************************").
			Prompt("").
			Validate(validateKey).
			Value(&p.APISecret))
	}

	// if it's first project, make it default
	isDefault := false
	if cmd.Bool("default") || defaultProject == nil {
		cliConfig.DefaultProject = p.Name
	} else if !cmd.IsSet("default") {
		prompts = append(prompts, util.Confirm().
			Title("Make this project default?").
			Value(&isDefault).
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
	if experimentalAuthEnabled(cmd) {
		return listUserProjects(ctx, cmd)
	}

	if len(cliConfig.Projects) == 0 {
		out.Status("No projects configured, use `lk cloud auth` to authenticate a new project.")
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
			Headers("Name", "Project ID", "URL", "API Key")
		for _, p := range cliConfig.Projects {
			var pName string
			if p.Name == cliConfig.DefaultProject {
				pName = "* " + p.Name
			} else {
				pName = "  " + p.Name
			}
			table.Row(pName, p.ProjectId, p.URL, p.APIKey)
		}
		out.Result(table)
	}

	return nil
}

// listUserProjects lists the projects accessible to the signed-in user via the
// Public API (user-based auth). Invoked by `lk project list --experimental-auth`.
//
// NOTE: it fetches live on each call. The per-user project cache
// (config.UserConfig.Projects) exists for project *resolution* by scoped
// commands and is populated there; a read-only list stays quiet and does not
// persist config.
func listUserProjects(ctx context.Context, cmd *cli.Command) error {
	conf, user, err := requireUserSession(cmd)
	if err != nil {
		return err
	}

	// Listing refreshes the per-user cache from the API.
	entries, err := refreshUserProjects(ctx, conf, user)
	if err != nil {
		return cloudAPIError(err)
	}

	if cmd.Bool("json") {
		util.PrintJSON(entries)
		return nil
	}
	if len(entries) == 0 {
		out.Status("No projects found for this account.")
		return nil
	}
	out.Result(projectTable(entries))
	return nil
}

// getUserProject implements `lk project get PROJECT_ID` (user auth only).
func getUserProject(ctx context.Context, cmd *cli.Command) error {
	if err := requireExperimentalAuth(cmd); err != nil {
		return err
	}
	client, conf, user, err := newCloudAPIClient(cmd)
	if err != nil {
		return err
	}
	id, err := resolveProjectRef(ctx, cmd, conf, user, cmd.Args().First())
	if err != nil {
		return err
	}
	project, err := client.GetProject(ctx, id)
	if err != nil {
		return cloudAPIError(err)
	}
	return renderProject(cmd, *project)
}

// createUserProject implements `lk project create PROJECT_NAME` (user auth only).
func createUserProject(ctx context.Context, cmd *cli.Command) error {
	if err := requireExperimentalAuth(cmd); err != nil {
		return err
	}
	name := cmd.Args().First()
	if name == "" {
		return errors.New("project name is required")
	}
	client, _, _, err := newCloudAPIClient(cmd)
	if err != nil {
		return err
	}
	project, err := client.CreateProject(ctx, name)
	if err != nil {
		return cloudAPIError(err)
	}
	out.Statusf("Created project %s", util.Accented(project.Name))
	return renderProject(cmd, *project)
}

// updateUserProject implements `lk project update PROJECT_ID --name NAME` (user auth only).
func updateUserProject(ctx context.Context, cmd *cli.Command) error {
	if err := requireExperimentalAuth(cmd); err != nil {
		return err
	}
	client, conf, user, err := newCloudAPIClient(cmd)
	if err != nil {
		return err
	}
	id, err := resolveProjectRef(ctx, cmd, conf, user, cmd.Args().First())
	if err != nil {
		return err
	}
	project, err := client.UpdateProject(ctx, id, cmd.String("name"))
	if err != nil {
		return cloudAPIError(err)
	}
	out.Statusf("Updated project %s", util.Accented(project.ID))
	return renderProject(cmd, *project)
}

// deleteUserProject implements `lk project delete PROJECT_ID` (user auth only).
func deleteUserProject(ctx context.Context, cmd *cli.Command) error {
	if err := requireExperimentalAuth(cmd); err != nil {
		return err
	}
	client, conf, user, err := newCloudAPIClient(cmd)
	if err != nil {
		return err
	}
	id, err := resolveProjectRef(ctx, cmd, conf, user, cmd.Args().First())
	if err != nil {
		return err
	}

	if !SkipPrompts(cmd) {
		confirm := false
		if err := huh.NewForm(huh.NewGroup(util.Confirm().
			Title(fmt.Sprintf("Delete project %s? This cannot be undone.", id)).
			Value(&confirm).
			WithTheme(util.Theme))).
			Run(); err != nil {
			return err
		}
		if !confirm {
			return errors.New("aborted")
		}
	}

	if err := client.DeleteProject(ctx, id); err != nil {
		return cloudAPIError(err)
	}

	if cmd.Bool("json") {
		util.PrintJSON(map[string]any{"id": id, "deleted": true})
		return nil
	}
	out.Statusf("Deleted project %s", util.Accented(id))
	return nil
}

// projectCacheEntries builds per-user cache entries for the given projects,
// assigning each a unique URL-safe alias derived from its name (deduplicated
// with a numeric suffix, in list order). Used both to populate the config cache
// and to render project listings, so the displayed alias matches the one stored
// for --project lookup.
func projectCacheEntries(projects []public.Project) []config.UserProjectConfig {
	used := make(map[string]bool, len(projects))
	entries := make([]config.UserProjectConfig, len(projects))
	for i, p := range projects {
		base := projectAliasBase(p)
		alias := base
		for k := 2; alias != "" && used[alias]; k++ {
			alias = fmt.Sprintf("%s-%d", base, k)
		}
		if alias != "" {
			used[alias] = true
		}
		entries[i] = config.UserProjectConfig{
			ProjectId: p.ID,
			Name:      p.Name,
			Subdomain: p.Subdomain,
			Alias:     alias,
		}
	}
	return entries
}

// projectAliasBase derives a project's base alias: the subdomain with its
// generated suffix stripped (as the old API-key flow did via util.URLSafeName),
// falling back to a slug of the display name when no subdomain is present.
func projectAliasBase(p public.Project) string {
	if p.Subdomain != "" {
		if i := strings.LastIndex(p.Subdomain, "-"); i > 0 {
			return p.Subdomain[:i]
		}
		return p.Subdomain
	}
	return util.Slugify(p.Name)
}

// projectTable renders cached projects (alias, name, id) as a table.
func projectTable(projects []config.UserProjectConfig) *table.Table {
	t := util.CreateTable().Headers("Alias", "Name", "Project ID")
	for _, p := range projects {
		t.Row(p.Alias, p.Name, p.ProjectId)
	}
	return t
}

// renderProject outputs a single Public API project as JSON (--json) or a table.
func renderProject(cmd *cli.Command, project public.Project) error {
	entries := projectCacheEntries([]public.Project{project})
	if cmd.Bool("json") {
		util.PrintJSON(entries[0])
		return nil
	}
	out.Result(projectTable(entries))
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
		out.Statusf("Default project set to [%s]", util.Accented(p.Name))
		return nil
	}

	return config.ProjectNotFoundError(cliConfig.Projects)
}
