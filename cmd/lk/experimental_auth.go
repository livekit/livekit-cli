// Copyright 2026 LiveKit, Inc.
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
	"strings"
	"time"

	"charm.land/huh/v2"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// experimentalAuthEnabled reports whether the command should run in user-based
// (session) auth mode: the global --experimental-auth flag is set AND legacy
// auth wasn't explicitly requested. Explicit API-key credentials or --legacy-auth
// always win — passing them is an unambiguous signal to use the SDK/key path.
func experimentalAuthEnabled(cmd *cli.Command) bool {
	return cmd.Bool("experimental-auth") && !legacyAuthForced(cmd)
}

// legacyAuthForced reports whether the user explicitly opted into legacy
// (API-key) auth: via --legacy-auth, or by supplying --api-key/--api-secret
// (or their LIVEKIT_API_KEY/SECRET env equivalents). This takes precedence over
// --experimental-auth, and — once user-auth becomes the default — will be the
// way to opt back into the key-based flow.
func legacyAuthForced(cmd *cli.Command) bool {
	return cmd.Bool("legacy-auth") || (cmd.String("api-key") != "" && cmd.String("api-secret") != "" && cmd.String("url") != "")
}

// userAuthIsDefault reports whether user-based (session) auth is the default
// auth mode. It is false during the experimental phase, where user auth is
// strictly opt-in via --experimental-auth. Flip it to true when user auth
// becomes the default (phase 2): at that point `lk cloud auth` signs a user in
// by default and the upgrade nudge below becomes correct to show.
const userAuthIsDefault = false

// maybeShowUpgradeNotice nudges users who haven't adopted user-based auth to run
// `lk cloud auth`. It prints once per invocation to stderr (never stdout, so it
// can't corrupt piped data), is suppressed by --quiet, and is skipped for `cloud`
// commands (where it would be redundant), when a session already exists, and
// when the user explicitly opted into legacy auth (--legacy-auth or explicit
// API-key credentials) — they've made their choice, so don't nag them.
//
// It is dormant while user auth is experimental: nudging every API-key user to a
// mode that's opt-in (and that most commands don't yet support) would be noise,
// and the message would be wrong — today it takes `lk cloud auth --experimental-auth`.
// The userAuthIsDefault gate turns it on once that command form is the default.
func maybeShowUpgradeNotice(cmd *cli.Command, conf *config.CLIConfig) {
	if !userAuthIsDefault {
		return
	}
	if conf == nil || len(conf.Users) > 0 {
		return
	}
	if cmd.Args().First() == "cloud" || legacyAuthForced(cmd) {
		return
	}
	out.Statusf("Tip: run %s to upgrade to LiveKit's account-based API — richer access control and audit logging.", util.Accented("lk cloud auth"))
}

// experimentalAuthGate refuses a command that only supports API-key auth when
// the user requested experimental user-based auth. User auth routes through the
// Public API, which does not yet implement most operations; rather than
// silently fall back to API-key auth — a different security model than the user
// asked for — we fail clearly. Command paths that DO support user auth branch on
// experimentalAuthEnabled before reaching this gate.
func experimentalAuthGate(cmd *cli.Command) error {
	if experimentalAuthEnabled(cmd) {
		return errors.New("this command is not yet available under --experimental-auth (user-based auth); re-run without it to use API-key authentication")
	}
	return nil
}

// authModeFlags declares, for a dual-mode command, which flags are valid only in
// one auth mode. It lets a command reject cross-mode misuse up front with a clear
// message instead of silently ignoring a flag the active mode can't honor:
// legacyOnly flags need API-key (SDK) auth; experimentalOnly flags need
// --experimental-auth. (This is validated at the command level rather than via
// urfave MutuallyExclusiveFlags because the auth selector is a root flag and the
// rule is conditional on the resolved mode, not a pairwise exclusion.)
type authModeFlags struct {
	legacyOnly       []string
	experimentalOnly []string
}

// validate returns an error if any flag set on cmd belongs to the other auth mode.
func (a authModeFlags) validate(cmd *cli.Command) error {
	if experimentalAuthEnabled(cmd) {
		for _, name := range a.legacyOnly {
			if cmd.IsSet(name) {
				return fmt.Errorf("--%s is not supported with --experimental-auth", name)
			}
		}
		return nil
	}
	for _, name := range a.experimentalOnly {
		if cmd.IsSet(name) {
			return fmt.Errorf("--%s is only supported with --experimental-auth", name)
		}
	}
	return nil
}

// requireExperimentalAuth is the inverse gate: it refuses commands that only
// exist in user-based auth mode when --experimental-auth is not set. The
// Public API operations (e.g. ProjectService create/update/delete) have no
// API-key/SDK equivalent, so they are available only in that mode.
func requireExperimentalAuth(cmd *cli.Command) error {
	if !experimentalAuthEnabled(cmd) {
		return errors.New("this command is only available under --experimental-auth (user-based auth)")
	}
	return nil
}

// cloudAPIError annotates Public API failures with actionable hints. An expired
// or missing session suggests re-auth; a permission denial explains that the
// signed-in account/session lacks access and points at the API-key escape hatch
// (rather than silently falling back to API-key "admin" access). Other errors
// pass through unchanged.
func cloudAPIError(err error) error {
	switch {
	case public.IsUnauthenticated(err):
		return fmt.Errorf("%w (run `lk cloud auth` to sign in again)", err)
	case public.IsPermissionDenied(err):
		return fmt.Errorf("%w — your account doesn't have access to this project or action. "+
			"To act with API-key credentials instead, pass `--legacy-auth` (or `--api-key`/`--api-secret`)", err)
	default:
		return err
	}
}

// requireUserSession loads the CLI config and resolves the default user with a
// valid (unexpired) session, for commands running under --experimental-auth.
// The returned *CLIConfig is the same instance the user was read from, so
// callers may cache data on it (e.g. via SetUserProjects) and persist.
func requireUserSession(cmd *cli.Command) (*config.CLIConfig, *config.UserConfig, error) {
	conf, err := config.LoadOrCreate()
	if err != nil {
		return nil, nil, err
	}
	if conf.DefaultUser == "" {
		return nil, nil, errors.New("no user is signed in (run `lk cloud auth` to sign in)")
	}
	user := conf.GetUser(conf.DefaultUser)
	if user == nil {
		return nil, nil, fmt.Errorf("default user %q not found in config", conf.DefaultUser)
	}
	if !user.SessionValid() {
		return nil, nil, fmt.Errorf("session for %s has expired (run `lk cloud auth` to sign in again)", userLabel(user))
	}
	return conf, user, nil
}

// publicClientForToken builds a Public API client authenticated with the given
// session token, honoring --experimental-api-url.
func publicClientForToken(token string) (*public.Client, error) {
	return public.New(experimentalAPIURL, token)
}

// requireCloudClient is the entry point for the Public-API-only commands: it
// enforces --experimental-auth, then builds a client authenticated as the
// signed-in user. It returns the same *CLIConfig/*UserConfig the session was
// read from, so callers can resolve project refs and persist the cache.
func requireCloudClient(cmd *cli.Command) (*public.Client, *config.CLIConfig, *config.UserConfig, error) {
	if err := requireExperimentalAuth(cmd); err != nil {
		return nil, nil, nil, err
	}
	return newCloudAPIClient(cmd)
}

// newCloudAPIClient builds a Public API client authenticated as the default
// user, honoring --experimental-api-url.
func newCloudAPIClient(cmd *cli.Command) (*public.Client, *config.CLIConfig, *config.UserConfig, error) {
	conf, user, err := requireUserSession(cmd)
	if err != nil {
		return nil, nil, nil, err
	}
	client, err := publicClientForToken(user.SessionToken)
	if err != nil {
		return nil, nil, nil, err
	}
	return client, conf, user, nil
}

// fetchUserProjects lists the projects the given session can access, shaped for
// the per-user config cache (config.UserConfig.Projects).
func fetchUserProjects(ctx context.Context, sessionToken string) ([]config.UserProjectConfig, error) {
	client, err := publicClientForToken(sessionToken)
	if err != nil {
		return nil, err
	}
	projects, err := client.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	return projectCacheEntries(projects), nil
}

// refreshUserProjects re-fetches the signed-in user's projects from the Public
// API and updates the per-user cache in place, persisting quietly. Returns the
// fresh entries.
func refreshUserProjects(ctx context.Context, conf *config.CLIConfig, user *config.UserConfig) ([]config.UserProjectConfig, error) {
	entries, err := fetchUserProjects(ctx, user.SessionToken)
	if err != nil {
		return nil, err
	}
	user.Projects = entries
	user.ProjectsFetchedAt = time.Now().Unix()
	if err := conf.PersistQuietly(); err != nil {
		return nil, err
	}
	return entries, nil
}

// fetchUserWorkspaces lists the workspaces the given session can access, shaped
// for the per-user config cache (config.UserConfig.Workspaces).
func fetchUserWorkspaces(ctx context.Context, sessionToken string) ([]config.UserWorkspaceConfig, error) {
	client, err := publicClientForToken(sessionToken)
	if err != nil {
		return nil, err
	}
	// Best-effort cache of the first (large) page; cache resolution refreshes on miss.
	workspaces, _, err := client.ListWorkspaces(ctx, 200, "")
	if err != nil {
		return nil, err
	}
	return workspaceCacheEntries(workspaces), nil
}

// refreshUserWorkspaces re-fetches the signed-in user's workspaces and updates
// the per-user cache in place, persisting quietly. Mirrors refreshUserProjects.
func refreshUserWorkspaces(ctx context.Context, conf *config.CLIConfig, user *config.UserConfig) ([]config.UserWorkspaceConfig, error) {
	entries, err := fetchUserWorkspaces(ctx, user.SessionToken)
	if err != nil {
		return nil, err
	}
	user.Workspaces = entries
	user.WorkspacesFetchedAt = time.Now().Unix()
	if err := conf.PersistQuietly(); err != nil {
		return nil, err
	}
	return entries, nil
}

// resolveWorkspaceRef resolves a workspace reference (id, name, or alias) to a
// workspace id using the signed-in user's cached workspaces, refreshing on a
// miss and falling back to an interactive picker when no ref is given. Mirrors
// resolveProjectRef.
func resolveWorkspaceRef(ctx context.Context, cmd *cli.Command, conf *config.CLIConfig, user *config.UserConfig, ref string) (string, error) {
	if ref == "" {
		return pickUserWorkspace(ctx, cmd, conf, user)
	}
	if w := user.FindWorkspace(ref); w != nil {
		return w.WorkspaceId, nil
	}
	if _, err := refreshUserWorkspaces(ctx, conf, user); err != nil {
		return ref, nil
	}
	if w := user.FindWorkspace(ref); w != nil {
		return w.WorkspaceId, nil
	}
	return ref, nil
}

// pickUserWorkspace resolves a workspace id interactively when none was supplied,
// mirroring pickUserProject.
func pickUserWorkspace(ctx context.Context, cmd *cli.Command, conf *config.CLIConfig, user *config.UserConfig) (string, error) {
	entries := user.Workspaces
	if len(entries) == 0 {
		var err error
		if entries, err = refreshUserWorkspaces(ctx, conf, user); err != nil {
			return "", cloudAPIError(err)
		}
	}
	if len(entries) == 0 {
		return "", errors.New("no workspaces found for this account")
	}
	if SkipPrompts(cmd) {
		if len(entries) == 1 {
			return entries[0].WorkspaceId, nil
		}
		return "", errors.New("multiple workspaces available; specify one by id, name, or alias via --workspace")
	}

	selected := entries[0].WorkspaceId
	options := make([]huh.Option[string], 0, len(entries))
	for _, w := range entries {
		label := w.Name
		if w.Alias != "" && !strings.EqualFold(w.Alias, w.Name) {
			label = w.Name + " " + util.Dimmed(w.Alias)
		}
		options = append(options, huh.NewOption(label, w.WorkspaceId))
	}
	if err := huh.NewForm(
		huh.NewGroup(huh.NewSelect[string]().
			Title("Select a workspace to use for this action").
			Options(options...).
			Value(&selected).
			WithTheme(util.FormTheme()))).
		RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("no workspace selected: %w", err)
	}
	return selected, nil
}

// resolveProjectRef resolves a project reference — an explicit positional value,
// or the global --project flag — to a project id using the signed-in user's
// cached projects (matched by id or by name/alias). On a cache miss it refreshes
// the cache from the API and retries once; a ref that's still unknown is returned
// as-is (assumed to be a literal project id). When no ref is given it falls back
// to an interactive picker (see pickUserProject). Only meaningful in experimental
// (user-auth) mode.
func resolveProjectRef(ctx context.Context, cmd *cli.Command, conf *config.CLIConfig, user *config.UserConfig, positional string) (string, error) {
	ref := positional
	if ref == "" {
		ref = cmd.String("project")
	}
	if ref == "" {
		return pickUserProject(ctx, cmd, conf, user)
	}
	if p := user.FindProject(ref); p != nil {
		return p.ProjectId, nil
	}
	// Cache miss — it may be stale (e.g. a project created elsewhere). Refresh
	// and retry once; on refresh failure, fall back to treating ref as an id.
	if _, err := refreshUserProjects(ctx, conf, user); err != nil {
		return ref, nil
	}
	if p := user.FindProject(ref); p != nil {
		return p.ProjectId, nil
	}
	return ref, nil
}

// pickUserProject resolves a project id interactively when none was supplied,
// mirroring selectProject (the API-key picker): it chooses from the signed-in
// user's projects, refreshing the cache when it's empty. In non-interactive mode
// it auto-selects a sole project and otherwise errors telling the user to pass
// --project.
func pickUserProject(ctx context.Context, cmd *cli.Command, conf *config.CLIConfig, user *config.UserConfig) (string, error) {
	entries := user.Projects
	if len(entries) == 0 {
		var err error
		if entries, err = refreshUserProjects(ctx, conf, user); err != nil {
			return "", cloudAPIError(err)
		}
	}
	if len(entries) == 0 {
		return "", errors.New("no projects found for this account")
	}
	if SkipPrompts(cmd) {
		if len(entries) == 1 {
			return entries[0].ProjectId, nil
		}
		return "", errors.New("multiple projects available; set --project in non-interactive mode")
	}

	selected := entries[0].ProjectId
	options := make([]huh.Option[string], 0, len(entries))
	for _, p := range entries {
		label := p.Name
		if p.Subdomain != "" {
			label = p.Name + " " + util.Dimmed(p.Subdomain)
		}
		options = append(options, huh.NewOption(label, p.ProjectId))
	}
	if err := huh.NewForm(
		huh.NewGroup(huh.NewSelect[string]().
			Title("Select a project to use for this action").
			Options(options...).
			Value(&selected).
			WithTheme(util.FormTheme()))).
		RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("no project selected: %w", err)
	}
	return selected, nil
}

// userLabel is a human-friendly identifier for a user, preferring email.
func userLabel(u *config.UserConfig) string {
	switch {
	case u.Email != "":
		return u.Email
	case u.Name != "":
		return u.Name
	default:
		return u.Id
	}
}
