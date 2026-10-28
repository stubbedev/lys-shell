package config

import (
	"slices"
	"strings"
	"testing"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/spawn"
	"github.com/stubbedev/lys-shell/service/logind/logindtest"
)

const displaySession = dbus.ObjectPath("/org/freedesktop/login1/session/_32")

// defaultLockCommands are the power menu's and the dashboard's default
// Lock commands.
func defaultLockCommands() []struct{ name, command string } {
	return []struct{ name, command string }{
		{"power", DefaultsPower().LockCommand},
		{"dashboard", DefaultsDashboard().DropdownLockCommand},
	}
}

// The default Lock commands, run as the shell runs a button's command
// from the systemd user unit (no XDG_SESSION_ID, outside any session),
// lock the user's display session through the real loginctl.
func TestDefaultLockCommandsLockTheDisplaySession(t *testing.T) {
	for _, c := range defaultLockCommands() {
		command := c.command
		t.Run(c.name, func(t *testing.T) {
			fake := logindtest.StartUnit(t, displaySession)
			out, err := spawn.Owned(t.Context(), command).CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v: %s", command, err, out)
			}
			if got := fake.Locked.Load(); !slices.Equal(got, []dbus.ObjectPath{displaySession}) {
				t.Errorf("%s locked %v, want [%s]", command, got, displaySession)
			}
		})
	}
}

// With no display session for "auto" to resolve, the default Lock
// commands fail and lock nothing.
func TestDefaultLockCommandsWithoutASessionFail(t *testing.T) {
	for _, c := range defaultLockCommands() {
		command := c.command
		t.Run(c.name, func(t *testing.T) {
			fake := logindtest.StartUnit(t, "")
			out, err := spawn.Owned(t.Context(), command).CombinedOutput()
			if err == nil || !strings.HasPrefix(string(out), "Failed to issue method call: ") {
				t.Errorf("%s: err %v output %q, want loginctl's failed Lock call", command, err, out)
			}
			if got := fake.Locked.Load(); len(got) != 0 {
				t.Errorf("%s locked %v with no session", command, got)
			}
		})
	}
}
