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
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/livekit/protocol/auth"
	agent "github.com/livekit/protocol/livekit/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/ipc"
)

// fakeTokenSource hands out the tokens in results, in order, as of now.
type fakeTokenSource struct {
	now     func() time.Time
	results []error // nil is a token "tN", valid for an hour
	calls   int
	grants  *auth.ClaimGrants
}

func (f *fakeTokenSource) Fetch(_ context.Context, grants *auth.ClaimGrants, ttl time.Duration) (string, time.Time, error) {
	f.calls++
	f.grants = grants
	if err := f.results[f.calls-1]; err != nil {
		return "", time.Time{}, err
	}
	return "t" + string(rune('0'+f.calls)), f.now().Add(ttl), nil
}

// A refresh comes at half of the token's lifetime; a failed one warns and is
// tried again after workerTokenRetry, without pushing; a new token is pushed.
func TestWorkerTokenKeepFresh(t *testing.T) {
	_, stderr := captureOut(t)
	now := time.Unix(1_000_000, 0)
	clock := func() time.Time { return now }
	src := &fakeTokenSource{now: clock, results: []error{nil, errors.New("api down"), nil}}
	waits := make(chan time.Duration)
	fire := make(chan time.Time)
	wt := newWorkerToken(src, "wss://one.test")
	wt.now = clock
	wt.after = func(d time.Duration) <-chan time.Time { waits <- d; return fire }
	require.NoError(t, wt.fetch(context.Background()))
	assert.Equal(t, "t1", wt.current())

	pushed := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { wt.keepFresh(ctx, func() { pushed <- wt.current() }); close(done) }()

	assert.Equal(t, 30*time.Minute, <-waits, "half of the hour-long token")
	now = now.Add(30 * time.Minute)
	fire <- now

	assert.Equal(t, workerTokenRetry, <-waits, "a failed refresh is tried again soon")
	assert.Contains(t, stderr.String(), "Couldn't refresh the agent's worker token")
	assert.Contains(t, stderr.String(), "api down")
	assert.Equal(t, "t1", wt.current(), "the agent keeps the current token")
	now = now.Add(workerTokenRetry)
	fire <- now

	assert.Equal(t, "t3", <-pushed)
	assert.Equal(t, 30*time.Minute, <-waits, "half of the new token's lifetime")
	cancel()
	<-done
	assert.Equal(t, 3, src.calls)
	assert.Equal(t, &auth.ClaimGrants{
		Video: &auth.VideoGrant{Agent: true},
	}, src.grants, "only what the worker uses itself; jobs get theirs from the server")

	creds := wt.credentials().GetCredentials()
	assert.Equal(t, "wss://one.test", creds.GetUrl())
	assert.Equal(t, "t3", creds.GetToken())
	assert.Equal(t, now.Add(time.Hour).Unix(), creds.GetExpiresAt())
}

// readCredentials reads the next dev message, which must be Credentials.
func readCredentials(t *testing.T, conn net.Conn) *agent.Credentials {
	t.Helper()
	require.NoError(t, conn.SetDeadline(time.Now().Add(2*time.Second)))
	msg := &agent.AgentDevMessage{}
	require.NoError(t, ipc.ReadProto(conn, msg))
	require.NotNil(t, msg.GetCredentials(), "got %v", msg)
	return msg.GetCredentials()
}

func serverInfo(supportsTokenMode bool) *agent.AgentDevMessage {
	return &agent.AgentDevMessage{Message: &agent.AgentDevMessage_ServerInfo{
		ServerInfo: &agent.ServerInfo{AgentName: "a", Url: "wss://one.test", SupportsTokenMode: supportsTokenMode},
	}}
}

func newTestTokenMode(t *testing.T, token string) *devTokenMode {
	t.Helper()
	wt := newWorkerToken(nil, "wss://one.test")
	wt.token, wt.expiresAt = token, time.Unix(2_000_000, 0)
	prev := cliConfig
	t.Cleanup(func() { cliConfig = prev })
	cliConfig = &config.CLIConfig{}
	noKey := missingKeyError(&config.ProjectConfig{Name: "one", URL: "wss://one.test"},
		keyRequest{dir: "agent", files: agentEnvFiles, purpose: "to run the agent with"},
		"your role on project [one] can't create API keys (that takes Admin)", nil)
	return &devTokenMode{token: wt, noKey: noKey, projectType: agentfs.ProjectTypePythonUV}
}

// An agent that supports token mode gets the current token when it connects
// (its environment may hold an older one), then each refreshed one.
func TestDevTokenMode_SendsCredentials(t *testing.T) {
	tm := newTestTokenMode(t, "current")
	rs := &devServer{tokenMode: tm, savedJobs: &agent.GetRunningAgentJobsResponse{}}
	cliConn, agentConn := net.Pipe()
	defer cliConn.Close()
	defer agentConn.Close()
	go rs.newSession(cliConn).run()

	writeMsg(t, agentConn, serverInfo(true))
	assert.Equal(t, "current", readCredentials(t, agentConn).GetToken())

	// The reload handshake still works after the credentials.
	writeMsg(t, agentConn, jobsRequest())
	resp := &agent.AgentDevMessage{}
	require.NoError(t, ipc.ReadProto(agentConn, resp))
	require.NotNil(t, resp.GetGetRunningJobsResponse())

	tm.token.mu.Lock()
	tm.token.token = "refreshed"
	tm.token.mu.Unlock()
	go tm.push()
	creds := readCredentials(t, agentConn)
	assert.Equal(t, "refreshed", creds.GetToken())
	assert.Equal(t, int64(2_000_000), creds.GetExpiresAt())
	assert.NoError(t, tm.exitError())
}

// An agent whose SDK says it doesn't support token mode gets no token, and lk
// asks for an SDK upgrade, then says how to supply a key instead; one that
// exits without saying anything gets the same when it exits.
func TestDevTokenMode_UnsupportedSDK(t *testing.T) {
	_, stderr := captureOut(t)
	tm := newTestTokenMode(t, "current")

	err := tm.exitError()
	require.Error(t, err, "no ServerInfo at all: the SDK may have failed on the missing key")
	assert.True(t, strings.HasPrefix(err.Error(), "upgrade livekit-agents (Python) to "+tokenModeMinPython+" or later"), "starts with the upgrade: %s", err)
	assert.True(t, strings.HasSuffix(err.Error(), "\n\n"+upperFirst(tm.noKey.Error())), "ends with the no-key error: %s", err)
	assert.Contains(t, err.Error(), "can't create API keys (that takes Admin)")
	assert.Contains(t, err.Error(), "exited before it reported")

	rs := &devServer{tokenMode: tm}
	cliConn, agentConn := net.Pipe()
	defer cliConn.Close()
	defer agentConn.Close()
	s := rs.newSession(cliConn)
	go s.run()

	writeMsg(t, agentConn, serverInfo(false))
	writeMsg(t, agentConn, jobsRequest()) // answered only once ServerInfo is handled
	require.NoError(t, agentConn.SetDeadline(time.Now().Add(2*time.Second)))
	resp := &agent.AgentDevMessage{}
	require.NoError(t, ipc.ReadProto(agentConn, resp))
	assert.NotNil(t, resp.GetGetRunningJobsResponse(), "no Credentials for an SDK without token mode")

	assert.Contains(t, stderr.String(), "No LiveKit API key to run the agent with for project [one]: your role")
	assert.Contains(t, stderr.String(), "Upgrade livekit-agents (Python) to "+tokenModeMinPython+" or later")
	assert.Contains(t, stderr.String(), "its SDK release doesn't support that")
	assert.NoError(t, tm.exitError(), "already reported at ServerInfo")
	tm.push() // no agent supports token mode: nothing is sent
}

// In token mode the agent gets the worker token, and an empty key and secret
// so its env file can't set them.
func TestAgentCredentialsEnv_TokenMode(t *testing.T) {
	wt := newWorkerToken(nil, "wss://one.test")
	wt.token = "worker-token"
	env := agentCredentials{url: "wss://one.test", workerToken: wt}.env()
	assert.Equal(t, []string{
		"LIVEKIT_URL=wss://one.test",
		"LIVEKIT_API_KEY=",
		"LIVEKIT_API_SECRET=",
		"LIVEKIT_TOKEN=worker-token",
	}, env)

	// A key keeps its env as it was.
	env = agentCredentials{url: "wss://one.test", apiKey: "APIk", apiSecret: "s"}.env()
	assert.Equal(t, []string{"LIVEKIT_URL=wss://one.test", "LIVEKIT_API_KEY=APIk", "LIVEKIT_API_SECRET=s"}, env)
}
