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
	"time"

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

// TestAgentBuilderAgentKeepsUnknownConfig checks that a configuration property
// this client's generated types don't have survives a read, and goes back
// on a write as the caller wrote it.
func TestAgentBuilderAgentKeepsUnknownConfig(t *testing.T) {
	const agent = `{"agent":{"id":"AB_1","name":"a","config":{"instructions":"hi","stateTools":[{"name":"x"}]}}}`
	c, req, _ := recordingServer(t, http.StatusOK, agent)

	got, err := c.GetAgentBuilderAgent(context.Background(), "p_1", "AB_1")
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/agent-builder/agents/AB_1", req.URL.Path)
	assert.Equal(t, "AB_1", *got.Id)
	assert.JSONEq(t, `{"instructions":"hi","stateTools":[{"name":"x"}]}`, string(got.Config))

	out, err := json.Marshal(got)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"AB_1","name":"a","config":{"instructions":"hi","stateTools":[{"name":"x"}]}}`, string(out))

	c, req, body := recordingServer(t, http.StatusOK, agent)
	_, err = c.UpdateAgentBuilderAgent(context.Background(), "p_1", "AB_1", nil, got.Config)
	require.NoError(t, err)
	assert.Equal(t, http.MethodPatch, req.Method)
	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
	assert.JSONEq(t, `{"config":{"instructions":"hi","stateTools":[{"name":"x"}]}}`, *body)
}

func TestCreateAgentBuilderAgentBody(t *testing.T) {
	c, req, body := recordingServer(t, http.StatusOK, `{"agent":{"id":"AB_1","name":"a"}}`)
	got, err := c.CreateAgentBuilderAgent(context.Background(), "p_1", "a", nil)
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, req.Method)
	assert.Equal(t, "/v0/projects/p_1/agent-builder/agents", req.URL.Path)
	assert.JSONEq(t, `{"name":"a"}`, *body, "no config takes the default")
	assert.Nil(t, got.Config)
}

func TestUpdateAgentBuilderAgentRenameOnly(t *testing.T) {
	c, _, body := recordingServer(t, http.StatusOK, `{"agent":{"id":"AB_1","name":"b"}}`)
	name := "b"
	_, err := c.UpdateAgentBuilderAgent(context.Background(), "p_1", "AB_1", &name, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"b"}`, *body)
}

func TestAgentBuilderAgentErrors(t *testing.T) {
	c, _, _ := recordingServer(t, http.StatusNotFound, `{"code":5,"message":"Agent Builder agent not found"}`)
	_, err := c.GetAgentBuilderAgent(context.Background(), "p_1", "AB_1")
	assert.True(t, IsNotFound(err))
	assert.EqualError(t, err, "Agent Builder agent not found")

	c, _, _ = recordingServer(t, http.StatusOK, `{}`)
	_, err = c.GetAgentBuilderAgent(context.Background(), "p_1", "AB_1")
	assert.EqualError(t, err, "unexpected response from server: missing Agent Builder agent")
}

func TestDeployAgentBuilderAgent(t *testing.T) {
	c, req, body := recordingServer(t, http.StatusOK, `{"versionId":"ABV_2","status":"DEPLOYMENT_STATUS_BUILDING"}`)
	res, err := c.DeployAgentBuilderAgent(context.Background(), "p_1", "AB_1", "ABV_2")
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/agent-builder/agents/AB_1:deploy", req.URL.Path)
	assert.JSONEq(t, `{"versionId":"ABV_2"}`, *body)
	assert.Equal(t, "ABV_2", *res.VersionId)
	assert.WithinDuration(t, time.Now(), res.Date, 2*time.Second, "the server's Date header")
	out, err := json.Marshal(res)
	require.NoError(t, err)
	assert.JSONEq(t, `{"versionId":"ABV_2","status":"DEPLOYMENT_STATUS_BUILDING"}`, string(out))

	_, err = c.DeployAgentBuilderAgent(context.Background(), "p_1", "AB_1", "")
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, *body, "no version deploys the current configuration")
}

func TestListAgentBuilderAgentVersions(t *testing.T) {
	c, req, _ := recordingServer(t, http.StatusOK, `{"items":[{"id":"ABV_2","config":{"futureField":true}}],"pageInfo":{"nextCursor":"c2","hasMore":true}}`)
	versions, next, err := c.ListAgentBuilderAgentVersions(context.Background(), "p_1", "AB_1", 1, "c1")
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/agent-builder/agents/AB_1/versions", req.URL.Path)
	assert.Equal(t, "c1", req.URL.Query().Get("page.cursor"))
	assert.Equal(t, "1", req.URL.Query().Get("page.pageSize"))
	assert.Equal(t, "c2", next)
	require.Len(t, versions, 1)
	assert.Equal(t, "ABV_2", *versions[0].Id)
	assert.JSONEq(t, `{"futureField":true}`, string(versions[0].Config))
}

func TestGetAgentBuilderAgentCodeURL(t *testing.T) {
	c, req, _ := recordingServer(t, http.StatusOK, `{"url":"https://example.test/code?sig=x","versionId":"ABV_1"}`)
	res, err := c.GetAgentBuilderAgentCodeURL(context.Background(), "p_1", "AB_1", "ABV_1", 60)
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/agent-builder/agents/AB_1/code-url", req.URL.Path)
	assert.Equal(t, "ABV_1", req.URL.Query().Get("versionId"))
	assert.Equal(t, "60", req.URL.Query().Get("expirySeconds"))
	assert.Equal(t, "ABV_1", *res.VersionId)

	c, _, _ = recordingServer(t, http.StatusOK, `{"versionId":"ABV_1"}`)
	_, err = c.GetAgentBuilderAgentCodeURL(context.Background(), "p_1", "AB_1", "", 0)
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

func TestDownloadAgentBuilderAgentCode(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Disposition", `attachment; filename="../greeter.zip"`)
		_, _ = w.Write([]byte("zip"))
	}))
	t.Cleanup(srv.Close)

	data, filename, err := DownloadAgentBuilderAgentCode(context.Background(), srv.URL+"/code?signature=s")
	require.NoError(t, err)
	assert.Equal(t, "zip", string(data))
	assert.Equal(t, "greeter.zip", filename, "only the base name of the suggestion")
	assert.Empty(t, auth, "the signed URL is the credential")
}

func TestDownloadAgentBuilderAgentCodeErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":7,"message":"this code URL expired; call GetAgentBuilderAgentCodeURL for a new one"}`))
	}))
	t.Cleanup(srv.Close)
	_, _, err := DownloadAgentBuilderAgentCode(context.Background(), srv.URL+"/code?signature=s")
	assert.EqualError(t, err, "this code URL expired; call GetAgentBuilderAgentCodeURL for a new one")

	// A body that echoes the URL doesn't print its signature.
	const sig = "0123456789abcdef0123"
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("bad gateway for " + r.URL.String()))
	}))
	t.Cleanup(echo.Close)
	_, _, err = DownloadAgentBuilderAgentCode(context.Background(), echo.URL+"/code?signature="+sig)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), sig)

	// A transport error doesn't quote the URL's signature, which is the
	// credential.
	srv.Close()
	_, _, err = DownloadAgentBuilderAgentCode(context.Background(), srv.URL+"/code?signature=secret")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret")
	assert.Contains(t, err.Error(), "download agent code: ")
}
