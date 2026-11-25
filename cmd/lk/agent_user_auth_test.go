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
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/protocol/livekit"
)

// recorder is a Public API that answers each path with a canned body and
// records the requests it gets.
type recorder struct {
	mu       sync.Mutex
	requests []string
	bodies   []string
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requests...)
}

func newAgentAPIForTest(t *testing.T, answers map[string]string) (*userAuthAgentAPI, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.requests = append(rec.requests, r.Method+" "+r.URL.Path)
		rec.bodies = append(rec.bodies, string(b))
		rec.mu.Unlock()
		body, ok := answers[r.Method+" "+r.URL.Path]
		if !ok {
			body = `{}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	client, err := public.New(srv.URL, "tok")
	require.NoError(t, err)
	return &userAuthAgentAPI{client: client, projectID: "p_1"}, rec
}

func TestCloudAgentsInfoPutsBackThePlaceholders(t *testing.T) {
	info := cloudAgentsInfo(&livekit.AgentInfo{
		AgentId:          "CA_1",
		AgentDeployments: []*livekit.AgentDeployment{{Deployment: "production"}, {Deployment: "staging", Version: "v2"}},
	})
	assert.Equal(t, "---", info.Version)
	require.NotNil(t, info.DeployedAt, "the commands sort and print the deploy time")
	assert.True(t, info.DeployedAt.AsTime().IsZero(), "a zero time, which the commands print as an empty value")
	assert.Equal(t, "---", info.AgentDeployments[0].Version)
	assert.Equal(t, "v2", info.AgentDeployments[1].Version, "a known version stays")

	known := cloudAgentsInfo(&livekit.AgentInfo{Version: "v3"})
	assert.Equal(t, "v3", known.Version)
}

func TestUpdateAgentSetsSecretsOrRestarts(t *testing.T) {
	api, rec := newAgentAPIForTest(t, nil)

	_, err := api.UpdateAgent(context.Background(), &livekit.UpdateAgentRequest{
		AgentId: "CA_1", Secrets: []*livekit.AgentSecret{{Name: "KEY", Value: []byte("v")}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"PATCH /v0/projects/p_1/cloud-agents/CA_1/secrets"}, rec.seen())
	assert.Contains(t, rec.bodies[0], `"set"`)

	_, err = api.UpdateAgent(context.Background(), &livekit.UpdateAgentRequest{AgentId: "CA_1"})
	require.NoError(t, err)
	assert.Equal(t, "POST /v0/projects/p_1/cloud-agents/CA_1/restart", rec.seen()[1], "with no secrets, an update restarts the agent")
}

func TestCreateAgentNeedsOneRegion(t *testing.T) {
	api, rec := newAgentAPIForTest(t, nil)
	for _, regions := range [][]string{nil, {"us-east", "eu-west"}} {
		_, err := api.CreateAgent(context.Background(), nil, nil, regions, nil, nil, io.Discard)
		require.EqualError(t, err, "an agent is created in one region")
	}
	assert.Empty(t, rec.seen(), "nothing reaches the API")
}

func TestListPrivateLinksReadsTheHealthOnce(t *testing.T) {
	api, rec := newAgentAPIForTest(t, map[string]string{
		"GET /v0/projects/p_1/cloud-agent-private-links": `{"items":[
			{"link":{"privateLinkId":"PL_1","name":"db"},"status":{}},
			{"link":{"privateLinkId":"PL_2","name":"cache"}}]}`,
		"GET /v0/projects/p_1/cloud-agent-private-links/PL_1/status": `{"status":{}}`,
		"GET /v0/projects/p_1/cloud-agent-private-links/PL_2/status": `{"status":{}}`,
	})
	ctx := context.Background()

	list, err := api.ListPrivateLinks(ctx, &livekit.ListPrivateLinksRequest{})
	require.NoError(t, err)
	require.Len(t, list.Items, 2)

	// The list command asks for the health of each link next.
	h1, err := api.GetPrivateLinkStatus(ctx, &livekit.GetPrivateLinkStatusRequest{PrivateLinkId: "PL_1"})
	require.NoError(t, err)
	require.NotNil(t, h1.Value)
	_, err = api.GetPrivateLinkStatus(ctx, &livekit.GetPrivateLinkStatusRequest{PrivateLinkId: "PL_2"})
	require.NoError(t, err)

	assert.Equal(t, []string{
		"GET /v0/projects/p_1/cloud-agent-private-links",
		"GET /v0/projects/p_1/cloud-agent-private-links/PL_2/status",
	}, rec.seen(), "the health that the list read needs no request, and one that it could not read is read again")

	// The entry is for one read: a later status call reads the current health.
	_, err = api.GetPrivateLinkStatus(ctx, &livekit.GetPrivateLinkStatusRequest{PrivateLinkId: "PL_1"})
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(rec.seen()[len(rec.seen())-1], "/PL_1/status"))
}

// deployFake is a Public API that serves the calls of a deploy from source.
// versions answers each read of the versions of the agent, in order, and the
// last answer repeats.
type deployFake struct {
	mu           sync.Mutex
	srv          *httptest.Server
	versions     []func(w http.ResponseWriter)
	reads        int
	buildLog     func(w http.ResponseWriter)
	onUpload     func()
	buildStarted bool
}

func newDeployFake(t *testing.T) (*deployFake, *userAuthAgentAPI) {
	t.Helper()
	f := &deployFake{}
	mux := http.NewServeMux()
	base := "/v0/projects/p_1/cloud-agents"
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"agentId":"CA_1"}`)
	})
	mux.HandleFunc("POST "+base+"/CA_1/deployments", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"tag":"t1","upload":{"url":"`+f.srv.URL+`/upload","fields":{"key":"k"}}}`)
	})
	mux.HandleFunc("POST /upload", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if f.onUpload != nil {
			f.onUpload()
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST "+base+"/CA_1/builds", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.buildStarted = true
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("GET "+base+"/CA_1/versions", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		i := min(f.reads, len(f.versions)-1)
		f.reads++
		answer := f.versions[i]
		f.mu.Unlock()
		answer(w)
	})
	mux.HandleFunc("GET "+base+"/CA_1/build-logs", func(w http.ResponseWriter, r *http.Request) {
		if f.buildLog != nil {
			f.buildLog(w)
			return
		}
		_, _ = io.WriteString(w, `{"log":"step 1\nstep 2\n"}`)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	client, err := public.New(f.srv.URL, "tok")
	require.NoError(t, err)

	old := buildPollInterval
	buildPollInterval = time.Millisecond
	t.Cleanup(func() { buildPollInterval = old })
	return f, &userAuthAgentAPI{client: client, projectID: "p_1"}
}

func versionIs(status string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, `{"items":[{"version":"other","status":"Available"},{"version":"t1","status":"`+status+`"}]}`)
	}
}

func failWith(code int) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(code)
		_, _ = io.WriteString(w, `{"code":13,"message":"internal error"}`)
	}
}

var deploySource = fstest.MapFS{"Dockerfile": {Data: []byte("FROM alpine\n")}, "requirements.txt": {Data: []byte("livekit-agents\n")}}

func TestDeployFollowsTheBuildToItsEnd(t *testing.T) {
	// The status shows progress, and the end is read in any case.
	f, api := newDeployFake(t)
	f.versions = []func(http.ResponseWriter){versionIs("Building"), versionIs("Building"), versionIs("available")}
	var out bytes.Buffer
	err := api.DeployAgentV2(context.Background(), "CA_1", deploySource, nil, nil, "", nil, &out)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(out.String(), "Status: Building"), "a status prints once")
	assert.Contains(t, out.String(), "Status: available")
	assert.Contains(t, out.String(), "step 2", "the build log is printed")
	assert.True(t, f.buildStarted)
}

func TestDeployReportsAFailedBuildWithItsLog(t *testing.T) {
	f, api := newDeployFake(t)
	f.versions = []func(http.ResponseWriter){versionIs("Building"), versionIs("Failed")}
	var out bytes.Buffer
	err := api.DeployAgentV2(context.Background(), "CA_1", deploySource, nil, nil, "", nil, &out)
	require.EqualError(t, err, "the build failed; its build log is above")
	assert.Contains(t, out.String(), "step 1")
}

func TestDeployStopsFollowingAfterRepeatedReadFailures(t *testing.T) {
	f, api := newDeployFake(t)
	f.versions = []func(http.ResponseWriter){failWith(http.StatusInternalServerError)}
	var out bytes.Buffer
	err := api.DeployAgentV2(context.Background(), "CA_1", deploySource, nil, nil, "", nil, &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "can't follow the build")
	assert.Contains(t, err.Error(), "lk agent status", "the build goes on, and the user reads its end")
	assert.Equal(t, maxPollFailures, f.reads)
}

func TestDeployRetriesAReadThatFailsOnce(t *testing.T) {
	f, api := newDeployFake(t)
	f.versions = []func(http.ResponseWriter){failWith(http.StatusBadGateway), versionIs("Available")}
	require.NoError(t, api.DeployAgentV2(context.Background(), "CA_1", deploySource, nil, nil, "", nil, io.Discard))
}

func TestDeployReportsAnUnreadableBuildLog(t *testing.T) {
	f, api := newDeployFake(t)
	f.versions = []func(http.ResponseWriter){versionIs("Available")}
	f.buildLog = failWith(http.StatusInternalServerError)
	var out bytes.Buffer
	require.NoError(t, api.DeployAgentV2(context.Background(), "CA_1", deploySource, nil, nil, "", nil, &out), "the build succeeded")
	assert.Contains(t, out.String(), "can't read the build log")
}

func TestDeployInterruptedBeforeTheBuildStarts(t *testing.T) {
	f, api := newDeployFake(t)
	f.versions = []func(http.ResponseWriter){versionIs("Available")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.onUpload = cancel // the user interrupts while the source uploads

	err := api.DeployAgentV2(ctx, "CA_1", deploySource, nil, nil, "", nil, io.Discard)
	require.ErrorIs(t, err, errInterruptedBeforeBuild)
	assert.False(t, errors.Is(err, context.Canceled), "the command must not say that a build goes on")
	assert.False(t, f.buildStarted)
}

func TestCreateKeepsTheIDOfAnAgentThatFailedToDeploy(t *testing.T) {
	f, api := newDeployFake(t)
	f.versions = []func(http.ResponseWriter){versionIs("Failed")}
	resp, err := api.CreateAgent(context.Background(), deploySource, nil, []string{"us-east"}, nil, nil, io.Discard)
	require.Error(t, err)
	assert.Nil(t, resp)
	var created *agentCreatedError
	require.ErrorAs(t, err, &created)
	assert.Equal(t, "CA_1", created.agentID, "the command saves the ID, so a retry does not make another agent")
	assert.Contains(t, err.Error(), "the build failed")
}

func TestAgentAPIErrorNamesTheRole(t *testing.T) {
	denied := func(permission string) error {
		return agentAPIError(&public.APIError{Status: http.StatusForbidden, Message: "this needs " + permission + " on the project"})
	}
	assert.Contains(t, denied("agent_secret:manage").Error(), "needs a project admin")
	assert.Contains(t, denied("agent_secret:manage").Error(), "leave them out of the command", "a writer can deploy without secrets")
	assert.Contains(t, denied("agent:delete").Error(), "needs a project admin")
	assert.Contains(t, denied("agent:deploy").Error(), "needs write access")
	assert.NoError(t, agentAPIError(nil))
}

func TestRoleHintsFollowTheServersWords(t *testing.T) {
	// A server that decides by role names the role, not a permission.
	legacy := agentAPIError(&public.APIError{Status: http.StatusForbidden, Message: "this needs ADMIN on the project"})
	assert.Contains(t, legacy.Error(), "needs a project admin")
	legacy = agentAPIError(&public.APIError{Status: http.StatusForbidden, Message: "this needs WRITE on the project"})
	assert.Contains(t, legacy.Error(), "needs write access")

	// A command that set secrets teaches the way around the refusal in either mode.
	secrets := []*livekit.AgentSecret{{Name: "KEY", Value: []byte("v")}}
	for _, words := range []string{"this needs ADMIN on the project", "this needs agent_secret:manage on the project"} {
		refused := agentAPIError(&public.APIError{Status: http.StatusForbidden, Message: words})
		got := withSecretsHint(refused, secrets).Error()
		assert.Contains(t, got, "leave them out of the command", words)
		assert.Equal(t, 1, strings.Count(got, "leave them out of the command"), "the hint is said once")
	}
	// A refusal in words of a role gets the hint only from a command that set secrets.
	roleRefusal := agentAPIError(&public.APIError{Status: http.StatusForbidden, Message: "this needs ADMIN on the project"})
	assert.NotContains(t, withSecretsHint(roleRefusal, nil).Error(), "leave them out", "a command with no secrets gets no such hint")
	assert.NoError(t, withSecretsHint(nil, secrets))
	other := errors.New("upload source: connection reset")
	assert.Equal(t, other, withSecretsHint(other, secrets), "an error that is not a refusal stays as it is")
}

func TestStreamLogsBuildNotesATruncatedLog(t *testing.T) {
	api, _ := newAgentAPIForTest(t, map[string]string{
		"GET /v0/projects/p_1/cloud-agents/CA_1/build-logs": `{"log":"the end\n","truncated":true}`,
	})
	var out bytes.Buffer
	require.NoError(t, api.StreamLogs(context.Background(), "build", "CA_1", "", &out, ""))
	assert.Contains(t, out.String(), "the end")
	assert.Contains(t, out.String(), "this is its end", "the user learns that the log is cut")
}
