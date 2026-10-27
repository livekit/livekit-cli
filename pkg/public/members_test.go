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

package public

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bodies are what the server's REST transcoder writes: the removal's
// numKeys, or a google.rpc.Status carrying a MemberRemovalNotConfirmed.
const (
	notConfirmedBody = `{"code":9, "message":"removing this member deletes the API keys they own (3 in total), and services using them stop working; repeat the request with confirmed_num_keys=3 to remove them", "details":[` +
		`{"@type":"type.googleapis.com/livekit.publicapi.common.v1.MemberRemovalNotConfirmed", "numKeys":3, "lastKeyProjectNames":[], "numPrivateLastKeyProjects":0}]}`
	lastKeyBody = `{"code":9, "message":"removing this member would delete the API keys they own (3 in total), including the last API key of \"prod\", 1 private project(s); a project without an API key stops handling inbound SIP calls and webhooks, so have a member who is staying create a replacement API key first", "details":[` +
		`{"@type":"type.googleapis.com/livekit.publicapi.common.v1.MemberRemovalNotConfirmed", "numKeys":3, "lastKeyProjectNames":["prod"], "numPrivateLastKeyProjects":1}]}`
)

// TestRemoveMember checks that both removals send the confirmed count, leaving
// out a count of 0, and return the count the API deleted.
func TestRemoveMember(t *testing.T) {
	tests := []struct {
		name     string
		remove   func(c *Client, confirmed uint32) (uint32, error)
		wantPath string
	}{
		{
			name: "project",
			remove: func(c *Client, confirmed uint32) (uint32, error) {
				return c.RemoveProjectMember(context.Background(), "p_1", "u_1", confirmed)
			},
			wantPath: "/v0/projects/p_1/members/u_1",
		},
		{
			name: "workspace",
			remove: func(c *Client, confirmed uint32) (uint32, error) {
				return c.DeleteWorkspaceMember(context.Background(), "ws_1", "u_1", confirmed)
			},
			wantPath: "/v0/workspaces/ws_1/members/u_1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.RequestURI())
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Query().Get("confirmedNumKeys") {
				case "":
					_, _ = w.Write([]byte(`{}`))
				default:
					_, _ = w.Write([]byte(`{"numKeys":3}`))
				}
			}))
			t.Cleanup(srv.Close)
			c, err := New(srv.URL, "tok")
			require.NoError(t, err)

			n, err := tt.remove(c, 0)
			require.NoError(t, err)
			assert.Equal(t, uint32(0), n, "a member who owns no keys")

			n, err = tt.remove(c, 3)
			require.NoError(t, err)
			assert.Equal(t, uint32(3), n)

			assert.Equal(t, []string{
				"DELETE " + tt.wantPath,
				"DELETE " + tt.wantPath + "?confirmedNumKeys=3",
			}, calls)
		})
	}
}

func TestMemberRemovalNotConfirmed(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantRemoval *MemberRemoval
	}{
		{
			name:        "keys to confirm",
			body:        notConfirmedBody,
			wantRemoval: &MemberRemoval{NumKeys: 3, LastKeyProjectNames: []string{}},
		},
		{
			name:        "last key",
			body:        lastKeyBody,
			wantRemoval: &MemberRemoval{NumKeys: 3, LastKeyProjectNames: []string{"prod"}, NumPrivateLastKeyProjects: 1},
		},
		{
			name: "another failed precondition",
			body: `{"code":9,"message":"something else","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"X"}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := jsonServer(t, http.StatusBadRequest, tt.body, nil, nil)
			c, err := New(srv.URL, "tok")
			require.NoError(t, err)

			_, err = c.RemoveProjectMember(context.Background(), "p_1", "u_1", 0)
			require.Error(t, err)
			removal, ok := MemberRemovalNotConfirmed(err)
			assert.Equal(t, tt.wantRemoval != nil, ok)
			assert.Equal(t, tt.wantRemoval, removal)
		})
	}

	t.Run("not an API error", func(t *testing.T) {
		_, ok := MemberRemovalNotConfirmed(errors.New("connection refused"))
		assert.False(t, ok)
	})
}

func TestMemberRemovalLeavesProjectWithoutKey(t *testing.T) {
	assert.False(t, (&MemberRemoval{NumKeys: 3}).LeavesProjectWithoutKey())
	assert.True(t, (&MemberRemoval{NumKeys: 3, LastKeyProjectNames: []string{"prod"}}).LeavesProjectWithoutKey())
	assert.True(t, (&MemberRemoval{NumKeys: 3, NumPrivateLastKeyProjects: 1}).LeavesProjectWithoutKey())
}
