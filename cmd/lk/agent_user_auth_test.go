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
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twitchtv/twirp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/tokenauth"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
)

const wantAdminRefusal = "signed in with --user-auth, this command needs a project admin (this needs ADMIN on the project). " +
	"A project API key works meanwhile: re-run without --user-auth"

// When the Public API refuses the token bridge the Agent.Admin grant, a
// call through the bridge, a Twirp call or a stream alike, says the command
// needs a project admin, and sends nothing to cloud agents.
func TestUserAuthAgentAPIRefusalNeedsAdmin(t *testing.T) {
	var mu sync.Mutex
	var grants []*auth.ClaimGrants
	var others []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodPost || r.URL.Path != "/v0/projects/p_1/tokens" {
			others = append(others, r.Method+" "+r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		var req struct {
			Grants *auth.ClaimGrants `json:"grants"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		grants = append(grants, req.Grants)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"code":7,"message":"this needs ADMIN on the project"}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LK_AGENTS_URL", srv.URL)

	client, err := public.New(srv.URL, "st_1")
	require.NoError(t, err)
	pc := &config.ProjectConfig{
		ProjectId: "p_1",
		URL:       srv.URL,
		APIKey:    tokenauth.PlaceholderAPIKey,
		APISecret: tokenauth.PlaceholderAPISecret,
	}
	sessionTokenSources[pc.ProjectId] = tokenauth.NewCachingTokenSource(&publicAPITokenSource{client: client, projectID: pc.ProjectId})
	t.Cleanup(func() { delete(sessionTokenSources, pc.ProjectId) })
	bridge, err := newAgentsClient(pc)
	require.NoError(t, err)
	a := &userAuthAgentAPI{client: client, projectID: pc.ProjectId, bridge: bridge}

	_, err = a.ListPrivateLinks(context.Background(), &livekit.ListPrivateLinksRequest{})
	require.EqualError(t, err, wantAdminRefusal)
	require.True(t, public.IsPermissionDenied(err), "the Public API's refusal stays the cause")

	err = a.StreamLogs(context.Background(), "deploy", "CA_1", "", io.Discard, "")
	require.EqualError(t, err, wantAdminRefusal)

	mu.Lock()
	defer mu.Unlock()
	require.Empty(t, others, "nothing reached cloud agents")
	require.Len(t, grants, 2)
	for _, g := range grants {
		require.NotNil(t, g.Agent)
		require.True(t, g.Agent.Admin, "the bridge asks for Agent.Admin")
	}
}

// Any other error from a call through the bridge comes back as it is, so
// the commands still read a Twirp error's message, and a lapsed session
// still says how to sign in again.
func TestBridgeErrorPassesOtherErrorsThrough(t *testing.T) {
	twerr := twirp.NewError(twirp.PermissionDenied, "agent belongs to another project")
	require.Equal(t, twerr, bridgeError(twerr))

	expired := cloudAPIError(&public.APIError{Status: http.StatusUnauthorized, Message: "authentication required"})
	require.Equal(t, expired, bridgeError(expired))

	plain := errors.New("connection refused")
	require.Equal(t, plain, bridgeError(plain))

	require.NoError(t, bridgeError(nil))
}

// cloudAgentsInfo puts back the placeholders cloud agents lists for a
// version or deploy time it doesn't know, and keeps the values it knows.
func TestCloudAgentsInfo(t *testing.T) {
	deployedAt := timestamppb.New(time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC))
	got := cloudAgentsInfo(&livekit.AgentInfo{
		AgentId: "CA_1",
		AgentDeployments: []*livekit.AgentDeployment{
			{Region: "us-east"},
			{Region: "eu-central", Version: "v2", Deployment: "staging"},
		},
	})
	want := &livekit.AgentInfo{
		AgentId:    "CA_1",
		Version:    "---",
		DeployedAt: timestamppb.New(time.Time{}),
		AgentDeployments: []*livekit.AgentDeployment{
			{Region: "us-east", Version: "---"},
			{Region: "eu-central", Version: "v2", Deployment: "staging"},
		},
	}
	require.True(t, proto.Equal(want, got), "got %v", got)

	known := &livekit.AgentInfo{AgentId: "CA_2", AgentName: "support", Version: "v1", DeployedAt: deployedAt}
	require.True(t, proto.Equal(proto.Clone(known), cloudAgentsInfo(known)))
}

// Only an agent the project doesn't have reads as an empty list. Any other
// refusal of a get is an error, worded as every Public API refusal is.
func TestUserAuthListAgentsErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"project not found": {http.StatusNotFound, `{"code":5,"message":"project not found"}`, "project not found"},
		"refused": {http.StatusForbidden, `{"code":7,"message":"not authorized"}`,
			"not authorized — your account doesn't have access to this project or action. To act with API-key credentials instead, re-run without `--user-auth`"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(srv.Close)
			client, err := public.New(srv.URL, "st_1")
			require.NoError(t, err)
			a := &userAuthAgentAPI{client: client, projectID: "p_1"}

			res, err := a.ListAgents(context.Background(), &livekit.ListAgentsRequest{AgentId: "CA_1"})
			require.EqualError(t, err, tc.want)
			require.Nil(t, res)
		})
	}
}

// The build log lk reads is the one cloud agents streams when lk names no
// version: that of the version the deployment runs, or for production, the
// agent's version.
func TestDeployedVersion(t *testing.T) {
	info := &livekit.AgentInfo{
		Version: "v3",
		AgentDeployments: []*livekit.AgentDeployment{
			{Region: "us-east", Version: "v2"},
			{Region: "us-east", Deployment: "staging"},
			{Region: "eu-central", Deployment: "staging", Version: "v4"},
			{Region: "eu-central", Deployment: "production", Version: "v2"},
		},
	}
	for deployment, want := range map[string]string{
		"":           "v2",
		"production": "v2",
		"staging":    "v4",
		"canary":     "",
	} {
		require.Equal(t, want, deployedVersion(info, deployment), "deployment %q", deployment)
	}
	require.Equal(t, "v3", deployedVersion(&livekit.AgentInfo{Version: "v3"}, ""), "production falls back to the agent's version")
	require.Empty(t, deployedVersion(&livekit.AgentInfo{Version: "v3"}, "staging"))
}

// AgentService's refusal of a member whose role is too low names who the
// command needs. Any other error is worded as cloudAPIError words it.
func TestAgentServiceError(t *testing.T) {
	lacksRole := &public.APIError{Status: http.StatusForbidden, Message: "this needs ADMIN on the project"}
	err := agentServiceError(lacksRole, projectAdmin)
	require.EqualError(t, err, "signed in with --user-auth, this command needs a project admin (this needs ADMIN on the project). "+
		"A project API key also works: re-run without --user-auth")
	require.ErrorIs(t, err, lacksRole)

	suspended := &public.APIError{Status: http.StatusForbidden, Message: "this project is suspended"}
	require.Equal(t, cloudAPIError(suspended).Error(), agentServiceError(suspended, projectAdmin).Error())

	notFound := &public.APIError{Status: http.StatusNotFound, Message: "agent not found"}
	require.Equal(t, notFound, agentServiceError(notFound, projectAdmin))

	require.NoError(t, agentServiceError(nil, projectAdmin))
}
