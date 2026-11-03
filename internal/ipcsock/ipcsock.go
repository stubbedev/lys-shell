// Package ipcsock bounds a request on a local unix socket, so a hung peer
// costs its client an error instead of the client's thread.
package ipcsock

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// Timeout is how long one request may take to write and read.
type Timeout time.Duration

// Standard is what every client runs with.
const Standard = Timeout(5 * time.Second)

// Dial connects without waiting: a unix connect on Go's non-blocking socket
// either succeeds at once or fails at once with EAGAIN on a full backlog.
func Dial(path string) (net.Conn, error) { return net.Dial("unix", path) }

func (t Timeout) deadline() time.Time { return time.Now().Add(time.Duration(t)) }

// Arm gives conn one request's time from now, for writing and reading.
func (t Timeout) Arm(conn net.Conn) error {
	if err := conn.SetDeadline(t.deadline()); err != nil {
		return fmt.Errorf("arm the request deadline: %w", err)
	}
	return nil
}

// ArmWrite is Arm for a connection whose reads have a life of their own.
func (t Timeout) ArmWrite(conn net.Conn) error {
	if err := conn.SetWriteDeadline(t.deadline()); err != nil {
		return fmt.Errorf("arm the write deadline: %w", err)
	}
	return nil
}

// ArmRead is ArmWrite's other half.
func (t Timeout) ArmRead(conn net.Conn) error {
	if err := conn.SetReadDeadline(t.deadline()); err != nil {
		return fmt.Errorf("arm the read deadline: %w", err)
	}
	return nil
}

// Open connects and arms the connection for one request.
func (t Timeout) Open(path string) (net.Conn, error) {
	conn, err := Dial(path)
	if err != nil {
		return nil, err
	}
	if err := t.Arm(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// Stream connects, writes request under the deadline, and hands back a
// connection that may stay silent for as long as nothing happens.
func (t Timeout) Stream(path string, request []byte) (net.Conn, error) {
	conn, err := t.Open(path)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(request); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("write the subscription: %w", err)
	}
	if err := Disarm(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// Disarm turns a connection that finished its handshake into a stream.
func Disarm(conn net.Conn) error {
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fmt.Errorf("disarm the request deadline: %w", err)
	}
	return nil
}

// Session is a connection kept for one request after another, replaced after a
// request that failed. The caller serializes requests; Close may come at any time.
type Session struct {
	path    string
	timeout Timeout

	mu     sync.Mutex
	conn   net.Conn
	fresh  bool
	closed bool
}

// Connect opens a session, failing if the socket cannot be reached now.
func Connect(path string, timeout Timeout) (*Session, error) {
	conn, err := Dial(path)
	if err != nil {
		return nil, err
	}
	return &Session{path: path, timeout: timeout, conn: conn, fresh: true}, nil
}

// Acquire is the connection armed for one request, and whether it is new since
// the last Acquire, in which case nothing buffered from the old one applies.
func (s *Session) Acquire() (net.Conn, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, false, net.ErrClosed
	}
	if s.conn == nil {
		conn, err := Dial(s.path)
		if err != nil {
			return nil, false, err
		}
		s.conn, s.fresh = conn, true
	}
	if err := s.timeout.Arm(s.conn); err != nil {
		_ = s.conn.Close()
		s.conn = nil
		return nil, false, err
	}
	fresh := s.fresh
	s.fresh = false
	return s.conn, fresh, nil
}

// Drop retires the connection of a request that failed.
func (s *Session) Drop(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = conn.Close()
	if s.conn == conn {
		s.conn = nil
	}
}

// Close ends the session and any request on it.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.conn == nil {
		return nil
	}
	conn := s.conn
	s.conn = nil
	return conn.Close()
}
