package main

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	agent "github.com/livekit/protocol/livekit/agent"

	"github.com/livekit/livekit-cli/v2/pkg/ipc"
)

// devServer owns the dev-mode IPC channel between Go and the agent (Python)
// processes. It listens on a loopback port that each (re)started agent connects
// back to; every connection is handled by its own devSession. Reload (capturing
// running jobs from the old process and restoring them in the new one) is one
// feature served over this channel:
//
//  1. Go → old agent: GetRunningJobsRequest → GetRunningJobsResponse (capture)
//  2. New agent → Go: GetRunningJobsRequest → Go replies with the saved jobs (restore)
type devServer struct {
	listener  *ipc.Listener
	mu        sync.Mutex
	savedJobs *agent.GetRunningAgentJobsResponse

	// onServerInfo, if set, is invoked when a (re)started agent reports its
	// ServerInfo over the dev channel (agent name + the LiveKit URL it uses).
	onServerInfo func(agentName, url string)
}

func newDevServer() (*devServer, error) {
	ln, err := ipc.Listen("127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("dev server: %w", err)
	}
	return &devServer{listener: ln}, nil
}

func (rs *devServer) addr() string {
	return rs.listener.Addr().String()
}

// newSession wraps a freshly accepted dev-channel connection. The session
// answers inbound GetRunningJobsRequests with the captured jobs (restore) and
// forwards any ServerInfo notifications.
func (rs *devServer) newSession(conn net.Conn) *devSession {
	s := newDevSession(conn)
	s.onServerInfo = rs.onServerInfo
	s.jobsProvider = rs.takeSavedJobs
	return s
}

// takeSavedJobs returns and clears the captured jobs. Jobs are restored to the
// first process that asks; subsequent asks (within the same generation) get none.
func (rs *devServer) takeSavedJobs() *agent.GetRunningAgentJobsResponse {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	saved := rs.savedJobs
	rs.savedJobs = nil
	return saved
}

// captureJobs asks the (old) session for its running jobs and stores them so the
// next process can restore them. Best-effort: failures are logged, not fatal.
func (rs *devServer) captureJobs(s *devSession) {
	resp, err := s.getRunningJobs(1500 * time.Millisecond)
	if err != nil {
		out.Warnf("reload: failed to capture running jobs: %v", err)
		return
	}
	if resp != nil {
		rs.mu.Lock()
		rs.savedJobs = resp
		rs.mu.Unlock()
		out.Statusf("reload: captured %d running job(s)", len(resp.Jobs))
	}
}

func (rs *devServer) close() error {
	return rs.listener.Close()
}

// devMsgKind identifies an outbound call awaiting a reply, keyed by the response
// message type. Type-based routing is unambiguous today because the CLI never has
// two outbound calls of the same response type in flight; add a correlation id to
// AgentDevMessage if that ever changes.
type devMsgKind int

const (
	kindJobsResponse devMsgKind = iota
)

var (
	errDevSessionClosed  = errors.New("dev session closed")
	errDevSessionTimeout = errors.New("dev session request timed out")
)

// devSession owns a single dev-channel IPC connection and multiplexes the
// AgentDevMessage protocol over it. A single read loop (run) dispatches inbound
// notifications (ServerInfo) and peer requests (GetRunningJobsRequest) to their
// handlers, while outbound calls (getRunningJobs) await their matching response.
// This keeps one owner on the connection so the CLI/agent API can grow new
// request/response pairs and callbacks without per-message lifecycle juggling.
type devSession struct {
	conn net.Conn

	// onServerInfo, if set, is invoked when the peer pushes a ServerInfo.
	onServerInfo func(agentName, url string)
	// jobsProvider, if set, supplies the response to an inbound
	// GetRunningJobsRequest (the restore side of a reload).
	jobsProvider func() *agent.GetRunningAgentJobsResponse

	writeMu sync.Mutex // serializes writes to conn

	mu      sync.Mutex
	pending map[devMsgKind]chan *agent.AgentDevMessage
}

func newDevSession(conn net.Conn) *devSession {
	return &devSession{
		conn:    conn,
		pending: make(map[devMsgKind]chan *agent.AgentDevMessage),
	}
}

// run reads and dispatches messages until the connection closes. It is meant to
// run in its own goroutine for the lifetime of the connection.
func (s *devSession) run() {
	defer s.failPending()
	for {
		msg := &agent.AgentDevMessage{}
		if err := ipc.ReadProto(s.conn, msg); err != nil {
			return
		}
		switch msg.Message.(type) {
		case *agent.AgentDevMessage_ServerInfo:
			if s.onServerInfo != nil {
				si := msg.GetServerInfo()
				s.onServerInfo(si.GetAgentName(), si.GetUrl())
			}
		case *agent.AgentDevMessage_GetRunningJobsRequest:
			s.handleJobsRequest()
		case *agent.AgentDevMessage_GetRunningJobsResponse:
			s.deliver(kindJobsResponse, msg)
		}
		// Unknown message types are ignored, leaving room for protocol growth.
	}
}

// handleJobsRequest answers a peer's GetRunningJobsRequest with the jobs supplied
// by jobsProvider (the restore side of a reload).
func (s *devSession) handleJobsRequest() {
	var resp *agent.GetRunningAgentJobsResponse
	if s.jobsProvider != nil {
		resp = s.jobsProvider()
	}
	if resp == nil {
		resp = &agent.GetRunningAgentJobsResponse{}
	}
	err := s.write(&agent.AgentDevMessage{
		Message: &agent.AgentDevMessage_GetRunningJobsResponse{GetRunningJobsResponse: resp},
	})
	if err != nil {
		out.Warnf("reload: failed to send restore response: %v", err)
	} else if len(resp.Jobs) > 0 {
		out.Statusf("reload: restored %d job(s) to new process", len(resp.Jobs))
	}
}

// getRunningJobs asks the peer for its running jobs and waits up to timeout for
// the reply (the capture side of a reload).
func (s *devSession) getRunningJobs(timeout time.Duration) (*agent.GetRunningAgentJobsResponse, error) {
	ch := s.register(kindJobsResponse)
	defer s.unregister(kindJobsResponse)

	err := s.write(&agent.AgentDevMessage{
		Message: &agent.AgentDevMessage_GetRunningJobsRequest{GetRunningJobsRequest: &agent.GetRunningAgentJobsRequest{}},
	})
	if err != nil {
		return nil, err
	}

	select {
	case msg, ok := <-ch:
		if !ok || msg == nil {
			return nil, errDevSessionClosed
		}
		return msg.GetGetRunningJobsResponse(), nil
	case <-time.After(timeout):
		return nil, errDevSessionTimeout
	}
}

func (s *devSession) write(msg *agent.AgentDevMessage) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return ipc.WriteProto(s.conn, msg)
}

func (s *devSession) register(kind devMsgKind) chan *agent.AgentDevMessage {
	ch := make(chan *agent.AgentDevMessage, 1)
	s.mu.Lock()
	s.pending[kind] = ch
	s.mu.Unlock()
	return ch
}

func (s *devSession) unregister(kind devMsgKind) {
	s.mu.Lock()
	delete(s.pending, kind)
	s.mu.Unlock()
}

// deliver routes an inbound response to the waiting caller, if any.
func (s *devSession) deliver(kind devMsgKind, msg *agent.AgentDevMessage) {
	s.mu.Lock()
	ch := s.pending[kind]
	delete(s.pending, kind)
	s.mu.Unlock()
	if ch != nil {
		ch <- msg // buffered (cap 1): never blocks
	}
}

// failPending unblocks every outstanding caller when the connection closes.
func (s *devSession) failPending() {
	s.mu.Lock()
	for kind, ch := range s.pending {
		close(ch)
		delete(s.pending, kind)
	}
	s.mu.Unlock()
}

func (s *devSession) close() error {
	return s.conn.Close()
}
