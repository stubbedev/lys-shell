package ipcsock_test

import (
	"errors"
	"net"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/sockdir"

	"github.com/stubbedev/lys-shell/internal/ipcsock"
	"github.com/stubbedev/lys-shell/internal/ipcsock/ipcsocktest"
)

func fullBacklog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(sockdir.Dir(t), "full.sock")
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Close(fd) })
	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: path}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Listen(fd, 0); err != nil {
		t.Fatal(err)
	}
	queued, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queued.Close() })
	return path
}

func TestConnectingToAFullBacklogFailsAtOnce(t *testing.T) {
	path := fullBacklog(t)
	start := time.Now()
	conn, err := ipcsock.Dial(path)
	if err == nil {
		_ = conn.Close()
		t.Fatal("connected to a socket with a full backlog")
	}
	if !errors.Is(err, syscall.EAGAIN) {
		t.Errorf("error = %v, want EAGAIN", err)
	}
	if took := time.Since(start); took >= time.Duration(ipcsock.Standard) {
		t.Errorf("gave up after %v, the whole request deadline", took)
	}
}

func TestARequestReadsUntilItsDeadlineOnly(t *testing.T) {
	conn, err := ipcsocktest.Fast.Open(ipcsocktest.Silent(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ipcsocktest.GivesUp(t, func() error {
		_, err := conn.Read(make([]byte, 1))
		return err
	})
}

func TestADisarmedConnectionWaitsPastTheDeadline(t *testing.T) {
	path := ipcsocktest.Silent(t)
	conn, err := ipcsocktest.Fast.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := ipcsock.Disarm(conn); err != nil {
		t.Fatal(err)
	}
	read := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 1))
		read <- err
	}()
	select {
	case err := <-read:
		t.Fatalf("a disarmed read ended: %v", err)
	case <-time.After(3 * time.Duration(ipcsocktest.Fast)):
	}
}

func TestASessionReconnectsAfterADrop(t *testing.T) {
	session, err := ipcsock.Connect(ipcsocktest.Silent(t), ipcsocktest.Fast)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	first, fresh, err := session.Acquire()
	if err != nil || !fresh {
		t.Fatalf("first Acquire: fresh %v, %v", fresh, err)
	}
	again, fresh, err := session.Acquire()
	if err != nil || fresh || again != first {
		t.Fatalf("second Acquire: same %v, fresh %v, %v", again == first, fresh, err)
	}
	session.Drop(first)
	next, fresh, err := session.Acquire()
	if err != nil || !fresh || next == first {
		t.Fatalf("Acquire after a drop: same %v, fresh %v, %v", next == first, fresh, err)
	}
}

func TestAClosedSessionStaysClosed(t *testing.T) {
	session, err := ipcsock.Connect(ipcsocktest.Silent(t), ipcsocktest.Fast)
	if err != nil {
		t.Fatal(err)
	}
	conn, _, err := session.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Close left a request reading: %v", err)
	}
	if _, _, err := session.Acquire(); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Acquire after Close = %v", err)
	}
}

func TestAStreamGivesUpOnAPeerThatNeverReads(t *testing.T) {
	path := ipcsocktest.Silent(t)
	ipcsocktest.GivesUp(t, func() error {
		conn, err := ipcsocktest.Fast.Stream(path, make([]byte, 16<<20))
		if err == nil {
			_ = conn.Close()
		}
		return err
	}, "subscription")
}

func TestAStreamMayStaySilentPastTheDeadline(t *testing.T) {
	conn, err := ipcsocktest.Fast.Stream(ipcsocktest.Silent(t), []byte("subscribe\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	read := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 1))
		read <- err
	}()
	select {
	case err := <-read:
		t.Fatalf("a stream's read ended: %v", err)
	case <-time.After(3 * time.Duration(ipcsocktest.Fast)):
	}
}
