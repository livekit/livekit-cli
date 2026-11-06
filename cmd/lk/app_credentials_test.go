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
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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

func TestWriteAgentEnvKey(t *testing.T) {
	creds := agentCredentials{url: "wss://p.livekit.cloud", apiKey: "APIk", apiSecret: "s"}
	read := func(dir, f string) string {
		b, err := os.ReadFile(filepath.Join(dir, f))
		require.NoError(t, err)
		return string(b)
	}

	// No env file: .env.local is created.
	dir := t.TempDir()
	writeAgentEnvKey(dir, agentEnvFiles, creds)
	assert.Contains(t, read(dir, ".env.local"), `LIVEKIT_API_KEY="APIk"`)

	// An existing .env is written to, keeping what else it holds.
	dir = t.TempDir()
	writeEnvFile(t, dir, ".env", "OPENAI_API_KEY=sk\nLIVEKIT_URL=wss://p.livekit.cloud/\n")
	writeAgentEnvKey(dir, agentEnvFiles, creds)
	got := read(dir, ".env")
	assert.Contains(t, got, "OPENAI_API_KEY=sk")
	assert.Contains(t, got, `LIVEKIT_API_SECRET="s"`)
	assert.NoFileExists(t, filepath.Join(dir, ".env.local"))

	// A file for another project is left alone.
	dir = t.TempDir()
	writeEnvFile(t, dir, ".env.local", "LIVEKIT_URL=wss://other.livekit.cloud\n")
	writeAgentEnvKey(dir, agentEnvFiles, creds)
	assert.Equal(t, "LIVEKIT_URL=wss://other.livekit.cloud\n", read(dir, ".env.local"))

	// A stale key is replaced.
	dir = t.TempDir()
	writeEnvFile(t, dir, ".env.local", "LIVEKIT_URL=wss://p.livekit.cloud\nLIVEKIT_API_KEY=APIold\nLIVEKIT_API_SECRET=old\n")
	writeAgentEnvKey(dir, agentEnvFiles, creds)
	got = read(dir, ".env.local")
	assert.Contains(t, got, `LIVEKIT_API_KEY="APIk"`)
	assert.NotContains(t, got, "APIold")
}

func TestGitIgnore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	// Outside a repository nothing is written.
	dir := t.TempDir()
	gitIgnore(dir, ".env.local")
	assert.NoFileExists(t, filepath.Join(dir, ".gitignore"))

	require.NoError(t, exec.Command("git", "init", "-q", dir).Run())
	writeEnvFile(t, dir, ".gitignore", "node_modules")
	gitIgnore(dir, ".env.local")
	b, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	assert.Equal(t, "node_modules\n.env.local\n", string(b))

	// Ignored already: unchanged.
	gitIgnore(dir, ".env.local")
	b, err = os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	assert.Equal(t, "node_modules\n.env.local\n", string(b))
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
	prev := project
	t.Cleanup(func() { project = prev })
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

	// A project with a real key is left alone.
	real := &config.ProjectConfig{Name: "p", URL: "wss://p.livekit.cloud", APIKey: "APIreal", APISecret: "real"}
	project = real
	runWithProjectFlag(t, nil, func(ctx context.Context, cmd *cli.Command) error {
		require.NoError(t, ensureProjectAPIKey(ctx, cmd, dir, appEnvFiles("")...))
		return nil
	})
	assert.Same(t, real, project)
}
