package bar

import (
	"context"
	"log"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/brightness"
	"github.com/stubbedev/lys-shell/shell/widgets"
)

// brightnessDeviceIcon is DEVICE_ICON.
const brightnessDeviceIcon = "ld-sun-symbolic"

// friendlyDeviceName is friendly_device_name: a display name for a raw
// sysfs backlight identifier.
func friendlyDeviceName(raw string, kind brightness.Type) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "kbd") || strings.Contains(lower, "keyboard"):
		return i18n.T("dropdown-brightness-device-keyboard")
	case strings.HasPrefix(lower, "ddcci") || strings.Contains(lower, "ddc") || strings.Contains(lower, "i2c"):
		return i18n.T("dropdown-brightness-device-external")
	case strings.Contains(lower, "backlight"),
		strings.HasPrefix(lower, "intel_"),
		strings.HasPrefix(lower, "amdgpu"),
		strings.HasPrefix(lower, "acpi_video"),
		strings.HasPrefix(lower, "nvidia"),
		kind == brightness.TypeFirmware || kind == brightness.TypePlatform:
		return i18n.T("dropdown-brightness-device-internal")
	}
	return prettifyIdentifier(raw)
}

// prettifyIdentifier is prettify: the words between '_', '-', and ' ',
// each with its first rune uppercased, joined by spaces.
func prettifyIdentifier(raw string) string {
	words := strings.FieldsFunc(raw, func(r rune) bool { return r == '_' || r == '-' || r == ' ' })
	for i, w := range words {
		r, size := utf8.DecodeRuneInString(w)
		words[i] = string(unicode.ToUpper(r)) + w[size:]
	}
	return strings.Join(words, " ")
}

// backlightTypeLabel is backlight_type_label.
func backlightTypeLabel(kind brightness.Type) string {
	switch kind {
	case brightness.TypeDDC:
		return "external"
	case brightness.TypePlatform:
		return "platform"
	case brightness.TypeFirmware:
		return "firmware"
	}
	return "raw"
}

// deviceSubtitle is device_subtitle: "name · type", only when several
// devices are listed.
func deviceSubtitle(name string, kind brightness.Type, multi bool) (string, bool) {
	if !multi {
		return "", false
	}
	return name + " · " + backlightTypeLabel(kind), true
}

// brightnessView is the brightness dropdown: a header, then a slider
// card per backlight or the empty state. The device list rebuilds only
// when the set of devices changes; a level change moves the slider in
// place (sync_single_device), which the slider ignores mid-drag.
type brightnessView struct {
	ctx  ModuleContext
	font render.Font
	px   float64

	*widget.Box
	body    *widget.Stack
	list    *widget.ItemGroup
	devices []brightness.Device
	sliders map[string]*widgets.DebouncedSlider

	once   sync.Once
	cancel context.CancelFunc
}

// templateEmptyState is the EmptyState widget template, the shared
// one in dropdown_template.go: the description comes back so the
// caller can tune it.

func brightnessDropdown(ctx ModuleContext) widget.Widget {
	font, px := dropdownFont(ctx)
	v := &brightnessView{ctx: ctx, font: font, px: px, cancel: func() {}}
	v.Box = widget.NewBox(widget.Column, 0, 0)
	v.AddClass("dropdown", "brightness-dropdown")
	v.Append(dropdownHeader(font, brightnessDeviceIcon, i18n.T("dropdown-brightness-title")), false)
	card, list := itemCard()
	card.AddClass("brightness-devices")
	v.list = list
	empty := widgets.EmptyState(font, px, brightnessDeviceIcon,
		i18n.T("dropdown-brightness-empty-title"), i18n.T("dropdown-brightness-empty-description"))
	empty.AddClass("brightness-empty")
	// The content holds the device list and the empty state. No
	// scroll: the list shows bare and the panel grows.
	content := dropdownContent()
	v.body = pageStack()
	v.body.Add("devices", atTop(card))
	v.body.Add("empty", empty)
	content.Append(v.body, true)
	v.Append(content, true)
	v.apply(v.read(context.Background()))
	v.follow()
	return v
}

// read snapshots the devices; a failed enumeration is no devices.
func (v *brightnessView) read(ctx context.Context) []brightness.Device {
	if v.ctx.Brightness == nil {
		return nil
	}
	devices, err := v.ctx.Brightness.Devices(ctx)
	if err != nil {
		log.Printf("brightness: %v", err)
		return nil
	}
	return devices
}

// apply shows a device snapshot: a rebuild when the names or types
// changed (sync_devices), otherwise the levels pushed into the sliders.
func (v *brightnessView) apply(devices []brightness.Device) {
	same := slices.EqualFunc(devices, v.devices, func(a, b brightness.Device) bool {
		return a.Name == b.Name && a.Type == b.Type
	})
	v.devices = devices
	if same && v.sliders != nil {
		for _, d := range devices {
			v.sliders[d.Name].Set(d.Percentage())
		}
		return
	}
	v.list.Clear()
	v.sliders = make(map[string]*widgets.DebouncedSlider, len(devices))
	if len(devices) == 0 {
		v.body.Show("empty")
		return
	}
	multi := len(devices) > 1
	for _, d := range devices {
		v.list.Append(v.item(d, multi))
	}
	v.body.Show("devices")
}

// item is BrightnessDeviceItem: the device as lys-ui's Item (the icon,
// the name over the optional subtitle) over the labeled slider
// committing to it.
func (v *brightnessView) item(d brightness.Device, multi bool) widget.Widget {
	card := widget.NewBox(widget.Column, 0, 0)
	card.AddClass("brightness-device")
	sub, _ := deviceSubtitle(d.Name, d.Type, multi)
	head := widget.NewItem(v.font, friendlyDeviceName(d.Name, d.Type), sub)
	head.SetSmall(true)
	head.SetEllipsize(true)
	head.SetMedia(widget.NewThemeIcon(brightnessDeviceIcon, itemIconPx), false)
	card.Append(head, false)

	slider := widgets.NewDebouncedSlider(d.Percentage(), v.font, v.px, 0, v.ctx.Invoke)
	slider.ValueLabel().AddClass("brightness-slider-value", css.Numeric)
	name := d.Name
	slider.Write = func(pct float64) func() { return v.write(name, pct) }
	v.sliders[name] = slider
	body := itemBody()
	body.Append(slider, false)
	card.Append(body, false)
	return card
}

// write is commit_brightness: the device write the slider runs off
// the loop, one at a time.
func (v *brightnessView) write(name string, pct float64) func() {
	src := v.ctx.Brightness
	return func() {
		if err := src.Set(context.Background(), name, pct); err != nil {
			log.Printf("brightness: set %s: %v", name, err)
		}
	}
}

// follow re-reads the devices on every service tick until the
// dropdown closes.
func (v *brightnessView) follow() {
	if v.ctx.Brightness == nil {
		return
	}
	life, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	followTicks(v.ctx, life, "brightness", v.ctx.Brightness.Subscribe, v.read, v.apply)
}

// dropdownClosed implements dropdownCloser.
func (v *brightnessView) dropdownClosed() { v.once.Do(func() { v.cancel() }) }
