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

	"github.com/stretchr/testify/require"
	"github.com/twitchtv/twirp"

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

	_, err = a.ListAgents(context.Background(), &livekit.ListAgentsRequest{})
	require.EqualError(t, err, wantAdminRefusal)
	require.True(t, public.IsPermissionDenied(err), "the Public API's refusal stays the cause")

	err = a.StreamLogs(context.Background(), "build", "CA_1", "", io.Discard, "")
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
