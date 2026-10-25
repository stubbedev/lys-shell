package bar

import (
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/brightness"
	"github.com/stubbedev/lys-shell/shell/treetest"
	"github.com/stubbedev/lys-shell/shell/widgets"
)

// brightnessOne is the only widget of type T carrying class under
// root, failing the test otherwise.
func brightnessOne[T widget.Widget](t *testing.T, root widget.Widget, class string) T {
	t.Helper()
	for _, w := range treetest.All[T](root) {
		if widget.HasClass(w, class) {
			return w
		}
	}
	var zero T
	t.Fatalf("no %T with class %q under the tree", zero, class)
	return zero
}

// The device list sits bare in the stack — Rust has no
// ScrolledWindow; the panel grows instead.
func TestBrightnessListIsNotInAScroll(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	source := &fakeBrightnessSource{devices: []brightness.Device{
		{Name: "intel_backlight", Type: brightness.TypeFirmware, Brightness: 50, Max: 100},
	}}
	ctx.Brightness = source
	v := brightnessDropdown(ctx).(*brightnessView)
	defer v.dropdownClosed()
	if v.body.Visible() != "devices" {
		t.Fatalf("page %q, want devices", v.body.Visible())
	}
	if n := len(treetest.All[*widget.Scroll](v)); n != 0 {
		t.Errorf("%d scrolled windows in the tree; the Rust list is bare", n)
	}
	if n := len(brightnessCards(v)); n != 1 {
		t.Errorf("the list holds %d items, want the one device", n)
	}
}

// The empty state follows mod.rs:96-111: the description wraps,
// centers, and caps at 32 characters — only the description; the
// title stays as the template builds it.
func TestBrightnessEmptyStateIconAndDescription(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	ctx.Brightness = &fakeBrightnessSource{}
	v := brightnessDropdown(ctx).(*brightnessView)
	defer v.dropdownClosed()
	if v.body.Visible() != "empty" {
		t.Fatalf("page %q, want empty", v.body.Visible())
	}
	empty := treetest.WithClass(v, "brightness-empty")
	if len(empty) != 1 || !widget.HasClass(empty[0], "empty-state") {
		t.Fatalf("empty state classes = %v, want brightness-empty + empty-state",
			len(empty))
	}
	desc := brightnessOne[*widget.Label](t, empty[0], "description")
	if !desc.Wrap() || desc.Alignment() != render.AlignCenter {
		t.Errorf("description wrap=%v align=%v, want wrapped and centered", desc.Wrap(), desc.Alignment())
	}
	if title := brightnessOne[*widget.Label](t, empty[0], "title"); title.Wrap() {
		t.Error("the title wraps")
	}
}

func TestFriendlyDeviceName(t *testing.T) {
	internal := i18n.T("dropdown-brightness-device-internal")
	external := i18n.T("dropdown-brightness-device-external")
	keyboard := i18n.T("dropdown-brightness-device-keyboard")
	for _, tc := range []struct {
		raw  string
		kind brightness.Type
		want string
	}{
		{"intel_backlight", brightness.TypeRaw, internal},
		{"amdgpu_bl0", brightness.TypeRaw, internal},
		{"acpi_video0", brightness.TypeRaw, internal},
		{"nvidia_0", brightness.TypeRaw, internal},
		{"panel", brightness.TypeFirmware, internal},
		{"panel", brightness.TypePlatform, internal},
		{"ddcci5", brightness.TypeDDC, external},
		{"dev-i2c-3", brightness.TypeRaw, external},
		// The keyboard check runs before the backlight one.
		{"tpacpi::kbd_backlight", brightness.TypePlatform, keyboard},
		{"Keyboard", brightness.TypeRaw, keyboard},
		// Nothing matches: the prettified identifier, separators collapsed.
		{"my_panel-x  two", brightness.TypeRaw, "My Panel X Two"},
		{"élan", brightness.TypeRaw, "Élan"},
	} {
		if got := friendlyDeviceName(tc.raw, tc.kind); got != tc.want {
			t.Errorf("friendlyDeviceName(%q, %v) = %q, want %q", tc.raw, tc.kind, got, tc.want)
		}
	}
}

func TestDeviceSubtitleOnlyWithSeveralDevices(t *testing.T) {
	if sub, ok := deviceSubtitle("intel_backlight", brightness.TypeFirmware, false); ok || sub != "" {
		t.Errorf("single device subtitle = %q, %v; want none", sub, ok)
	}
	if sub, ok := deviceSubtitle("intel_backlight", brightness.TypeFirmware, true); !ok || sub != "intel_backlight · firmware" {
		t.Errorf("multi subtitle = %q, %v", sub, ok)
	}
	for kind, want := range map[brightness.Type]string{
		brightness.TypeRaw: "raw", brightness.TypePlatform: "platform",
		brightness.TypeFirmware: "firmware", brightness.TypeDDC: "external",
	} {
		if got := backlightTypeLabel(kind); got != want {
			t.Errorf("backlightTypeLabel(%v) = %q, want %q", kind, got, want)
		}
	}
}

// brightnessCards is the device cards in the list, in order.
func brightnessCards(v *brightnessView) []widget.Widget {
	return treetest.WithClass(v.list, "brightness-device")
}

// brightnessItemMeta reports whether a device card carries the
// subtitle line.
func brightnessItemMeta(card widget.Widget) bool {
	return itemText(card, "description") != ""
}

func TestBrightnessDropdownEmptyWithoutDevices(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	v := brightnessDropdown(ctx).(*brightnessView)
	if v.body.Visible() != "empty" {
		t.Errorf("no service: page %q, want empty", v.body.Visible())
	}
	ctx.Brightness = &fakeBrightnessSource{}
	v = brightnessDropdown(ctx).(*brightnessView)
	defer v.dropdownClosed()
	if v.body.Visible() != "empty" || len(brightnessCards(v)) != 0 {
		t.Errorf("no devices: page %q, %d items", v.body.Visible(), len(brightnessCards(v)))
	}
}

func TestBrightnessDropdownItemsSubtitlesAndCommit(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	source := &fakeBrightnessSource{devices: []brightness.Device{
		{Name: "intel_backlight", Type: brightness.TypeFirmware, Brightness: 50, Max: 100},
	}}
	ctx.Brightness = source
	v := brightnessDropdown(ctx).(*brightnessView)
	defer v.dropdownClosed()
	if v.body.Visible() != "devices" || len(brightnessCards(v)) != 1 {
		t.Fatalf("page %q with %d items, want one device", v.body.Visible(), len(brightnessCards(v)))
	}
	if brightnessItemMeta(brightnessCards(v)[0]) {
		t.Error("a lone device shows a subtitle")
	}
	slider := v.sliders["intel_backlight"]
	if slider.Value() != 50 {
		t.Errorf("slider = %v, want 50", slider.Value())
	}
	slider.Knob.SetValue(70)
	waitHeadless(t, "the commit write", func() bool { return len(source.recorded()) == 1 })
	if got := source.recorded()[0]; got != (brightnessSet{"intel_backlight", 70}) {
		t.Errorf("set = %+v", got)
	}

	source.setDevices([]brightness.Device{
		{Name: "intel_backlight", Type: brightness.TypeFirmware, Brightness: 50, Max: 100},
		{Name: "ddcci5", Type: brightness.TypeDDC, Brightness: 10, Max: 100},
	})
	v.apply(v.read(t.Context()))
	if cards := brightnessCards(v); len(cards) != 2 || !brightnessItemMeta(cards[0]) {
		t.Error("two devices: want two items with subtitles")
	}
}

// A level change moves the existing slider (no rebuild); a changed
// device set rebuilds; the follow loop stops on close.
func TestBrightnessDropdownFollowsLevels(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	source := &fakeBrightnessSource{
		devices: []brightness.Device{{Name: "intel_backlight", Brightness: 20, Max: 100}},
		ticks:   make(chan struct{}, 1),
	}
	ctx.Brightness = source
	v := brightnessDropdown(ctx).(*brightnessView)
	slider := onHeadlessLoop(func() *widgets.DebouncedSlider { return v.sliders["intel_backlight"] })

	source.setDevices([]brightness.Device{{Name: "intel_backlight", Brightness: 80, Max: 100}})
	source.ticks <- struct{}{}
	waitHeadless(t, "the level update", func() bool { return slider.Value() == 80 })
	if onHeadlessLoop(func() *widgets.DebouncedSlider { return v.sliders["intel_backlight"] }) != slider {
		t.Error("a level change rebuilt the slider")
	}
	if len(source.recorded()) != 0 {
		t.Error("a backend level change wrote the device back")
	}

	source.setDevices(nil)
	source.ticks <- struct{}{}
	waitHeadless(t, "the empty page", func() bool { return v.body.Visible() == "empty" })

	onHeadlessLoop(func() bool { v.dropdownClosed(); return true })
	source.setDevices([]brightness.Device{{Name: "intel_backlight", Brightness: 5, Max: 100}})
	select {
	case source.ticks <- struct{}{}:
	default:
	}
	// A tick after close never lands: the page stays empty.
	time.Sleep(50 * time.Millisecond)
	if onHeadlessLoop(func() string { return v.body.Visible() }) != "empty" {
		t.Fatal("a tick after close rebuilt the closed dropdown")
	}
}
