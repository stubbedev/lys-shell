package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/service/lyswm/lyswmtest"
	"github.com/stubbedev/lys-lib/logind"
	"github.com/stubbedev/lys-lib/logind/logindtest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/wmipc"
)

func defaultLogoutCommands() []struct{ name, command string } {
	return []struct{ name, command string }{
		{"power", config.DefaultsPower().LogoutCommand},
		{"dashboard", config.DefaultsDashboard().DropdownLogoutCommand},
	}
}

func runLysShellCommand(t *testing.T, command string) (stdout, stderr string, code int) {
	t.Helper()
	argv := strings.Fields(command)
	if len(argv) == 0 || argv[0] != "lys-shell" {
		t.Fatalf("default logout command %q is not a lys-shell command", command)
	}
	return runCaptured(t, false, argv[1:]...)
}

// The power menu's and the dashboard's default Log out commands end
// the user's display session with no XDG_SESSION_ID to go on.
func TestDefaultLogoutCommandsEndTheDisplaySession(t *testing.T) {
	const display logind.SessionID = "2"
	for _, c := range defaultLogoutCommands() {
		command := c.command
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(wmipc.SocketEnv, "")
			fake := logindtest.StartUnit(t, logindtest.Config{Display: display})
			stdout, stderr, code := runLysShellCommand(t, command)
			if code != 0 || stdout != "" || stderr != "" {
				t.Fatalf("%s: code %d stdout %q stderr %q", command, code, stdout, stderr)
			}
			if got := terminated(fake); !slices.Equal(got, []dbus.ObjectPath{logind.SessionPath(display)}) {
				t.Errorf("terminated %v, want [%s]", got, logind.SessionPath(display))
			}
		})
	}
}

// With no session to resolve, logout fails loudly and ends nothing.
func TestLogoutWithoutASessionIsAnError(t *testing.T) {
	t.Setenv(wmipc.SocketEnv, "")
	fake := logindtest.StartUnit(t, logindtest.Config{})
	stdout, stderr, code := runCaptured(t, false, "logout")
	want := "Error: log out: logind Manager.GetSession(\"auto\") on /org/freedesktop/login1: No session 'auto' known\n"
	if code != 1 || stdout != "" || stderr != want {
		t.Errorf("logout: code %d stdout %q stderr %q, want code 1 stderr %q", code, stdout, stderr, want)
	}
	if got := terminated(fake); len(got) != 0 {
		t.Errorf("terminated %v with no session", got)
	}
}

func TestDefaultLogoutCommandsExitLysWM(t *testing.T) {
	const display logind.SessionID = "2"
	for _, c := range defaultLogoutCommands() {
		command := c.command
		t.Run(c.name, func(t *testing.T) {
			fake := logindtest.StartUnit(t, logindtest.Config{Display: display})
			wm := lyswmtest.Start(t)
			stdout, stderr, code := runLysShellCommand(t, command)
			if code != 0 || stdout != "" || stderr != "" {
				t.Fatalf("%s: code %d stdout %q stderr %q", command, code, stdout, stderr)
			}
			wm.ExpectDispatch(t, "exit")
			if got := terminated(fake); len(got) != 0 {
				t.Errorf("logind terminated %v while lys-wm runs", got)
			}
		})
	}
}

func TestLogoutLysWMRejectionIsAnError(t *testing.T) {
	fake := logindtest.StartUnit(t, logindtest.Config{Display: "2"})
	wm := lyswmtest.Start(t)
	wm.Answer("dispatch", `{"version":1,"error":"exit refused"}`)
	stdout, stderr, code := runCaptured(t, false, "logout")
	want := "Error: log out: lys-wm rejected dispatch: exit refused\n"
	if code != 1 || stdout != "" || stderr != want {
		t.Errorf("logout: code %d stdout %q stderr %q, want code 1 stderr %q", code, stdout, stderr, want)
	}
	if got := terminated(fake); len(got) != 0 {
		t.Errorf("logind terminated %v after lys-wm refused the exit", got)
	}
}

func terminated(fake *logindtest.Logind) []dbus.ObjectPath {
	calls := fake.CallsTo("Session.Terminate")
	paths := make([]dbus.ObjectPath, len(calls))
	for i, c := range calls {
		paths[i] = c.Path
	}
	return paths
}
