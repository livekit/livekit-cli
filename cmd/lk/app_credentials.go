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
	"fmt"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/bootstrap"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// App credentials under --experimental-auth.
//
// Session tokens cover the CLI's own calls, but an app or agent runs on its own
// and needs a real API key. Commands that write credentials into an app
// (`lk app create`, `lk app env`, `lk agent init`) or sign with the secret
// themselves (sandbox tokens) call ensureProjectAPIKey, which reuses a key
// already in the app's env files or generates a service-account key.
// Commands that run an agent (`lk agent dev`, `start`, the debugger) only ever
// read the key from the agent's env files (agentEnvCredentials): running an
// agent never creates a key.

// agentEnvFiles are the env files an app or agent keeps its credentials in, in
// priority order: the templates write .env.local, and .env is common too.
var agentEnvFiles = []string{".env.local", ".env"}

// envFileCredentials returns the LiveKit credentials from the first of files in
// dir that has both an API key and secret, along with that file's name. It
// returns zero credentials when none does.
func envFileCredentials(dir string, files ...string) (agentCredentials, string, error) {
	for _, f := range files {
		env, err := bootstrap.ReadDotEnv(dir, f)
		if err != nil {
			return agentCredentials{}, "", fmt.Errorf("reading %s: %w", filepath.Join(dir, f), err)
		}
		if env["LIVEKIT_API_KEY"] != "" && env["LIVEKIT_API_SECRET"] != "" {
			return agentCredentials{
				url:       env["LIVEKIT_URL"],
				apiKey:    env["LIVEKIT_API_KEY"],
				apiSecret: env["LIVEKIT_API_SECRET"],
			}, f, nil
		}
	}
	return agentCredentials{}, "", nil
}

// agentEnvCredentials returns the credentials an agent in dir runs with under
// --experimental-auth: the API key in its env files. A file without
// LIVEKIT_URL gets the session project's URL. With --project, the file's key
// must belong to that project.
func agentEnvCredentials(ctx context.Context, cmd *cli.Command, dir string) (agentCredentials, error) {
	creds, file, err := envFileCredentials(dir, agentEnvFiles...)
	if err != nil {
		return agentCredentials{}, err
	}
	if creds.apiKey == "" {
		return agentCredentials{}, fmt.Errorf(
			"no LiveKit API key in %s (looked in %s); run `lk app env --write --experimental-auth` there to create one",
			dir, strings.Join(agentEnvFiles, ", "))
	}
	if creds.url == "" || cmd.String("project") != "" {
		pc, err := loadSessionProject(ctx, cmd)
		if err != nil {
			return agentCredentials{}, err
		}
		if creds.url == "" {
			creds.url = pc.URL
		} else if !sameProjectURL(creds.url, pc.URL) {
			return agentCredentials{}, fmt.Errorf("the API key in %s is for %s, not project [%s]",
				filepath.Join(dir, file), creds.url, pc.Name)
		}
	}
	out.Statusf("Using API key [%s] from %s", util.Accented(creds.apiKey), util.Accented(filepath.Join(dir, file)))
	return creds, nil
}

// ensureProjectAPIKey gives a session project real API credentials, for
// commands that write them into an app in dir or sign with the secret. It
// reuses a key for the same project already in one of dir's envFiles, and
// otherwise generates a service-account key, which outlives the user's
// membership in the project. A project loaded with an API key is left alone.
func ensureProjectAPIKey(ctx context.Context, cmd *cli.Command, dir string, envFiles ...string) error {
	if !isSessionProject(project) {
		return nil
	}
	if dir != "" {
		creds, file, err := envFileCredentials(dir, envFiles...)
		if err != nil {
			return err
		}
		if creds.apiKey != "" && (creds.url == "" || sameProjectURL(creds.url, project.URL)) {
			project = withAPIKey(project, creds.apiKey, creds.apiSecret)
			out.Statusf("Using API key [%s] from %s", util.Accented(creds.apiKey), util.Accented(filepath.Join(dir, file)))
			return nil
		}
	}

	client, _, _, err := newCloudAPIClient(cmd)
	if err != nil {
		return err
	}
	description := "lk " + strings.TrimPrefix(cmd.FullName(), cmd.Root().Name+" ")
	if dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			description += " (" + filepath.Base(abs) + ")"
		}
	}
	key, err := client.GenerateKey(ctx, project.ProjectId, description, true)
	if err != nil {
		return cloudAPIError(err)
	}
	if util.Deref(key.ApiKey) == "" || util.Deref(key.Secret) == "" {
		return fmt.Errorf("the API returned no key for project [%s]", project.Name)
	}
	project = withAPIKey(project, *key.ApiKey, *key.Secret)
	out.Statusf("Created API key [%s] on project [%s]", util.Accented(*key.ApiKey), util.Accented(project.Name))
	return nil
}

// withAPIKey returns a copy of pc carrying a real API key, which ends its
// session-project status.
func withAPIKey(pc *config.ProjectConfig, apiKey, apiSecret string) *config.ProjectConfig {
	c := *pc
	c.APIKey, c.APISecret = apiKey, apiSecret
	return &c
}

func sameProjectURL(a, b string) bool {
	return strings.TrimSuffix(a, "/") == strings.TrimSuffix(b, "/")
}

// appEnvFiles lists the env files to look for existing credentials in: the
// destination file a command writes, then the usual ones.
func appEnvFiles(dest string) []string {
	files := []string{}
	if dest != "" {
		files = append(files, dest)
	}
	for _, f := range agentEnvFiles {
		if f != dest {
			files = append(files, f)
		}
	}
	return files
}
