//go:build console

package console

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"google.golang.org/protobuf/proto"

	agent "github.com/livekit/protocol/livekit/agent"
)

type TCPServer struct {
	listener net.Listener
	conn     net.Conn
	mu       sync.Mutex
	closed   bool
}

func NewTCPServer(addr string) (*TCPServer, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("console tcp: listen on %s: %w", addr, err)
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

	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
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

// WriteSessionMessage sends a protobuf-framed message: [4 bytes BE length][proto bytes].
// Uses a single write call to avoid split TCP segments.
func WriteSessionMessage(w io.Writer, msg *agent.SessionMessage) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("console tcp: marshal: %w", err)
	}

	// Combine header + payload into a single write
	buf := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(data)))
	copy(buf[4:], data)
	_, err = w.Write(buf)
	return err
}

// ReadSessionMessage reads a protobuf-framed message: [4 bytes BE length][proto bytes].
// Blocks until a complete message is available.
func ReadSessionMessage(r io.Reader) (*agent.SessionMessage, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}

	length := binary.BigEndian.Uint32(header[:])

	if length > 1<<20 { // 1MB sanity limit
		return nil, fmt.Errorf("console tcp: message too large: %d bytes", length)
	}

	data := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, fmt.Errorf("console tcp: partial message: %w", err)
		}
	}

	msg := &agent.SessionMessage{}
	if err := proto.Unmarshal(data, msg); err != nil {
		return nil, fmt.Errorf("console tcp: unmarshal: %w", err)
	}
	return msg, nil
}

