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
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/livekit/protocol/livekit"
)

// request is what a test server saw.
type request struct {
	method, path, query, body string
}

func agentServer(t *testing.T, status int, response string, seen *request) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*seen = request{r.Method, r.URL.Path, r.URL.RawQuery, string(b)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "tok")
	require.NoError(t, err)
	return c
}

func TestListCloudAgents(t *testing.T) {
	var seen request
	c := agentServer(t, http.StatusOK, `{"items":[
		{"info":{"agentId":"CA_1","agentDeployments":[{"deployment":"production","status":"Running"}]},"status":"CLOUD_AGENT_STATUS_RUNNING"},
		{"info":{"agentId":"CA_2"},"agentBuilderId":"AB_1","status":"CLOUD_AGENT_STATUS_NOT_DEPLOYED"}]}`, &seen)

	agents, err := c.ListCloudAgents(context.Background(), "p_1", []string{"CA_1", "CA_2"})
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/cloud-agents", seen.path)
	assert.Contains(t, seen.query, "agentIds=CA_1")
	require.Len(t, agents, 2)
	assert.Equal(t, "CA_1", agents[0].Info.GetAgentId())
	assert.Equal(t, "CLOUD_AGENT_STATUS_RUNNING", agents[0].Status)
	assert.Empty(t, agents[0].AgentBuilderID)
	assert.Equal(t, "Running", agents[0].Info.GetAgentDeployments()[0].GetStatus())
	assert.Equal(t, "AB_1", agents[1].AgentBuilderID)
	assert.Empty(t, agents[1].Info.GetVersion(), "the API leaves out a version that LiveKit does not know")
	assert.Nil(t, agents[1].Info.GetDeployedAt())
}

func TestCreateCloudAgentSendsOneRegion(t *testing.T) {
	var seen request
	c := agentServer(t, http.StatusOK, `{"agentId":"CA_new"}`, &seen)

	id, err := c.CreateCloudAgent(context.Background(), "p_1", "us-east", []*livekit.AgentSecret{{Name: "KEY", Value: []byte("v")}})
	require.NoError(t, err)
	assert.Equal(t, "CA_new", id)
	assert.Equal(t, http.MethodPost, seen.method)
	assert.Equal(t, "/v0/projects/p_1/cloud-agents", seen.path)
	assert.JSONEq(t, `{"region":"us-east","secrets":[{"name":"KEY","value":"dg=="}]}`, seen.body, "a secret value is base64 text")
}

func TestDeployCloudAgentReadsTheUpload(t *testing.T) {
	var seen request
	c := agentServer(t, http.StatusOK, `{"tag":"abc123","upload":{"url":"https://store.example/up","fields":{"key":"k","policy":"p"}}}`, &seen)

	d, err := c.DeployCloudAgent(context.Background(), "p_1", "CA_1", "staging", nil)
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/cloud-agents/CA_1/deployments", seen.path)
	assert.Equal(t, "abc123", d.Tag)
	assert.Equal(t, "https://store.example/up", d.UploadURL)
	assert.Equal(t, map[string]string{"key": "k", "policy": "p"}, d.UploadFields)
}

func TestUpdateCloudAgentSecretsSendsSetAndRemove(t *testing.T) {
	var seen request
	c := agentServer(t, http.StatusOK, `{}`, &seen)

	err := c.UpdateCloudAgentSecrets(context.Background(), "p_1", "CA_1", []*livekit.AgentSecret{{Name: "A", Value: []byte("1")}}, []string{"B"})
	require.NoError(t, err)
	assert.Equal(t, http.MethodPatch, seen.method)
	assert.Equal(t, "/v0/projects/p_1/cloud-agents/CA_1/secrets", seen.path)
	assert.JSONEq(t, `{"set":[{"name":"A","value":"MQ=="}],"remove":["B"]}`, seen.body)
	assert.NotContains(t, seen.body, "overwrite", "the API never deletes a secret that a request does not name")
}

func TestListCloudAgentPrivateLinksWithHealth(t *testing.T) {
	var seen request
	c := agentServer(t, http.StatusOK, `{"items":[
		{"link":{"privateLinkId":"PL_1","name":"db"},"status":{"status":"PRIVATE_LINK_STATUS_ACTIVE"}},
		{"link":{"privateLinkId":"PL_2","name":"cache"}}]}`, &seen)

	links, err := c.ListCloudAgentPrivateLinks(context.Background(), "p_1")
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/cloud-agent-private-links", seen.path)
	require.Len(t, links, 2)
	assert.Equal(t, "PL_1", links[0].Link.GetPrivateLinkId())
	require.NotNil(t, links[0].Status)
	assert.Nil(t, links[1].Status, "a link whose health the API could not read has none")
}

func TestCloudAgentErrorsKeepTheFieldViolations(t *testing.T) {
	var seen request
	c := agentServer(t, http.StatusBadRequest, `{"code":3,"message":"secrets are invalid","details":[{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"set[0].name","description":"must have only letters, digits and _"}]}]}`, &seen)

	err := c.UpdateCloudAgentSecrets(context.Background(), "p_1", "CA_1", []*livekit.AgentSecret{{Name: "bad name", Value: []byte("1")}}, nil)
	require.Error(t, err)
	violations := FieldViolations(err)
	require.Len(t, violations, 1)
	assert.Equal(t, "set[0].name", violations[0].Field)
}
