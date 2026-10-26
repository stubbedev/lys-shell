package logind_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbustest"
	"github.com/stubbedev/lys-shell/service/logind"
	"github.com/stubbedev/lys-shell/service/logind/logindtest"
)

const display = dbus.ObjectPath("/org/freedesktop/login1/session/_32")

// Outside any session, with no XDG_SESSION_ID, logout still ends the
// user's display session, and only that one.
func TestLogoutTerminatesTheDisplaySession(t *testing.T) {
	t.Setenv("XDG_SESSION_ID", "")
	fake := logindtest.Start(t, display)
	if err := logind.Logout(t.Context(), fake.Bus.Conn(t)); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if got := fake.Terminated.Load(); !slices.Equal(got, []dbus.ObjectPath{display}) {
		t.Errorf("terminated %v, want [%s]", got, display)
	}
}

func TestSessionResolvesTheDisplaySession(t *testing.T) {
	fake := logindtest.Start(t, display)
	path, err := logind.Session(t.Context(), fake.Bus.Conn(t))
	if err != nil || path != display {
		t.Errorf("Session = %q, %v; want %s", path, err, display)
	}
}

// A user with no session to resolve gets logind's error back, and
// nothing is terminated.
func TestLogoutWithoutASessionFails(t *testing.T) {
	fake := logindtest.Start(t, "")
	err := logind.Logout(t.Context(), fake.Bus.Conn(t))
	if err == nil || !strings.Contains(err.Error(), `log out: logind GetSession "auto": no session auto`) {
		t.Errorf("Logout = %v, want the GetSession failure", err)
	}
	if got := fake.Terminated.Load(); len(got) != 0 {
		t.Errorf("terminated %v with no session", got)
	}
}

func TestLogoutWithoutLogindFails(t *testing.T) {
	bus := dbustest.Start(t)
	err := logind.Logout(t.Context(), bus.Conn(t))
	if err == nil || !strings.Contains(err.Error(), "log out: logind GetSession") {
		t.Errorf("Logout = %v, want the GetSession failure", err)
	}
}
