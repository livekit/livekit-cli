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
)

func TestUpdateWorkspaceProject(t *testing.T) {
	var calls []string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"project":{"id":"p_1","name":"Old"}}`))
		case http.MethodPatch:
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
			_, _ = w.Write([]byte(`{"project":{"id":"p_1","name":"New"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "tok")
	require.NoError(t, err)

	p, err := c.UpdateWorkspaceProject(context.Background(), "ws_1", "p_1", ProjectSettings{Name: ptr("New")})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"GET /v0/workspaces/ws_1/projects/p_1",
		"PATCH /v0/projects/p_1",
	}, calls)
	assert.Equal(t, map[string]any{"name": "New"}, gotBody)
	assert.Equal(t, "New", *p.Name)
}

func TestUpdateWorkspaceProjectOutsideWorkspace(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":5,"message":"project not found"}`))
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "tok")
	require.NoError(t, err)

	_, err = c.UpdateWorkspaceProject(context.Background(), "ws_other", "p_1", ProjectSettings{Name: ptr("New")})
	assert.True(t, IsNotFound(err))
	assert.Equal(t, []string{"GET /v0/workspaces/ws_other/projects/p_1"}, calls)
}
