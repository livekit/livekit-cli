//go:build console

package console

import (
	"errors"
	"io"
	"net"
	"sync"

	"github.com/livekit/livekit-cli/v2/pkg/ipc"

	agent "github.com/livekit/protocol/livekit/agent"
)

type TCPServer struct {
	listener *ipc.Listener
	conn     net.Conn
	mu       sync.Mutex
	closed   bool
}

func NewTCPServer(addr string) (*TCPServer, error) {
	ln, err := ipc.Listen(addr)
	if err != nil {
		return nil, err
	}
	return &TCPServer{listener: ln}, nil
}

func (s *TCPServer) Addr() net.Addr {
	return s.listener.Addr()
}

// Accept waits for a single agent connection; subsequent connections are rejected.
func (s *TCPServer) Accept() (net.Conn, error) {
	conn, err := s.listener.Accept()
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.conn != nil {
		s.mu.Unlock()
		conn.Close()
		return nil, errors.New("console tcp: already connected")
	}
	s.conn = conn
	s.mu.Unlock()

	// Close listener to reject further connections
	s.listener.Close()

	return conn, nil
}

// Conn returns the accepted connection, or nil if none.
func (s *TCPServer) Conn() net.Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn
}

func (s *TCPServer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true
	var errs []error
	if s.conn != nil {
		errs = append(errs, s.conn.Close())
		s.conn = nil
	}
	errs = append(errs, s.listener.Close())
	return errors.Join(errs...)
}

// WriteSessionMessage sends a protobuf-framed AgentSessionMessage.
func WriteSessionMessage(w io.Writer, msg *agent.AgentSessionMessage) error {
	return ipc.WriteProto(w, msg)
}

// ReadSessionMessage reads a protobuf-framed AgentSessionMessage.
func ReadSessionMessage(r io.Reader) (*agent.AgentSessionMessage, error) {
	msg := &agent.AgentSessionMessage{}
	if err := ipc.ReadProto(r, msg); err != nil {
		return nil, err
	}
	return msg, nil
}
