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
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/protocol/livekit"
)

// sdkPrivateLinks is a privateLinkAPI that, as the SDK client, lists links
// without their health, and answers each link's health with its own call.
type sdkPrivateLinks struct {
	privateLinkAPI
	links  []*livekit.PrivateLink
	health map[string]*livekit.PrivateLinkStatus
	calls  []string
}

func (s *sdkPrivateLinks) ListPrivateLinks(context.Context, *livekit.ListPrivateLinksRequest) (*livekit.ListPrivateLinksResponse, error) {
	s.calls = append(s.calls, "list")
	return &livekit.ListPrivateLinksResponse{Items: s.links}, nil
}

func (s *sdkPrivateLinks) GetPrivateLinkStatus(_ context.Context, req *livekit.GetPrivateLinkStatusRequest) (*livekit.GetPrivateLinkStatusResponse, error) {
	s.calls = append(s.calls, "status "+req.PrivateLinkId)
	if h, ok := s.health[req.PrivateLinkId]; ok {
		return &livekit.GetPrivateLinkStatusResponse{Value: h}, nil
	}
	return nil, errors.New("status unavailable")
}

var healthy = &livekit.PrivateLinkStatus{Status: livekit.PrivateLinkStatus_PRIVATE_LINK_STATUS_HEALTHY}

// With the SDK, lk reads each link's health with its own call, and keeps
// the error of one it can't read. Under --user-auth, the one list call
// brings each link's health, and a link without one has errHealthUnread.
func TestPrivateLinksWithHealth(t *testing.T) {
	sdk := &sdkPrivateLinks{
		links:  []*livekit.PrivateLink{{PrivateLinkId: "PL_1"}, {PrivateLinkId: "PL_2"}},
		health: map[string]*livekit.PrivateLinkStatus{"PL_1": healthy},
	}
	links, health, healthErr, err := privateLinksWithHealth(context.Background(), sdk)
	require.NoError(t, err)
	require.Len(t, links, 2)
	require.Equal(t, map[string]*livekit.PrivateLinkStatus{"PL_1": healthy}, health)
	require.EqualError(t, healthErr["PL_2"], "status unavailable")
	require.Equal(t, []string{"list", "status PL_1", "status PL_2"}, sdk.calls)

	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[`+
			`{"link":{"privateLinkId":"PL_1"},"status":{"status":"PRIVATE_LINK_STATUS_HEALTHY"}},`+
			`{"link":{"privateLinkId":"PL_2"}}]}`)
	}))
	t.Cleanup(srv.Close)
	client, err := public.New(srv.URL, "st_1")
	require.NoError(t, err)
	a := &userAuthAgentAPI{client: client, projectID: "p_1"}

	links, health, healthErr, err = privateLinksWithHealth(context.Background(), a)
	require.NoError(t, err)
	require.Equal(t, []string{"PL_1", "PL_2"}, []string{links[0].PrivateLinkId, links[1].PrivateLinkId})
	require.Equal(t, livekit.PrivateLinkStatus_PRIVATE_LINK_STATUS_HEALTHY, health["PL_1"].Status)
	require.NotContains(t, health, "PL_2")
	require.Equal(t, map[string]error{"PL_2": errHealthUnread}, healthErr)
	require.Equal(t, []string{"GET /v0/projects/p_1/private-links"}, requests, "one call, no status calls")

	listed, err := a.ListPrivateLinks(context.Background(), &livekit.ListPrivateLinksRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Items, 2, "ListPrivateLinks lists the links without their health")
}

// Under --user-auth, the other private link calls answer in the SDK's
// shapes, and a member below WRITE hears that the command needs write
// access.
func TestUserAuthPrivateLinks(t *testing.T) {
	var status int
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	client, err := public.New(srv.URL, "st_1")
	require.NoError(t, err)
	a := &userAuthAgentAPI{client: client, projectID: "p_1"}
	ctx := context.Background()

	status, body = http.StatusOK, `{"privateLink":{"privateLinkId":"PL_1","name":"db"}}`
	created, err := a.CreatePrivateLink(ctx, &livekit.CreatePrivateLinkRequest{Name: "db"})
	require.NoError(t, err)
	require.Equal(t, "PL_1", created.PrivateLink.PrivateLinkId)

	status, body = http.StatusOK, `{"status":{"status":"PRIVATE_LINK_STATUS_HEALTHY"}}`
	health, err := a.GetPrivateLinkStatus(ctx, &livekit.GetPrivateLinkStatusRequest{PrivateLinkId: "PL_1"})
	require.NoError(t, err)
	require.Equal(t, livekit.PrivateLinkStatus_PRIVATE_LINK_STATUS_HEALTHY, health.Value.Status)

	status, body = http.StatusOK, `{}`
	_, err = a.DestroyPrivateLink(ctx, &livekit.DestroyPrivateLinkRequest{PrivateLinkId: "PL_1"})
	require.NoError(t, err)

	status, body = http.StatusForbidden, `{"code":7,"message":"this needs WRITE on the project"}`
	_, err = a.ListPrivateLinks(ctx, &livekit.ListPrivateLinksRequest{})
	require.EqualError(t, err, "signed in with --user-auth, this command needs write access to the project. "+
		"A project API key also works: re-run without --user-auth")

	status, body = http.StatusNotFound, `{"code":5,"message":"private link not found"}`
	_, err = a.DestroyPrivateLink(ctx, &livekit.DestroyPrivateLinkRequest{PrivateLinkId: "PL_9"})
	require.EqualError(t, err, "private link not found")
}
