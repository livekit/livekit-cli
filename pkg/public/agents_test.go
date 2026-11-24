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
