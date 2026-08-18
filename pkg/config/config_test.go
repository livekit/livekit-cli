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

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestUserConfigSessionValid(t *testing.T) {
	now := time.Now().Unix()
	tests := []struct {
		name string
		user *UserConfig
		want bool
	}{
		{"nil user", nil, false},
		{"no token", &UserConfig{SessionToken: ""}, false},
		{"token, no expiry", &UserConfig{SessionToken: "t"}, true},
		{"token, future expiry", &UserConfig{SessionToken: "t", SessionExpiry: now + 3600}, true},
		{"token, past expiry", &UserConfig{SessionToken: "t", SessionExpiry: now - 3600}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.user.SessionValid())
		})
	}
}

func TestCLIConfigGetUser(t *testing.T) {
	c := &CLIConfig{
		Users: []UserConfig{
			{Id: "usr_1", Email: "Alice@LiveKit.io"},
			{Id: "usr_2", Email: "bob@livekit.io"},
		},
	}

	assert.Equal(t, "usr_1", c.GetUser("usr_1").Id)
	// email match is case-insensitive
	assert.Equal(t, "usr_1", c.GetUser("alice@livekit.io").Id)
	assert.Equal(t, "usr_2", c.GetUser("bob@livekit.io").Id)
	assert.Nil(t, c.GetUser("nobody@livekit.io"))
	assert.Nil(t, c.GetUser(""))

	// GetUser returns an aliasing pointer: mutations are visible on the config.
	c.GetUser("usr_1").Projects = []UserProjectConfig{{ProjectId: "p_abc"}}
	assert.Len(t, c.Users[0].Projects, 1)
	assert.Equal(t, "p_abc", c.Users[0].Projects[0].ProjectId)
}

func TestUserConfigFindProject(t *testing.T) {
	u := &UserConfig{
		Projects: []UserProjectConfig{
			{ProjectId: "p_abc", Name: "My App"},
			{ProjectId: "p_def", Name: "other"},
		},
	}

	// by id
	assert.Equal(t, "p_abc", u.FindProject("p_abc").ProjectId)
	// by name/alias (case-insensitive)
	assert.Equal(t, "p_abc", u.FindProject("my app").ProjectId)
	assert.Equal(t, "p_def", u.FindProject("other").ProjectId)
	// misses
	assert.Nil(t, u.FindProject("nope"))
	assert.Nil(t, u.FindProject(""))
	assert.Nil(t, (*UserConfig)(nil).FindProject("p_abc"))
}

func TestCLIConfigUpsertUser(t *testing.T) {
	c := &CLIConfig{
		Users: []UserConfig{
			{Id: "usr_1", Email: "alice@livekit.io", Name: "Alice", SessionToken: "old"},
		},
	}

	// Re-auth as the same person by Id replaces wholesale (new token, and stale
	// fields like a cached project list are dropped, not merged).
	c.Users[0].Projects = []UserProjectConfig{{ProjectId: "p_stale"}}
	stored, replaced := c.UpsertUser(UserConfig{Id: "usr_1", Email: "alice@livekit.io", Name: "Alice A.", SessionToken: "new"})
	assert.True(t, replaced)
	assert.Len(t, c.Users, 1, "must not duplicate")
	assert.Equal(t, "new", stored.SessionToken)
	assert.Equal(t, "Alice A.", c.Users[0].Name)
	assert.Empty(t, c.Users[0].Projects, "replace is wholesale, not a merge")

	// Match by email (case-insensitive) even when the Id differs.
	_, replaced = c.UpsertUser(UserConfig{Id: "usr_1_new", Email: "ALICE@livekit.io", SessionToken: "newer"})
	assert.True(t, replaced)
	assert.Len(t, c.Users, 1)
	assert.Equal(t, "usr_1_new", c.Users[0].Id)
	assert.Equal(t, "newer", c.Users[0].SessionToken)

	// A genuinely new user is appended.
	_, replaced = c.UpsertUser(UserConfig{Id: "usr_2", Email: "bob@livekit.io"})
	assert.False(t, replaced)
	assert.Len(t, c.Users, 2)
}
