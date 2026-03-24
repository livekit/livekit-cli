package main

import (
	"fmt"
	"net"
	"sync"
	"time"

	agent "github.com/livekit/protocol/livekit/agent"

	"github.com/livekit/livekit-cli/v2/pkg/ipc"
)

// reloadServer manages the dev-mode reload protocol between Go and Python processes.
// Flow:
// 1. Go → old Python: GetRunningJobsRequest → receives GetRunningJobsResponse (capture)
// 2. New Python → Go: GetRunningJobsRequest → Go replies with saved GetRunningJobsResponse (restore)
type reloadServer struct {
	listener  *ipc.Listener
	mu        sync.Mutex
	savedJobs *agent.GetRunningAgentJobsResponse
}

func newReloadServer() (*reloadServer, error) {
	ln, err := ipc.Listen("127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("reload server: %w", err)
	}
	return &reloadServer{listener: ln}, nil
}

func (rs *reloadServer) addr() string {
	return rs.listener.Addr().String()
}

// captureJobs sends GetRunningJobsRequest to the old Python process and stores the response.
func (rs *reloadServer) captureJobs(conn net.Conn) {
	conn.SetDeadline(time.Now().Add(1500 * time.Millisecond))
	defer conn.SetDeadline(time.Time{})

	req := &agent.AgentDevMessage{
		Message: &agent.AgentDevMessage_GetRunningJobsRequest{
			GetRunningJobsRequest: &agent.GetRunningAgentJobsRequest{},
		},
	}
	if err := ipc.WriteProto(conn, req); err != nil {
		fmt.Printf("reload: failed to send capture request: %v\n", err)
		return
	}

	resp := &agent.AgentDevMessage{}
	if err := ipc.ReadProto(conn, resp); err != nil {
		fmt.Printf("reload: failed to read capture response: %v\n", err)
		return
	}

	if jobs := resp.GetGetRunningJobsResponse(); jobs != nil {
		rs.mu.Lock()
		rs.savedJobs = jobs
		rs.mu.Unlock()
		fmt.Printf("reload: captured %d running job(s)\n", len(jobs.Jobs))
	}
}

// serveNewProcess handles a GetRunningJobsRequest from the new Python process,
// replying with the previously captured jobs.
func (rs *reloadServer) serveNewProcess(conn net.Conn) {
	req := &agent.AgentDevMessage{}
	if err := ipc.ReadProto(conn, req); err != nil {
		return
	}
	if req.GetGetRunningJobsRequest() == nil {
		return
	}

	rs.mu.Lock()
	saved := rs.savedJobs
	rs.savedJobs = nil
	rs.mu.Unlock()

	if saved == nil {
		saved = &agent.GetRunningAgentJobsResponse{}
	}

	resp := &agent.AgentDevMessage{
		Message: &agent.AgentDevMessage_GetRunningJobsResponse{
			GetRunningJobsResponse: saved,
		},
	}
	if err := ipc.WriteProto(conn, resp); err != nil {
		fmt.Printf("reload: failed to send restore response: %v\n", err)
	} else if len(saved.Jobs) > 0 {
		fmt.Printf("reload: restored %d job(s) to new process\n", len(saved.Jobs))
	}
}

func (rs *reloadServer) close() error {
	return rs.listener.Close()
}
