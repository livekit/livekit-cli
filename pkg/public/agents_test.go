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
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
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

func TestAgentWrites(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		call     func(c *Client) error
		response string
		method   string
		path     string
		query    string
		body     string
	}{
		"restart": {
			call:   func(c *Client) error { return c.RestartAgent(ctx, "p_1", "CA_1") },
			method: http.MethodPost, path: "/v0/projects/p_1/agents/CA_1:restart",
		},
		"rollback": {
			call:   func(c *Client) error { return c.RollbackAgent(ctx, "p_1", "CA_1", "v1") },
			method: http.MethodPost, path: "/v0/projects/p_1/agents/CA_1:rollback", body: `{"version":"v1"}`,
		},
		"rollback to the previous version": {
			call:   func(c *Client) error { return c.RollbackAgent(ctx, "p_1", "CA_1", "") },
			method: http.MethodPost, path: "/v0/projects/p_1/agents/CA_1:rollback", body: `{}`,
		},
		"promote": {
			call: func(c *Client) error {
				version, err := c.PromoteAgent(ctx, "p_1", "CA_1", "staging")
				if version != "v2" {
					return fmt.Errorf("version %q", version)
				}
				return err
			},
			response: `{"version":"v2"}`,
			method:   http.MethodPost, path: "/v0/projects/p_1/agents/CA_1:promote", body: `{"deployment":"staging"}`,
		},
		"delete": {
			call:   func(c *Client) error { return c.DeleteAgent(ctx, "p_1", "CA_1", "") },
			method: http.MethodDelete, path: "/v0/projects/p_1/agents/CA_1",
		},
		"delete a deployment": {
			call:   func(c *Client) error { return c.DeleteAgent(ctx, "p_1", "CA_1", "staging") },
			method: http.MethodDelete, path: "/v0/projects/p_1/agents/CA_1", query: "deployment=staging",
		},
	} {
		t.Run(name, func(t *testing.T) {
			response := tc.response
			if response == "" {
				response = `{}`
			}
			c, got := telephonyServer(t, http.StatusOK, response)
			require.NoError(t, tc.call(c))
			assert.Equal(t, tc.method, got.method)
			assert.Equal(t, tc.path, got.path)
			assert.Equal(t, tc.query, got.query)
			if tc.body == "" {
				assert.Empty(t, got.body)
			} else {
				assert.JSONEq(t, tc.body, got.body)
			}
		})
	}
}

// A secret's value goes out as base64, as protojson sends bytes, with its
// kind by name.
func TestUpdateAgentSecrets(t *testing.T) {
	c, got := telephonyServer(t, http.StatusOK, `{}`)
	err := c.UpdateAgentSecrets(context.Background(), "p_1", "CA_1", []*livekit.AgentSecret{
		{Name: "API_TOKEN", Value: []byte("s3cr3t\n"), Kind: livekit.AgentSecretKind_AGENT_SECRET_KIND_ENVIRONMENT},
		{Name: "creds.json", Value: []byte{0xff, 0x00}, Kind: livekit.AgentSecretKind_AGENT_SECRET_KIND_FILE},
	}, []string{"OLD"})
	require.NoError(t, err)
	assert.Equal(t, http.MethodPatch, got.method)
	assert.Equal(t, "/v0/projects/p_1/agents/CA_1/secrets", got.path)
	assert.JSONEq(t, `{
		"set": [
			{"name": "API_TOKEN", "value": "czNjcjN0Cg==", "kind": "AGENT_SECRET_KIND_ENVIRONMENT"},
			{"name": "creds.json", "value": "/wA=", "kind": "AGENT_SECRET_KIND_FILE"}
		],
		"remove": ["OLD"]
	}`, got.body)

	// The value decodes as the API decodes it, with protojson.
	var sent struct {
		Set []json.RawMessage `json:"set"`
	}
	require.NoError(t, json.Unmarshal([]byte(got.body), &sent))
	secret := new(livekit.AgentSecret)
	require.NoError(t, protojson.Unmarshal(sent.Set[1], secret))
	assert.Equal(t, []byte{0xff, 0x00}, secret.Value)
}

// Only set or only remove leaves the other out.
func TestUpdateAgentSecretsOnlyRemove(t *testing.T) {
	c, got := telephonyServer(t, http.StatusOK, `{}`)
	require.NoError(t, c.UpdateAgentSecrets(context.Background(), "p_1", "CA_1", nil, []string{"OLD"}))
	assert.JSONEq(t, `{"remove": ["OLD"]}`, got.body)
}

func TestFieldViolations(t *testing.T) {
	c, _ := telephonyServer(t, http.StatusBadRequest, `{"code":3,"message":"secrets are invalid; this error's details name each field that is wrong","details":[`+
		`{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"set[0].name","description":"must have only letters, digits and _, and must not start with a digit"},{"field":"remove[1]","description":"names a secret that an earlier item of remove names"}]}]}`)
	err := c.UpdateAgentSecrets(context.Background(), "p_1", "CA_1", []*livekit.AgentSecret{{Name: "1KEY", Value: []byte("v")}}, []string{"OLD", "OLD"})
	require.EqualError(t, err, "secrets are invalid; this error's details name each field that is wrong")
	assert.Equal(t, []FieldViolation{{"set[0].name", "must have only letters, digits and _, and must not start with a digit"}, {"remove[1]", "names a secret that an earlier item of remove names"}}, FieldViolations(err))

	assert.Nil(t, FieldViolations(&APIError{Status: http.StatusBadRequest, Message: "set or remove is required"}))
	assert.Nil(t, FieldViolations(errors.New("plain")))
}

// Rollback, promote and delete refuse an Agent Builder agent with
// FailedPrecondition, which REST sends as 400.
func TestIsAgentBuilderAgent(t *testing.T) {
	c, _ := telephonyServer(t, http.StatusBadRequest,
		`{"code":9,"message":"agent is an Agent Builder agent; delete it with AgentBuilderService.DeleteAgent"}`)
	err := c.DeleteAgent(context.Background(), "p_1", "CA_1", "")
	assert.True(t, IsAgentBuilderAgent(err))

	assert.False(t, IsAgentBuilderAgent(&APIError{Status: http.StatusBadRequest, Message: "version is not available: its build failed or did not finish"}))
	assert.False(t, IsAgentBuilderAgent(&APIError{Status: http.StatusNotFound, Message: "agent not found"}))
}

func TestPromoteAgentMissingVersion(t *testing.T) {
	c, _ := telephonyServer(t, http.StatusOK, `{}`)
	_, err := c.PromoteAgent(context.Background(), "p_1", "CA_1", "staging")
	require.EqualError(t, err, "unexpected response from server: missing version")
}

// CreateAgent sends the region and the secrets, as UpdateAgentSecrets sends
// them, and returns the new agent's ID.
func TestCreateAgent(t *testing.T) {
	c, got := telephonyServer(t, http.StatusOK, `{"agentId":"CA_1"}`)
	id, err := c.CreateAgent(context.Background(), "p_1", "us-east", []*livekit.AgentSecret{
		{Name: "API_TOKEN", Value: []byte("s3cr3t"), Kind: livekit.AgentSecretKind_AGENT_SECRET_KIND_ENVIRONMENT},
	})
	require.NoError(t, err)
	assert.Equal(t, "CA_1", id)
	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/v0/projects/p_1/agents", got.path)
	assert.JSONEq(t, `{"region":"us-east","secrets":[{"name":"API_TOKEN","value":"czNjcjN0","kind":"AGENT_SECRET_KIND_ENVIRONMENT"}]}`, got.body)

	c, got = telephonyServer(t, http.StatusOK, `{"agentId":"CA_2"}`)
	_, err = c.CreateAgent(context.Background(), "p_1", "us-east", nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"region":"us-east"}`, got.body, "no secrets leaves them out")

	c, _ = telephonyServer(t, http.StatusOK, `{}`)
	_, err = c.CreateAgent(context.Background(), "p_1", "us-east", nil)
	require.EqualError(t, err, "unexpected response from server: missing agent ID")
}

// DeployAgent returns the tag and the presigned POST; production is no
// deployment.
func TestDeployAgent(t *testing.T) {
	c, got := telephonyServer(t, http.StatusOK,
		`{"tag":"T1","upload":{"url":"https://uploads.example/bucket","fields":{"key":"CA_1/source.tar.gz","policy":"p"}}}`)
	d, err := c.DeployAgent(context.Background(), "p_1", "CA_1", "staging", nil)
	require.NoError(t, err)
	assert.Equal(t, &SourceDeploy{
		Tag:          "T1",
		UploadURL:    "https://uploads.example/bucket",
		UploadFields: map[string]string{"key": "CA_1/source.tar.gz", "policy": "p"},
	}, d)
	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/v0/projects/p_1/agents/CA_1:deploy", got.path)
	assert.JSONEq(t, `{"deployment":"staging"}`, got.body)

	c, got = telephonyServer(t, http.StatusOK, `{"tag":"T1","upload":{"url":"https://uploads.example/bucket"}}`)
	d, err = c.DeployAgent(context.Background(), "p_1", "CA_1", "", []*livekit.AgentSecret{{Name: "S", Value: []byte("v"), Kind: livekit.AgentSecretKind_AGENT_SECRET_KIND_ENVIRONMENT}})
	require.NoError(t, err)
	assert.Empty(t, d.UploadFields)
	assert.JSONEq(t, `{"secrets":[{"name":"S","value":"dg==","kind":"AGENT_SECRET_KIND_ENVIRONMENT"}]}`, got.body)

	c, _ = telephonyServer(t, http.StatusOK, `{"tag":"T1"}`)
	_, err = c.DeployAgent(context.Background(), "p_1", "CA_1", "", nil)
	require.EqualError(t, err, "unexpected response from server: missing tag or upload")
}

func TestStartAgentBuild(t *testing.T) {
	c, got := telephonyServer(t, http.StatusOK, `{}`)
	require.NoError(t, c.StartAgentBuild(context.Background(), "p_1", "CA_1", "staging", "T1", map[string]string{"commit": "abc"}))
	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/v0/projects/p_1/agents/CA_1:startBuild", got.path)
	assert.JSONEq(t, `{"tag":"T1","deployment":"staging","attributes":{"commit":"abc"}}`, got.body)

	c, got = telephonyServer(t, http.StatusOK, `{}`)
	require.NoError(t, c.StartAgentBuild(context.Background(), "p_1", "CA_1", "", "T1", nil))
	assert.JSONEq(t, `{"tag":"T1"}`, got.body)

	c, _ = telephonyServer(t, http.StatusTooManyRequests, `{"code":8,"message":"too many builds; try again later"}`)
	err := c.StartAgentBuild(context.Background(), "p_1", "CA_1", "", "T1", nil)
	require.EqualError(t, err, "too many builds; try again later")
}
