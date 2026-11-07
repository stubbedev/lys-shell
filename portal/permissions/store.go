package permissions

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// The frontend's permission store on the session bus.
const (
	StoreName  = "org.freedesktop.impl.portal.PermissionStore"
	StorePath  = dbus.ObjectPath("/org/freedesktop/impl/portal/PermissionStore")
	StoreIface = StoreName
)

// Table is where a kind of grant is kept: a permission-store table the
// frontend writes, or lys's shortcut store.
type Table string

// The tables the frontend keeps grants in (xdg-desktop-portal's
// *_PERMISSION_TABLE), and lys's shortcut store.
const (
	Background    Table = "background"
	Devices       Table = "devices"
	GameMode      Table = "gamemode"
	Inhibit       Table = "inhibit"
	InputCapture  Table = "input-capture"
	Location      Table = "location"
	Notifications Table = "notifications"
	Realtime      Table = "realtime"
	RemoteDesktop Table = "remote-desktop"
	ScreenCast    Table = "screencast"
	Screenshot    Table = "screenshot"
	USB           Table = "usb"
	Wallpaper     Table = "wallpaper"
	Shortcuts     Table = "global-shortcuts"
)

// Tables is every table, in the order grants are listed.
var Tables = []Table{
	Background, Devices, GameMode, Shortcuts, Inhibit, InputCapture, Location,
	Notifications, Realtime, RemoteDesktop, ScreenCast, Screenshot, USB, Wallpaper,
}

// ParseTable is the table named s.
func ParseTable(s string) (Table, error) {
	if t := Table(s); slices.Contains(Tables, t) {
		return t, nil
	}
	names := make([]string, len(Tables))
	for i, t := range Tables {
		names[i] = string(t)
	}
	return "", fmt.Errorf("unknown permission table %q; the tables are %s", s, strings.Join(names, ", "))
}

// Grant is one thing an app was granted: an entry of a table (a device
// such as camera, a screen-share restore token, a USB device, a bound
// shortcut) and its value (the stored permissions, or the shortcut's
// description).
type Grant struct {
	Table Table
	ID    string
	Value string
}

// App is an app and everything it was granted, by table then entry.
type App struct {
	ID     string
	Grants []Grant
}

// ErrNotGranted is a revoke of something the app was not granted.
var ErrNotGranted = errors.New("not granted")

// Store reads and revokes grants: the permission store on conn and the
// shortcut store at ShortcutsPath ("" for none).
type Store struct {
	Conn          *dbus.Conn
	ShortcutsPath string
}

func (s Store) obj() dbus.BusObject { return s.Conn.Object(StoreName, StorePath) }

// entry is one permission-store entry: the permissions it gives each app.
type entry struct {
	table       Table
	id          string
	permissions map[string][]string
}

// entries is every entry of the store's tables. The lookups go out
// together and are collected after, so the store answers them in one
// pass rather than a round trip each.
func (s Store) entries(ctx context.Context, tables []Table) ([]entry, error) {
	var out []entry
	var calls []*dbus.Call
	for _, t := range tables {
		var ids []string
		if err := s.obj().CallWithContext(ctx, StoreIface+".List", 0, string(t)).Store(&ids); err != nil {
			return nil, fmt.Errorf("list the permission store's %s table: %w", t, err)
		}
		slices.Sort(ids)
		for _, id := range ids {
			out = append(out, entry{table: t, id: id})
			calls = append(calls, s.obj().GoWithContext(ctx, StoreIface+".Lookup", 0, make(chan *dbus.Call, 1), string(t), id))
		}
	}
	for i, c := range calls {
		<-c.Done
		var data dbus.Variant
		if err := c.Store(&out[i].permissions, &data); err != nil {
			return nil, fmt.Errorf("look up %s in the permission store's %s table: %w", out[i].id, out[i].table, err)
		}
	}
	return out, nil
}

// List is every app with a grant, by app id.
func (s Store) List(ctx context.Context) ([]App, error) {
	byApp := map[string][]Grant{}
	stored := slices.DeleteFunc(slices.Clone(Tables), func(t Table) bool { return t == Shortcuts })
	entries, err := s.entries(ctx, stored)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		for app, perms := range e.permissions {
			byApp[app] = append(byApp[app], Grant{Table: e.table, ID: e.id, Value: strings.Join(perms, ",")})
		}
	}
	shortcuts, err := s.shortcuts()
	if err != nil {
		return nil, err
	}
	for app, list := range shortcuts {
		for _, sc := range list {
			byApp[app] = append(byApp[app], Grant{Table: Shortcuts, ID: sc.ID, Value: sc.Description})
		}
	}
	apps := make([]App, 0, len(byApp))
	for id, grants := range byApp {
		slices.SortFunc(grants, func(a, b Grant) int {
			if c := slices.Index(Tables, a.Table) - slices.Index(Tables, b.Table); c != 0 {
				return c
			}
			return strings.Compare(a.ID, b.ID)
		})
		apps = append(apps, App{ID: id, Grants: grants})
	}
	slices.SortFunc(apps, func(a, b App) int { return strings.Compare(a.ID, b.ID) })
	return apps, nil
}

func (s Store) shortcuts() (map[string][]Shortcut, error) {
	if s.ShortcutsPath == "" {
		return map[string][]Shortcut{}, nil
	}
	return ReadShortcuts(s.ShortcutsPath)
}

// Revoke takes back what app was granted in table: the entry id, or
// every entry of the table when id is "". An entry the app alone was
// granted is deleted with its data (a restore token goes with it);
// one shared with other apps loses only the app's permissions. It
// fails with ErrNotGranted when there was nothing to take back, and
// returns how many grants it took.
func (s Store) Revoke(ctx context.Context, app string, table Table, id string) (int, error) {
	n, err := s.revoke(ctx, app, table, id)
	if err == nil && n == 0 {
		what := string(table)
		if id != "" {
			what += " " + id
		}
		return 0, fmt.Errorf("revoke %s for %s: %w", what, app, ErrNotGranted)
	}
	return n, err
}

func (s Store) revoke(ctx context.Context, app string, table Table, id string) (int, error) {
	if table == Shortcuts {
		return s.revokeShortcuts(app, id)
	}
	entries, err := s.entries(ctx, []Table{table})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if _, granted := e.permissions[app]; !granted || (id != "" && e.id != id) {
			continue
		}
		method, args := "DeletePermission", []any{string(table), e.id, app}
		if len(e.permissions) == 1 {
			method, args = "Delete", []any{string(table), e.id}
		}
		if err := s.obj().CallWithContext(ctx, StoreIface+"."+method, 0, args...).Err; err != nil {
			return n, fmt.Errorf("revoke %s %s for %s: %w", table, e.id, app, err)
		}
		n++
	}
	return n, nil
}

func (s Store) revokeShortcuts(app, id string) (int, error) {
	if s.ShortcutsPath == "" {
		return 0, nil
	}
	n := 0
	err := UpdateShortcuts(s.ShortcutsPath, func(all map[string][]Shortcut) {
		kept := slices.DeleteFunc(all[app], func(sc Shortcut) bool { return id == "" || sc.ID == id })
		n = len(all[app]) - len(kept)
		if len(kept) == 0 {
			delete(all, app)
			return
		}
		all[app] = kept
	})
	return n, err
}

// Set gives app perms on the entry id of table (SetPermission,
// creating the entry when missing): the yes or no of a background or
// notification grant.
func (s Store) Set(ctx context.Context, app string, table Table, id string, perms []string) error {
	if table == Shortcuts {
		return fmt.Errorf("set %s for %s: shortcuts are bound through the portal, not set", id, app)
	}
	if err := s.obj().CallWithContext(ctx, StoreIface+".SetPermission", 0, string(table), true, id, app, perms).Err; err != nil {
		return fmt.Errorf("set %s %s for %s to %v: %w", table, id, app, perms, err)
	}
	return nil
}

// Watch calls changed on every edit of the permission store, until
// stop; edits of the shortcut store are not announced.
func (s Store) Watch(changed func()) (stop func(), err error) {
	opts := []dbus.MatchOption{dbus.WithMatchInterface(StoreIface), dbus.WithMatchMember("Changed"), dbus.WithMatchObjectPath(StorePath)}
	if err := s.Conn.AddMatchSignal(opts...); err != nil {
		return nil, fmt.Errorf("follow the permission store: %w", err)
	}
	signals := make(chan *dbus.Signal, 16)
	s.Conn.Signal(signals)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-signals:
				if sig.Path == StorePath && sig.Name == StoreIface+".Changed" {
					changed()
				}
			case <-done:
				return
			}
		}
	}()
	return func() {
		s.Conn.RemoveSignal(signals)
		close(done)
		_ = s.Conn.RemoveMatchSignal(opts...)
	}, nil
}
