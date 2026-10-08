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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
