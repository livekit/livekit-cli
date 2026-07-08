// Copyright 2025 LiveKit, Inc.
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
	"net"
	"testing"
	"time"

	agent "github.com/livekit/protocol/livekit/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/ipc"
)

// writeMsg is a test helper that frames and sends a dev message.
func writeMsg(t *testing.T, conn net.Conn, m *agent.AgentDevMessage) {
	t.Helper()
	require.NoError(t, ipc.WriteProto(conn, m))
}

func jobsRequest() *agent.AgentDevMessage {
	return &agent.AgentDevMessage{
		Message: &agent.AgentDevMessage_GetRunningJobsRequest{
			GetRunningJobsRequest: &agent.GetRunningAgentJobsRequest{},
		},
	}
}

// TestDevSession_Restore_ServerInfo verifies the dev-channel handshake: a leading
// ServerInfo is surfaced via onServerInfo, and the following GetRunningJobsRequest
// is answered with the saved jobs.
func TestDevSession_Restore_ServerInfo(t *testing.T) {
	rs := &devServer{
		savedJobs: &agent.GetRunningAgentJobsResponse{
			Jobs: []*agent.RunningAgentJobInfo{{WorkerId: "w1"}},
		},
	}
	var gotName, gotURL string
	rs.onServerInfo = func(agentName, url string) { gotName, gotURL = agentName, url }

	cliConn, agentConn := net.Pipe()
	defer cliConn.Close()
	defer agentConn.Close()

	s := rs.newSession(cliConn)
	go s.run()

	// Agent sends ServerInfo, then the jobs request.
	writeMsg(t, agentConn, &agent.AgentDevMessage{
		Message: &agent.AgentDevMessage_ServerInfo{
			ServerInfo: &agent.ServerInfo{AgentName: "inbound", Url: "wss://dztest2.livekit.cloud"},
		},
	})
	writeMsg(t, agentConn, jobsRequest())

	// Should receive the saved jobs in reply.
	agentConn.SetDeadline(time.Now().Add(2 * time.Second))
	resp := &agent.AgentDevMessage{}
	require.NoError(t, ipc.ReadProto(agentConn, resp))

	jobs := resp.GetGetRunningJobsResponse()
	require.NotNil(t, jobs)
	require.Len(t, jobs.Jobs, 1)
	assert.Equal(t, "w1", jobs.Jobs[0].WorkerId)
	assert.Equal(t, "inbound", gotName)
	assert.Equal(t, "wss://dztest2.livekit.cloud", gotURL)
}

// TestDevSession_Restore_NoServerInfo verifies backward compatibility: a client
// that sends only the jobs request (no ServerInfo) is still served, and
// onServerInfo is never invoked.
func TestDevSession_Restore_NoServerInfo(t *testing.T) {
	rs := &devServer{savedJobs: &agent.GetRunningAgentJobsResponse{}}
	called := false
	rs.onServerInfo = func(string, string) { called = true }

	cliConn, agentConn := net.Pipe()
	defer cliConn.Close()
	defer agentConn.Close()

	s := rs.newSession(cliConn)
	go s.run()

	writeMsg(t, agentConn, jobsRequest())

	agentConn.SetDeadline(time.Now().Add(2 * time.Second))
	resp := &agent.AgentDevMessage{}
	require.NoError(t, ipc.ReadProto(agentConn, resp))
	assert.NotNil(t, resp.GetGetRunningJobsResponse())
	assert.False(t, called, "onServerInfo must not fire without a ServerInfo message")
}

// TestDevSession_TakeSavedJobs_SingleUse verifies the saved jobs are restored to
// the first asker only, then cleared.
func TestDevSession_TakeSavedJobs_SingleUse(t *testing.T) {
	rs := &devServer{
		savedJobs: &agent.GetRunningAgentJobsResponse{
			Jobs: []*agent.RunningAgentJobInfo{{WorkerId: "w1"}},
		},
	}

	cliConn, agentConn := net.Pipe()
	defer cliConn.Close()
	defer agentConn.Close()

	s := rs.newSession(cliConn)
	go s.run()

	agentConn.SetDeadline(time.Now().Add(2 * time.Second))

	// First request gets the saved job.
	writeMsg(t, agentConn, jobsRequest())
	resp := &agent.AgentDevMessage{}
	require.NoError(t, ipc.ReadProto(agentConn, resp))
	require.Len(t, resp.GetGetRunningJobsResponse().GetJobs(), 1)

	// Second request gets an empty response — the read loop keeps serving.
	writeMsg(t, agentConn, jobsRequest())
	resp = &agent.AgentDevMessage{}
	require.NoError(t, ipc.ReadProto(agentConn, resp))
	require.NotNil(t, resp.GetGetRunningJobsResponse())
	assert.Empty(t, resp.GetGetRunningJobsResponse().GetJobs())
}

// TestDevSession_CaptureJobs verifies the outbound capture call: the session
// requests jobs from the peer and returns the peer's response.
func TestDevSession_CaptureJobs(t *testing.T) {
	cliConn, agentConn := net.Pipe()
	defer cliConn.Close()
	defer agentConn.Close()

	s := newDevSession(cliConn)
	go s.run()

	// Stand-in agent: answer the inbound jobs request with two jobs.
	go func() {
		req := &agent.AgentDevMessage{}
		if ipc.ReadProto(agentConn, req) != nil {
			return
		}
		_ = ipc.WriteProto(agentConn, &agent.AgentDevMessage{
			Message: &agent.AgentDevMessage_GetRunningJobsResponse{
				GetRunningJobsResponse: &agent.GetRunningAgentJobsResponse{
					Jobs: []*agent.RunningAgentJobInfo{{WorkerId: "a"}, {WorkerId: "b"}},
				},
			},
		})
	}()

	resp, err := s.getRunningJobs(2 * time.Second)
	require.NoError(t, err)
	require.Len(t, resp.GetJobs(), 2)
}

// TestDevSession_CaptureJobs_Timeout verifies an unanswered capture call returns a
// timeout rather than blocking forever.
func TestDevSession_CaptureJobs_Timeout(t *testing.T) {
	cliConn, agentConn := net.Pipe()
	defer cliConn.Close()
	defer agentConn.Close()

	// Drain the request but never reply.
	go func() {
		req := &agent.AgentDevMessage{}
		_ = ipc.ReadProto(agentConn, req)
	}()

	s := newDevSession(cliConn)
	go s.run()

	_, err := s.getRunningJobs(100 * time.Millisecond)
	assert.ErrorIs(t, err, errDevSessionTimeout)
}

// TestDevSession_CaptureJobs_Closed verifies a pending capture call is unblocked
// when the connection closes.
func TestDevSession_CaptureJobs_Closed(t *testing.T) {
	cliConn, agentConn := net.Pipe()
	defer agentConn.Close()

	s := newDevSession(cliConn)
	go s.run()

	go func() {
		req := &agent.AgentDevMessage{}
		_ = ipc.ReadProto(agentConn, req)
		cliConn.Close() // drop the connection instead of replying
	}()

	_, err := s.getRunningJobs(2 * time.Second)
	assert.ErrorIs(t, err, errDevSessionClosed)
}
