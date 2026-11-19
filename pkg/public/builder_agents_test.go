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
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingServer answers every request with status and body, and records
// the last request's method, URL (path and query) and body.
func recordingServer(t *testing.T, status int, body string) (*Client, *http.Request, *string) {
	t.Helper()
	var got http.Request
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "tok")
	require.NoError(t, err)
	return c, &got, &gotBody
}

// TestBuilderAgentKeepsUnknownConfig checks that a configuration property
// this client's generated types don't have survives a read, and goes back
// on a write as the caller wrote it.
func TestBuilderAgentKeepsUnknownConfig(t *testing.T) {
	const agent = `{"builderAgent":{"id":"AB_1","name":"a","config":{"instructions":"hi","stateTools":[{"name":"x"}]}}}`
	c, req, _ := recordingServer(t, http.StatusOK, agent)

	got, err := c.GetBuilderAgent(context.Background(), "p_1", "AB_1")
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/agents/builder/AB_1", req.URL.Path)
	assert.Equal(t, "AB_1", *got.Id)
	assert.JSONEq(t, `{"instructions":"hi","stateTools":[{"name":"x"}]}`, string(got.Config))

	out, err := json.Marshal(got)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"AB_1","name":"a","config":{"instructions":"hi","stateTools":[{"name":"x"}]}}`, string(out))

	c, req, body := recordingServer(t, http.StatusOK, agent)
	_, err = c.UpdateBuilderAgent(context.Background(), "p_1", "AB_1", nil, got.Config)
	require.NoError(t, err)
	assert.Equal(t, http.MethodPatch, req.Method)
	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
	assert.JSONEq(t, `{"config":{"instructions":"hi","stateTools":[{"name":"x"}]}}`, *body)
}

func TestCreateBuilderAgentBody(t *testing.T) {
	c, req, body := recordingServer(t, http.StatusOK, `{"builderAgent":{"id":"AB_1","name":"a"}}`)
	got, err := c.CreateBuilderAgent(context.Background(), "p_1", "a", nil)
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, req.Method)
	assert.Equal(t, "/v0/projects/p_1/agents/builder", req.URL.Path)
	assert.JSONEq(t, `{"name":"a"}`, *body, "no config takes the default")
	assert.Nil(t, got.Config)
}

func TestUpdateBuilderAgentRenameOnly(t *testing.T) {
	c, _, body := recordingServer(t, http.StatusOK, `{"builderAgent":{"id":"AB_1","name":"b"}}`)
	name := "b"
	_, err := c.UpdateBuilderAgent(context.Background(), "p_1", "AB_1", &name, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"b"}`, *body)
}

func TestBuilderAgentErrors(t *testing.T) {
	c, _, _ := recordingServer(t, http.StatusNotFound, `{"code":5,"message":"builder agent not found"}`)
	_, err := c.GetBuilderAgent(context.Background(), "p_1", "AB_1")
	assert.True(t, IsNotFound(err))
	assert.EqualError(t, err, "builder agent not found")

	c, _, _ = recordingServer(t, http.StatusOK, `{}`)
	_, err = c.GetBuilderAgent(context.Background(), "p_1", "AB_1")
	assert.EqualError(t, err, "unexpected response from server: missing builder agent")
}

func TestDeployBuilderAgent(t *testing.T) {
	c, req, body := recordingServer(t, http.StatusOK, `{"versionId":"ABV_2","status":"DEPLOYMENT_STATUS_BUILDING"}`)
	res, err := c.DeployBuilderAgent(context.Background(), "p_1", "AB_1", "ABV_2")
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/agents/builder/AB_1:deploy", req.URL.Path)
	assert.JSONEq(t, `{"versionId":"ABV_2"}`, *body)
	assert.Equal(t, "ABV_2", *res.VersionId)

	_, err = c.DeployBuilderAgent(context.Background(), "p_1", "AB_1", "")
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, *body, "no version deploys the current configuration")
}

func TestListBuilderAgentVersions(t *testing.T) {
	c, req, _ := recordingServer(t, http.StatusOK, `{"items":[{"id":"ABV_2","config":{"futureField":true}}],"pageInfo":{"nextCursor":"c2","hasMore":true}}`)
	versions, next, err := c.ListBuilderAgentVersions(context.Background(), "p_1", "AB_1", 1, "c1")
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/agents/builder/AB_1/versions", req.URL.Path)
	assert.Equal(t, "c1", req.URL.Query().Get("page.cursor"))
	assert.Equal(t, "1", req.URL.Query().Get("page.pageSize"))
	assert.Equal(t, "c2", next)
	require.Len(t, versions, 1)
	assert.Equal(t, "ABV_2", *versions[0].Id)
	assert.JSONEq(t, `{"futureField":true}`, string(versions[0].Config))
}

func TestGetBuilderAgentCodeURL(t *testing.T) {
	c, req, _ := recordingServer(t, http.StatusOK, `{"url":"https://example.test/code?sig=x","versionId":"ABV_1"}`)
	res, err := c.GetBuilderAgentCodeURL(context.Background(), "p_1", "AB_1", "ABV_1", 60)
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/agents/builder/AB_1/code-url", req.URL.Path)
	assert.Equal(t, "ABV_1", req.URL.Query().Get("versionId"))
	assert.Equal(t, "60", req.URL.Query().Get("expirySeconds"))
	assert.Equal(t, "ABV_1", *res.VersionId)

	c, _, _ = recordingServer(t, http.StatusOK, `{"versionId":"ABV_1"}`)
	_, err = c.GetBuilderAgentCodeURL(context.Background(), "p_1", "AB_1", "", 0)
	assert.EqualError(t, err, "unexpected response from server: missing url")
}

func TestHasHiddenHeaderValues(t *testing.T) {
	for _, tc := range []struct {
		config string
		want   bool
	}{
		{`{"httpTools":[{"headers":[{"name":"Authorization","value":"********"}]}]}`, true},
		{`{"mcpServers":[{"headers":[{"name":"a","value":"x"},{"name":"b","value":"********"}]}]}`, true},
		{`{"httpTools":[{"headers":[{"name":"Authorization","value":"Bearer x"}]}]}`, false},
		{`{"instructions":"********"}`, false},
		{`{"headers":"********"}`, false},
		{`not json`, false},
	} {
		assert.Equal(t, tc.want, HasHiddenHeaderValues(json.RawMessage(tc.config)), tc.config)
	}
}
