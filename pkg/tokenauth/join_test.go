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
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/livekit/protocol/auth"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// joinTokens records the participant tokens room joins present to a fake
// signal server, which then refuses the connection.
type joinTokens struct {
	mu     sync.Mutex
	tokens []string
}

func (j *joinTokens) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := r.URL.Query().Get("access_token")
		if tok == "" {
			tok = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if tok != "" {
			j.mu.Lock()
			j.tokens = append(j.tokens, tok)
			j.mu.Unlock()
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (j *joinTokens) last(t *testing.T) *auth.ClaimGrants {
	j.mu.Lock()
	defer j.mu.Unlock()
	require.NotEmpty(t, j.tokens, "join sent no token")
	v, err := auth.ParseAPIToken(j.tokens[len(j.tokens)-1])
	require.NoError(t, err)
	_, grants, err := v.Verify(realSecret)
	require.NoError(t, err)
	return grants
}

// TestJoinRoomMatchesSDKGrants guards ParticipantGrants against drifting from
// the token lksdk signs itself: joining through a source must request exactly
// what a key-signed join carries.
func TestJoinRoomMatchesSDKGrants(t *testing.T) {
	info := lksdk.ConnectInfo{
		APIKey:                realKey,
		APISecret:             realSecret,
		RoomName:              "r1",
		ParticipantIdentity:   "alice",
		ParticipantName:       "Alice",
		ParticipantMetadata:   "meta",
		ParticipantAttributes: map[string]string{"k": "v"},
		ParticipantKind:       lksdk.ParticipantAgent,
	}
	ctx := context.Background()

	sdk := &joinTokens{}
	_, err := ConnectToRoom(ctx, nil, sdk.server(t).URL, info, nil)
	require.Error(t, err)
	want := sdk.last(t)

	source := &countingTokenSource{keyTokenSource: keyTokenSource{APIKey: realKey, APISecret: realSecret}}
	fetched := &joinTokens{}
	placeholder := info
	placeholder.APIKey, placeholder.APISecret = PlaceholderAPIKey, PlaceholderAPISecret
	_, err = ConnectToRoom(ctx, NewCachingTokenSource(source), fetched.server(t).URL, placeholder, nil)
	require.Error(t, err)

	assert.Equal(t, want, fetched.last(t))
	assert.Equal(t, 1, source.calls)
}

func TestSign(t *testing.T) {
	ctx := context.Background()
	grants := &auth.ClaimGrants{Identity: "obs", Video: &auth.VideoGrant{RoomJoin: true, Room: "r1"}}

	local, err := Sign(ctx, nil, realKey, realSecret, grants, time.Hour)
	require.NoError(t, err)
	source := &countingTokenSource{keyTokenSource: keyTokenSource{APIKey: realKey, APISecret: realSecret}}
	fetched, err := Sign(ctx, NewCachingTokenSource(source), PlaceholderAPIKey, PlaceholderAPISecret, grants, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, source.calls)

	for _, tok := range []string{local, fetched} {
		v, err := auth.ParseAPIToken(tok)
		require.NoError(t, err)
		_, got, err := v.Verify(realSecret)
		require.NoError(t, err)
		assert.Equal(t, "obs", got.Identity)
		assert.Equal(t, "r1", got.Video.Room)
	}
}
