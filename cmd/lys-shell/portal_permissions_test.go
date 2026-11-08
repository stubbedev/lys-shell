package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stubbedev/lys-desktop/portal/permissions"
	"github.com/stubbedev/lys-desktop/portal/permissions/permstoretest"
	"github.com/stubbedev/lys-lib/dbustest"
)

// permissionsCLI is the session bus with xdg-permission-store holding
// two apps' device grants, a screen-share token and a USB device, and a
// shortcut store holding one app's shortcuts.
func permissionsCLI(t *testing.T) {
	t.Helper()
	bus := dbustest.Start(t)
	bus.UseAsSessionBus(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	permstoretest.Start(t, bus, t.TempDir())
	conn := bus.Conn(t)
	permstoretest.Set(t, conn, permissions.Devices, "camera", map[string][]string{"org.app": {"yes"}, "com.example.Chat": {"no"}}, byte(0))
	permstoretest.Set(t, conn, permissions.Devices, "microphone", map[string][]string{"com.example.Chat": {"yes"}}, byte(0))
	permstoretest.Set(t, conn, permissions.ScreenCast, "6f1f0c5e-2b0e-4a5e-9a51-7b1e7a1d2c3f", map[string][]string{"com.obsproject.Studio": {"yes"}}, byte(0))
	permstoretest.Set(t, conn, permissions.USB, "usb:1d6b/0003/", map[string][]string{"org.app": {"yes"}}, byte(0))
	if err := permissions.UpdateShortcuts(permissions.ShortcutsPath(), func(all map[string][]permissions.Shortcut) {
		all["org.app"] = []permissions.Shortcut{{ID: "toggle-recording", Description: "Start or stop recording"}}
	}); err != nil {
		t.Fatal(err)
	}
}

// `portal permissions list` prints every app's grants, pinned byte for
// byte by its golden.
func TestPortalPermissionsListMatchesTheGolden(t *testing.T) {
	permissionsCLI(t)
	stdout, stderr, code := runCaptured(t, false, "portal", "permissions", "list")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "portal-permissions-list.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if stdout != string(want) {
		t.Errorf("lys-shell portal permissions list\n--- got\n%s\n--- want\n%s", stdout, want)
	}
}

// Revoking one entry says what it took and the list no longer shows
// it; revoking it again is an error, exit 1, and an empty store lists
// nothing.
func TestPortalPermissionsRevoke(t *testing.T) {
	permissionsCLI(t)
	if stdout, stderr, code := runCaptured(t, false, "portal", "permissions", "revoke", "org.app", "devices", "camera"); code != 0 || stdout != "Revoked 1 grant of org.app in devices\n" {
		t.Fatalf("revoke = %d %q %q", code, stdout, stderr)
	}
	stdout, stderr, code := runCaptured(t, false, "portal", "permissions", "revoke", "org.app", "devices", "camera")
	if code != 1 || stdout != "" || stderr != "Error: revoke devices camera for org.app: not granted\n" {
		t.Errorf("revoke again = %d %q %q", code, stdout, stderr)
	}
	for _, revoke := range [][]string{
		{"org.app", "usb"}, {"org.app", "global-shortcuts"}, {"com.example.Chat", "devices"}, {"com.obsproject.Studio", "screencast"},
	} {
		if _, stderr, code := runCaptured(t, false, append([]string{"portal", "permissions", "revoke"}, revoke...)...); code != 0 {
			t.Fatalf("revoke %v = %d %q", revoke, code, stderr)
		}
	}
	if stdout, _, code := runCaptured(t, false, "portal", "permissions", "list"); code != 0 || stdout != "No app was granted anything\n" {
		t.Errorf("empty list = %d %q", code, stdout)
	}
	if _, stderr, code := runCaptured(t, false, "portal", "permissions", "revoke", "org.app", "camera"); code != 2 || stderr == "" {
		t.Errorf("an unknown table = %d %q, want a parse error", code, stderr)
	}
}
