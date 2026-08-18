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
	"time"

	"connectrpc.com/connect"
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

// maybeShowUpgradeNotice nudges users who haven't adopted user-based auth to run
// `lk cloud auth`. It prints once per invocation to stderr (never stdout, so it
// can't corrupt piped data), is suppressed by --quiet, and is skipped for `cloud`
// commands (where it would be redundant), when a session already exists, and
// when the user explicitly opted into legacy auth (--legacy-auth or explicit
// API-key credentials) — they've made their choice, so don't nag them.
func maybeShowUpgradeNotice(cmd *cli.Command, conf *config.CLIConfig) {
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
	return public.New(experimentalAPIURL, token, connect.WithGRPC())
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

// resolveProjectRef resolves a project reference — an explicit positional value,
// or the global --project flag — to a project id using the signed-in user's
// cached projects (matched by id or by name/alias). On a cache miss it refreshes
// the cache from the API and retries once; a ref that's still unknown is returned
// as-is (assumed to be a literal project id). Only meaningful in experimental
// (user-auth) mode.
func resolveProjectRef(ctx context.Context, cmd *cli.Command, conf *config.CLIConfig, user *config.UserConfig, positional string) (string, error) {
	ref := positional
	if ref == "" {
		ref = cmd.String("project")
	}
	if ref == "" {
		return "", errors.New("a project id or name is required (pass it as an argument or via --project)")
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
