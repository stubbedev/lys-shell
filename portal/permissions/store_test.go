package permissions_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/portal/permissions"
	"github.com/stubbedev/lys-shell/portal/permissions/permstoretest"
)

// storeRig is xdg-permission-store on a private bus holding camera and
// microphone grants of two apps and a screen-share token, and a
// shortcut store holding one app's shortcuts.
func storeRig(t *testing.T) (permissions.Store, *dbus.Conn) {
	t.Helper()
	bus := dbustest.Start(t)
	permstoretest.Start(t, bus, t.TempDir())
	conn := bus.Conn(t)
	permstoretest.Set(t, conn, permissions.Devices, "camera", map[string][]string{"org.app": {"yes"}, "org.other": {"yes"}}, byte(0))
	permstoretest.Set(t, conn, permissions.Devices, "microphone", map[string][]string{"org.app": {"no"}, "org.other": {"yes"}}, byte(0))
	permstoretest.Set(t, conn, permissions.ScreenCast, "token-1", map[string][]string{"org.app": {"yes"}}, "restore data")
	shortcuts := filepath.Join(t.TempDir(), "portal-shortcuts.json")
	if err := permissions.UpdateShortcuts(shortcuts, func(all map[string][]permissions.Shortcut) {
		all["org.app"] = []permissions.Shortcut{{ID: "toggle", Description: "Toggle"}, {ID: "mute", Description: "Mute"}}
	}); err != nil {
		t.Fatal(err)
	}
	return permissions.Store{Conn: conn, ShortcutsPath: shortcuts}, conn
}

// List merges the permission store's tables and the shortcut store into
// one per-app model, apps by id and grants by table then entry.
func TestListMergesTheStores(t *testing.T) {
	s, _ := storeRig(t)
	apps, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []permissions.App{
		{ID: "org.app", Grants: []permissions.Grant{
			{Table: permissions.Devices, ID: "camera", Value: "yes"},
			{Table: permissions.Devices, ID: "microphone", Value: "no"},
			{Table: permissions.Shortcuts, ID: "mute", Value: "Mute"},
			{Table: permissions.Shortcuts, ID: "toggle", Value: "Toggle"},
			{Table: permissions.ScreenCast, ID: "token-1", Value: "yes"},
		}},
		{ID: "org.other", Grants: []permissions.Grant{
			{Table: permissions.Devices, ID: "camera", Value: "yes"},
			{Table: permissions.Devices, ID: "microphone", Value: "yes"},
		}},
	}
	if !reflect.DeepEqual(apps, want) {
		t.Errorf("List =\n%+v\nwant\n%+v", apps, want)
	}
}

type snapshot struct {
	perms map[string][]string
	data  dbus.Variant
}

func lookup(t *testing.T, conn *dbus.Conn, table permissions.Table, id string) (snapshot, bool) {
	t.Helper()
	perms, data, ok := permstoretest.Lookup(t, conn, table, id)
	return snapshot{perms, data}, ok
}

// Revoking camera for org.app removes only its camera permission:
// org.other's camera grant and every microphone grant are unchanged.
// A token the app alone held is deleted with its data. Revoking what
// was not granted fails with ErrNotGranted and changes nothing.
func TestRevokeTakesOnlyTheAppsEntry(t *testing.T) {
	s, conn := storeRig(t)
	ctx := context.Background()
	micBefore, _ := lookup(t, conn, permissions.Devices, "microphone")
	if n, err := s.Revoke(ctx, "org.app", permissions.Devices, "camera"); err != nil || n != 1 {
		t.Fatalf("revoke camera = %d, %v", n, err)
	}
	camera, ok := lookup(t, conn, permissions.Devices, "camera")
	if want := map[string][]string{"org.other": {"yes"}}; !ok || !reflect.DeepEqual(camera.perms, want) {
		t.Errorf("camera after = %v, want only org.other's", camera.perms)
	}
	if micAfter, _ := lookup(t, conn, permissions.Devices, "microphone"); !reflect.DeepEqual(micAfter, micBefore) {
		t.Errorf("microphone changed: %+v -> %+v", micBefore, micAfter)
	}
	if n, err := s.Revoke(ctx, "org.app", permissions.Devices, "camera"); !errors.Is(err, permissions.ErrNotGranted) || n != 0 {
		t.Errorf("revoking camera again = %d, %v; want ErrNotGranted", n, err)
	}
	if n, err := s.Revoke(ctx, "org.app", permissions.ScreenCast, ""); err != nil || n != 1 {
		t.Fatalf("revoke screencast = %d, %v", n, err)
	}
	if _, ok := lookup(t, conn, permissions.ScreenCast, "token-1"); ok {
		t.Error("the app's only token outlived the revoke")
	}
	if n, err := s.Revoke(ctx, "org.nobody", permissions.Location, ""); !errors.Is(err, permissions.ErrNotGranted) || n != 0 {
		t.Errorf("revoking an empty table = %d, %v", n, err)
	}
}

// Revoking a table takes every entry of it the app holds; revoking
// shortcuts edits lys's store, one or all.
func TestRevokeATableAndShortcuts(t *testing.T) {
	s, conn := storeRig(t)
	ctx := context.Background()
	if n, err := s.Revoke(ctx, "org.app", permissions.Devices, ""); err != nil || n != 2 {
		t.Fatalf("revoke devices = %d, %v", n, err)
	}
	for _, id := range []string{"camera", "microphone"} {
		if snap, _ := lookup(t, conn, permissions.Devices, id); !reflect.DeepEqual(snap.perms, map[string][]string{"org.other": {"yes"}}) {
			t.Errorf("%s after = %v", id, snap.perms)
		}
	}
	if n, err := s.Revoke(ctx, "org.app", permissions.Shortcuts, "mute"); err != nil || n != 1 {
		t.Fatalf("revoke mute = %d, %v", n, err)
	}
	all, _ := permissions.ReadShortcuts(s.ShortcutsPath)
	if !reflect.DeepEqual(all["org.app"], []permissions.Shortcut{{ID: "toggle", Description: "Toggle"}}) {
		t.Errorf("shortcuts after one = %v", all)
	}
	if n, err := s.Revoke(ctx, "org.app", permissions.Shortcuts, ""); err != nil || n != 1 {
		t.Fatalf("revoke shortcuts = %d, %v", n, err)
	}
	if all, _ := permissions.ReadShortcuts(s.ShortcutsPath); len(all) != 0 {
		t.Errorf("shortcuts after all = %v", all)
	}
	if _, err := s.Revoke(ctx, "org.app", permissions.Shortcuts, ""); !errors.Is(err, permissions.ErrNotGranted) {
		t.Errorf("revoking no shortcuts: %v", err)
	}
}

// Watch follows the store's edits until stopped.
func TestWatchFollowsEdits(t *testing.T) {
	s, conn := storeRig(t)
	changed := make(chan struct{}, 8)
	stop, err := s.Watch(func() { changed <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	permstoretest.Set(t, conn, permissions.Location, "location", map[string][]string{"org.app": {"EXACT", "0"}}, byte(0))
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("no change announced")
	}
	stop()
	permstoretest.Set(t, conn, permissions.Location, "location", map[string][]string{"org.app": {"NONE", "0"}}, byte(0))
	if _, err := s.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
		t.Error("a change announced after stop")
	default:
	}
}

func TestParseTable(t *testing.T) {
	for _, table := range permissions.Tables {
		if got, err := permissions.ParseTable(string(table)); err != nil || got != table {
			t.Errorf("ParseTable(%q) = %q, %v", table, got, err)
		}
	}
	if !slices.Contains(permissions.Tables, permissions.Shortcuts) {
		t.Error("the shortcut store is not a table")
	}
	if _, err := permissions.ParseTable("camera"); err == nil {
		t.Error("an entry parsed as a table")
	}
}
