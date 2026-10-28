package portal

import (
	"errors"
	"maps"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/gtksettings"
)

// rig is a backend serving on a private bus, with a client connection
// that calls it the way the frontend does.
type rig struct {
	bus     *dbustest.Bus
	cfg     *config.Service
	backend *Backend
	client  *dbus.Conn
	obj     dbus.BusObject
	signals chan *dbus.Signal
}

// processDataHome is XDG_DATA_HOME as the test process started with
// it: a test that points it elsewhere keeps its own.
var processDataHome = os.Getenv("XDG_DATA_HOME")

// newRig serves a backend on a private bus, its state, cache and data
// directories temporary ones so no test writes to the user's.
func newRig(t *testing.T, edit func(*Backend)) *rig {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if os.Getenv("XDG_DATA_HOME") == processDataHome {
		t.Setenv("XDG_DATA_HOME", t.TempDir())
	}
	bus := dbustest.Start(t)
	cfg := config.Load(t.TempDir(), config.DiscardDiagnostics)
	client := bus.Conn(t)
	signals := make(chan *dbus.Signal, 64)
	if err := client.AddMatchSignal(dbus.WithMatchObjectPath(ObjectPath)); err != nil {
		t.Fatal(err)
	}
	client.Signal(signals)
	b := New(bus.Conn(t), cfg)
	if edit != nil {
		edit(b)
	}
	stop, err := b.Serve()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return &rig{bus: bus, cfg: cfg, backend: b, client: client, obj: client.Object(BusName, ObjectPath), signals: signals}
}

// changed collects the SettingChanged "namespace key" pairs emitted
// within a short wait.
func (r *rig) changed(t *testing.T) []string {
	t.Helper()
	var keys []string
	timeout := time.After(300 * time.Millisecond)
	for {
		select {
		case s := <-r.signals:
			if s.Name != SettingsIface+".SettingChanged" {
				continue
			}
			keys = append(keys, s.Body[0].(string)+" "+s.Body[1].(string))
		case <-timeout:
			return keys
		}
	}
}

// settingsRig is a rig whose settings read gtk as the host's
// settings.ini and env as the session's environment.
func settingsRig(t *testing.T, gtk gtksettings.Settings, env map[string]string) *rig {
	t.Helper()
	return newRig(t, func(b *Backend) {
		b.settings.gtk = func() gtksettings.Settings { return gtk }
		b.settings.getenv = func(k string) string { return env[k] }
	})
}

func (r *rig) read(ns, key string) (any, error) {
	var v dbus.Variant
	err := r.obj.Call(SettingsIface+".Read", 0, ns, key).Store(&v)
	return v.Value(), err
}

func (r *rig) readAll(t *testing.T, namespaces ...string) map[string]map[string]dbus.Variant {
	t.Helper()
	var all map[string]map[string]dbus.Variant
	if err := r.obj.Call(SettingsIface+".ReadAll", 0, append([]string{}, namespaces...)).Store(&all); err != nil {
		t.Fatal(err)
	}
	return all
}

func TestSettingsReadAllServesAppearance(t *testing.T) {
	r := settingsRig(t, gtksettings.Settings{}, nil)
	got := r.readAll(t)[AppearanceNS]
	// The default palette is the dark lys-shell theme with a #e0947a accent.
	if got[keyColorScheme].Value() != uint32(1) || got[keyContrast].Value() != uint32(0) || got[keyReducedMotion].Value() != uint32(0) {
		t.Errorf("appearance = %v", got)
	}
	// The accent is the spec's (ddd) structure, not an array.
	var acc dbus.Variant
	if err := r.obj.Call(SettingsIface+".Read", 0, AppearanceNS, keyAccentColor).Store(&acc); err != nil || acc.Signature().String() != "(ddd)" {
		t.Errorf("accent = %v (%s), %v; want (ddd)", acc, acc.Signature(), err)
	}
	if want := []any{0xe0 / 255.0, 0x94 / 255.0, 0x7a / 255.0}; !slices.Equal(acc.Value().([]any), want) {
		t.Errorf("accent = %v, want %v", acc.Value(), want)
	}
}

// Namespaces select by exact name or a trailing glob; "" or no names
// select everything; a bare prefix selects nothing.
func TestSettingsNamespaceGlobs(t *testing.T) {
	r := settingsRig(t, gtksettings.Settings{}, nil)
	for _, tc := range []struct {
		namespaces []string
		want       []string
	}{
		{nil, []string{AppearanceNS, GnomeInterfaceNS}},
		{[]string{""}, []string{AppearanceNS, GnomeInterfaceNS}},
		{[]string{"org.freedesktop.*"}, []string{AppearanceNS}},
		{[]string{"org.gnome.*"}, []string{GnomeInterfaceNS}},
		{[]string{AppearanceNS}, []string{AppearanceNS}},
		{[]string{"org.freedesktop.app"}, nil},
		{[]string{"org.kde.*"}, nil},
	} {
		all := r.readAll(t, tc.namespaces...)
		got := slices.Sorted(maps.Keys(all))
		if !slices.Equal(got, tc.want) {
			t.Errorf("ReadAll(%q) = %v, want %v", tc.namespaces, got, tc.want)
		}
	}
}

func TestSettingsReadAndUnknownKeys(t *testing.T) {
	r := settingsRig(t, gtksettings.Settings{}, nil)
	if v, err := r.read(AppearanceNS, keyColorScheme); err != nil || v != uint32(1) {
		t.Errorf("Read color-scheme = %v, %v", v, err)
	}
	for _, tc := range [][2]string{{AppearanceNS, "font-name"}, {"org.kde.kdeglobals", keyColorScheme}, {GnomeInterfaceNS, "icon-theme"}} {
		_, err := r.read(tc[0], tc[1])
		var de dbus.Error
		if !errors.As(err, &de) || de.Name != "org.freedesktop.portal.Error.NotFound" {
			t.Errorf("Read %s %s = %v, want NotFound", tc[0], tc[1], err)
		}
	}
	var v dbus.Variant
	if err := r.obj.Call(SettingsIface+".ReadOne", 0, AppearanceNS, keyColorScheme).Store(&v); err == nil {
		t.Error("ReadOne, which the impl interface does not define, still answers")
	}
}

// The GTK keys come from the host's settings.ini and the session's
// cursor; what neither sets falls back to the shell's palette and font,
// or is not served.
func TestSettingsServeTheGTKInterfaceKeys(t *testing.T) {
	r := settingsRig(t, gtksettings.Settings{IconTheme: "Papirus", CursorTheme: "Vimix", CursorSize: 22, FontName: "Inter 11", XftDPI: 98304 * 5 / 4},
		map[string]string{"XCURSOR_THEME": "Bibata", "XCURSOR_SIZE": "28"})
	got := r.readAll(t, GnomeInterfaceNS)[GnomeInterfaceNS]
	want := map[string]any{
		"color-scheme": "prefer-dark", "gtk-theme": "Adwaita-dark", "icon-theme": "Papirus",
		"cursor-theme": "Bibata", "cursor-size": int32(28), "font-name": "Inter 11", "text-scaling-factor": 1.25,
	}
	if len(got) != len(want) {
		t.Errorf("served %v, want %v", got, want)
	}
	for k, w := range want {
		if got[k].Value() != w {
			t.Errorf("%s = %#v, want %#v", k, got[k].Value(), w)
		}
	}

	bare := settingsRig(t, gtksettings.Settings{}, nil)
	got = bare.readAll(t, GnomeInterfaceNS)[GnomeInterfaceNS]
	for _, k := range []string{"icon-theme", "cursor-theme", "cursor-size"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s served with nothing to serve: %v", k, got[k])
		}
	}
	if got["font-name"].Value() != config.Defaults().General.FontSans+" 10" || got["text-scaling-factor"].Value() != 1.0 {
		t.Errorf("fallbacks = %v", got)
	}
	host := settingsRig(t, gtksettings.Settings{ThemeName: "Orchis", CursorTheme: "Vimix", CursorSize: 22}, nil)
	got = host.readAll(t, GnomeInterfaceNS)[GnomeInterfaceNS]
	if got["gtk-theme"].Value() != "Orchis" || got["cursor-theme"].Value() != "Vimix" || got["cursor-size"].Value() != int32(22) {
		t.Errorf("settings.ini values = %v", got)
	}
}

// contrast follows its own setting, never the matugen tweak.
func TestSettingsContrastHasItsOwnSource(t *testing.T) {
	r := settingsRig(t, gtksettings.Settings{}, nil)
	if err := r.cfg.SetByPath("styling.matugen-contrast", 0.5); err != nil {
		t.Fatal(err)
	}
	if v, _ := r.read(AppearanceNS, keyContrast); v != uint32(0) {
		t.Errorf("a positive matugen contrast asked for high contrast: %v", v)
	}
	if got := r.changed(t); len(got) != 0 {
		t.Errorf("a matugen change emitted %v", got)
	}
	if err := r.cfg.SetByPath("styling.high-contrast", true); err != nil {
		t.Fatal(err)
	}
	if v, _ := r.read(AppearanceNS, keyContrast); v != uint32(1) {
		t.Errorf("high-contrast on: contrast = %v", v)
	}
	if got := r.changed(t); !slices.Equal(got, []string{AppearanceNS + " " + keyContrast}) {
		t.Errorf("high-contrast on emitted %v", got)
	}
}

func TestColorScheme(t *testing.T) {
	s := config.DefaultsStyling()
	if colorScheme(s) != 1 {
		t.Error("auto over a dark background is not dark")
	}
	s.Palette.Bg, _ = config.ParseHexColor("#f0f0f0")
	if colorScheme(s) != 2 {
		t.Error("auto over a light background is not light")
	}
	s.Appearance = config.AppearanceDark
	if colorScheme(s) != 1 {
		t.Error("forced dark is not dark")
	}
	s.Appearance = config.AppearanceLight
	s.Palette.Bg, _ = config.ParseHexColor("#000")
	if colorScheme(s) != 2 {
		t.Error("forced light is not light")
	}
}

func TestSettingChangedFollowsTheConfig(t *testing.T) {
	r := settingsRig(t, gtksettings.Settings{}, nil)
	// Nothing is emitted until a value actually changes.
	if got := r.changed(t); len(got) != 0 {
		t.Fatalf("startup emitted %v", got)
	}
	if err := r.cfg.SetByPath("animations.enabled", false); err != nil {
		t.Fatal(err)
	}
	if got := r.changed(t); !slices.Equal(got, []string{AppearanceNS + " " + keyReducedMotion}) {
		t.Errorf("animations off emitted %v", got)
	}
	if v, _ := r.read(AppearanceNS, keyReducedMotion); v != uint32(1) {
		t.Errorf("reduced-motion = %v after animations off", v)
	}
	if err := r.cfg.SetByPath("styling.appearance", "light"); err != nil {
		t.Fatal(err)
	}
	want := []string{AppearanceNS + " " + keyColorScheme, GnomeInterfaceNS + " color-scheme", GnomeInterfaceNS + " gtk-theme"}
	if got := r.changed(t); !slices.Equal(got, want) {
		t.Errorf("appearance light emitted %v, want %v", got, want)
	}
	// Forcing dark over the dark palette changes nothing anyone reads.
	if err := r.cfg.SetByPath("styling.appearance", "auto"); err != nil {
		t.Fatal(err)
	}
	r.changed(t)
	if err := r.cfg.SetByPath("styling.appearance", "dark"); err != nil {
		t.Fatal(err)
	}
	if got := r.changed(t); len(got) != 0 {
		t.Errorf("an unchanged effective scheme emitted %v", got)
	}
	// A setting the portal does not serve emits nothing.
	if err := r.cfg.SetByPath("styling.scale", 1.5); err != nil {
		t.Fatal(err)
	}
	if got := r.changed(t); len(got) != 0 {
		t.Errorf("an unserved setting emitted %v", got)
	}
}

func readLockdown(t *testing.T, r *rig) map[string]dbus.Variant {
	t.Helper()
	var all map[string]dbus.Variant
	if err := r.obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, LockdownIface).Store(&all); err != nil {
		t.Fatal(err)
	}
	return all
}

// By default nothing is locked down, and the interface carries only the
// seven properties the spec defines.
func TestLockdownLocksNothingByDefault(t *testing.T) {
	r := newRig(t, nil)
	all := readLockdown(t, r)
	if len(all) != 7 {
		t.Fatalf("lockdown = %v, want the seven spec properties", all)
	}
	for name, v := range all {
		if v.Value() != false {
			t.Errorf("%s = %v", name, v)
		}
	}
}

// A config value flips its property and announces it; the others stay
// put, and a D-Bus write is refused.
func TestLockdownFollowsTheConfig(t *testing.T) {
	r := newRig(t, nil)
	if err := r.client.AddMatchSignal(dbus.WithMatchInterface("org.freedesktop.DBus.Properties")); err != nil {
		t.Fatal(err)
	}
	if err := r.cfg.SetByPath("portal.lockdown.disable-printing", true); err != nil {
		t.Fatal(err)
	}
	all := readLockdown(t, r)
	if all["disable-printing"].Value() != true || all["disable-camera"].Value() != false {
		t.Errorf("after disabling printing: %v", all)
	}
	timeout := time.After(2 * time.Second)
	for {
		select {
		case s := <-r.signals:
			if s.Name != "org.freedesktop.DBus.Properties.PropertiesChanged" {
				continue
			}
			changed := s.Body[1].(map[string]dbus.Variant)
			if s.Body[0] != LockdownIface || len(changed) != 1 || changed["disable-printing"].Value() != true {
				t.Errorf("PropertiesChanged %v", s.Body)
			}
			err := r.obj.Call("org.freedesktop.DBus.Properties.Set", 0, LockdownIface, "disable-printing", dbus.MakeVariant(false)).Err
			if err == nil {
				t.Error("a D-Bus write to a lockdown property was accepted")
			}
			return
		case <-timeout:
			t.Fatal("no PropertiesChanged after a lockdown config change")
		}
	}
}

func TestServeRefusesAnOwnedName(t *testing.T) {
	bus := dbustest.Start(t)
	other := bus.Conn(t)
	if _, err := other.RequestName(BusName, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	if _, err := New(bus.Conn(t), config.Load(t.TempDir(), config.DiscardDiagnostics)).Serve(); err == nil {
		t.Error("a second backend took an owned name")
	}
}

func TestRequestCloseCancels(t *testing.T) {
	bus := dbustest.Start(t)
	server, client := bus.Conn(t), bus.Conn(t)
	handle := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_1/t")
	req, err := mountRequest(server, handle)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-req.Cancelled():
		t.Fatal("cancelled before Close")
	default:
	}
	obj := client.Object(server.Names()[0], handle)
	if err := obj.Call(RequestIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	select {
	case <-req.Cancelled():
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel")
	}
	// A second Close is harmless; after end the object is gone.
	if err := obj.Call(RequestIface+".Close", 0).Err; err != nil {
		t.Error(err)
	}
	req.end()
	if err := obj.Call(RequestIface+".Close", 0).Err; err == nil {
		t.Error("the Request outlived its call")
	}
}

func TestSessionCloseRunsCleanupOnce(t *testing.T) {
	bus := dbustest.Start(t)
	server, client := bus.Conn(t), bus.Conn(t)
	reg := newSessions(server)
	var closes dbustest.Var[int]
	first, second := dbus.ObjectPath("/org/freedesktop/portal/desktop/session/1_1/a"), dbus.ObjectPath("/org/freedesktop/portal/desktop/session/1_1/b")
	for _, p := range []dbus.ObjectPath{first, second} {
		s, err := reg.create(p, kindScreenCast, "org.app")
		if err != nil {
			t.Fatal(err)
		}
		s.onClose(func() { closes.Update(func(n int) int { return n + 1 }) })
	}
	signals := make(chan *dbus.Signal, 4)
	_ = client.AddMatchSignal(dbus.WithMatchInterface(SessionIface))
	client.Signal(signals)

	obj := client.Object(server.Names()[0], first)
	if v, err := obj.GetProperty(SessionIface + ".version"); err != nil || v.Value() != uint32(1) {
		t.Errorf("version = %v, %v", v, err)
	}
	// The app's Close ends the session without Closed, which the
	// interface reserves for closes the implementation makes.
	if err := obj.Call(SessionIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-signals:
		t.Errorf("the app's Close emitted %v", s)
	case <-time.After(100 * time.Millisecond):
	}
	if closes.Load() != 1 {
		t.Errorf("cleanups after Close = %d", closes.Load())
	}
	if err := obj.Call(SessionIface+".Close", 0).Err; err == nil {
		t.Error("a closed session still answers")
	}
	// Shutdown closes the live session only, once, and says so.
	reg.clearAll()
	reg.clearAll()
	if closes.Load() != 2 {
		t.Errorf("cleanups after shutdown = %d, want 2", closes.Load())
	}
	select {
	case s := <-signals:
		if s.Path != second || s.Name != SessionIface+".Closed" {
			t.Errorf("signal = %v", s)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown emitted no Closed")
	}
	// A cleanup registered on a closed session runs at once.
	late, _ := reg.create("/org/freedesktop/portal/desktop/session/1_1/c", kindScreenCast, "org.app")
	reg.close(late)
	ran := false
	late.onClose(func() { ran = true })
	if !ran {
		t.Error("a cleanup on a closed session never ran")
	}
}

// A session answers only to the interfaces and the app that own it.
func TestSessionLookupChecksKindAndApp(t *testing.T) {
	bus := dbustest.Start(t)
	reg := newSessions(bus.Conn(t))
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/session/1_1/a")
	if _, err := reg.create(path, kindRemoteDesktop, "org.app"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.lookup(path, "org.app", kindScreenCast, kindRemoteDesktop); err != nil {
		t.Errorf("own kind: %v", err)
	}
	if _, err := reg.create(path, kindScreenCast, "org.app"); err == nil {
		t.Error("a second session took a live path")
	}
	for _, tc := range []struct {
		path  dbus.ObjectPath
		app   string
		kinds []sessionKind
	}{
		{path, "org.app", []sessionKind{kindScreenCast}},
		{path, "org.other", []sessionKind{kindRemoteDesktop}},
		{"/org/freedesktop/portal/desktop/session/1_1/missing", "org.app", []sessionKind{kindRemoteDesktop}},
	} {
		if _, err := reg.lookup(tc.path, tc.app, tc.kinds...); err == nil {
			t.Errorf("lookup %s for %s as %v succeeded", tc.path, tc.app, tc.kinds)
		}
	}
}

func TestOptions(t *testing.T) {
	o := Vardict{
		"u32": dbus.MakeVariant(uint32(7)), "u64": dbus.MakeVariant(uint64(9)), "big": dbus.MakeVariant(uint64(1) << 40),
		"b": dbus.MakeVariant(true), "s": dbus.MakeVariant("hi"), "wrong": dbus.MakeVariant(int32(3)),
	}
	for key, want := range map[string]uint32{"u32": 7, "u64": 9} {
		if v, ok := optU32(o, key); !ok || v != want {
			t.Errorf("optU32 %s = %d, %v", key, v, ok)
		}
	}
	for _, key := range []string{"big", "wrong", "missing"} {
		if _, ok := optU32(o, key); ok {
			t.Errorf("optU32 %s read a value", key)
		}
	}
	if v, ok := optBool(o, "b"); !ok || !v {
		t.Error("optBool")
	}
	if _, ok := optBool(o, "s"); ok {
		t.Error("optBool read a string")
	}
	if stringOr(o, "s", "x") != "hi" || stringOr(o, "missing", "x") != "x" || stringOr(o, "b", "x") != "x" {
		t.Error("stringOr")
	}
}
