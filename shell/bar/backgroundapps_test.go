package bar

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/backgroundapps"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// fakeMonitor is xdg-desktop-portal's org.freedesktop.background.Monitor.
type fakeMonitor struct {
	conn *dbus.Conn
	mu   sync.Mutex
	apps []map[string]dbus.Variant
}

type noMonitorMethods struct{}

func (f *fakeMonitor) set(t *testing.T, apps ...map[string]dbus.Variant) {
	t.Helper()
	f.mu.Lock()
	f.apps = apps
	f.mu.Unlock()
	if err := f.conn.Emit(backgroundapps.Path, "org.freedesktop.DBus.Properties.PropertiesChanged", backgroundapps.Interface,
		map[string]dbus.Variant{backgroundapps.Property: dbus.MakeVariant(apps)}, []string{}); err != nil {
		t.Fatal(err)
	}
}

func backgroundApp(id, instance, message string) map[string]dbus.Variant {
	return map[string]dbus.Variant{"app_id": dbus.MakeVariant(id), "instance": dbus.MakeVariant(instance), "message": dbus.MakeVariant(message)}
}

// argvRecorder is the spawner Stop runs flatpak kill through.
type argvRecorder struct {
	mu    sync.Mutex
	argvs [][]string
}

func (r *argvRecorder) run(_ context.Context, argv []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.argvs = append(r.argvs, slices.Clone(argv))
	return nil
}

func (r *argvRecorder) recorded() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.argvs)
}

// followBackgroundApps points the bar at the background monitor on a
// private bus, a fake one when serve is set, naming apps from the
// desktop entries under a private data home.
func followBackgroundApps(t *testing.T, serve bool) (*fakeMonitor, *argvRecorder, ModuleContext) {
	t.Helper()
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_DATA_DIRS", t.TempDir())
	if err := os.MkdirAll(filepath.Join(data, "applications"), 0o700); err != nil {
		t.Fatal(err)
	}
	entry := "[Desktop Entry]\nType=Application\nName=Fractal\nIcon=org.gnome.Fractal\nExec=sh\n"
	if err := os.WriteFile(filepath.Join(data, "applications", "org.gnome.Fractal.desktop"), []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	bus := dbustest.Start(t)
	f := &fakeMonitor{conn: bus.Conn(t)}
	if serve {
		release, err := dbusx.Serve(f.conn, dbusx.Service{
			Name: backgroundapps.BusName, Path: backgroundapps.Path, Interface: backgroundapps.Interface, Methods: noMonitorMethods{},
			Properties: dbusx.Getters{backgroundapps.Property: func() any {
				f.mu.Lock()
				defer f.mu.Unlock()
				return slices.Clone(f.apps)
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(release)
	}
	rec := &argvRecorder{}
	apps, err := backgroundapps.Follow(bus.Conn(t), describeApp, rec.run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(apps.Close)
	ctx := newTestContext(t, config.Defaults())
	ctx.BackgroundApps = apps
	return f, rec, ctx
}

func backgroundIndicator(t *testing.T, ctx ModuleContext) (*countIndicator, *widget.Box) {
	t.Helper()
	m, err := newBackgroundApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ind := m.(*countIndicator)
	t.Cleanup(ind.Stop)
	chrome := widget.NewBox(widget.Row, 0, 0)
	onHeadlessLoop(func() bool { ind.setChrome(chrome); return true })
	return ind, chrome
}

func TestBackgroundAppsAreListedAndStopKillsTheInstance(t *testing.T) {
	f, rec, ctx := followBackgroundApps(t, true)
	ind, chrome := backgroundIndicator(t, ctx)
	if onHeadlessLoop(chrome.Visible) {
		t.Fatal("the indicator shows with nothing in the background")
	}
	f.set(t, backgroundApp("org.gnome.Fractal", "1234", "Syncing messages"), backgroundApp("com.example.Unlisted", "77", "Playing"))
	waitHeadless(t, "two background apps", func() bool { return chrome.Visible() && ind.btn.Label().Text() == "2" })

	v := onHeadlessLoop(func() *backgroundAppsView {
		return backgroundAppsDropdown(ctx).(*backgroundAppsView)
	})
	defer onHeadlessLoop(func() bool { v.dropdownClosed(); return true })
	labels := onHeadlessLoop(func() []string { return treetest.Labels(v) })
	for _, want := range []string{"Fractal", "Syncing messages", "com.example.Unlisted", "Playing"} {
		if !slices.Contains(labels, want) {
			t.Errorf("dropdown labels %q lack %q", labels, want)
		}
	}
	icons := onHeadlessLoop(func() []string {
		var names []string
		for _, row := range treetest.WithClass(v, "background-apps-dropdown-row") {
			names = append(names, treetest.First[*widget.Icon](t, row).Name())
		}
		return names
	})
	if !slices.Equal(icons, []string{"org.gnome.Fractal", genericAppIcon}) {
		t.Errorf("row icons %q, want the entry's icon and the generic one for the app without an entry", icons)
	}

	f.set(t, backgroundApp("com.example.Unlisted", "77", "Playing"))
	waitHeadless(t, "the removed app's row gone", func() bool {
		return len(treetest.WithClass(v, "background-apps-dropdown-row")) == 1 && !slices.Contains(treetest.Labels(v), "Fractal")
	})
	waitHeadless(t, "one background app", func() bool { return ind.btn.Label().Text() == "1" })

	stops := onHeadlessLoop(func() []widget.Widget { return treetest.WithClass(v, "background-apps-stop") })
	if len(stops) != 1 {
		t.Fatalf("%d stop buttons, want one per app", len(stops))
	}
	onHeadlessLoop(func() bool { stops[0].(*widget.Button).OnClick(); return true })
	waitHeadless(t, "the kill", func() bool { return len(rec.recorded()) == 1 })
	if got := rec.recorded(); !slices.Equal(got[0], []string{"flatpak", "kill", "77"}) {
		t.Errorf("Stop ran %q, want the argv [flatpak kill 77]", got)
	}
}

func TestNoBackgroundMonitorShowsNoIndicator(t *testing.T) {
	_, _, ctx := followBackgroundApps(t, false)
	_, chrome := backgroundIndicator(t, ctx)
	if onHeadlessLoop(chrome.Visible) {
		t.Error("the indicator shows with no background monitor on the bus")
	}
	v := onHeadlessLoop(func() *backgroundAppsView {
		return backgroundAppsDropdown(ctx).(*backgroundAppsView)
	})
	defer onHeadlessLoop(func() bool { v.dropdownClosed(); return true })
	if rows := onHeadlessLoop(func() []widget.Widget { return treetest.WithClass(v, "background-apps-dropdown-row") }); len(rows) != 0 {
		t.Errorf("%d rows with no background monitor", len(rows))
	}
}

func TestBackgroundAppsNeedTheSessionBus(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	if stop := startBackgroundApps(&ctx, nil); ctx.BackgroundApps != nil {
		stop()
		t.Fatal("background apps followed with no session bus")
	}
	if _, err := newBackgroundApps(ctx); !errors.Is(err, errBackgroundAppsNoService) {
		t.Errorf("the module with no session bus = %v, want errBackgroundAppsNoService", err)
	}
}
