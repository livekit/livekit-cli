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
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/protocol/livekit"
)

// agentInfoJSON is an Agent's info as the API sends it, with every field of
// livekit.AgentInfo and of the messages in it set.
const agentInfoJSON = `{
	"agentId": "CA_1",
	"agentName": "support",
	"version": "v20261120",
	"deployedAt": "2026-11-20T10:00:00Z",
	"secrets": [{"name": "S", "value": "dg==", "createdAt": "2026-11-01T00:00:00Z", "updatedAt": "2026-11-02T00:00:00Z", "kind": "AGENT_SECRET_KIND_FILE", "deployments": ["staging"]}],
	"agentDeployments": [{
		"region": "us-east",
		"agentId": "CA_1",
		"status": "Running",
		"replicas": 2,
		"minReplicas": 1,
		"maxReplicas": 5,
		"cpuReq": "500m",
		"curCpu": "120m",
		"curMem": "300Mi",
		"memReq": "1Gi",
		"memLimit": "2Gi",
		"cpuLimit": "1",
		"serverRegion": "osbxash1a",
		"events": [{"type": "AGENT_EVENT_TYPE_APPLICATION_CRASHED", "count": 3, "timestamp": "2026-11-21T08:00:00Z"}],
		"deployment": "staging",
		"version": "v20261120",
		"agentName": "support",
		"deploymentEnabled": true,
		"lastScrapedAt": "2026-11-22T09:30:00Z",
		"aFieldFromANewerServer": 1
	}]
}`

// wantAgentInfo is agentInfoJSON as livekit.AgentInfo.
func wantAgentInfo() *livekit.AgentInfo {
	ts := func(s string) *timestamppb.Timestamp {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			panic(err)
		}
		return timestamppb.New(t)
	}
	return &livekit.AgentInfo{
		AgentId:    "CA_1",
		AgentName:  "support",
		Version:    "v20261120",
		DeployedAt: ts("2026-11-20T10:00:00Z"),
		Secrets: []*livekit.AgentSecret{{
			Name: "S", Value: []byte("v"), CreatedAt: ts("2026-11-01T00:00:00Z"), UpdatedAt: ts("2026-11-02T00:00:00Z"),
			Kind: livekit.AgentSecretKind_AGENT_SECRET_KIND_FILE, Deployments: []string{"staging"},
		}},
		AgentDeployments: []*livekit.AgentDeployment{{
			Region: "us-east", AgentId: "CA_1", Status: "Running",
			Replicas: 2, MinReplicas: 1, MaxReplicas: 5,
			CpuReq: "500m", CurCpu: "120m", CurMem: "300Mi", MemReq: "1Gi", MemLimit: "2Gi", CpuLimit: "1",
			ServerRegion: "osbxash1a",
			Events: []*livekit.AgentEvent{{
				Type: livekit.AgentEventType_AGENT_EVENT_TYPE_APPLICATION_CRASHED, Count: 3, Timestamp: ts("2026-11-21T08:00:00Z"),
			}},
			Deployment: "staging", Version: "v20261120", AgentName: "support",
			DeploymentEnabled: true, LastScrapedAt: ts("2026-11-22T09:30:00Z"),
		}},
	}
}

// requireEveryField fails unless m and every message in it set each of
// their fields, so that a field livekit/protocol adds to an agent message
// fails here until agentInfoJSON sets it too.
func requireEveryField(t *testing.T, m protoreflect.Message) {
	t.Helper()
	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		require.Truef(t, m.Has(fd), "%s is not set", fd.FullName())
		if fd.Message() == nil || fd.Message().FullName() == "google.protobuf.Timestamp" {
			continue
		}
		if fd.IsList() {
			list := m.Get(fd).List()
			for j := range list.Len() {
				requireEveryField(t, list.Get(j).Message())
			}
			continue
		}
		requireEveryField(t, m.Get(fd).Message())
	}
}

// The agent's info comes back as livekit.AgentInfo with every field, as
// protojson sends it: camelCase names, enums by name, timestamps as RFC
// 3339. A field the client doesn't know is ignored.
func TestGetAgent(t *testing.T) {
	c, got := telephonyServer(t, http.StatusOK,
		`{"agent":{"info":`+agentInfoJSON+`,"status":"DEPLOYMENT_STATUS_RUNNING","agentBuilderId":"AB_1"}}`)
	agent, err := c.GetAgent(context.Background(), "p_1", "CA_1")
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, got.method)
	assert.Equal(t, "/v0/projects/p_1/agents/CA_1", got.path)

	requireEveryField(t, agent.Info.ProtoReflect())
	require.True(t, proto.Equal(wantAgentInfo(), agent.Info), "got %v", agent.Info)
	assert.Equal(t, oapi.LivekitPublicapiAgentsV1DeploymentStatusDEPLOYMENTSTATUSRUNNING, agent.Status)
	assert.Equal(t, "AB_1", agent.AgentBuilderID)
}

// An agent the project doesn't have is IsAgentNotFound. A project that
// isn't found is NotFound too, but not IsAgentNotFound.
func TestGetAgentNotFound(t *testing.T) {
	c, _ := telephonyServer(t, http.StatusNotFound, `{"code":5,"message":"agent not found"}`)
	_, err := c.GetAgent(context.Background(), "p_1", "CA_2")
	require.EqualError(t, err, "agent not found")
	assert.True(t, IsAgentNotFound(err))

	c, _ = telephonyServer(t, http.StatusNotFound, `{"code":5,"message":"project not found"}`)
	_, err = c.GetAgent(context.Background(), "p_2", "CA_2")
	require.EqualError(t, err, "project not found")
	assert.True(t, IsNotFound(err))
	assert.False(t, IsAgentNotFound(err))
}

func TestListAgents(t *testing.T) {
	c, got := telephonyServer(t, http.StatusOK,
		`{"items":[{"info":`+agentInfoJSON+`,"status":"DEPLOYMENT_STATUS_RUNNING"},{"info":{"agentId":"CA_2"},"status":"DEPLOYMENT_STATUS_NOT_DEPLOYED"}],"pageInfo":{}}`)
	agents, err := c.ListAgents(context.Background(), "p_1")
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, got.method)
	assert.Equal(t, "/v0/projects/p_1/agents", got.path)

	require.Len(t, agents, 2)
	require.True(t, proto.Equal(wantAgentInfo(), agents[0].Info), "got %v", agents[0].Info)
	assert.Empty(t, agents[0].AgentBuilderID)
	require.True(t, proto.Equal(&livekit.AgentInfo{AgentId: "CA_2"}, agents[1].Info), "got %v", agents[1].Info)
	assert.Equal(t, oapi.LivekitPublicapiAgentsV1DeploymentStatusDEPLOYMENTSTATUSNOTDEPLOYED, agents[1].Status)
}

// A list response the client can't use in full is an error, not a partial
// list.
func TestListAgentsBadResponse(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"next page": {http.StatusOK, `{"items":[],"pageInfo":{"nextCursor":"c","hasMore":true}}`, "unexpected response from server: the list has more than one page"},
		"no info":   {http.StatusOK, `{"items":[{"status":"DEPLOYMENT_STATUS_RUNNING"}]}`, "unexpected response from server: missing info"},
		"bad info":  {http.StatusOK, `{"items":[{"info":{"agentDeployments":[{"replicas":"x"}]}}]}`, "replicas"},
		"not json":  {http.StatusOK, `<html>`, "invalid character"},
		"refused":   {http.StatusForbidden, `{"code":7,"message":"not authorized"}`, "not authorized"},
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := telephonyServer(t, tc.status, tc.body)
			agents, err := c.ListAgents(context.Background(), "p_1")
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, agents)
		})
	}
}

// A get response without its agent is an error, not a nil agent.
func TestGetAgentMissingAgent(t *testing.T) {
	c, _ := telephonyServer(t, http.StatusOK, `{}`)
	_, err := c.GetAgent(context.Background(), "p_1", "CA_1")
	require.EqualError(t, err, "unexpected response from server: missing agent")
}

// Versions come back as livekit.AgentVersion, newest first as the API sends
// them, with a deploy time only where the API sends one.
func TestListAgentVersions(t *testing.T) {
	c, got := telephonyServer(t, http.StatusOK,
		`{"items":[{"version":"v2","createdAt":"2026-11-21T09:00:00Z","status":"Failed"},{"version":"v1","current":true,"active":true,"draining":true,"owner":"o","createdAt":"2026-11-20T09:00:00Z","deployedAt":"2026-11-20T10:00:00Z","attributes":{"git_sha":"abc"},"status":"Succeeded"}],"pageInfo":{}}`)
	versions, err := c.ListAgentVersions(context.Background(), "p_1", "CA_1")
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, got.method)
	assert.Equal(t, "/v0/projects/p_1/agents/CA_1/versions", got.path)

	require.Len(t, versions, 2)
	require.True(t, proto.Equal(&livekit.AgentVersion{
		Version: "v2", CreatedAt: timestamppb.New(time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)), Status: "Failed",
	}, versions[0]), "got %v", versions[0])
	require.True(t, proto.Equal(&livekit.AgentVersion{
		Version: "v1", Current: true, Active: true, Draining: true, Owner: "o",
		CreatedAt:  timestamppb.New(time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)),
		DeployedAt: timestamppb.New(time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)),
		Attributes: map[string]string{"git_sha": "abc"}, Status: "Succeeded",
	}, versions[1]), "got %v", versions[1])
}

func TestListAgentSecrets(t *testing.T) {
	c, got := telephonyServer(t, http.StatusOK,
		`{"items":[{"name":"API_TOKEN","createdAt":"2026-11-01T00:00:00Z","kind":"AGENT_SECRET_KIND_FILE","deployments":["staging"]}],"pageInfo":{}}`)
	secrets, err := c.ListAgentSecrets(context.Background(), "p_1", "CA_1")
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, got.method)
	assert.Equal(t, "/v0/projects/p_1/agents/CA_1/secrets", got.path)
	require.Len(t, secrets, 1)
	require.True(t, proto.Equal(&livekit.AgentSecret{
		Name: "API_TOKEN", CreatedAt: timestamppb.New(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)),
		Kind: livekit.AgentSecretKind_AGENT_SECRET_KIND_FILE, Deployments: []string{"staging"},
	}, secrets[0]), "got %v", secrets[0])
}

// A member below admin gets the API's refusal as it is.
func TestListAgentSecretsRefused(t *testing.T) {
	c, _ := telephonyServer(t, http.StatusForbidden, `{"code":7,"message":"this needs ADMIN on the project"}`)
	secrets, err := c.ListAgentSecrets(context.Background(), "p_1", "CA_1")
	require.EqualError(t, err, "this needs ADMIN on the project")
	assert.True(t, IsPermissionDenied(err))
	assert.Nil(t, secrets)
}

func TestGetAgentBuildLog(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want BuildLog
	}{
		"whole":     {`{"text":"#1 resolve\n#2 DONE\n"}`, BuildLog{Text: "#1 resolve\n#2 DONE\n"}},
		"truncated": {`{"text":"#2 DONE\n","truncated":true}`, BuildLog{Text: "#2 DONE\n", Truncated: true}},
		"empty":     {`{}`, BuildLog{}},
	} {
		t.Run(name, func(t *testing.T) {
			c, got := telephonyServer(t, http.StatusOK, tc.body)
			log, err := c.GetAgentBuildLog(context.Background(), "p_1", "CA_1", "v20261120")
			require.NoError(t, err)
			assert.Equal(t, http.MethodGet, got.method)
			assert.Equal(t, "/v0/projects/p_1/agents/CA_1/versions/v20261120/build-log", got.path)
			assert.Equal(t, tc.want, *log)
		})
	}
}

// A build that hasn't ended has no log yet.
func TestGetAgentBuildLogNotFound(t *testing.T) {
	c, _ := telephonyServer(t, http.StatusNotFound, `{"code":5,"message":"build log not found"}`)
	log, err := c.GetAgentBuildLog(context.Background(), "p_1", "CA_1", "v1")
	require.EqualError(t, err, "build log not found")
	assert.True(t, IsNotFound(err))
	assert.Nil(t, log)
}

func TestGetAgentClientSettings(t *testing.T) {
	c, got := telephonyServer(t, http.StatusOK,
		`{"params":[{"name":"available_regions","value":"us-east,eu-central"},{"name":"python-min-sdk-version","value":"1.0.0"}],"aFieldFromANewerServer":1}`)
	settings, err := c.GetAgentClientSettings(context.Background(), "p_1")
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, got.method)
	assert.Equal(t, "/v0/projects/p_1/agent-client-settings", got.path)
	require.True(t, proto.Equal(&livekit.ClientSettingsResponse{Params: []*livekit.SettingsParam{
		{Name: "available_regions", Value: "us-east,eu-central"},
		{Name: "python-min-sdk-version", Value: "1.0.0"},
	}}, settings), "got %v", settings)
}

// Settings that aren't a list are an error, not no settings.
func TestGetAgentClientSettingsBadResponse(t *testing.T) {
	c, _ := telephonyServer(t, http.StatusOK, `{"params":"x"}`)
	settings, err := c.GetAgentClientSettings(context.Background(), "p_1")
	require.ErrorContains(t, err, "params")
	assert.Nil(t, settings)
}
