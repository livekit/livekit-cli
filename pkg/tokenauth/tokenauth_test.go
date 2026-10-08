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

package tokenauth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/livekit/server-sdk-go/v2/pkg/cloudagents"
)

const (
	realKey    = "APIreal"
	realSecret = "real-secret-for-tests-only-0123456789"
)

// fakeServer answers every twirp call with an empty protobuf message and
// records the grants carried by each request's token (verified against the
// real secret, as the LiveKit server would).
type fakeServer struct {
	mu     sync.Mutex
	grants []*auth.ClaimGrants
	keys   []string
	tokens []string
}

func (f *fakeServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		v, err := auth.ParseAPIToken(raw)
		require.NoError(t, err)
		_, grants, err := v.Verify(realSecret)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.grants = append(f.grants, grants)
		f.keys = append(f.keys, v.APIKey())
		f.tokens = append(f.tokens, raw)
		f.mu.Unlock()

		var body []byte
		switch {
		case strings.HasSuffix(r.URL.Path, "/ListEgress"):
			// Three pages, chained by page token.
			reqBody, _ := io.ReadAll(r.Body)
			var req livekit.ListEgressRequest
			require.NoError(t, proto.Unmarshal(reqBody, &req))
			next := map[string]string{"": "page-2", "page-2": "page-3", "page-3": ""}[req.GetPageToken().GetToken()]
			res := &livekit.ListEgressResponse{Items: []*livekit.EgressInfo{{EgressId: "EG_" + next}}}
			if next != "" {
				res.NextPageToken = &livekit.TokenPagination{Token: next}
			}
			body, _ = proto.Marshal(res)
		case strings.HasSuffix(r.URL.Path, "/ListRooms"):
			body, _ = proto.Marshal(&livekit.ListRoomsResponse{})
		case strings.HasSuffix(r.URL.Path, "/ListSIPInboundTrunk"):
			body, _ = proto.Marshal(&livekit.ListSIPInboundTrunkResponse{})
		}
		w.Header().Set("Content-Type", "application/protobuf")
		_, _ = w.Write(body)
	})
}

// keyTokenSource stands in for the Public API in tests, signing locally with a real
// key and secret.
type keyTokenSource struct {
	APIKey    string
	APISecret string
}

func (m keyTokenSource) Fetch(_ context.Context, grants *auth.ClaimGrants, ttl time.Duration) (string, time.Time, error) {
	at := auth.NewAccessToken(m.APIKey, m.APISecret).SetValidFor(ttl)
	*at.GetGrants() = *grants.Clone()
	token, err := at.ToJWT()
	return token, time.Now().Add(ttl), err
}

type countingTokenSource struct {
	keyTokenSource
	calls int
}

func (m *countingTokenSource) Fetch(ctx context.Context, g *auth.ClaimGrants, ttl time.Duration) (string, time.Time, error) {
	m.calls++
	return m.keyTokenSource.Fetch(ctx, g, ttl)
}

func TestSDKClientsUseMintedTokens(t *testing.T) {
	fake := &fakeServer{}
	srv := httptest.NewServer(fake.handler(t))
	t.Cleanup(srv.Close)

	source := &countingTokenSource{keyTokenSource: keyTokenSource{APIKey: realKey, APISecret: realSecret}}
	src := NewCachingTokenSource(source)
	ctx := context.Background()

	rooms := lksdk.NewRoomServiceClient(srv.URL, PlaceholderAPIKey, PlaceholderAPISecret, src.ClientOptions()...)
	_, err := rooms.ListRooms(ctx, &livekit.ListRoomsRequest{})
	require.NoError(t, err)
	_, err = rooms.ListRooms(ctx, &livekit.ListRoomsRequest{})
	require.NoError(t, err)

	sip := lksdk.NewSIPClient(srv.URL, PlaceholderAPIKey, PlaceholderAPISecret, src.ClientOptions()...)
	_, err = sip.ListSIPInboundTrunk(ctx, &livekit.ListSIPInboundTrunkRequest{})
	require.NoError(t, err)

	require.Len(t, fake.grants, 3)
	for _, k := range fake.keys {
		assert.Equal(t, realKey, k, "placeholder token must never reach the server")
	}
	// Grants are exactly what the SDK asked for, per method.
	assert.True(t, fake.grants[0].Video.RoomList)
	assert.False(t, fake.grants[0].Video.RoomAdmin)
	assert.Nil(t, fake.grants[0].SIP)
	require.NotNil(t, fake.grants[2].SIP)
	assert.True(t, fake.grants[2].SIP.Admin)

	// Two ListRooms calls share one minted token; SIP needs its own.
	assert.Equal(t, 2, source.calls)
}

// TestPaginationReusesCachedToken pages through ListEgress the way
// `lk egress list` does: every page must go out with the same cached token,
// and the server must accept it each time.
func TestPaginationReusesCachedToken(t *testing.T) {
	fake := &fakeServer{}
	srv := httptest.NewServer(fake.handler(t))
	t.Cleanup(srv.Close)

	source := &countingTokenSource{keyTokenSource: keyTokenSource{APIKey: realKey, APISecret: realSecret}}
	src := NewCachingTokenSource(source)
	ctx := context.Background()
	egress := lksdk.NewEgressClient(srv.URL, PlaceholderAPIKey, PlaceholderAPISecret, src.ClientOptions()...)

	var items []*livekit.EgressInfo
	var res *livekit.ListEgressResponse
	for res == nil || res.NextPageToken.GetToken() != "" {
		req := &livekit.ListEgressRequest{}
		if res != nil {
			req.PageToken = &livekit.TokenPagination{Token: res.NextPageToken.GetToken()}
		}
		var err error
		res, err = egress.ListEgress(ctx, req)
		require.NoError(t, err)
		items = append(items, res.Items...)
	}

	require.Len(t, items, 3)
	require.Len(t, fake.tokens, 3)
	assert.Equal(t, 1, source.calls, "pages should share one fetched token")
	for i, tok := range fake.tokens {
		assert.Equal(t, fake.tokens[0], tok, "page %d sent a different token", i+1)
		assert.Equal(t, realKey, fake.keys[i])
		assert.True(t, fake.grants[i].Video.RoomRecord)
	}
}

func TestCachingTokenSourceRefreshesNearExpiry(t *testing.T) {
	source := &countingTokenSource{keyTokenSource: keyTokenSource{APIKey: realKey, APISecret: realSecret}}
	src := NewCachingTokenSource(source)
	now := time.Now()
	src.now = func() time.Time { return now }

	g := &auth.ClaimGrants{Video: &auth.VideoGrant{RoomList: true}}
	_, _, err := src.Fetch(context.Background(), g, time.Minute)
	require.NoError(t, err)
	_, _, err = src.Fetch(context.Background(), g, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 1, source.calls)

	now = now.Add(time.Minute - refreshMargin + time.Second)
	_, _, err = src.Fetch(context.Background(), g, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 2, source.calls)
}

func TestAuthorizeRejectsRealCredentials(t *testing.T) {
	src := NewCachingTokenSource(keyTokenSource{APIKey: realKey, APISecret: realSecret})
	tok, err := auth.NewAccessToken(realKey, realSecret).SetVideoGrant(&auth.VideoGrant{RoomList: true}).ToJWT()
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	_, err = src.authorize(context.Background(), req)
	assert.Error(t, err)
}

func TestCheckGrants(t *testing.T) {
	no := false
	want := &auth.ClaimGrants{
		Identity: "alice",
		Video:    &auth.VideoGrant{RoomJoin: true, Room: "r", CanPublish: &no},
		SIP:      &auth.SIPGrant{Admin: true},
	}
	mint := func(g *auth.ClaimGrants) string {
		tok, _, err := keyTokenSource{APIKey: realKey, APISecret: realSecret}.Fetch(context.Background(), g, time.Minute)
		require.NoError(t, err)
		return tok
	}

	require.NoError(t, CheckGrants(mint(want), want))

	// The server dropped one grant and changed another.
	dropped := want.Clone()
	dropped.SIP = nil
	dropped.Video.CanPublish = nil
	err := CheckGrants(mint(dropped), want)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sip.admin")
	assert.Contains(t, err.Error(), "video.canPublish")
}

// TestTransportAuthorizesCloudAgents drives a real cloudagents client through
// Transport: its twirp calls (and its plain HTTP requests, which share the same
// http.Client) reach the server with fetched tokens.
func TestTransportAuthorizesCloudAgents(t *testing.T) {
	fake := &fakeServer{}
	srv := httptest.NewServer(fake.handler(t))
	t.Cleanup(srv.Close)

	source := &countingTokenSource{keyTokenSource: keyTokenSource{APIKey: realKey, APISecret: realSecret}}
	src := NewCachingTokenSource(source)
	// cloudagents derives an https agents.<domain> host from the project URL;
	// route whatever it dials to the fake server.
	toFake := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme, r.URL.Host = "http", strings.TrimPrefix(srv.URL, "http://")
		return http.DefaultTransport.RoundTrip(r)
	})
	client, err := cloudagents.New(
		cloudagents.WithProject("wss://project.livekit.cloud", PlaceholderAPIKey, PlaceholderAPISecret),
		cloudagents.WithHTTPClient(&http.Client{Transport: src.Transport(toFake)}),
	)
	require.NoError(t, err)

	_, err = client.ListAgents(context.Background(), &livekit.ListAgentsRequest{})
	require.NoError(t, err)

	require.Len(t, fake.keys, 1)
	assert.Equal(t, realKey, fake.keys[0])
	require.NotNil(t, fake.grants[0].Agent)
	assert.True(t, fake.grants[0].Agent.Admin)
}

func TestTransport(t *testing.T) {
	fake := &fakeServer{}
	srv := httptest.NewServer(fake.handler(t))
	t.Cleanup(srv.Close)
	source := &countingTokenSource{keyTokenSource: keyTokenSource{APIKey: realKey, APISecret: realSecret}}
	client := &http.Client{Transport: NewCachingTokenSource(source).Transport(nil)}

	placeholder, err := auth.NewAccessToken(PlaceholderAPIKey, PlaceholderAPISecret).
		SetAgentGrant(&auth.AgentGrant{Admin: true}).ToJWT()
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/logs", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+placeholder)
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Bearer "+placeholder, req.Header.Get("Authorization"), "caller's request must not be mutated")
	require.Len(t, fake.keys, 1)
	assert.Equal(t, realKey, fake.keys[0])

	// A real credential is refused rather than forwarded.
	real, err := auth.NewAccessToken(realKey, realSecret).SetAgentGrant(&auth.AgentGrant{Admin: true}).ToJWT()
	require.NoError(t, err)
	req, err = http.NewRequest(http.MethodGet, srv.URL+"/logs", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+real)
	_, err = client.Do(req)
	assert.Error(t, err)
	assert.Len(t, fake.keys, 1)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
