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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/config"
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

// TestExperimentalAuthEnabled checks that --experimental-auth alone selects
// user-based auth: API-key credentials, from flags or the LIVEKIT_* env vars
// most shells have set, don't push the command back onto the API-key path.
func TestExperimentalAuthEnabled(t *testing.T) {
	t.Setenv("LIVEKIT_URL", "wss://example.livekit.cloud")
	t.Setenv("LIVEKIT_API_KEY", "envkey")
	t.Setenv("LIVEKIT_API_SECRET", "envsecret")

	enabled := func(t *testing.T, args ...string) bool {
		t.Helper()
		var got bool
		cmd := &cli.Command{
			Name: "lk",
			Flags: []cli.Flag{
				&cli.BoolFlag{Name: "experimental-auth"},
				&cli.BoolFlag{Name: "legacy-auth"},
				&cli.StringFlag{Name: "url", Sources: cli.EnvVars("LIVEKIT_URL")},
				&cli.StringFlag{Name: "api-key", Sources: cli.EnvVars("LIVEKIT_API_KEY")},
				&cli.StringFlag{Name: "api-secret", Sources: cli.EnvVars("LIVEKIT_API_SECRET")},
			},
			Action: func(_ context.Context, cmd *cli.Command) error {
				got = experimentalAuthEnabled(cmd)
				return nil
			},
		}
		require.NoError(t, cmd.Run(context.Background(), append([]string{"lk"}, args...)))
		return got
	}

	assert.True(t, enabled(t, "--experimental-auth"), "env credentials must not override --experimental-auth")
	assert.True(t, enabled(t, "--experimental-auth", "--api-key", "k", "--api-secret", "s", "--url", "ws://x"),
		"flag credentials must not override --experimental-auth")
	assert.False(t, enabled(t), "API-key auth remains the default")
	assert.False(t, enabled(t, "--legacy-auth"))
}
