package pipewire

import (
	"encoding/binary"
	"errors"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// connPair is two connected ends of a socket, closed with the test.
func connPair(t *testing.T) (a, b *conn) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	a, b = newConn(fds[0]), newConn(fds[1])
	t.Cleanup(func() {
		a.close()
		b.close()
	})
	return a, b
}

// Messages arrive whole and in order with their header fields and
// their own fds, which stay open in the receiver after the sender
// closes its copies.
func TestConnCarriesMessagesAndTheirFds(t *testing.T) {
	a, b := connPair(t)
	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pipe[0])
	if _, err := a.send(7, 3, podStruct(podInt(1)).bytes(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.send(9, 255, podStruct(podString("two"), podFd(0)).bytes(), []int{pipe[1]}); err != nil {
		t.Fatal(err)
	}
	_ = unix.Close(pipe[1])

	first, err := b.recv()
	if err != nil {
		t.Fatal(err)
	}
	f := structFields(first.body)
	if first.id != 7 || first.opcode != 3 || first.seq != 0 || len(first.fds) != 0 || f.int() != 1 {
		t.Errorf("first = %+v", first)
	}
	second, err := b.recv()
	if err != nil {
		t.Fatal(err)
	}
	defer second.closeFds()
	f = structFields(second.body)
	if second.id != 9 || second.opcode != 255 || second.seq != 1 || f.string() != "two" || len(second.fds) != 1 {
		t.Fatalf("second = %+v", second)
	}
	fd, err := second.takeFd(f.fd())
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if _, err := unix.Write(fd, []byte("x")); err != nil {
		t.Fatalf("the received fd is not the pipe: %v", err)
	}
	got := make([]byte, 1)
	if n, _ := unix.Read(pipe[0], got); n != 1 || got[0] != 'x' {
		t.Errorf("read %q through the pipe", got[:n])
	}
}

// An fd is taken once; a second take, or an index the message does not
// carry, fails, and what no handler took is closed.
func TestMessageFdsAreTakenOnce(t *testing.T) {
	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	m := message{fds: []int{pipe[0], pipe[1]}}
	fd, err := m.takeFd(0)
	if err != nil || fd != pipe[0] {
		t.Fatalf("take = %d, %v", fd, err)
	}
	defer unix.Close(fd)
	if _, err := m.takeFd(0); err == nil {
		t.Error("an fd was taken twice")
	}
	if _, err := m.takeFd(2); err == nil {
		t.Error("an index past the fds was taken")
	}
	m.closeFds()
	if _, err := unix.Write(pipe[1], []byte("x")); !errors.Is(err, unix.EBADF) {
		t.Errorf("the untaken fd stayed open: %v", err)
	}
}

// A header that claims fds which never arrived, more fds than the
// server receives per message, and a hang-up each fail precisely.
func TestConnRefusesWhatTheProtocolCannotCarry(t *testing.T) {
	a, b := connPair(t)
	header := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(header[4:], 0)
	binary.LittleEndian.PutUint32(header[12:], 2)
	if _, err := unix.Write(a.fd, header); err != nil {
		t.Fatal(err)
	}
	if _, err := b.recv(); err == nil || !strings.Contains(err.Error(), "claims 2 fds, 0 arrived") {
		t.Errorf("a lying fd count: %v", err)
	}

	fds := slices.Repeat([]int{0}, maxFdsPerMessage+1)
	if _, err := a.send(1, 1, podStruct().bytes(), fds); err == nil || !strings.Contains(err.Error(), "carries 29 fds") {
		t.Errorf("29 fds: %v", err)
	}

	c, d := connPair(t)
	c.shutdown()
	if _, err := d.recv(); !errors.Is(err, errServerClosed) {
		t.Errorf("hang-up: %v", err)
	}
}

// The socket is $PIPEWIRE_REMOTE, or pipewire-0, under
// $PIPEWIRE_RUNTIME_DIR, else $XDG_RUNTIME_DIR, then the system
// daemon's directory; an absolute or abstract remote is taken as is.
func TestRemotePathsFollowLibpipewire(t *testing.T) {
	for name, tc := range map[string]struct {
		remote, pwDir, xdgDir string
		want                  []string
	}{
		"defaults":       {"", "", "/run/user/1", []string{"/run/user/1/pipewire-0", "/run/pipewire/pipewire-0"}},
		"pipewire dir":   {"", "/tmp/pw", "/run/user/1", []string{"/tmp/pw/pipewire-0", "/run/pipewire/pipewire-0"}},
		"named remote":   {"pipewire-1", "/tmp/pw", "", []string{"/tmp/pw/pipewire-1", "/run/pipewire/pipewire-1"}},
		"no runtime dir": {"", "", "", []string{"/run/pipewire/pipewire-0"}},
		"absolute":       {"/srv/pw.sock", "/tmp/pw", "", []string{"/srv/pw.sock"}},
		"abstract":       {"@pw", "/tmp/pw", "", []string{"@pw"}},
	} {
		t.Setenv("PIPEWIRE_REMOTE", tc.remote)
		t.Setenv("PIPEWIRE_RUNTIME_DIR", tc.pwDir)
		t.Setenv("XDG_RUNTIME_DIR", tc.xdgDir)
		if got := remotePaths(); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}
