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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/tokenauth"
)

func writeEnvFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600))
}

func TestEnvFileCredentials(t *testing.T) {
	dir := t.TempDir()
	creds, file, err := envFileCredentials(dir, agentEnvFiles...)
	require.NoError(t, err)
	assert.Empty(t, creds.apiKey, "no files means no credentials")
	assert.Empty(t, file)

	// A file with only half the pair doesn't count.
	writeEnvFile(t, dir, ".env.local", "LIVEKIT_API_KEY=APIlocal\n")
	writeEnvFile(t, dir, ".env", "LIVEKIT_URL=wss://p.livekit.cloud\nLIVEKIT_API_KEY=APIenv\nLIVEKIT_API_SECRET=envsecret\n")
	creds, file, err = envFileCredentials(dir, agentEnvFiles...)
	require.NoError(t, err)
	assert.Equal(t, ".env", file)
	assert.Equal(t, agentCredentials{url: "wss://p.livekit.cloud", apiKey: "APIenv", apiSecret: "envsecret"}, creds)

	// .env.local wins once complete.
	writeEnvFile(t, dir, ".env.local", "LIVEKIT_API_KEY=APIlocal\nLIVEKIT_API_SECRET=localsecret\n")
	creds, file, err = envFileCredentials(dir, agentEnvFiles...)
	require.NoError(t, err)
	assert.Equal(t, ".env.local", file)
	assert.Equal(t, "APIlocal", creds.apiKey)
}

func TestAppEnvFiles(t *testing.T) {
	assert.Equal(t, []string{".env.local", ".env"}, appEnvFiles(""))
	assert.Equal(t, []string{".env.local", ".env"}, appEnvFiles(".env.local"))
	assert.Equal(t, []string{".env.dev", ".env.local", ".env"}, appEnvFiles(".env.dev"))
}

func runWithProjectFlag(t *testing.T, args []string, action cli.ActionFunc) {
	t.Helper()
	app := &cli.Command{
		Name:   "lk",
		Flags:  []cli.Flag{&cli.StringFlag{Name: "project"}},
		Action: action,
	}
	require.NoError(t, app.Run(context.Background(), append([]string{"lk"}, args...)))
}

func TestTaskfileEnvFile(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, []string{".env.local", ".env"}, agentEnvFileNames(dir), "no Taskfile")
	writeEnvFile(t, dir, "taskfile.yaml", "version: '3'\nvars:\n  env_file: .env.agent\n")
	assert.Equal(t, []string{".env.agent", ".env.local", ".env"}, agentEnvFileNames(dir))
	writeEnvFile(t, dir, "taskfile.yaml", "not: [valid")
	assert.Equal(t, []string{".env.local", ".env"}, agentEnvFileNames(dir), "a broken Taskfile is ignored")
	assert.Equal(t, []string{".env.local", ".env"}, agentEnvFileNames(""))
}

func TestUniqueProjectName(t *testing.T) {
	conf := &config.CLIConfig{Projects: []config.ProjectConfig{{Name: "my-app"}, {Name: "my-app-2"}}}
	assert.Equal(t, "my-app-3", uniqueProjectName(conf, "my app", "wss://x.livekit.cloud"))
	assert.Equal(t, "other", uniqueProjectName(conf, "other", ""))
	assert.Equal(t, "sub", uniqueProjectName(conf, "", "wss://sub.livekit.cloud"))
	assert.Equal(t, "project", uniqueProjectName(conf, "!!", ""))
}

func TestMissingKeyError(t *testing.T) {
	prev := cliConfig
	t.Cleanup(func() { cliConfig = prev })
	cliConfig = &config.CLIConfig{}

	pc := &config.ProjectConfig{Name: "one", URL: "wss://one.livekit.cloud"}
	err := missingKeyError(pc, keyRequest{dir: "agent", files: agentEnvFiles, purpose: "to run the agent with"},
		"your role on project [one] can't create API keys (that takes Admin)", nil)
	assert.EqualError(t, err, `no LiveKit API key to run the agent with for project [one]: your role on project [one] can't create API keys (that takes Admin)

To supply one, do one of:
  - re-run in a terminal and enter a key when asked (find or create one under Settings › API keys at `+dashboardURL+`, or ask a project admin)
  - add LIVEKIT_URL=wss://one.livekit.cloud, LIVEKIT_API_KEY and LIVEKIT_API_SECRET to agent/.env.local
  - save a key for lk to use: lk project add one --url wss://one.livekit.cloud --api-key KEY --api-secret SECRET
  - pass --api-key and --api-secret, or set LIVEKIT_API_KEY and LIVEKIT_API_SECRET`)

	// No project at all, with a signed-in user API-key mode didn't use.
	err = missingKeyError(nil, keyRequest{purpose: "to run the agent with"}, "no LiveKit project is configured",
		&config.UserConfig{Email: "dev@example.com"})
	assert.ErrorContains(t, err, "run `lk cloud auth` in a terminal")
	assert.ErrorContains(t, err, "re-run with --user-auth to use your signed-in account (dev@example.com)")
	assert.NotContains(t, err.Error(), ".env.local", "no directory, no env file advice")
}

func TestEnsureProjectAPIKeyReusesEnvFile(t *testing.T) {
	prev, prevCheck := project, checkAPIKey
	t.Cleanup(func() { project, checkAPIKey = prev, prevCheck })
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	rejected := map[string]bool{}
	checkAPIKey = func(_ context.Context, _, apiKey, _ string) error {
		if rejected[apiKey] {
			return errKeyRejected
		}
		return nil
	}
	session := &config.ProjectConfig{
		Name: "p", ProjectId: "p_1", URL: "wss://p.livekit.cloud",
		APIKey: tokenauth.PlaceholderAPIKey, APISecret: tokenauth.PlaceholderAPISecret,
	}

	dir := t.TempDir()
	writeEnvFile(t, dir, ".env.local", "LIVEKIT_URL=wss://p.livekit.cloud/\nLIVEKIT_API_KEY=APIk\nLIVEKIT_API_SECRET=s\n")
	project = session
	runWithProjectFlag(t, nil, func(ctx context.Context, cmd *cli.Command) error {
		require.NoError(t, ensureProjectAPIKey(ctx, cmd, dir, appEnvFiles("")...))
		return nil
	})
	assert.Equal(t, "APIk", project.APIKey)
	assert.Equal(t, "s", project.APISecret)
	assert.False(t, isSessionProject(project))
	assert.True(t, isSessionProject(session), "the session project itself is not modified")

	// A key LiveKit rejects isn't used for the app: the chain goes on (and,
	// with no terminal and no way to create one, says how to supply a key).
	rejected["APIk"] = true
	project = session
	runWithProjectFlag(t, nil, func(ctx context.Context, cmd *cli.Command) error {
		var noKey *noKeyError
		assert.ErrorAs(t, ensureProjectAPIKey(ctx, cmd, dir, appEnvFiles("")...), &noKey)
		return nil
	})
	assert.True(t, isSessionProject(project), "the rejected key isn't used")
	rejected["APIk"] = false

	// A project with a real key is left alone.
	real := &config.ProjectConfig{Name: "p", URL: "wss://p.livekit.cloud", APIKey: "APIreal", APISecret: "real"}
	project = real
	runWithProjectFlag(t, nil, func(ctx context.Context, cmd *cli.Command) error {
		require.NoError(t, ensureProjectAPIKey(ctx, cmd, dir, appEnvFiles("")...))
		return nil
	})
	assert.Same(t, real, project)
}

func TestCheckAPIKey(t *testing.T) {
	var (
		status int
		body   string
		auth   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()

	status, body = http.StatusOK, ""
	require.NoError(t, checkAPIKey(ctx, srv.URL, "APIk", "secret"))

	// The token survives a clock that's off: no nbf, a day's exp.
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(strings.TrimPrefix(auth, "Bearer "), claims, func(*jwt.Token) (any, error) { return []byte("secret"), nil })
	require.NoError(t, err)
	assert.Equal(t, "APIk", claims["iss"])
	assert.NotContains(t, claims, "nbf")
	assert.NotContains(t, claims, "iat")
	exp, err := claims.GetExpirationTime()
	require.NoError(t, err)
	assert.Greater(t, time.Until(exp.Time), 23*time.Hour)

	// LiveKit sends its 401 as plain text, which twirp can't tell from a
	// proxy's; every 401 counts.
	status, body = http.StatusUnauthorized, "invalid API key"
	assert.ErrorIs(t, checkAPIKey(ctx, srv.URL, "APIk", "secret"), errKeyRejected)
	status, body = http.StatusUnauthorized, `{"code":"unauthenticated","msg":"invalid API key"}`
	assert.ErrorIs(t, checkAPIKey(ctx, srv.URL, "APIk", "secret"), errKeyRejected)

	// A 403 (a suspended project) doesn't prove the key bad.
	status, body = http.StatusForbidden, "project suspended"
	assert.NoError(t, checkAPIKey(ctx, srv.URL, "APIk", "secret"))
}

func TestSaveProjectKeyUpdatesMovedURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".livekit"), 0o700))
	writeEnvFile(t, filepath.Join(home, ".livekit"), "cli-config.yaml",
		"projects:\n  - name: one\n    project_id: p_1\n    url: ws://old.test\n    api_key: APIold\n    api_secret: old\n")
	prev := cliConfig
	t.Cleanup(func() { cliConfig = prev })
	cliConfig = nil

	pc := &config.ProjectConfig{Name: "one", ProjectId: "p_1", URL: "ws://new.test",
		APIKey: tokenauth.PlaceholderAPIKey, APISecret: tokenauth.PlaceholderAPISecret}
	saveProjectKey(pc, agentCredentials{url: "ws://new.test", apiKey: "APInew", apiSecret: "new"})

	conf, err := config.LoadOrCreate()
	require.NoError(t, err)
	require.Len(t, conf.Projects, 1)
	assert.Equal(t, config.ProjectConfig{Name: "one", ProjectId: "p_1", URL: "ws://new.test", APIKey: "APInew", APISecret: "new"}, conf.Projects[0])

	// The same key, reused for a project that moved again: only the URL changes.
	pc.URL = "ws://newer.test"
	saveProjectKey(pc, agentCredentials{url: "ws://newer.test", apiKey: "APInew", apiSecret: "new"})
	conf, err = config.LoadOrCreate()
	require.NoError(t, err)
	assert.Equal(t, "ws://newer.test", conf.Projects[0].URL)
	assert.Equal(t, "APInew", conf.Projects[0].APIKey)
}

// An agent that can run without a LiveKit key (console, the text debugger)
// starts without one when there's none, where agent start stops.
func TestResolveOptionalCredentials(t *testing.T) {
	clearLiveKitEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	prev := cliConfig
	t.Cleanup(func() { cliConfig = prev })
	cliConfig = nil

	run := func(resolve func(context.Context, *cli.Command, string) ([]string, error)) ([]string, error) {
		var (
			env []string
			err error
		)
		app := &cli.Command{
			Name:  "lk",
			Flags: globalFlags,
			Action: func(ctx context.Context, cmd *cli.Command) error {
				env, err = resolve(ctx, cmd, t.TempDir())
				return nil
			},
		}
		require.NoError(t, app.Run(context.Background(), []string{"lk"}))
		return env, err
	}

	_, err := run(resolveCredentials)
	var noKey *noKeyError
	require.ErrorAs(t, err, &noKey)
	assert.ErrorContains(t, err, "no LiveKit project is configured")

	env, err := run(resolveOptionalCredentials)
	require.NoError(t, err)
	assert.Empty(t, env)
}
