// Package ipcsocktest fakes a hung compositor on a unix socket.
package ipcsocktest

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/sockdir"

	"github.com/stubbedev/lys-shell/internal/ipcsock"
)

// Fast is a timeout a test can afford to run into.
const Fast = ipcsock.Timeout(50 * time.Millisecond)

const failFast = 2 * time.Second

// Silent is a socket that accepts every connection and then never reads or
// answers, and the path to it.
func Silent(t *testing.T) string {
	t.Helper()
	path := filepath.Join(sockdir.Dir(t), "hung.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu   sync.Mutex
		held []net.Conn
		done sync.WaitGroup
	)
	done.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, conn)
			mu.Unlock()
		}
	})
	t.Cleanup(func() {
		_ = ln.Close()
		done.Wait()
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range held {
			_ = conn.Close()
		}
	})
	return path
}

// GivesUp runs call, which must fail at the deadline, in time, and with an
// error naming every piece of what it attempted.
func GivesUp(t *testing.T, call func() error, names ...string) {
	t.Helper()
	failed := make(chan error, 1)
	go func() { failed <- call() }()
	select {
	case err := <-failed:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("error = %v, want a deadline exceeded", err)
		}
		for _, name := range names {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error = %q, want it to name %q", err, name)
			}
		}
	case <-time.After(failFast):
		t.Fatalf("still blocked after %v", failFast)
	}
}
