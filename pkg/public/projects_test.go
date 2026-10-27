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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

// recordProjectRequest serves one project and records the request's method,
// path, and JSON body.
func recordProjectRequest(t *testing.T) (*Client, *string, *map[string]any) {
	t.Helper()
	var call string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call = r.Method + " " + r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"project":{"id":"p_1","name":"demo"}}`))
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "tok")
	require.NoError(t, err)
	return c, &call, &body
}

func TestCreateProjectSendsSettings(t *testing.T) {
	c, call, body := recordProjectRequest(t)
	_, err := c.CreateProject(context.Background(), ProjectSettings{
		Name:                   ptr("demo"),
		IsPrivate:              ptr(true),
		EnableAnalytics:        ptr(false),
		PinnedRegions:          ptr([]string{"us-east"}),
		UserDataRegion:         ptr("eu"),
		PiiRedactionCategories: ptr([]oapi.CloudProtocolPIIRedactionCategory{oapi.PIIREDACTIONCATEGORYSSN}),
	})
	require.NoError(t, err)
	assert.Equal(t, "POST /v0/projects", *call)
	assert.Equal(t, map[string]any{
		"name":                   "demo",
		"isPrivate":              true,
		"enableAnalytics":        false,
		"pinnedRegions":          []any{"us-east"},
		"userDataRegion":         "eu",
		"piiRedactionCategories": []any{"PII_REDACTION_CATEGORY_SSN"},
	}, *body)
}

func TestCreateWorkspaceProjectSendsSettings(t *testing.T) {
	c, call, body := recordProjectRequest(t)
	_, err := c.CreateWorkspaceProject(context.Background(), "ws_1", ProjectSettings{
		Name:                    ptr("demo"),
		RequireExplicitDispatch: ptr(true),
	})
	require.NoError(t, err)
	assert.Equal(t, "POST /v0/workspaces/ws_1/projects", *call)
	assert.Equal(t, map[string]any{"name": "demo", "requireExplicitDispatch": true}, *body)
}

func TestUpdateProjectSendsSettings(t *testing.T) {
	c, call, body := recordProjectRequest(t)
	_, err := c.UpdateProject(context.Background(), "p_1", ProjectSettings{
		EnableAutoCreate: ptr(false),
		PinnedRegions:    ptr([]string{}),
	})
	require.NoError(t, err)
	assert.Equal(t, "PATCH /v0/projects/p_1", *call)
	// An empty pin list is sent, as the message that clears the pins.
	assert.Equal(t, map[string]any{
		"enableAutoCreate": false,
		"pinnedRegions":    map[string]any{"regions": []any{}},
	}, *body)
}

func TestUpdateProjectRefusesUserDataRegion(t *testing.T) {
	c, call, _ := recordProjectRequest(t)
	_, err := c.UpdateProject(context.Background(), "p_1", ProjectSettings{UserDataRegion: ptr("eu")})
	assert.ErrorContains(t, err, "cannot be changed")
	assert.Empty(t, *call)
}
