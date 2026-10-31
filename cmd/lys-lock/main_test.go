package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stubbedev/lys-shell/internal/cli"
	"github.com/stubbedev/lys-shell/internal/version"
)

const help = `Lock the session via lys-shell's lock screen

Usage: lys-lock

Options:
  -h, --help     Print help
  -V, --version  Print version
`

func runCaptured(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, cli.Output{Stdout: &out, Stderr: &errOut})
	return out.String(), errOut.String(), code
}

// noSession points the session at sockets that do not exist, so a run
// that connected would fail instead of printing.
func noSession(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("WAYLAND_DISPLAY", "no-such-wayland")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+dir+"/no-such-bus")
}

// -h, --help and --version print and exit 0 without the session.
func TestHelpAndVersionNeedNoSession(t *testing.T) {
	noSession(t)
	for _, tc := range []struct{ arg, want string }{
		{"-h", help},
		{"--help", help},
		{"--version", "lys-lock " + version.Version + "\n"},
	} {
		stdout, stderr, code := runCaptured(t, tc.arg)
		if code != 0 || stderr != "" || stdout != tc.want {
			t.Errorf("lys-lock %s: code %d stderr %q stdout\n%s\nwant\n%s", tc.arg, code, stderr, stdout, tc.want)
		}
	}
}

// An argument the command does not take is a usage error, exit 2,
// before anything connects.
func TestUnknownArgumentIsAUsageError(t *testing.T) {
	noSession(t)
	stdout, stderr, code := runCaptured(t, "--bogus")
	if code != 2 || stdout != "" || !strings.HasPrefix(stderr, "error: unexpected argument '--bogus' found") {
		t.Errorf("lys-lock --bogus: code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

// A bare run dispatches the lock, and without a session bus it fails
// with exit 1 and the reason.
func TestBareRunLocksThroughTheSession(t *testing.T) {
	noSession(t)
	stdout, stderr, code := runCaptured(t)
	if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "Error: ") {
		t.Errorf("bare lys-lock without a bus: code %d stdout %q stderr %q", code, stdout, stderr)
	}
}
