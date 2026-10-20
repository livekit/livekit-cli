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
	"cmp"
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

// userAuthEnabled reports whether the command should run in user-based
// (session) auth mode. The --user-auth flag alone decides: API-key
// credentials (--api-key/--api-secret/--url, or their LIVEKIT_* env
// equivalents) don't switch modes, since most users have those set in their
// shell and would otherwise be silently put on the API-key path.
func userAuthEnabled(cmd *cli.Command) bool {
	return cmd.Bool("user-auth")
}

// legacyAuthForced reports whether the user explicitly opted into legacy
// (API-key) auth via --legacy-auth. Once user-auth becomes the default, this
// will be the way to opt back into the key-based flow.
func legacyAuthForced(cmd *cli.Command) bool {
	return cmd.Bool("legacy-auth")
}

// userAuthIsDefault reports whether user-based (session) auth is the default
// auth mode. It is false during the experimental phase, where user auth is
// strictly opt-in via --user-auth. Flip it to true when user auth
// becomes the default (phase 2): at that point `lk cloud auth` signs a user in
// by default and the upgrade nudge below becomes correct to show.
const userAuthIsDefault = false

// maybeShowUpgradeNotice nudges users who haven't adopted user-based auth to run
// `lk cloud auth`. It prints once per invocation to stderr (never stdout, so it
// can't corrupt piped data), is suppressed by --quiet, and is skipped for `cloud`
// commands (where it would be redundant), when a session already exists, and
// when the user explicitly opted into legacy auth (--legacy-auth) — they've
// made their choice, so don't nag them.
//
// It is dormant while user auth is experimental: nudging every API-key user to a
// mode that's opt-in (and that most commands don't yet support) would be noise,
// and the message would be wrong — today it takes `lk cloud auth --user-auth`.
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

// userAuthGate refuses a command that only supports API-key auth when
// the user requested experimental user-based auth. User auth routes through the
// Public API, which does not yet implement most operations; rather than
// silently fall back to API-key auth — a different security model than the user
// asked for — we fail clearly. Command paths that DO support user auth branch on
// userAuthEnabled before reaching this gate.
func userAuthGate(cmd *cli.Command) error {
	if userAuthEnabled(cmd) {
		return errors.New("this command is not yet available under --user-auth (user-based auth); re-run without it to use API-key authentication")
	}
	return nil
}

// authModeFlags declares, for a dual-mode command, which flags are valid only in
// one auth mode. It lets a command reject cross-mode misuse up front with a clear
// message instead of silently ignoring a flag the active mode can't honor:
// legacyOnly flags need API-key (SDK) auth; experimentalOnly flags need
// --user-auth. (This is validated at the command level rather than via
// urfave MutuallyExclusiveFlags because the auth selector is a root flag and the
// rule is conditional on the resolved mode, not a pairwise exclusion.)
type authModeFlags struct {
	legacyOnly       []string
	experimentalOnly []string
}

// validate returns an error if any flag set on cmd belongs to the other auth mode.
func (a authModeFlags) validate(cmd *cli.Command) error {
	if userAuthEnabled(cmd) {
		for _, name := range a.legacyOnly {
			if cmd.IsSet(name) {
				return fmt.Errorf("--%s is not supported with --user-auth", name)
			}
		}
		return nil
	}
	for _, name := range a.experimentalOnly {
		if cmd.IsSet(name) {
			return fmt.Errorf("--%s is only supported with --user-auth", name)
		}
	}
	return nil
}

// requireUserAuth is the inverse gate: it refuses commands that only
// exist in user-based auth mode when --user-auth is not set. The
// Public API operations (e.g. ProjectService create/update/delete) have no
// API-key/SDK equivalent, so they are available only in that mode.
func requireUserAuth(cmd *cli.Command) error {
	if !userAuthEnabled(cmd) {
		return errors.New("this command is only available under --user-auth (user-based auth)")
	}
	return nil
}

// signInCommand is the command that signs a user in to the Public API this
// invocation targets. A bare `lk cloud auth` runs the API-key flow, so the hint
// must carry --user-auth, plus --public-api-url when the target isn't
// the default API (a session is only valid against the API that issued it).
func signInCommand() string {
	if config.SameAPIURL(publicAPIURL, public.DefaultBaseURL) {
		return "`lk cloud auth --user-auth`"
	}
	return fmt.Sprintf("`lk cloud auth --user-auth --public-api-url %s`", publicAPIURL)
}

// cloudAPIError annotates Public API failures with actionable hints. An expired
// or missing session suggests re-auth; a permission denial explains that the
// signed-in account/session lacks access and points at the API-key escape hatch
// (rather than silently falling back to API-key "admin" access): re-running
// without --user-auth. Other errors
// pass through unchanged.
func cloudAPIError(err error) error {
	switch {
	case public.IsUnauthenticated(err):
		return fmt.Errorf("%w (run %s to sign in again)", err, signInCommand())
	case public.IsPermissionDenied(err):
		return fmt.Errorf("%w — your account doesn't have access to this project or action. "+
			"To act with API-key credentials instead, re-run without `--user-auth`", err)
	default:
		return err
	}
}

// requireUserSession loads the CLI config and resolves the signed-in user with
// a valid (unexpired) session, for commands running under --user-auth:
// the --user flag if given, otherwise the default user (see resolveSessionUser).
// The returned *CLIConfig is the same instance the user was read from, so
// callers may cache data on it (e.g. via SetUserProjects) and persist.
func requireUserSession(cmd *cli.Command) (*config.CLIConfig, *config.UserConfig, error) {
	conf, err := config.LoadOrCreate()
	if err != nil {
		return nil, nil, err
	}
	var userRef string
	apiURLSet := false
	if cmd != nil {
		userRef = cmd.String("user")
		apiURLSet = cmd.IsSet("public-api-url")
	}
	user, err := resolveSessionUser(conf, userRef, apiURLSet)
	if err != nil {
		return nil, nil, err
	}
	if !user.SessionValid() {
		return nil, nil, fmt.Errorf("session for %s has expired (run %s to sign in again)", userLabel(user), signInCommand())
	}
	announceUser(user)
	return conf, user, nil
}

// userAnnounced records that announceUser has printed, so a command that
// resolves the session more than once still prints the breadcrumb only once.
var userAnnounced bool

// announceUser surfaces which signed-in user a command acts as — the user-auth
// counterpart of the "Using project" breadcrumb (stderr, suppressed by --quiet).
// The API is named when it isn't the default, e.g. for a staging sign-in.
func announceUser(user *config.UserConfig) {
	if userAnnounced || user == nil {
		return
	}
	userAnnounced = true
	if config.SameAPIURL(publicAPIURL, public.DefaultBaseURL) {
		out.Statusf("Using user [%s]", util.Accented(userLabel(user)))
	} else {
		out.Statusf("Using user [%s] on [%s]", util.Accented(userLabel(user)), util.Accented(publicAPIURL))
	}
}

// resolveSessionUser picks the user to act as: the one named by userRef (the
// --user flag; an id or email) if given, otherwise the default user.
//
// Unless apiURLSet (--public-api-url or LIVEKIT_API_URL was given), the
// Public API is the one that user signed in to, and publicAPIURL is
// updated so later Public API clients (publicClientForToken) follow suit. A
// --user signed in to several APIs is ambiguous without an explicit URL.
//
// A session is only valid against the API that issued it, so the user must be
// signed in to the target API. When the default user isn't, the only user
// signed in there (if exactly one) is used instead; an explicit --user never
// falls back.
func resolveSessionUser(conf *config.CLIConfig, userRef string, apiURLSet bool) (*config.UserConfig, error) {
	explicit := userRef != ""
	if !explicit {
		userRef = conf.DefaultUser
	}
	if len(conf.Users) == 0 {
		return nil, fmt.Errorf("no user is signed in (run %s to sign in)", signInCommand())
	}

	if userRef != "" && !apiURLSet {
		matches := conf.FindUsers(userRef)
		switch {
		case len(matches) == 0 && explicit:
			return nil, fmt.Errorf("user %q is not signed in (run %s to sign in)", userRef, signInCommand())
		case len(matches) > 1 && explicit:
			apis := make([]string, len(matches))
			for i, u := range matches {
				apis[i] = cmp.Or(u.APIURL, public.DefaultBaseURL)
			}
			return nil, fmt.Errorf("user %q is signed in to multiple APIs (%s); pass --public-api-url to choose one",
				userRef, strings.Join(apis, ", "))
		case len(matches) > 0:
			// For the default user, an id is unique across APIs; an email
			// signed in to several resolves to the first entry.
			if matches[0].APIURL != "" {
				publicAPIURL = matches[0].APIURL
			}
		}
	}

	if userRef != "" {
		if u := conf.GetUser(userRef, publicAPIURL); u != nil {
			return u, nil
		}
		if explicit {
			return nil, fmt.Errorf("user %q is not signed in to %s (run %s to sign in)", userRef, publicAPIURL, signInCommand())
		}
	}
	if users := conf.UsersForAPIURL(publicAPIURL); len(users) == 1 {
		return users[0], nil
	}
	return nil, fmt.Errorf("no user is signed in to %s (run %s to sign in)", publicAPIURL, signInCommand())
}

// publicClientForToken builds a Public API client authenticated with the given
// session token, honoring --public-api-url.
func publicClientForToken(token string) (*public.Client, error) {
	return public.New(publicAPIURL, token)
}

// requireCloudClient is the entry point for the Public-API-only commands: it
// enforces --user-auth, then builds a client authenticated as the
// signed-in user. It returns the same *CLIConfig/*UserConfig the session was
// read from, so callers can resolve project refs and persist the cache.
func requireCloudClient(cmd *cli.Command) (*public.Client, *config.CLIConfig, *config.UserConfig, error) {
	if err := requireUserAuth(cmd); err != nil {
		return nil, nil, nil, err
	}
	return newCloudAPIClient(cmd)
}

// newCloudAPIClient builds a Public API client authenticated as the default
// user, honoring --public-api-url.
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
// as-is (assumed to be a literal project id). When no ref is given it uses the
// configured default project (see defaultUserProject), then falls back to an
// interactive picker (see pickUserProject). Only meaningful in experimental
// (user-auth) mode.
func resolveProjectRef(ctx context.Context, cmd *cli.Command, conf *config.CLIConfig, user *config.UserConfig, positional string) (string, error) {
	ref := positional
	if ref == "" {
		ref = cmd.String("project")
	}
	if ref == "" {
		if id := useDefaultUserProject(ctx, conf, user); id != "" {
			return id, nil
		}
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

// useDefaultUserProject returns the id of the user's default project (or "" if
// none applies), announcing it via the shared "Using project" breadcrumb.
func useDefaultUserProject(ctx context.Context, conf *config.CLIConfig, user *config.UserConfig) string {
	p := defaultUserProject(ctx, conf, user)
	if p == nil {
		return ""
	}
	rp := &resolvedProject{project: &config.ProjectConfig{Name: cmp.Or(p.Name, p.ProjectId), ProjectId: p.ProjectId}, source: sourceDefault}
	rp.announce()
	return p.ProjectId
}

// defaultUserProject resolves the configured default_project to one of the
// signed-in user's projects, refreshing the cache once on a miss. It returns nil
// (with a warning) when a default is set but the user can't access it, so the
// caller falls back to the picker rather than failing every command.
func defaultUserProject(ctx context.Context, conf *config.CLIConfig, user *config.UserConfig) *config.UserProjectConfig {
	if conf.DefaultProject == "" {
		return nil
	}
	if p := matchDefaultProject(conf, user); p != nil {
		return p
	}
	if _, err := refreshUserProjects(ctx, conf, user); err == nil {
		if p := matchDefaultProject(conf, user); p != nil {
			return p
		}
	}
	out.Warnf("Default project %q is not accessible as %s; ignoring it", conf.DefaultProject, userLabel(user))
	return nil
}

// matchDefaultProject maps default_project onto the user's cached projects.
// default_project names an API-key project entry (set by `lk cloud auth` or
// `lk project set-default`), which is matched by its project id, then by the
// subdomain of its URL. With no such entry, default_project is treated as a
// project reference (id, name, or alias) in its own right.
func matchDefaultProject(conf *config.CLIConfig, user *config.UserConfig) *config.UserProjectConfig {
	ref := conf.DefaultProject
	for _, lp := range conf.Projects {
		if lp.Name != ref {
			continue
		}
		if lp.ProjectId != "" {
			return user.FindProject(lp.ProjectId)
		}
		if sub := util.ExtractSubdomain(lp.URL); sub != "" {
			for i := range user.Projects {
				if strings.EqualFold(user.Projects[i].Subdomain, sub) {
					return &user.Projects[i]
				}
			}
		}
		return nil
	}
	return user.FindProject(ref)
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
