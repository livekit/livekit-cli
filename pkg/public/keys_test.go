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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/livekit/protocol/auth"
)

func TestGenerateKey(t *testing.T) {
	var path string
	srv := jsonServer(t, http.StatusOK, `{"apiKey":"APIabc","secret":"s3cret"}`, nil, &path)
	c, err := New(srv.URL, "tok")
	require.NoError(t, err)

	key, err := c.GenerateKey(context.Background(), "p_1", "ci", true)
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/keys", path)
	assert.Equal(t, "APIabc", *key.ApiKey)
	assert.Equal(t, "s3cret", *key.Secret)
}

func TestListScopedKeys(t *testing.T) {
	var path string
	srv := jsonServer(t, http.StatusOK, `{"personalKeys":[{"apiKey":"APIme"}],"otherKeys":[{"apiKey":"APIa"},{"apiKey":"APIb"}]}`, nil, &path)
	c, err := New(srv.URL, "tok")
	require.NoError(t, err)

	personal, other, err := c.ListScopedKeys(context.Background(), "p_1")
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/scoped-keys", path)
	require.Len(t, personal, 1)
	assert.Equal(t, "APIme", *personal[0].ApiKey)
	assert.Len(t, other, 2)
}

func TestDeleteKeyError(t *testing.T) {
	srv := jsonServer(t, http.StatusForbidden, `{"message":"admin only"}`, nil, nil)
	c, err := New(srv.URL, "tok")
	require.NoError(t, err)

	err = c.DeleteKey(context.Background(), "p_1", "APIabc")
	assert.True(t, IsPermissionDenied(err))
}

func TestCreateToken(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v0/projects/p_1/tokens", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"jwt","expiresAt":"2030-01-01T00:00:00Z"}`))
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "tok")
	require.NoError(t, err)

	grants := &auth.ClaimGrants{Video: &auth.VideoGrant{RoomList: true}}
	token, exp, err := c.CreateToken(context.Background(), "p_1", grants, 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "jwt", token)
	assert.Equal(t, 2030, exp.Year())
	assert.Equal(t, float64(300), gotBody["ttlSeconds"])
	assert.Equal(t, map[string]any{"video": map[string]any{"roomList": true}}, gotBody["grants"])
}
