package ipc

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"

	"google.golang.org/protobuf/proto"
)

const maxMessageSize = 1 << 20 // 1MB

// WriteProto sends a protobuf message with a 4-byte big-endian length prefix.
func WriteProto(w io.Writer, msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("ipc: marshal: %w", err)
	}

	buf := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(data)))
	copy(buf[4:], data)
	_, err = w.Write(buf)
	return err
}

// ReadProto reads a length-prefixed protobuf message into msg.
func ReadProto(r io.Reader, msg proto.Message) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}

	length := binary.BigEndian.Uint32(header[:])
	if length > maxMessageSize {
		return fmt.Errorf("ipc: message too large: %d bytes", length)
	}

	data := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, data); err != nil {
			return fmt.Errorf("ipc: partial message: %w", err)
		}
	}

	if err := proto.Unmarshal(data, msg); err != nil {
		return fmt.Errorf("ipc: unmarshal: %w", err)
	}
	return nil
}

// Listener wraps a net.Listener for protobuf IPC.
type Listener struct {
	listener net.Listener
	mu       sync.Mutex
	closed   bool
}

// Listen creates a new IPC listener on the given address.
func Listen(addr string) (*Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ipc: listen on %s: %w", addr, err)
	}
	return &Listener{listener: ln}, nil
}

// Accept waits for a new connection.
func (l *Listener) Accept() (net.Conn, error) {
	conn, err := l.listener.Accept()
	if err != nil {
		return nil, err
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	return conn, nil
}

// Addr returns the listener's address.
func (l *Listener) Addr() net.Addr {
	return l.listener.Addr()
}

// Close closes the listener.
func (l *Listener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	return l.listener.Close()
}
