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
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/tokenauth"
	"github.com/livekit/protocol/auth"
)

// fixedTokenSource answers every token request with the same token.
type fixedTokenSource string

func (s fixedTokenSource) Fetch(context.Context, *auth.ClaimGrants, time.Duration) (string, time.Time, error) {
	return string(s), time.Now().Add(time.Hour), nil
}

// TestDeployPrebuiltImageUnderUserAuth pushes an --image-tar image for a
// session project. Every request to the registry must carry the token minted
// for the session, not the placeholder-signed one the SDK puts there.
func TestDeployPrebuiltImageUnderUserAuth(t *testing.T) {
	const minted = "minted-session-token"

	var mu sync.Mutex
	var registryAuth []string
	regHandler := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		registryAuth = append(registryAuth, r.Header.Get("Authorization"))
		mu.Unlock()
		regHandler.ServeHTTP(w, r)
	}))
	defer reg.Close()
	regHost := strings.TrimPrefix(reg.URL, "http://")

	agents := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/push-target" || r.Header.Get("Authorization") != "Bearer "+minted {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"proxy_host": regHost, "name": "p_1/agent", "tag": "v1"})
	}))
	defer agents.Close()

	pc := &config.ProjectConfig{
		ProjectId: "p_1",
		URL:       agents.URL,
		APIKey:    tokenauth.PlaceholderAPIKey,
		APISecret: tokenauth.PlaceholderAPISecret,
	}
	sessionTokenSources[pc.ProjectId] = tokenauth.NewCachingTokenSource(fixedTokenSource(minted))
	defer delete(sessionTokenSources, pc.ProjectId)

	prev := agentsClient
	defer func() { agentsClient = prev }()
	var err error
	agentsClient, err = newAgentsClient(pc)
	require.NoError(t, err)

	img, err := random.Image(1024, 1)
	require.NoError(t, err)
	tarPath := filepath.Join(t.TempDir(), "image.tar")
	require.NoError(t, crane.Save(img, "agent:latest", tarPath))

	require.NoError(t, deployPrebuiltImage(context.Background(), "CA_1", "", tarPath, nil))

	mu.Lock()
	pushAuth := registryAuth
	mu.Unlock()
	require.NotEmpty(t, pushAuth)
	for _, got := range pushAuth {
		require.Equal(t, "Bearer "+minted, got)
	}
	_, err = crane.Head(regHost + "/p_1/agent:v1")
	require.NoError(t, err, "the image reached the registry")
}
