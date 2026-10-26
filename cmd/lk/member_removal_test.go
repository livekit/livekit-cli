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
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/public"
)

// notConfirmed is the google.rpc.Status the Public API answers a removal with
// while the member owns numKeys keys the caller didn't confirm.
func notConfirmed(numKeys int) string {
	return fmt.Sprintf(`{"code":9,"message":"removing this member deletes the API keys they own (%[1]d in total), and services using them stop working; repeat the request with confirmed_num_keys=%[1]d to remove them","details":[`+
		`{"@type":"type.googleapis.com/livekit.publicapi.common.v1.MemberRemovalNotConfirmed","numKeys":%[1]d,"lastKeyProjectNames":[],"numPrivateLastKeyProjects":0}]}`, numKeys)
}

const lastKey = `{"code":9,"message":"removing this member would delete the API keys they own (3 in total), including the last API key of \"prod\", 1 private project(s); a project without an API key stops handling inbound SIP calls and webhooks, so have a member who is staying create a replacement API key first","details":[` +
	`{"@type":"type.googleapis.com/livekit.publicapi.common.v1.MemberRemovalNotConfirmed","numKeys":3,"lastKeyProjectNames":["prod"],"numPrivateLastKeyProjects":1}]}`

type cannedRemoval struct {
	status int
	body   string
}

// TestRemoveMember runs removeMember against a stand-in Public API that answers
// each confirmedNumKeys with a canned removal, as lk runs non-interactively.
func TestRemoveMember(t *testing.T) {
	tests := []struct {
		name string
		yes  bool
		// answers are by the confirmedNumKeys the request sent.
		answers     map[string]cannedRemoval
		wantSent    []string
		wantNumKeys uint32
		wantErr     string
	}{
		{
			name:     "no keys",
			answers:  map[string]cannedRemoval{"0": {http.StatusOK, `{}`}},
			wantSent: []string{"0"},
		},
		{
			name: "keys confirmed with --yes",
			yes:  true,
			answers: map[string]cannedRemoval{
				"0": {http.StatusBadRequest, notConfirmed(3)},
				"3": {http.StatusOK, `{"numKeys":3}`},
			},
			wantSent:    []string{"0", "3"},
			wantNumKeys: 3,
		},
		{
			name:     "keys without --yes",
			answers:  map[string]cannedRemoval{"0": {http.StatusBadRequest, notConfirmed(3)}},
			wantSent: []string{"0"},
			wantErr:  "removing u_2 deletes the 3 API key(s) they own, and anything using them stops working; re-run with --yes to remove them",
		},
		{
			name: "count changes once",
			yes:  true,
			answers: map[string]cannedRemoval{
				"0": {http.StatusBadRequest, notConfirmed(3)},
				"3": {http.StatusBadRequest, notConfirmed(4)},
				"4": {http.StatusOK, `{"numKeys":4}`},
			},
			wantSent:    []string{"0", "3", "4"},
			wantNumKeys: 4,
		},
		{
			name: "count changes twice",
			yes:  true,
			answers: map[string]cannedRemoval{
				"0": {http.StatusBadRequest, notConfirmed(3)},
				"3": {http.StatusBadRequest, notConfirmed(4)},
				"4": {http.StatusBadRequest, notConfirmed(5)},
			},
			wantSent: []string{"0", "3", "4"},
			wantErr:  "u_2's API keys changed again (they now own 5); nothing was removed, run the command again",
		},
		{
			name:     "last key, even with --yes",
			yes:      true,
			answers:  map[string]cannedRemoval{"0": {http.StatusBadRequest, lastKey}},
			wantSent: []string{"0"},
			wantErr:  `can't remove u_2: it would delete the last API key of "prod" and 1 private project(s)`,
		},
		{
			name: "last key after confirming",
			yes:  true,
			answers: map[string]cannedRemoval{
				"0": {http.StatusBadRequest, notConfirmed(3)},
				"3": {http.StatusBadRequest, lastKey},
			},
			wantSent: []string{"0", "3"},
			wantErr:  `can't remove u_2: it would delete the last API key of "prod" and 1 private project(s)`,
		},
		{
			name:     "another refusal",
			yes:      true,
			answers:  map[string]cannedRemoval{"0": {http.StatusForbidden, `{"code":7,"message":"permission denied"}`}},
			wantSent: []string{"0"},
			wantErr:  "permission denied",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A run with no terminal, the way CI runs lk.
			t.Setenv("CI", "true")
			var sent []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// lk leaves out a count of 0, which the API reads as 0.
				confirmed := cmp.Or(r.URL.Query().Get("confirmedNumKeys"), "0")
				sent = append(sent, confirmed)
				answer, ok := tt.answers[confirmed]
				if !assert.True(t, ok, "no answer for confirmedNumKeys=%s", confirmed) {
					w.WriteHeader(http.StatusNotImplemented)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(answer.status)
				_, _ = w.Write([]byte(answer.body))
			}))
			t.Cleanup(srv.Close)
			client, err := public.New(srv.URL, "tok")
			require.NoError(t, err)

			var numKeys uint32
			var removeErr error
			app := &cli.Command{
				Name:  "lk",
				Flags: []cli.Flag{&cli.BoolFlag{Name: "yes"}},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					numKeys, removeErr = removeMember(ctx, cmd, "u_2", func(confirmedNumKeys uint32) (uint32, error) {
						return client.RemoveProjectMember(ctx, "p_1", "u_2", confirmedNumKeys)
					})
					return nil
				},
			}
			args := []string{"lk"}
			if tt.yes {
				args = append(args, "--yes")
			}
			require.NoError(t, app.Run(context.Background(), args))

			assert.Equal(t, tt.wantSent, sent)
			if tt.wantErr != "" {
				assert.ErrorContains(t, removeErr, tt.wantErr)
				return
			}
			require.NoError(t, removeErr)
			assert.Equal(t, tt.wantNumKeys, numKeys)
		})
	}
}

func TestLastKeyError(t *testing.T) {
	const why = "; a project without an API key stops handling inbound SIP calls and webhooks, " +
		"so have a member who is staying create a replacement API key there first"
	tests := []struct {
		name    string
		removal public.MemberRemoval
		want    string
	}{
		{
			name:    "named projects",
			removal: public.MemberRemoval{NumKeys: 3, LastKeyProjectNames: []string{"prod", "staging"}},
			want:    `can't remove u_2: it would delete the last API key of "prod" and "staging"` + why,
		},
		{
			name:    "three named projects",
			removal: public.MemberRemoval{NumKeys: 3, LastKeyProjectNames: []string{"dev", "prod", "staging"}},
			want:    `can't remove u_2: it would delete the last API key of "dev", "prod" and "staging"` + why,
		},
		{
			name:    "private projects only",
			removal: public.MemberRemoval{NumKeys: 3, NumPrivateLastKeyProjects: 2},
			want:    `can't remove u_2: it would delete the last API key of 2 private project(s)` + why,
		},
		{
			name:    "both",
			removal: public.MemberRemoval{NumKeys: 3, LastKeyProjectNames: []string{"prod"}, NumPrivateLastKeyProjects: 1},
			want:    `can't remove u_2: it would delete the last API key of "prod" and 1 private project(s)` + why,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.EqualError(t, lastKeyError("u_2", &tt.removal), tt.want)
		})
	}
}

func TestRenderMemberRemoved(t *testing.T) {
	tests := []struct {
		name       string
		json       bool
		numKeys    uint32
		wantStdout string
		wantStderr string
	}{
		{name: "no keys", wantStderr: "Removed member u_2\n"},
		{name: "keys", numKeys: 3, wantStderr: "Removed member u_2 and deleted the 3 API key(s) they owned\n"},
		{name: "json", json: true, numKeys: 3, wantStdout: "{\n  \"numKeys\": 3,\n  \"removed\": true,\n  \"userId\": \"u_2\"\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr := captureOut(t)
			app := &cli.Command{
				Name:  "lk",
				Flags: []cli.Flag{&cli.BoolFlag{Name: "json"}},
				Action: func(_ context.Context, cmd *cli.Command) error {
					return renderMemberRemoved(cmd, "u_2", tt.numKeys)
				},
			}
			args := []string{"lk"}
			if tt.json {
				args = append(args, "--json")
			}
			require.NoError(t, app.Run(context.Background(), args))
			assert.Equal(t, tt.wantStdout, stdout.String())
			assert.Equal(t, tt.wantStderr, stderr.String())
		})
	}
}
