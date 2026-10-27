package control

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stubbedev/lys-lib/sockdir"

	"github.com/stubbedev/lys-shell/secrets/keyring"
)

type fakeUnlocker struct {
	mu   sync.Mutex
	want string
	got  []string
	err  error
}

func (f *fakeUnlocker) UnlockLogin(password []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, string(password))
	if f.err != nil {
		return f.err
	}
	if string(password) != f.want {
		return keyring.ErrWrongPassword
	}
	return nil
}

func serve(t *testing.T, u Unlocker) string {
	t.Helper()
	path := filepath.Join(sockdir.Dir(t), "lys-secrets", "control")
	ln, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { Serve(ctx, ln, u); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return path
}

func TestUnlockOverTheSocket(t *testing.T) {
	u := &fakeUnlocker{want: "right"}
	path := serve(t, u)
	if err := Unlock(path, []byte("wrong")); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong password = %v", err)
	}
	if err := Unlock(path, []byte("right")); err != nil {
		t.Errorf("right password = %v", err)
	}
	if strings.Join(u.got, ",") != "wrong,right" {
		t.Errorf("the server saw %v", u.got)
	}
	u.err = errors.New("disk full")
	if err := Unlock(path, []byte("right")); err == nil || errors.Is(err, ErrWrongPassword) {
		t.Errorf("a failing unlock = %v, want a generic failure", err)
	}
}

func TestUnlockWithoutAServer(t *testing.T) {
	path := filepath.Join(sockdir.Dir(t), "absent")
	if err := Unlock(path, []byte("x")); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Unlock = %v, want ErrNotRunning", err)
	}
	if err := Unlock(path, nil); err == nil || errors.Is(err, ErrNotRunning) {
		t.Errorf("empty password = %v, want a refusal before dialing", err)
	}
}

func TestServerRefusesMalformedRequests(t *testing.T) {
	u := &fakeUnlocker{want: "x"}
	path := serve(t, u)
	for _, msg := range [][]byte{
		binary.BigEndian.AppendUint32(nil, 0),
		binary.BigEndian.AppendUint32(nil, maxPassword+1),
		append(binary.BigEndian.AppendUint32(nil, 10), "short"...),
	} {
		conn, err := net.Dial("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = conn.Write(msg)
		_ = conn.(*net.UnixConn).CloseWrite()
		status := make([]byte, 1)
		n, _ := conn.Read(status)
		_ = conn.Close()
		if n != 1 || status[0] != statusFailed {
			t.Errorf("%x: status %v", msg, status[:n])
		}
	}
	if len(u.got) != 0 {
		t.Errorf("a malformed request reached the unlocker: %v", u.got)
	}
}

func TestListenRefusesALiveServerAndReplacesAStaleSocket(t *testing.T) {
	path := serve(t, &fakeUnlocker{})
	if _, err := Listen(path); err == nil {
		t.Error("a second server took a live socket")
	}
	stale := filepath.Join(sockdir.Dir(t), "stale")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: stale, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ln.SetUnlinkOnClose(false)
	_ = ln.Close()
	ln2, err := Listen(stale)
	if err != nil {
		t.Fatalf("a stale socket blocked Listen: %v", err)
	}
	_ = ln2.Close()
}

func TestReadPassword(t *testing.T) {
	for in, want := range map[string]string{"pw\n": "pw", "pw": "pw", "a b\n": "a b"} {
		got, err := ReadPassword(strings.NewReader(in))
		if err != nil || string(got) != want {
			t.Errorf("%q = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "\n", strings.Repeat("x", maxPassword+1)} {
		if _, err := ReadPassword(strings.NewReader(bad)); err == nil {
			t.Errorf("%d bytes accepted", len(bad))
		}
	}
}
