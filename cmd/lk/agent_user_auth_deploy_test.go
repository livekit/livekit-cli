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
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/protocol/livekit"
)

// fakeBuild is a Public API that deploys from source: it answers
// CreateAgent, DeployAgent with a presigned POST to its own /upload,
// StartAgentBuild, and then GetAgent and ListAgentVersions with the
// deployment's statuses and the version's status, one poll at a time.
type fakeBuild struct {
	t *testing.T

	// statuses is the deployment's status at each GetAgent, the last one
	// repeating. versions is the version's status at each
	// ListAgentVersions, "" for not listed yet, the last one repeating.
	statuses []string
	versions []string
	// refuse answers a path with a status and a body in place of its
	// usual answer. unavailable answers a path with 503 that many times
	// before its usual answer.
	refuse      map[string]string
	unavailable map[string]int

	mu       sync.Mutex
	requests []string          // the method and the URL, with its query, of each request
	bodies   map[string]string // the JSON body of each request that has one, by method and path, the last one kept
	polls    int
	upload   map[string]string // the form fields of the upload
	files    []string          // the names in the uploaded tarball
}

const (
	fakeTag      = "T1"
	fakeBuildLog = "#1 [build] RUN pip install\n#1 DONE"
)

func (f *fakeBuild) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		b, err := io.ReadAll(r.Body)
		require.NoError(f.t, err)
		if f.bodies == nil {
			f.bodies = map[string]string{}
		}
		f.bodies[r.Method+" "+r.URL.Path] = string(b)
	}
	w.Header().Set("Content-Type", "application/json")
	if f.unavailable[r.Method+" "+r.URL.Path] > 0 {
		f.unavailable[r.Method+" "+r.URL.Path]--
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"code":14,"message":"cloud agents is unavailable"}`)
		return
	}
	if refusal, ok := f.refuse[r.Method+" "+r.URL.Path]; ok {
		status, body, _ := strings.Cut(refusal, " ")
		var code int
		_, _ = fmt.Sscan(status, &code)
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
		return
	}
	at := func(s []string, i int) string { return s[min(i, len(s)-1)] }
	switch r.Method + " " + r.URL.Path {
	case "POST /v0/projects/p_1/agents":
		_, _ = io.WriteString(w, `{"agentId":"CA_1"}`)
	case "POST /v0/projects/p_1/agents/CA_1:deploy":
		fmt.Fprintf(w, `{"tag":%q,"upload":{"url":"http://%s/upload","fields":{"key":"CA_1/source.tar.gz","policy":"p"}}}`, fakeTag, r.Host)
	case "POST /upload":
		f.readUpload(r)
		w.WriteHeader(http.StatusNoContent)
	case "POST /v0/projects/p_1/agents/CA_1:startBuild":
		_, _ = io.WriteString(w, `{}`)
	case "GET /v0/projects/p_1/agents/CA_1":
		fmt.Fprintf(w, `{"agent":{"info":{"agentId":"CA_1","agentDeployments":[`+
			`{"region":"us-east","deployment":"production","status":%q,"serverRegion":"osbxash1a"},`+
			`{"region":"us-east","deployment":"staging","status":"Running","serverRegion":"osbxash1b"}]}}}`, at(f.statuses, f.polls))
	case "GET /v0/projects/p_1/agents/CA_1/versions":
		items := `{"version":"T0","status":"Available"}`
		if status := at(f.versions, f.polls); status != "" {
			items = fmt.Sprintf(`{"version":%q,"status":%q},`, fakeTag, status) + items
		}
		f.polls++
		fmt.Fprintf(w, `{"items":[%s]}`, items)
	case "GET /v0/projects/p_1/agents/CA_1/versions/" + fakeTag + "/build-log":
		fmt.Fprintf(w, `{"text":%q}`, fakeBuildLog)
	default:
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = io.WriteString(w, `{"code":12,"message":"fake build: no answer"}`)
	}
}

// readUpload keeps the form fields and the tarball's file names of the
// upload r.
func (f *fakeBuild) readUpload(r *http.Request) {
	require.NoError(f.t, r.ParseMultipartForm(1<<20))
	f.upload = map[string]string{}
	for k, v := range r.MultipartForm.Value {
		f.upload[k] = v[0]
	}
	file, _, err := r.FormFile("file")
	require.NoError(f.t, err)
	gz, err := gzip.NewReader(file)
	require.NoError(f.t, err)
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(f.t, err)
		f.files = append(f.files, h.Name)
	}
}

// start serves f, and returns a userAuthAgentAPI that calls it. awaitBuild
// polls it every millisecond, and makes a read that failed transiently
// again after a millisecond.
func (f *fakeBuild) start() *userAuthAgentAPI {
	f.t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	f.t.Cleanup(srv.Close)
	client, err := public.New(srv.URL, "st_1")
	require.NoError(f.t, err)
	interval := buildPollInterval
	buildPollInterval = time.Millisecond
	f.t.Cleanup(func() { buildPollInterval = interval })
	backoff := pollRetryBackoff
	pollRetryBackoff = time.Millisecond
	f.t.Cleanup(func() { pollRetryBackoff = backoff })
	return &userAuthAgentAPI{client: client, projectID: "p_1"}
}

// agentSource is an agent's directory, with the files that lk leaves out of
// the upload.
var agentSource = fstest.MapFS{
	"Dockerfile":          {Data: []byte("FROM python")},
	"agent.py":            {Data: []byte("print('hi')")},
	"livekit.toml":        {Data: []byte("[agent]")},
	".env.local":          {Data: []byte("API_TOKEN=s3cr3t")},
	"node_modules/x/x.js": {Data: []byte("x")},
}

var agentExcludes = []string{"**/livekit.toml"}

// create registers the agent in its region, uploads the source without
// what lk leaves out, starts the build with the tag and the attributes, and
// polls until the version is available. It writes each new status and the
// build log, and returns the agent's ID and the server region of its
// production deployment, where lk streams its runtime log from.
func TestUserAuthCreateAgent(t *testing.T) {
	f := &fakeBuild{t: t, statuses: []string{"Building", "Building", "Running"}, versions: []string{"", "", "Available"}}
	a := f.start()
	var log strings.Builder

	res, err := a.CreateAgent(context.Background(), agentSource, agentSecrets, []string{"us-east"}, map[string]string{"commit": "abc"}, agentExcludes, &log)
	require.NoError(t, err)
	require.Equal(t, "CA_1", res.AgentId)
	require.Equal(t, []string{"osbxash1a"}, res.ServerRegions)

	require.Equal(t, "Building version T1. This deploy continues even if you disconnect.\n"+
		"Status: Building\n"+
		"Status: Running\n"+
		fakeBuildLog+"\n", log.String())
	require.Equal(t, map[string]string{"key": "CA_1/source.tar.gz", "policy": "p"}, f.upload)
	slices.Sort(f.files)
	require.Equal(t, []string{"./", "Dockerfile", "agent.py"}, f.files)
	require.Equal(t, []string{
		"POST /v0/projects/p_1/agents",
		"POST /v0/projects/p_1/agents/CA_1:deploy",
		"POST /upload",
		"POST /v0/projects/p_1/agents/CA_1:startBuild",
		"GET /v0/projects/p_1/agents/CA_1",
		"GET /v0/projects/p_1/agents/CA_1/versions",
		"GET /v0/projects/p_1/agents/CA_1",
		"GET /v0/projects/p_1/agents/CA_1/versions",
		"GET /v0/projects/p_1/agents/CA_1",
		"GET /v0/projects/p_1/agents/CA_1/versions",
		"GET /v0/projects/p_1/agents/CA_1/versions/T1/build-log",
	}, f.requests)
	// CreateAgent sets the secrets, so the deploy sends none.
	require.JSONEq(t, `{"region":"us-east","secrets":`+agentSecretsJSON+`}`, f.bodies["POST /v0/projects/p_1/agents"])
	require.JSONEq(t, `{}`, f.bodies["POST /v0/projects/p_1/agents/CA_1:deploy"])
	require.JSONEq(t, `{"tag":"T1","attributes":{"commit":"abc"}}`, f.bodies["POST /v0/projects/p_1/agents/CA_1:startBuild"])
}

// agentSecrets are the secrets that the tests deploy, and agentSecretsJSON
// is how the Public API takes them.
var (
	agentSecrets = []*livekit.AgentSecret{
		{Name: "API_TOKEN", Value: []byte("s3cr3t"), Kind: livekit.AgentSecretKind_AGENT_SECRET_KIND_ENVIRONMENT},
		{Name: "creds.json", Value: []byte("{}"), Kind: livekit.AgentSecretKind_AGENT_SECRET_KIND_FILE},
	}
	agentSecretsJSON = `[{"name":"API_TOKEN","value":"czNjcjN0","kind":"AGENT_SECRET_KIND_ENVIRONMENT"},` +
		`{"name":"creds.json","value":"e30=","kind":"AGENT_SECRET_KIND_FILE"}]`
)

// An agent is created in one region.
func TestUserAuthCreateAgentNeedsOneRegion(t *testing.T) {
	f := &fakeBuild{t: t}
	a := f.start()
	_, err := a.CreateAgent(context.Background(), agentSource, nil, nil, nil, agentExcludes, io.Discard)
	require.EqualError(t, err, "an agent is created in one region, not 0")
	require.Empty(t, f.requests)
}

// deploy to a deployment follows that deployment's status, and sends the
// deployment, the secrets and the attributes.
func TestUserAuthDeployAgent(t *testing.T) {
	f := &fakeBuild{t: t, statuses: []string{"Running"}, versions: []string{"", "Available"}}
	a := f.start()
	var log strings.Builder

	err := a.DeployAgentV2(context.Background(), "CA_1", agentSource, agentSecrets, map[string]string{"commit": "abc"}, "staging", agentExcludes, &log)
	require.NoError(t, err)
	require.Equal(t, "Building version T1. This deploy continues even if you disconnect.\n"+
		"Status: Running\n"+fakeBuildLog+"\n", log.String(), "staging's status, not production's")
	require.JSONEq(t, `{"deployment":"staging","secrets":`+agentSecretsJSON+`}`, f.bodies["POST /v0/projects/p_1/agents/CA_1:deploy"])
	require.JSONEq(t, `{"deployment":"staging","tag":"T1","attributes":{"commit":"abc"}}`, f.bodies["POST /v0/projects/p_1/agents/CA_1:startBuild"])

	// To production, the deploy names no deployment.
	f = &fakeBuild{t: t, statuses: []string{"Running"}, versions: []string{"Available"}}
	a = f.start()
	require.NoError(t, a.DeployAgentV2(context.Background(), "CA_1", agentSource, nil, nil, "", agentExcludes, io.Discard))
	require.JSONEq(t, `{}`, f.bodies["POST /v0/projects/p_1/agents/CA_1:deploy"])
	require.JSONEq(t, `{"tag":"T1"}`, f.bodies["POST /v0/projects/p_1/agents/CA_1:startBuild"])
}

// A build that fails writes its build log and says so, whether the version
// or only the deployment's status says it failed first.
func TestUserAuthDeployAgentBuildFailed(t *testing.T) {
	for name, f := range map[string]*fakeBuild{
		"version failed":      {statuses: []string{"Building"}, versions: []string{"", "Failed"}},
		"status build failed": {statuses: []string{"Building", "Build Failed"}, versions: []string{""}},
	} {
		t.Run(name, func(t *testing.T) {
			f.t = t
			a := f.start()
			var log strings.Builder
			err := a.DeployAgentV2(context.Background(), "CA_1", agentSource, nil, nil, "", agentExcludes, &log)
			require.EqualError(t, err, "the build of version T1 failed: its build log is above")
			require.True(t, strings.HasSuffix(log.String(), fakeBuildLog+"\n"), log.String())
		})
	}

	f := &fakeBuild{t: t, statuses: []string{"Building"}, versions: []string{"Failed"}}
	a := f.start()
	_, err := a.CreateAgent(context.Background(), agentSource, nil, []string{"us-east"}, nil, agentExcludes, io.Discard)
	require.EqualError(t, err, "created agent CA_1, but the build of version T1 failed: its build log is above",
		"create names the agent it created")
}

// When ctx ends before the build does, the deploy returns ctx's error, which
// lk reads as a build that continues on LiveKit Cloud.
func TestUserAuthDeployAgentTimeout(t *testing.T) {
	f := &fakeBuild{t: t, statuses: []string{"Building"}, versions: []string{""}}
	a := f.start()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := a.DeployAgentV2(ctx, "CA_1", agentSource, nil, nil, "", agentExcludes, io.Discard)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	f.mu.Lock()
	defer f.mu.Unlock()
	require.Greater(t, f.polls, 1, "it polled until ctx ended")
	require.NotContains(t, f.requests, "GET /v0/projects/p_1/agents/CA_1/versions/T1/build-log")
}

// ctx ending before StartAgentBuild is not a build that continues: no
// version was built, though DeployAgent has set the secrets.
func TestUserAuthDeployAgentCanceledBeforeBuild(t *testing.T) {
	f := &fakeBuild{t: t}
	a := f.start()
	ctx, cancel := context.WithCancel(context.Background())
	a.client = cancelAfterUpload(t, f, cancel)

	err := a.DeployAgentV2(ctx, "CA_1", agentSource, nil, nil, "", agentExcludes, io.Discard)
	require.EqualError(t, err, "interrupted before the build started: no version was built or deployed, "+
		"but the secrets this deploy sent are set, and the agent's status may show Building (context canceled)")
	require.False(t, errors.Is(err, context.Canceled), "lk would say the build continues")
	require.NotContains(t, f.requests, "POST /v0/projects/p_1/agents/CA_1:startBuild")
}

// cancelAfterUpload serves f, and calls cancel once f has the upload.
func cancelAfterUpload(t *testing.T, f *fakeBuild, cancel context.CancelFunc) *public.Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.serve(w, r)
		if r.URL.Path == "/upload" {
			cancel()
		}
	}))
	t.Cleanup(srv.Close)
	client, err := public.New(srv.URL, "st_1")
	require.NoError(t, err)
	return client
}

// A member below admin who sends secrets hears that a writer may deploy
// without them. Invalid secrets get a line each, by name. A deploy of an
// Agent Builder agent points at Agent Builder.
func TestUserAuthDeployAgentRefusals(t *testing.T) {
	secrets := []*livekit.AgentSecret{{Name: "LIVEKIT_X", Value: []byte("v"), Kind: livekit.AgentSecretKind_AGENT_SECRET_KIND_ENVIRONMENT}}
	for name, tc := range map[string]struct {
		refusal string
		secrets []*livekit.AgentSecret
		want    string
	}{
		"secrets need an admin": {
			refusal: `403 {"code":7,"message":"this needs ADMIN on the project"}`,
			secrets: secrets,
			want: "signed in with --user-auth, this command needs a project admin. Write access to the project is enough without secrets " +
				"(from --secrets, --secrets-file, --secret-mount or an env file): a project admin can set them with `lk agent update-secrets`. " +
				"A project API key also works: re-run without --user-auth",
		},
		"a reader": {
			refusal: `403 {"code":7,"message":"this needs WRITE on the project"}`,
			want:    "signed in with --user-auth, this command needs write access to the project. A project API key also works: re-run without --user-auth",
		},
		"invalid secrets": {
			refusal: `400 {"code":3,"message":"secrets are invalid","details":[{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"secrets[0].name","description":"is reserved"}]}]}`,
			secrets: secrets,
			want:    "invalid secrets:\n  secret LIVEKIT_X: name is reserved",
		},
		"an Agent Builder agent": {
			refusal: `400 {"code":9,"message":"agent is an Agent Builder agent; deploy one of its versions with AgentBuilderService.DeployAgent"}`,
			want: "agent CA_1 is an Agent Builder agent: deploy it in Agent Builder, in the LiveKit Cloud dashboard " +
				"(agent is an Agent Builder agent; deploy one of its versions with AgentBuilderService.DeployAgent)",
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeBuild{t: t, refuse: map[string]string{"POST /v0/projects/p_1/agents/CA_1:deploy": tc.refusal}}
			a := f.start()
			err := a.DeployAgentV2(context.Background(), "CA_1", agentSource, tc.secrets, nil, "", agentExcludes, io.Discard)
			require.EqualError(t, err, tc.want)
			require.Equal(t, []string{"POST /v0/projects/p_1/agents/CA_1:deploy"}, f.requests)
		})
	}

	// A build that doesn't start is as the Public API words it.
	f := &fakeBuild{t: t, refuse: map[string]string{"POST /v0/projects/p_1/agents/CA_1:startBuild": `429 {"code":8,"message":"too many builds; try again later"}`}}
	a := f.start()
	err := a.DeployAgentV2(context.Background(), "CA_1", agentSource, nil, nil, "", agentExcludes, io.Discard)
	require.EqualError(t, err, "too many builds; try again later")
}

// A poll that fails transiently is made again, a few times.
func TestUserAuthDeployAgentRetriesPolls(t *testing.T) {
	f := &fakeBuild{t: t, statuses: []string{"Building", "Running"}, versions: []string{"", "Available"},
		unavailable: map[string]int{"GET /v0/projects/p_1/agents/CA_1": 1, "GET /v0/projects/p_1/agents/CA_1/versions": pollAttempts - 1}}
	a := f.start()
	require.NoError(t, a.DeployAgentV2(context.Background(), "CA_1", agentSource, nil, nil, "", agentExcludes, io.Discard))

	// So is one that is rate limited.
	f = &fakeBuild{t: t, statuses: []string{"Running"}, versions: []string{"Available"},
		refuse: map[string]string{"GET /v0/projects/p_1/agents/CA_1": `429 {"code":8,"message":"too many requests"}`}}
	a = f.start()
	err := a.DeployAgentV2(context.Background(), "CA_1", agentSource, nil, nil, "", agentExcludes, io.Discard)
	require.ErrorContains(t, err, "can't follow the build of version T1")
	require.Equal(t, pollAttempts, countRequests(f, "GET /v0/projects/p_1/agents/CA_1"))

	// Any other refusal isn't.
	f = &fakeBuild{t: t, statuses: []string{"Running"}, versions: []string{"Available"},
		refuse: map[string]string{"GET /v0/projects/p_1/agents/CA_1/versions": `403 {"code":7,"message":"this needs READ on the project"}`}}
	a = f.start()
	err = a.DeployAgentV2(context.Background(), "CA_1", agentSource, nil, nil, "", agentExcludes, io.Discard)
	require.ErrorContains(t, err, "can't follow the build of version T1")
	require.Equal(t, 1, countRequests(f, "GET /v0/projects/p_1/agents/CA_1/versions"))
}

// countRequests is how many requests f got for request, a method and a URL.
func countRequests(f *fakeBuild, request string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r == request {
			n++
		}
	}
	return n
}

// A poll that fails says that the build continues, and how to follow it.
func TestUserAuthDeployAgentLosesTrack(t *testing.T) {
	f := &fakeBuild{t: t, statuses: []string{"Building"}, versions: []string{""},
		refuse: map[string]string{"GET /v0/projects/p_1/agents/CA_1/versions": `503 {"code":14,"message":"cloud agents is unavailable"}`}}
	a := f.start()
	err := a.DeployAgentV2(context.Background(), "CA_1", agentSource, nil, nil, "", agentExcludes, io.Discard)
	require.EqualError(t, err, "can't follow the build of version T1, which continues on LiveKit Cloud: "+
		"run `lk agent status` to follow it (cloud agents is unavailable)")
}
