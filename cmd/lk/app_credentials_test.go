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

func TestAgentEnvCredentials(t *testing.T) {
	dir := t.TempDir()
	runWithProjectFlag(t, nil, func(ctx context.Context, cmd *cli.Command) error {
		_, err := agentEnvCredentials(ctx, cmd, dir)
		assert.ErrorContains(t, err, "lk app env --write", "a missing key points at the command that creates one")
		return nil
	})

	// A complete file is used as is, without consulting the session.
	writeEnvFile(t, dir, ".env.local", "LIVEKIT_URL=wss://p.livekit.cloud\nLIVEKIT_API_KEY=APIk\nLIVEKIT_API_SECRET=s\n")
	runWithProjectFlag(t, nil, func(ctx context.Context, cmd *cli.Command) error {
		creds, err := agentEnvCredentials(ctx, cmd, dir)
		require.NoError(t, err)
		assert.Equal(t, agentCredentials{url: "wss://p.livekit.cloud", apiKey: "APIk", apiSecret: "s"}, creds)
		return nil
	})
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
