package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/logind/logindtest"
)

// The power menu's and the dashboard's default Log out commands end
// the user's display session with no XDG_SESSION_ID to go on.
func TestDefaultLogoutCommandsEndTheDisplaySession(t *testing.T) {
	const display = dbus.ObjectPath("/org/freedesktop/login1/session/_32")
	for _, c := range []struct{ name, command string }{
		{"power", config.DefaultsPower().LogoutCommand},
		{"dashboard", config.DefaultsDashboard().DropdownLogoutCommand},
	} {
		command := c.command
		t.Run(c.name, func(t *testing.T) {
			fake := logindtest.StartUnit(t, display)
			argv := strings.Fields(command)
			if len(argv) == 0 || argv[0] != "lys-shell" {
				t.Fatalf("default logout command %q is not a lys-shell command", command)
			}
			stdout, stderr, code := runCaptured(t, false, argv[1:]...)
			if code != 0 || stdout != "" || stderr != "" {
				t.Fatalf("%s: code %d stdout %q stderr %q", command, code, stdout, stderr)
			}
			if got := fake.Terminated.Load(); !slices.Equal(got, []dbus.ObjectPath{display}) {
				t.Errorf("terminated %v, want [%s]", got, display)
			}
		})
	}
}

// With no session to resolve, logout fails loudly and ends nothing.
func TestLogoutWithoutASessionIsAnError(t *testing.T) {
	fake := logindtest.StartUnit(t, "")
	stdout, stderr, code := runCaptured(t, false, "logout")
	want := "Error: log out: logind GetSession \"auto\": no session auto\n"
	if code != 1 || stdout != "" || stderr != want {
		t.Errorf("logout: code %d stdout %q stderr %q, want code 1 stderr %q", code, stdout, stderr, want)
	}
	if got := fake.Terminated.Load(); len(got) != 0 {
		t.Errorf("terminated %v with no session", got)
	}
}
