// Package ipcsock bounds a request on a compositor's unix socket, so a hung
// compositor costs its client an error instead of the client's thread.
package ipcsock

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// Limits is how long a connect may take, and a request once connected.
type Limits struct {
	Connect time.Duration
	Request time.Duration
}

// Standard is what every compositor client runs with.
func Standard() Limits { return Limits{Connect: 2 * time.Second, Request: 5 * time.Second} }

// Dial connects with no deadline on the connection, for a stream.
func (l Limits) Dial(path string) (net.Conn, error) {
	return net.DialTimeout("unix", path, l.Connect)
}

// Arm gives conn one request's time from now, for writing and reading.
func (l Limits) Arm(conn net.Conn) error {
	if err := conn.SetDeadline(time.Now().Add(l.Request)); err != nil {
		return fmt.Errorf("arm the request deadline: %w", err)
	}
	return nil
}

// Open connects and arms the connection for one request.
func (l Limits) Open(path string) (net.Conn, error) {
	conn, err := l.Dial(path)
	if err != nil {
		return nil, err
	}
	if err := l.Arm(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// Disarm turns a connection that finished its handshake into a stream, which
// may stay silent for as long as nothing happens.
func Disarm(conn net.Conn) error {
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fmt.Errorf("disarm the request deadline: %w", err)
	}
	return nil
}

// Session is a connection kept for one request after another, replaced after a
// request that failed. The caller serializes requests; Close may come at any time.
type Session struct {
	path   string
	limits Limits

	mu     sync.Mutex
	conn   net.Conn
	fresh  bool
	closed bool
}

// Connect opens a session, failing if the socket cannot be reached now.
func Connect(path string, limits Limits) (*Session, error) {
	conn, err := limits.Dial(path)
	if err != nil {
		return nil, err
	}
	return &Session{path: path, limits: limits, conn: conn, fresh: true}, nil
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
		conn, err := s.limits.Dial(s.path)
		if err != nil {
			return nil, false, err
		}
		s.conn, s.fresh = conn, true
	}
	if err := s.limits.Arm(s.conn); err != nil {
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
