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
	"reflect"
	"strings"
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

// settingsWithOnly returns ProjectSettings with only the i'th field set, to a
// non-nil value.
func settingsWithOnly(i int) ProjectSettings {
	var s ProjectSettings
	field := reflect.ValueOf(&s).Elem().Field(i)
	value := reflect.New(field.Type().Elem())
	if value.Elem().Kind() == reflect.Slice {
		value.Elem().Set(reflect.MakeSlice(value.Elem().Type(), 1, 1))
	}
	field.Set(value)
	return s
}

// requestKeys returns the JSON keys a request body sends.
func requestKeys(t *testing.T, req any) []string {
	t.Helper()
	data, err := json.Marshal(req)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(data, &body))
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	return keys
}

// Every ProjectSettings field must reach both request bodies under its own
// name, so a field added without its mapping fails here.
func TestProjectSettingsMapEveryField(t *testing.T) {
	typ := reflect.TypeOf(ProjectSettings{})
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		key := strings.ToLower(name[:1]) + name[1:]
		s := settingsWithOnly(i)

		assert.Equal(t, []string{key}, requestKeys(t, s.createRequest()), "create request for ProjectSettings.%s", name)

		update, err := s.updateRequest()
		if name == "UserDataRegion" {
			assert.Error(t, err, "update request for ProjectSettings.%s", name)
			continue
		}
		require.NoError(t, err)
		assert.Equal(t, []string{key}, requestKeys(t, update), "update request for ProjectSettings.%s", name)
	}
}

func TestUpdateProjectRefusesUserDataRegion(t *testing.T) {
	c, call, _ := recordProjectRequest(t)
	_, err := c.UpdateProject(context.Background(), "p_1", ProjectSettings{UserDataRegion: ptr("eu")})
	assert.ErrorContains(t, err, "cannot be changed")
	assert.Empty(t, *call)
}
