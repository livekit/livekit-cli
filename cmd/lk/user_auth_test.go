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
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/public"
)

func TestMatchDefaultProject(t *testing.T) {
	user := &config.UserConfig{Projects: []config.UserProjectConfig{
		{ProjectId: "p_a", Name: "Alpha", Alias: "alpha", Subdomain: "alpha-xyz"},
		{ProjectId: "p_b", Name: "Beta", Alias: "beta", Subdomain: "beta-xyz"},
	}}

	cases := []struct {
		name   string
		conf   config.CLIConfig
		wantID string
	}{
		{"no default", config.CLIConfig{}, ""},
		{
			"api-key entry matched by project id",
			config.CLIConfig{DefaultProject: "mine", Projects: []config.ProjectConfig{{Name: "mine", ProjectId: "p_b"}}},
			"p_b",
		},
		{
			"api-key entry without id matched by url subdomain",
			config.CLIConfig{DefaultProject: "mine", Projects: []config.ProjectConfig{{Name: "mine", URL: "wss://alpha-xyz.livekit.cloud"}}},
			"p_a",
		},
		{
			// An api-key entry pins the default to that project; don't fall back
			// to a same-named project the user happens to have.
			"api-key entry for an inaccessible project",
			config.CLIConfig{DefaultProject: "Alpha", Projects: []config.ProjectConfig{{Name: "Alpha", ProjectId: "p_other"}}},
			"",
		},
		{"bare project id", config.CLIConfig{DefaultProject: "p_b"}, "p_b"},
		{"bare alias", config.CLIConfig{DefaultProject: "alpha"}, "p_a"},
		{"unknown ref", config.CLIConfig{DefaultProject: "nope"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchDefaultProject(&tc.conf, user)
			if tc.wantID == "" {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tc.wantID, got.ProjectId)
		})
	}
}

// TestUserAuthEnabled checks that --user-auth alone selects
// user-based auth: API-key credentials, from flags or the LIVEKIT_* env vars
// most shells have set, don't push the command back onto the API-key path.
func TestUserAuthEnabled(t *testing.T) {
	t.Setenv("LIVEKIT_URL", "wss://example.livekit.cloud")
	t.Setenv("LIVEKIT_API_KEY", "envkey")
	t.Setenv("LIVEKIT_API_SECRET", "envsecret")

	enabled := func(t *testing.T, args ...string) bool {
		t.Helper()
		var got bool
		cmd := &cli.Command{
			Name: "lk",
			Flags: []cli.Flag{
				&cli.BoolFlag{Name: "user-auth", Aliases: []string{"experimental-auth"}},
				&cli.BoolFlag{Name: "legacy-auth"},
				&cli.StringFlag{Name: "url", Sources: cli.EnvVars("LIVEKIT_URL")},
				&cli.StringFlag{Name: "api-key", Sources: cli.EnvVars("LIVEKIT_API_KEY")},
				&cli.StringFlag{Name: "api-secret", Sources: cli.EnvVars("LIVEKIT_API_SECRET")},
			},
			Action: func(_ context.Context, cmd *cli.Command) error {
				got = userAuthEnabled(cmd)
				return nil
			},
		}
		require.NoError(t, cmd.Run(context.Background(), append([]string{"lk"}, args...)))
		return got
	}

	assert.True(t, enabled(t, "--user-auth"), "env credentials must not override --user-auth")
	assert.True(t, enabled(t, "--user-auth", "--api-key", "k", "--api-secret", "s", "--url", "ws://x"),
		"flag credentials must not override --user-auth")
	assert.True(t, enabled(t, "--experimental-auth"), "the old --experimental-auth spelling still selects user auth")
	assert.False(t, enabled(t), "API-key auth remains the default")
	assert.False(t, enabled(t, "--legacy-auth"))
}

func TestRequireUserSessionAPIURL(t *testing.T) {
	const staging = "https://beta-api.staging.livekit.cloud"
	setHome(t, t.TempDir())
	conf := &config.CLIConfig{
		DefaultUser: "usr_stg",
		Users: []config.UserConfig{
			{Id: "usr_prod", Email: "alice@livekit.io", SessionToken: "prod"},
			{Id: "usr_stg", Email: "alice@livekit.io", SessionToken: "stg", APIURL: staging},
			{Id: "usr_bob", Email: "bob@livekit.io", SessionToken: "bob", APIURL: staging},
		},
	}
	require.NoError(t, conf.PersistQuietly())

	resolve := func(t *testing.T, args ...string) (token, apiURL string, err error) {
		t.Helper()
		prev := publicAPIURL
		t.Cleanup(func() { publicAPIURL = prev })
		cmd := &cli.Command{
			Name: "lk",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "user"},
				&cli.StringFlag{
					Name:        "public-api-url",
					Aliases:     []string{"experimental-api-url"},
					Value:       public.DefaultBaseURL,
					Destination: &publicAPIURL,
					Sources:     cli.EnvVars("LIVEKIT_API_URL"),
				},
			},
			Action: func(_ context.Context, cmd *cli.Command) error {
				_, user, err := requireUserSession(cmd)
				if err != nil {
					return err
				}
				token = user.SessionToken
				return nil
			},
		}
		err = cmd.Run(context.Background(), append([]string{"lk"}, args...))
		return token, publicAPIURL, err
	}

	t.Run("default user", func(t *testing.T) {
		token, apiURL, err := resolve(t)
		require.NoError(t, err)
		assert.Equal(t, "stg", token, "default user's API is used when no URL is given")
		assert.Equal(t, staging, apiURL)

		token, apiURL, err = resolve(t, "--public-api-url", public.DefaultBaseURL)
		require.NoError(t, err)
		assert.Equal(t, "prod", token, "an explicit URL overrides the default user's API")
		assert.Equal(t, public.DefaultBaseURL, apiURL)

		token, _, err = resolve(t, "--experimental-api-url", public.DefaultBaseURL)
		require.NoError(t, err)
		assert.Equal(t, "prod", token, "the old --experimental-api-url spelling still sets the URL")

		_, _, err = resolve(t, "--public-api-url", "https://other.example")
		assert.ErrorContains(t, err, `the default user "usr_stg" is signed in to `+staging+`, not https://other.example`,
			"an API nobody is signed in to names where the default user is")
	})

	t.Run("--user", func(t *testing.T) {
		token, apiURL, err := resolve(t, "--user", "bob@livekit.io")
		require.NoError(t, err)
		assert.Equal(t, "bob", token, "another user on the same API")
		assert.Equal(t, staging, apiURL, "--user's API is used when no URL is given")

		token, apiURL, err = resolve(t, "--user", "usr_prod")
		require.NoError(t, err)
		assert.Equal(t, "prod", token)
		assert.Equal(t, public.DefaultBaseURL, apiURL)

		token, _, err = resolve(t, "--user", "alice@livekit.io", "--public-api-url", staging)
		require.NoError(t, err)
		assert.Equal(t, "stg", token)

		_, _, err = resolve(t, "--user", "alice@livekit.io")
		assert.ErrorContains(t, err, "signed in to multiple APIs", "an email on several APIs needs a URL")

		_, _, err = resolve(t, "--user", "bob@livekit.io", "--public-api-url", public.DefaultBaseURL)
		assert.ErrorContains(t, err, "not signed in to", "an explicit --user never falls back to another user")

		_, _, err = resolve(t, "--user", "nobody@livekit.io")
		assert.ErrorContains(t, err, "not signed in")
	})

	t.Run("LIVEKIT_API_URL", func(t *testing.T) {
		t.Setenv("LIVEKIT_API_URL", public.DefaultBaseURL)
		token, _, err := resolve(t)
		require.NoError(t, err)
		assert.Equal(t, "prod", token, "LIVEKIT_API_URL overrides the default user's API")
	})
}

// TestSignInHint checks that re-auth hints name the user-auth sign-in (a bare
// `lk cloud auth` runs the API-key flow) and carry --public-api-url when
// the session belongs to a non-default API.
func TestSignInHint(t *testing.T) {
	const staging = "https://beta-api.staging.livekit.cloud"
	prev := publicAPIURL
	t.Cleanup(func() { publicAPIURL = prev })

	publicAPIURL = public.DefaultBaseURL
	assert.Equal(t, "`lk cloud auth --user-auth`", signInCommand())
	publicAPIURL = staging
	assert.Equal(t, "`lk cloud auth --user-auth --public-api-url "+staging+"`", signInCommand())

	setHome(t, t.TempDir())
	publicAPIURL = public.DefaultBaseURL
	_, _, err := requireUserSession(nil)
	assert.ErrorContains(t, err, "no user is signed in (run `lk cloud auth --user-auth` to sign in)")

	conf := &config.CLIConfig{
		DefaultUser: "usr_stg",
		Users:       []config.UserConfig{{Id: "usr_stg", SessionToken: "stg", SessionExpiry: 1, APIURL: staging}},
	}
	require.NoError(t, conf.PersistQuietly())
	_, _, err = requireUserSession(nil)
	assert.ErrorContains(t, err, "has expired (run `lk cloud auth --user-auth --public-api-url "+staging+"` to sign in again)",
		"the hint follows the API the default user signed in to")
}

// TestCloudAPIError checks how a Public API refusal reads once a command
// passes it through cloudAPIError. The scripts only check that commands do.
func TestCloudAPIError(t *testing.T) {
	prev := publicAPIURL
	t.Cleanup(func() { publicAPIURL = prev })
	publicAPIURL = public.DefaultBaseURL

	plain := errors.New("connection refused")
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"unauthenticated says how to sign in again",
			&public.APIError{Status: http.StatusUnauthorized, Message: "authentication required"},
			"authentication required (run `lk cloud auth --user-auth` to sign in again)"},
		{"permission denied says why and what to do instead",
			&public.APIError{Status: http.StatusForbidden, Message: "permission denied: this needs ADMIN on the project"},
			"permission denied: this needs ADMIN on the project — your account doesn't have access to this project or action. To act with API-key credentials instead, re-run without `--user-auth`"},
		{"not found passes through",
			&public.APIError{Status: http.StatusNotFound, Message: "project not found"},
			"project not found"},
		{"other errors pass through", plain, "connection refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := cloudAPIError(tc.err)
			assert.EqualError(t, err, tc.want)
			assert.ErrorIs(t, err, tc.err)
		})
	}
}

func TestSetUserAuthHidden(t *testing.T) {
	root := &cli.Command{Flags: globalFlags}
	for _, cmds := range [][]*cli.Command{ProjectCommands, WorkspaceCommands, UserCommands, CloudCommands, AgentCommands} {
		root.Commands = append(root.Commands, cmds...)
	}
	t.Cleanup(func() { setUserAuthHidden(root, true) })

	names := func(cmds []*cli.Command) []string {
		var out []string
		for _, c := range cmds {
			out = append(out, c.Name)
		}
		return out
	}
	project := root.Command("project")
	simulateList := root.Command("agent").Command("simulate").Command("list")

	setUserAuthHidden(root, true)
	assert.NotContains(t, names(root.VisibleCommands()), "workspace")
	assert.NotContains(t, names(project.VisibleCommands()), "member")
	assert.True(t, userAuthFlag.Hidden)
	assert.True(t, simulateList.Flags[1].(*cli.StringFlag).Hidden)

	setUserAuthHidden(root, false)
	assert.Contains(t, names(root.VisibleCommands()), "workspace")
	assert.Contains(t, names(root.VisibleCommands()), "user")
	assert.Contains(t, names(project.VisibleCommands()), "member")
	assert.Contains(t, names(root.Command("cloud").VisibleCommands()), "whoami")
	assert.False(t, userAuthFlag.Hidden)
	assert.False(t, simulateList.Flags[1].(*cli.StringFlag).Hidden)
}

// A Hidden command that requires --user-auth must carry the userAuthOnly
// marker, or it stays invisible to signed-in beta users.
func TestUserAuthOnlyCommandsMarked(t *testing.T) {
	var walk func(path string, cmds []*cli.Command)
	walk = func(path string, cmds []*cli.Command) {
		for _, c := range cmds {
			p := path + " " + c.Name
			if c.Hidden && strings.Contains(c.Usage, "requires --user-auth") {
				assert.Equal(t, true, c.Metadata[userAuthOnlyKey], "%s is not marked userAuthOnly", p)
			}
			walk(p, c.Commands)
		}
	}
	for _, cmds := range [][]*cli.Command{ProjectCommands, WorkspaceCommands, UserCommands, CloudCommands, AgentCommands, AnalyticsCommands} {
		walk("lk", cmds)
	}
}
