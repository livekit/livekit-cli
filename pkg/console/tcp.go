//go:build console

package console

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Message types for the TCP protocol.
const (
	MsgCapture byte = 0x01 // capture audio (CLI → Agent)
	MsgRender  byte = 0x02 // render audio  (Agent → CLI)
	MsgConfig  byte = 0x03 // config (bidirectional)
	MsgEOF     byte = 0x04 // graceful shutdown
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

// WriteMessage sends a framed message: [1 byte type][4 bytes BE length][payload].
func WriteMessage(w io.Writer, msgType byte, payload []byte) error {
	header := [5]byte{msgType}
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		_, err := w.Write(payload)
		return err
	}
	return nil
}

// ReadMessage reads a framed message. Returns (type, payload, error).
// Sets a read deadline to detect stale connections.
func ReadMessage(r io.Reader) (byte, []byte, error) {
	if conn, ok := r.(net.Conn); ok {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	}

	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}

	msgType := header[0]
	length := binary.BigEndian.Uint32(header[1:])

	if length > 1<<20 { // 1MB sanity limit
		return 0, nil, fmt.Errorf("console tcp: message too large: %d bytes", length)
	}

	if length == 0 {
		return msgType, nil, nil
	}

	// Reset deadline for payload read
	if conn, ok := r.(net.Conn); ok {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, fmt.Errorf("console tcp: partial message: %w", err)
	}

	return msgType, payload, nil
}

func SamplesToBytes(samples []int16) []byte {
	buf := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	return buf
}

func BytesToSamples(data []byte) []int16 {
	n := len(data) / 2
	samples := make([]int16, n)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}
	return samples
}
