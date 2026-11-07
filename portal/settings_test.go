package portal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/gtksettings"
)

// toolkitKeys are the keys GTK 4's portal settings backend reads beyond
// the theme, icons, cursor and font, with what a full settings.ini
// serves for each and the D-Bus type GTK expects.
var toolkitKeys = []struct {
	ns, key, signature string
	want               any
}{
	{gnomeWMPreferencesNS, "button-layout", "s", ":minimize,maximize,close"},
	{GnomeInterfaceNS, "font-antialiasing", "s", "rgba"},
	{GnomeInterfaceNS, "font-hinting", "s", "slight"},
	{GnomeInterfaceNS, "font-rgba-order", "s", "bgr"},
	{GnomeInterfaceNS, "cursor-blink", "b", false},
	{GnomeInterfaceNS, "cursor-blink-time", "i", int32(1000)},
	{GnomeInterfaceNS, "cursor-blink-timeout", "i", int32(7)},
	{GnomeInterfaceNS, "gtk-enable-primary-paste", "b", false},
	{GnomeInterfaceNS, "overlay-scrolling", "b", true},
	{gnomeMouseNS, "double-click", "i", int32(250)},
	{gnomeMouseNS, "drag-threshold", "i", int32(12)},
	{gnomeA11yInterfaceNS, "show-status-shapes", "b", true},
	{gnomeSoundNS, "theme-name", "s", "freedesktop"},
	{gnomeSoundNS, "event-sounds", "b", false},
}

const toolkitIni = `[Settings]
gtk-decoration-layout=:minimize,maximize,close
gtk-xft-antialias=1
gtk-xft-hintstyle=hintslight
gtk-xft-rgba=bgr
gtk-cursor-blink=false
gtk-cursor-blink-time=1000
gtk-cursor-blink-timeout=7
gtk-enable-primary-paste=false
gtk-overlay-scrolling=true
gtk-double-click-time=250
gtk-dnd-drag-threshold=12
gtk-show-status-shapes=true
gtk-sound-theme-name=freedesktop
gtk-enable-event-sounds=false
`

func TestSettingsServeTheToolkitKeys(t *testing.T) {
	r := settingsRig(t, gtksettings.Parse(toolkitIni), nil)
	all := r.readAll(t)
	for _, k := range toolkitKeys {
		v, ok := all[k.ns][k.key]
		if !ok {
			t.Errorf("ReadAll serves no %s %s", k.ns, k.key)
			continue
		}
		if v.Signature().String() != k.signature || v.Value() != k.want {
			t.Errorf("%s %s = %#v (%s), want %#v (%s)", k.ns, k.key, v.Value(), v.Signature(), k.want, k.signature)
		}
		if got, err := r.read(k.ns, k.key); err != nil || got != k.want {
			t.Errorf("Read %s %s = %#v, %v; want %#v", k.ns, k.key, got, err, k.want)
		}
	}

	bare := settingsRig(t, gtksettings.Settings{}, nil)
	for _, k := range toolkitKeys {
		_, err := bare.read(k.ns, k.key)
		var de dbus.Error
		if !errors.As(err, &de) || de.Name != errNotFound {
			t.Errorf("Read %s %s with no settings.ini key = %v, want NotFound", k.ns, k.key, err)
		}
	}
	for _, ns := range []string{gnomeWMPreferencesNS, gnomeMouseNS, gnomeA11yInterfaceNS, gnomeSoundNS} {
		if got, ok := bare.readAll(t)[ns]; ok {
			t.Errorf("ReadAll serves %s with nothing set: %v", ns, got)
		}
	}
}

func TestFontHintingFollowsXft(t *testing.T) {
	for _, c := range []struct {
		hinting gtksettings.Toggle
		style   gtksettings.HintStyle
		want    string
	}{
		{gtksettings.ToggleUnset, gtksettings.HintNone, "none"},
		{gtksettings.ToggleUnset, gtksettings.HintSlight, "slight"},
		{gtksettings.ToggleOn, gtksettings.HintMedium, "medium"},
		{gtksettings.ToggleUnset, gtksettings.HintFull, "full"},
		{gtksettings.ToggleOff, gtksettings.HintFull, "none"},
		{gtksettings.ToggleOff, gtksettings.HintStyleUnset, "none"},
		{gtksettings.ToggleOn, gtksettings.HintStyleUnset, ""},
		{gtksettings.ToggleUnset, gtksettings.HintStyleUnset, ""},
	} {
		if got := fontHinting(gtksettings.Settings{XftHinting: c.hinting, XftHintStyle: c.style}); got != c.want {
			t.Errorf("hinting %v, style %v: font-hinting = %q, want %q", c.hinting, c.style, got, c.want)
		}
	}
}

func TestFontAntialiasingAndRGBAOrderFollowXft(t *testing.T) {
	for _, c := range []struct {
		antialias    gtksettings.Toggle
		rgba         gtksettings.SubpixelOrder
		antialiasing string
		order        string
	}{
		{gtksettings.ToggleUnset, gtksettings.SubpixelRGB, "rgba", "rgb"},
		{gtksettings.ToggleOn, gtksettings.SubpixelBGR, "rgba", "bgr"},
		{gtksettings.ToggleUnset, gtksettings.SubpixelVRGB, "rgba", "vrgb"},
		{gtksettings.ToggleUnset, gtksettings.SubpixelVBGR, "rgba", "vbgr"},
		{gtksettings.ToggleOn, gtksettings.SubpixelNone, "grayscale", ""},
		{gtksettings.ToggleUnset, gtksettings.SubpixelNone, "grayscale", ""},
		{gtksettings.ToggleOn, gtksettings.SubpixelUnset, "grayscale", ""},
		{gtksettings.ToggleOff, gtksettings.SubpixelRGB, "none", "rgb"},
		{gtksettings.ToggleUnset, gtksettings.SubpixelUnset, "", ""},
	} {
		s := gtksettings.Settings{XftAntialias: c.antialias, XftRGBA: c.rgba}
		if got := fontAntialiasing(s); got != c.antialiasing {
			t.Errorf("antialias %v, rgba %v: font-antialiasing = %q, want %q", c.antialias, c.rgba, got, c.antialiasing)
		}
		if got := fontRGBAOrders[c.rgba]; got != c.order {
			t.Errorf("rgba %v: font-rgba-order = %q, want %q", c.rgba, got, c.order)
		}
	}
}

// writeAtomically replaces path the way an editor saving it does.
func writeAtomically(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

// changedUntil collects the SettingChanged "namespace key" pairs up to
// and including the first for stop, failing after a bounded wait.
func (r *rig) changedUntil(t *testing.T, stop string) []string {
	t.Helper()
	var keys []string
	timeout := time.After(10 * time.Second)
	for {
		select {
		case s := <-r.signals:
			if s.Name != SettingsIface+".SettingChanged" {
				continue
			}
			key := s.Body[0].(string) + " " + s.Body[1].(string)
			keys = append(keys, key)
			if key == stop {
				return keys
			}
		case <-timeout:
			t.Fatalf("no SettingChanged for %s; saw %v", stop, keys)
		}
	}
}

func count(keys []string, key string) int {
	n := 0
	for _, k := range keys {
		if k == key {
			n++
		}
	}
	return n
}

// An edit of the host's settings.ini reaches apps as one SettingChanged
// per moved key, even when the gtk-4.0 directory appears only after
// the portal started; rewriting the same value emits nothing.
func TestSettingsIniEditsEmitOnceForTheMovedKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_CONFIG_DIRS", t.TempDir())
	r := newRig(t, nil)
	ini := filepath.Join(home, "gtk-4.0", "settings.ini")
	layout := gnomeWMPreferencesNS + " button-layout"
	contrast := AppearanceNS + " " + keyContrast
	highContrast := false
	// settle flips high contrast, whose SettingChanged follows every
	// one the edit before it caused, and returns everything up to it.
	settle := func(edit string) []string {
		t.Helper()
		got := r.changedUntil(t, edit)
		highContrast = !highContrast
		if err := r.cfg.SetByPath("styling.high-contrast", highContrast); err != nil {
			t.Fatal(err)
		}
		return append(got, r.changedUntil(t, contrast)...)
	}

	writeAtomically(t, ini, "[Settings]\ngtk-decoration-layout=:close\n")
	if got := settle(layout); count(got, layout) != 1 {
		t.Errorf("creating settings.ini emitted %v, want one %s", got, layout)
	}
	if v, err := r.read(gnomeWMPreferencesNS, "button-layout"); err != nil || v != ":close" {
		t.Errorf("button-layout = %v, %v; want :close", v, err)
	}

	writeAtomically(t, ini, "[Settings]\ngtk-decoration-layout=:minimize,maximize,close\ngtk-cursor-blink=false\n")
	blink := GnomeInterfaceNS + " cursor-blink"
	got := settle(blink)
	if count(got, layout) != 1 || count(got, blink) != 1 || len(got) != 3 {
		t.Errorf("the edit emitted %v, want one %s, one %s and the contrast", got, layout, blink)
	}

	writeAtomically(t, ini, "[Settings]\ngtk-cursor-blink=false\ngtk-decoration-layout=:minimize,maximize,close\n")
	writeAtomically(t, ini, "[Settings]\ngtk-cursor-blink=false\ngtk-decoration-layout=:minimize,maximize,close\ngtk-sound-theme-name=ocean\n")
	sound := gnomeSoundNS + " theme-name"
	if got := r.changedUntil(t, sound); count(got, layout) != 0 || count(got, blink) != 0 {
		t.Errorf("rewriting unchanged values emitted %v", got)
	}
}
