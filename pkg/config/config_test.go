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

	"github.com/livekit/livekit-cli/v2/pkg/public"
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

	assert.Equal(t, "usr_1", c.GetUser("usr_1", "").Id)
	// email match is case-insensitive
	assert.Equal(t, "usr_1", c.GetUser("alice@livekit.io", "").Id)
	assert.Equal(t, "usr_2", c.GetUser("bob@livekit.io", "").Id)
	assert.Nil(t, c.GetUser("nobody@livekit.io", ""))
	assert.Nil(t, c.GetUser("", ""))

	// GetUser returns an aliasing pointer: mutations are visible on the config.
	c.GetUser("usr_1", "").Projects = []UserProjectConfig{{ProjectId: "p_abc"}}
	assert.Len(t, c.Users[0].Projects, 1)
	assert.Equal(t, "p_abc", c.Users[0].Projects[0].ProjectId)
}

func TestCLIConfigGetUserScopedByAPIURL(t *testing.T) {
	const staging = "https://beta-api.staging.livekit.cloud"
	c := &CLIConfig{
		Users: []UserConfig{
			// legacy entry without api_url: the default Public API
			{Id: "usr_prod", Email: "alice@livekit.io"},
			{Id: "usr_stg", Email: "alice@livekit.io", APIURL: staging},
		},
	}

	assert.Equal(t, "usr_prod", c.GetUser("alice@livekit.io", "").Id)
	assert.Equal(t, "usr_prod", c.GetUser("alice@livekit.io", public.DefaultBaseURL).Id)
	assert.Equal(t, "usr_stg", c.GetUser("alice@livekit.io", staging).Id)
	// trailing slash and case are ignored
	assert.Equal(t, "usr_stg", c.GetUser("alice@livekit.io", "HTTPS://beta-api.staging.livekit.cloud/").Id)
	// ids don't resolve across APIs
	assert.Nil(t, c.GetUser("usr_prod", staging))
	assert.Nil(t, c.GetUser("alice@livekit.io", "https://other.example"))

	assert.Len(t, c.UsersForAPIURL(staging), 1)
	assert.Empty(t, c.UsersForAPIURL("https://other.example"))
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

	// The same person on a different Public API is stored separately.
	const staging = "https://beta-api.staging.livekit.cloud"
	_, replaced = c.UpsertUser(UserConfig{Id: "usr_1_new", Email: "alice@livekit.io", SessionToken: "staging", APIURL: staging})
	assert.False(t, replaced)
	assert.Len(t, c.Users, 3)
	assert.Equal(t, "newer", c.Users[0].SessionToken, "production session untouched")

	// Re-auth on that API replaces only its entry; an explicit default URL
	// matches a legacy entry with no api_url.
	_, replaced = c.UpsertUser(UserConfig{Id: "usr_1_new", Email: "alice@livekit.io", SessionToken: "staging2", APIURL: staging + "/"})
	assert.True(t, replaced)
	_, replaced = c.UpsertUser(UserConfig{Id: "usr_1_new", Email: "alice@livekit.io", SessionToken: "prod2", APIURL: public.DefaultBaseURL})
	assert.True(t, replaced)
	assert.Len(t, c.Users, 3)
	assert.Equal(t, "prod2", c.Users[0].SessionToken)
	assert.Equal(t, "staging2", c.Users[2].SessionToken)
}

func TestCLIConfigRemoveUser(t *testing.T) {
	const staging = "https://beta-api.staging.livekit.cloud"
	alice := UserConfig{Id: "usr_1", Email: "alice@livekit.io", SessionToken: "prod"}
	aliceStaging := UserConfig{Id: "usr_1", Email: "alice@livekit.io", SessionToken: "staging", APIURL: staging}
	bob := UserConfig{Id: "usr_2", Email: "bob@livekit.io", SessionToken: "bob"}

	// Removing the staging entry keeps the same person's production entry,
	// and so the default user.
	c := &CLIConfig{DefaultUser: "usr_1", Users: []UserConfig{alice, aliceStaging, bob}}
	c.RemoveUser(aliceStaging)
	assert.Equal(t, []UserConfig{alice, bob}, c.Users)
	assert.Equal(t, "usr_1", c.DefaultUser)

	// Removing their last entry clears the default user. A legacy entry with
	// no api_url matches an explicit default URL.
	c.RemoveUser(UserConfig{Id: "usr_1", APIURL: public.DefaultBaseURL})
	assert.Equal(t, []UserConfig{bob}, c.Users)
	assert.Empty(t, c.DefaultUser)

	// Removing another user keeps the default user.
	c = &CLIConfig{DefaultUser: "bob@livekit.io", Users: []UserConfig{alice, bob}}
	c.RemoveUser(alice)
	assert.Equal(t, []UserConfig{bob}, c.Users)
	assert.Equal(t, "bob@livekit.io", c.DefaultUser)

	// Of two entries for the same person on one API, which only a
	// hand-edited config has, only the first is removed: the second holds a
	// token that wasn't revoked.
	bobAgain := UserConfig{Id: "usr_2", Email: "bob@livekit.io", SessionToken: "bob2"}
	c = &CLIConfig{DefaultUser: "usr_2", Users: []UserConfig{bob, bobAgain}}
	c.RemoveUser(bob)
	assert.Equal(t, []UserConfig{bobAgain}, c.Users)
	assert.Equal(t, "usr_2", c.DefaultUser)

	// Removing a user who isn't signed in changes nothing.
	c = &CLIConfig{DefaultUser: "bob@livekit.io", Users: []UserConfig{bob}}
	c.RemoveUser(UserConfig{Id: "usr_3", Email: "carol@livekit.io"})
	assert.Equal(t, []UserConfig{bob}, c.Users)
	assert.Equal(t, "bob@livekit.io", c.DefaultUser)
}
