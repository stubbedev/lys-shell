package backgroundapps

import (
	"bytes"
	"context"
	"errors"
	"log"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// Monitor is a fake org.freedesktop.background.Monitor.
type Monitor struct {
	conn *dbus.Conn
	mu   sync.Mutex
	apps []map[string]dbus.Variant
}

type noMethods struct{}

// ServeMonitor serves a fake monitor listing apps on conn.
func serveMonitor(t *testing.T, conn *dbus.Conn, apps ...map[string]dbus.Variant) *Monitor {
	t.Helper()
	m := &Monitor{conn: conn, apps: apps}
	release, err := dbusx.Serve(conn, dbusx.Service{
		Name: BusName, Path: Path, Interface: Interface, Methods: noMethods{},
		Properties: dbusx.Getters{Property: func() any {
			m.mu.Lock()
			defer m.mu.Unlock()
			return slices.Clone(m.apps)
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return m
}

func (m *Monitor) set(t *testing.T, apps ...map[string]dbus.Variant) {
	t.Helper()
	m.mu.Lock()
	m.apps = apps
	m.mu.Unlock()
	if err := m.conn.Emit(Path, propertiesChanged, Interface, map[string]dbus.Variant{Property: dbus.MakeVariant(apps)}, []string{}); err != nil {
		t.Fatal(err)
	}
}

func entry(id, instance, message string) map[string]dbus.Variant {
	e := map[string]dbus.Variant{"app_id": dbus.MakeVariant(id), "instance": dbus.MakeVariant(instance)}
	if message != "" {
		e["message"] = dbus.MakeVariant(message)
	}
	return e
}

func describe(appID string) (string, string) {
	if appID == "org.gnome.Fractal" {
		return "Fractal", "org.gnome.Fractal"
	}
	return appID, ""
}

// recorder records each argv Stop runs.
type recorder struct {
	mu    sync.Mutex
	argvs [][]string
	err   error
}

func (r *recorder) run(_ context.Context, argv []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.argvs = append(r.argvs, slices.Clone(argv))
	return r.err
}

func waitFor(t *testing.T, a *Apps, what string, ok func([]App) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok(a.List()) {
		if time.Now().After(deadline) {
			t.Fatalf("%s: apps %+v", what, a.List())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAppsFollowTheMonitor(t *testing.T) {
	bus := dbustest.Start(t)
	m := serveMonitor(t, bus.Conn(t),
		entry("org.gnome.Fractal", "1234", "Syncing messages"),
		entry("com.example.NoEntry", "99", ""))
	rec := &recorder{}
	apps, err := Follow(bus.Conn(t), describe, rec.run)
	if err != nil {
		t.Fatal(err)
	}
	defer apps.Close()
	want := []App{
		{ID: "org.gnome.Fractal", Instance: "1234", Message: "Syncing messages", Name: "Fractal", Icon: "org.gnome.Fractal"},
		{ID: "com.example.NoEntry", Instance: "99", Name: "com.example.NoEntry"},
	}
	if got := apps.List(); !slices.Equal(got, want) {
		t.Fatalf("apps = %+v, want %+v", got, want)
	}
	ticks, stop := apps.Subscribe()
	defer stop()
	m.set(t, entry("com.example.NoEntry", "99", ""))
	waitFor(t, apps, "the removed app's row", func(l []App) bool { return slices.Equal(l, want[1:]) })
	select {
	case <-ticks:
	case <-time.After(5 * time.Second):
		t.Fatal("the removal ticked nobody")
	}

	if err := apps.Stop(t.Context(), "99"); err != nil {
		t.Fatal(err)
	}
	rec.mu.Lock()
	argvs := rec.argvs
	rec.mu.Unlock()
	if len(argvs) != 1 || !slices.Equal(argvs[0], []string{"flatpak", "kill", "99"}) {
		t.Errorf("Stop ran %q, want [flatpak kill 99]", argvs)
	}
	rec.err = errors.New("exit status 1")
	if err := apps.Stop(t.Context(), "99"); err == nil || !strings.Contains(err.Error(), "99") {
		t.Errorf("a failed kill = %v, want an error naming the instance", err)
	}
}

func TestAppsSkipEntriesNothingCouldStop(t *testing.T) {
	bus := dbustest.Start(t)
	serveMonitor(t, bus.Conn(t),
		entry("org.gnome.Fractal", "--help", ""),
		entry("", "12", ""),
		entry("org.gnome.Fractal", "", ""),
		entry("org.gnome.Fractal", "7", ""))
	apps, err := Follow(bus.Conn(t), describe, (&recorder{}).run)
	if err != nil {
		t.Fatal(err)
	}
	defer apps.Close()
	if got := apps.List(); len(got) != 1 || got[0].Instance != "7" {
		t.Errorf("apps = %+v, want only instance 7", got)
	}
}

// captureLog collects the standard logger's output for the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(flags)
	})
	return &buf
}

func TestNoMonitorListsNothingAndLogsOnce(t *testing.T) {
	logged := captureLog(t)
	bus := dbustest.Start(t)
	apps, err := Follow(bus.Conn(t), describe, (&recorder{}).run)
	if err != nil {
		t.Fatal(err)
	}
	defer apps.Close()
	if got := apps.List(); len(got) != 0 {
		t.Fatalf("apps with no monitor: %+v", got)
	}
	apps.refresh(t.Context())
	apps.refresh(t.Context())
	if n := strings.Count(logged.String(), "\n"); n != 1 || !strings.Contains(logged.String(), BusName) {
		t.Errorf("log with no monitor = %q, want one line naming it", logged.String())
	}

	m := serveMonitor(t, bus.Conn(t))
	m.set(t, entry("org.gnome.Fractal", "5", ""))
	waitFor(t, apps, "the monitor appearing", func(l []App) bool { return len(l) == 1 })
}
