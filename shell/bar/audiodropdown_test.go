package bar

import (
	"context"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/pulse"
	"github.com/stubbedev/lys-shell/service/pulse/native"
	"github.com/stubbedev/lys-shell/service/pulse/pulsetest"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

func TestAudioVolumeAndInputIcons(t *testing.T) {
	for _, tc := range []struct {
		pct   float64
		muted bool
		want  string
	}{
		{50, true, "ld-volume-x-symbolic"},
		{0, false, "ld-volume-x-symbolic"},
		{20, false, "ld-volume-symbolic"},
		{50, false, "ld-volume-1-symbolic"},
		{80, false, "ld-volume-2-symbolic"},
	} {
		if got := audioVolumeIcon(tc.pct, tc.muted); got != tc.want {
			t.Errorf("audioVolumeIcon(%v, %v) = %q, want %q", tc.pct, tc.muted, got, tc.want)
		}
	}
	if audioInputIcon(true) != "ld-mic-off-symbolic" || audioInputIcon(false) != "ld-mic-symbolic" {
		t.Error("input icons")
	}
}

func TestAppDisplayName(t *testing.T) {
	for _, tc := range []struct{ app, stream, want string }{
		{"Firefox", "AudioStream", "Firefox"},
		{"", "AudioStream", "AudioStream"},
		{"spotify", "AudioStream", "Spotify"},
		{"", "", ""},
		{"élan", "", "Élan"},
	} {
		if got := appDisplayName(tc.app, tc.stream); got != tc.want {
			t.Errorf("appDisplayName(%q, %q) = %q, want %q", tc.app, tc.stream, got, tc.want)
		}
	}
}

func TestStreamIcon(t *testing.T) {
	mapped, native := config.AppIconSourceMapped, config.AppIconSourceNative
	for _, tc := range []struct {
		props  map[string]string
		source config.AppIconSource
		want   string
	}{
		{map[string]string{paAppIconName: "firefox"}, mapped, "si-firefox-symbolic"},
		{map[string]string{}, mapped, ""},
		{map[string]string{paAppName: "Microsoft Edge", paAppIconName: "microsoft-edge"}, mapped, "ld-globe-symbolic"},
		{map[string]string{paAppIconName: "xyzzy-unknown"}, mapped, "xyzzy-unknown-symbolic"},
		{map[string]string{paAppIconName: "my-app-symbolic"}, mapped, "my-app-symbolic"},
		{map[string]string{paAppIconName: "firefox"}, native, "firefox"},
		{map[string]string{paAppProcessBinary: "spotify"}, native, "spotify"},
		{map[string]string{paAppIconName: "custom-icon", paAppProcessBinary: "myapp"}, native, "custom-icon"},
		{map[string]string{}, native, ""},
	} {
		if got := streamIcon(tc.props, tc.source); got != tc.want {
			t.Errorf("streamIcon(%v, %s) = %q, want %q", tc.props, tc.source, got, tc.want)
		}
	}
	// A native icon the theme lacks falls back to the mapped one; one
	// it has is kept.
	props := map[string]string{paAppIconName: "firefox"}
	if got := resolveStreamIcon(props, native, func(string) bool { return false }); got != "si-firefox-symbolic" {
		t.Errorf("missing native icon = %q, want the mapped one", got)
	}
	if got := resolveStreamIcon(props, native, func(string) bool { return true }); got != "firefox" {
		t.Errorf("present native icon = %q", got)
	}
	if !isEventStream(map[string]string{paStreamRestoreID: paRoleEvent}) || isEventStream(map[string]string{}) {
		t.Error("isEventStream")
	}
}

func TestDeviceIcons(t *testing.T) {
	none := map[string]string{}
	for _, tc := range []struct {
		name, desc string
		props      map[string]string
		want       string
	}{
		{"alsa_output.pci", "Built-in Audio", map[string]string{paFormFactor: "speaker"}, "ld-speaker-symbolic"},
		{"alsa_output.pci", "Something", map[string]string{paFormFactor: "headphone"}, "ld-headphones-symbolic"},
		{"alsa_output.hdmi", "HDMI", none, "ld-tv-symbolic"},
		{"bluez_output", "AirPods Pro", none, "ld-headset-symbolic"},
		{"alsa_output.pci", "Built-in Audio", none, "ld-speaker-symbolic"},
		// An unknown form factor falls through to the name.
		{"alsa_output.hdmi", "x", map[string]string{paFormFactor: "toaster"}, "ld-tv-symbolic"},
	} {
		if got := outputDeviceIcon(tc.name, tc.desc, tc.props); got != tc.want {
			t.Errorf("outputDeviceIcon(%q, %q) = %q, want %q", tc.name, tc.desc, got, tc.want)
		}
	}
	for _, tc := range []struct {
		name, desc string
		props      map[string]string
		want       string
	}{
		{"bluez_input", "Headset", map[string]string{paFormFactor: "headset"}, "ld-headset-symbolic"},
		{"v4l2_input", "USB Webcam", none, "ld-webcam-symbolic"},
		{"alsa_input.pci", "Built-in Audio", none, "ld-mic-symbolic"},
	} {
		if got := inputDeviceIcon(tc.name, tc.desc, tc.props); got != tc.want {
			t.Errorf("inputDeviceIcon(%q, %q) = %q, want %q", tc.name, tc.desc, got, tc.want)
		}
	}
	ports := []pulse.DevicePort{{Name: "speaker", Description: "Speaker"}}
	if got := activePortDescription("speaker", ports); got != "Speaker" {
		t.Errorf("active port = %q", got)
	}
	if activePortDescription("", ports) != "" || activePortDescription("gone", ports) != "" {
		t.Error("no or unlisted active port: want none")
	}
}

func TestAppVolumeRowsFilterDedupeAndSort(t *testing.T) {
	streams := []pulse.AudioStream{
		{Key: pulse.StreamKey{Index: 1}, ApplicationName: "zed", PID: 7},
		{Key: pulse.StreamKey{Index: 2}, ApplicationName: "zed", PID: 7},
		{Key: pulse.StreamKey{Index: 3}, ApplicationName: "bell", Properties: map[string]string{paStreamRestoreID: paRoleEvent}},
		{Key: pulse.StreamKey{Index: 4}, Name: "anon"},
		{Key: pulse.StreamKey{Index: 5}, Name: "anon2"},
	}
	rows := appVolumeRows(streams, config.AppIconSourceMapped, func(string) bool { return true })
	var got []uint32
	for _, r := range rows {
		got = append(got, r.key.Index)
	}
	// No event stream, one row for PID 7, no-PID streams all kept,
	// sorted by display name.
	if want := []uint32{4, 5, 1}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("rows = %v, want %v", got, want)
	}
}

// audioFixture is a pulsetest server with two sinks (one a headset by
// form factor), a monitor, a microphone, and a Firefox stream.
func audioFixture(t *testing.T) (*pulsetest.Server, *pulse.Service) {
	t.Helper()
	srv := pulsetest.New(t)
	srv.PutSink(pulsetest.Sink(1, "speakers", "Speakers", 2))
	headset := pulsetest.Sink(3, "bt", "Headset", 4)
	headset.Props[paFormFactor] = "headset"
	srv.PutSink(headset)
	srv.PutSource(pulsetest.Source(2, "speakers.monitor", "Monitor of Speakers", 1, "speakers"))
	srv.PutSource(pulsetest.Source(10, "mic", "Microphone", native.InvalidIndex, ""))
	srv.PutSinkInput(native.StreamInfo{
		Index: 40, Name: "Playback", OwnerModule: native.InvalidIndex, Client: 12, Device: 1,
		SampleSpec: pulsetest.StereoSpec(), ChannelMap: pulsetest.StereoMap(),
		Volume:    native.CVolume{native.VolumeNorm, native.VolumeNorm},
		Props:     native.PropList{"application.name": "firefox", "application.process.id": "4242"},
		HasVolume: true, VolumeWritable: true,
		Format: native.FormatInfo{Encoding: native.EncodingPCM, Props: native.PropList{}},
	})
	srv.SetDefaults("speakers", "mic")
	svc, err := pulse.Connect(context.Background(), srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return srv, svc
}

func TestAudioDropdownEmptyWithoutDevices(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	v := audioDropdown(ctx).(*audioView)
	if v.main.Visible() != "empty" || v.apps.Visible() != "empty" {
		t.Errorf("no service: main %q apps %q", v.main.Visible(), v.apps.Visible())
	}
	if v.output.item.Visible() || v.output.sliderRow.Visible() || !v.output.noDevice.Visible() {
		t.Error("no service: want the no-device row")
	}
}

func TestAudioDropdownSectionsAndApps(t *testing.T) {
	srv, svc := audioFixture(t)
	ctx := newTestContext(t, config.Defaults())
	ctx.Pulse = svc
	v := audioDropdown(ctx).(*audioView)
	defer v.dropdownClosed()

	if v.main.Visible() != "devices" {
		t.Fatalf("main = %q", v.main.Visible())
	}
	if name := itemText(v.output.item, "title"); name != "Speakers" || v.output.icon.Name() != "ld-speaker-symbolic" {
		t.Errorf("output = %q %q", name, v.output.icon.Name())
	}
	if v.output.slider.Value() != 50 || v.output.muteIcon.Name() != "ld-volume-1-symbolic" {
		t.Errorf("output slider %v icon %q", v.output.slider.Value(), v.output.muteIcon.Name())
	}
	if name := itemText(v.input.item, "title"); name != "Microphone" || v.input.muteIcon.Name() != "ld-mic-symbolic" {
		t.Errorf("input = %q %q", name, v.input.muteIcon.Name())
	}
	if got := len(pickerRows(v.pickers[audioInput])); got != 1 {
		t.Errorf("input picker rows = %d, want the monitor dropped", got)
	}
	if got := len(pickerRows(v.pickers[audioOutput])); got != 2 {
		t.Errorf("output picker rows = %d", got)
	}
	selected := 0
	for _, c := range pickerRows(v.pickers[audioOutput]) {
		if widget.HasClass(c, "selected") {
			selected++
		}
	}
	if selected != 1 {
		t.Errorf("%d rows marked the default, want 1", selected)
	}
	if v.apps.Visible() != "apps" || len(v.apps.items) != 1 {
		t.Fatalf("apps page %q with %d rows", v.apps.Visible(), len(v.apps.items))
	}
	app := v.apps.items[pulse.StreamKey{Index: 40, Type: pulse.StreamPlayback}]
	if app == nil || app.value.Text() != "100%" {
		t.Fatalf("firefox row = %+v", app)
	}

	// Mute writes to the server and the follow loop reflects it.
	v.output.toggleMute()
	waitHeadless(t, "the mute", func() bool { return v.output.mute.HasClass("muted") })
	if s, _ := srv.Sink(1); !s.Mute {
		t.Error("the server sink is not muted")
	}
	if v.output.muteIcon.Name() != "ld-volume-x-symbolic" {
		t.Errorf("muted icon = %q", v.output.muteIcon.Name())
	}

	// A slider commit sets the device volume.
	v.output.slider.Knob.SetValue(30)
	waitHeadless(t, "the volume write", func() bool {
		s, _ := srv.Sink(1)
		return s.Volume[0] < 0x8000
	})

	// An app commit sets the stream volume; the row survives (no
	// rebuild on a property change).
	app.slider.Knob.SetValue(40)
	waitHeadless(t, "the app volume", func() bool {
		st, _ := srv.SinkInput(40)
		return st.Volume[0] < native.VolumeNorm
	})
	if v.apps.items[pulse.StreamKey{Index: 40, Type: pulse.StreamPlayback}] != app {
		t.Error("a volume change rebuilt the app row")
	}

	// Picking the headset returns to main and changes the default.
	v.showPage("output")
	var headsetRow *widget.Item
	for _, c := range pickerRows(v.pickers[audioOutput]) {
		if it := c.(*widget.Item); !it.HasClass("selected") {
			headsetRow = it
		}
	}
	headsetRow.ClickAt(widget.Point{})
	if v.pages.Visible() != "main" {
		t.Errorf("after pick page = %q", v.pages.Visible())
	}
	waitHeadless(t, "the new default", func() bool { return itemText(v.output.item, "title") == "Headset" })
	if sink, _ := srv.Defaults(); sink != "bt" {
		t.Errorf("default sink = %q", sink)
	}
	if v.output.icon.Name() != "ld-headset-symbolic" {
		t.Errorf("headset icon = %q", v.output.icon.Name())
	}

	// The stream going away shows the no-apps state.
	srv.RemoveSinkInput(40)
	waitHeadless(t, "the no-apps state", func() bool { return v.apps.Visible() == "empty" })
	if i18n.T("dropdown-audio-no-apps") == "" {
		t.Error("no-apps string missing")
	}
}

// boundsOf reads a widget's arranged rect.
func boundsOf(w widget.Widget) render.Rect {
	return w.(interface{ Bounds() render.Rect }).Bounds()
}

func midX(r render.Rect) int { return r.X + r.W/2 }
func midY(r render.Rect) int { return r.Y + r.H/2 }

// sameWidgets reports the two children lists holding the same widget
// pointers in order.
func sameWidgets(a, b []widget.Widget) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Both empty states sit centered in their stack page (the Rust
// vexpand wrappers), the app one's icon carries the sm size modifier
// and the main one does not (main_section/mod.rs has no modifier).
func TestAudioEmptyStatesCenterVertically(t *testing.T) {
	restore := widget.SetAnimationsInstant(true)
	t.Cleanup(restore)
	v := audioDropdown(newTestContext(t, config.Defaults())).(*audioView)
	sz := v.Measure(widgetConstraintsMax(420, 700))
	v.Arrange(render.Rect{W: sz.W, H: 700})

	for _, tc := range []struct {
		what string
		root widget.Widget
	}{
		{"main empty state", v.main},
		{"app empty state", v.apps},
	} {
		st := findByClass(tc.root, "empty-state")
		if st == nil {
			t.Fatalf("%s: no empty-state in the tree", tc.what)
		}
		labels := treetest.All[*widget.Label](st)
		top := boundsOf(treetest.First[*widget.Icon](t, st)).Y - boundsOf(st).Y
		last := boundsOf(labels[len(labels)-1])
		bottom := boundsOf(st).Y + boundsOf(st).H - (last.Y + last.H)
		if top <= 0 || bottom <= 0 || top-bottom > 25 || bottom-top > 25 {
			t.Errorf("%s content sits %dpx from the top and %dpx from the bottom, want it centered", tc.what, top, bottom)
		}
	}
}

// The picker's back button is the GhostIconButton (icon-sized beside
// picker-back), and an option is lys-ui's Item with the device icon as
// its media: the check rides only the active one, and activating it
// goes back to the main page.
func TestAudioPickerBackAndOptionItems(t *testing.T) {
	v := audioDropdown(newTestContext(t, config.Defaults())).(*audioView)
	found := findByClass(v.pickers[audioOutput], "picker-back")
	back, ok := found.(*widget.Button)
	if !ok {
		t.Fatalf("picker-back = %T, want a button", found)
	}
	if !back.HasClass(css.Icon) {
		t.Error("the back button is not icon-sized")
	}
	speakers := audioDevice{description: "Speakers", subtitle: "Built-in", icon: "ld-speaker-symbolic"}
	idle := v.pickers[audioOutput].row(speakers, false)
	if len(treetest.WithClass(idle, "media")) != 1 || len(treetest.WithClass(idle, "audio-device-option-check")) != 0 {
		t.Error("an idle option lacks its media tile or carries the check")
	}
	active := v.pickers[audioOutput].row(speakers, true)
	if !active.HasClass("selected") || len(treetest.WithClass(active, "audio-device-option-check")) != 1 {
		t.Error("the active option lacks the selected class or the check")
	}
	v.showPage("output")
	active.ClickAt(widget.Point{})
	if v.pages.Visible() != "main" {
		t.Errorf("activating an option left page %q, want main", v.pages.Visible())
	}
}

// The picker title keeps its natural width: the free width after it
// stays empty, as the Rust label without hexpand does.
func TestAudioPickerTitleKeepsNaturalWidth(t *testing.T) {
	v := audioDropdown(newTestContext(t, config.Defaults())).(*audioView)
	p := v.pickers[audioOutput]
	title := findByClass(p, "picker-title").(*widget.Label)
	header := findByClass(p, "picker-header").(*widget.Box)
	hh := header.Measure(widgetConstraintsMax(420, 60))
	nat := title.Measure(widgetConstraintsMax(hh.W+200, 60))
	header.Arrange(render.Rect{W: hh.W + 200, H: hh.H})
	if got := boundsOf(title).W; got != nat.W {
		t.Errorf("title width = %d, want its natural %d (it took the free width)", got, nat.W)
	}
}

// Picker rows rebuild only when the list or the default changed: a
// no-op tick — even one that only moved a device's volume, which the
// Rust DeviceInfo does not carry — keeps the row widgets (their hover
// state with them), while a real change rebuilds.
func TestAudioPickerRowsRebuildOnlyOnChange(t *testing.T) {
	srv, svc := audioFixture(t)
	ctx := newTestContext(t, config.Defaults())
	ctx.Pulse = svc
	v := audioDropdown(ctx).(*audioView)
	defer v.dropdownClosed()
	p := v.pickers[audioOutput]
	rows := pickerRows(p)

	// The service learns of server mutations through its event loop,
	// so each step waits until a read reflects it.
	await := func(what string, cond func(audioState) bool) audioState {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			st := readAudioState(context.Background(), svc)
			if cond(st) {
				return st
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	// The same state again: nothing rebuilds.
	st := await("the settled state", func(st audioState) bool {
		return len(st.outputs) == 2 && st.defaultOut != nil && st.defaultOut.key.Index == 1
	})
	v.ctx.Invoke(func() { p.apply(st.outputs, st.defaultOut) })
	if !sameWidgets(rows, pickerRows(p)) {
		t.Error("a no-op tick rebuilt the picker rows")
	}

	// A volume change alone is still a no-op.
	quiet := pulsetest.Sink(1, "speakers", "Speakers", 2)
	quiet.Volume = native.CVolume{native.VolumeNorm / 4, native.VolumeNorm / 4}
	srv.PutSink(quiet)
	st = await("the quieter sink", func(st audioState) bool {
		return len(st.outputs) == 2 && st.outputs[0].volume.AveragePercentage() < 50
	})
	v.ctx.Invoke(func() { p.apply(st.outputs, st.defaultOut) })
	if !sameWidgets(rows, pickerRows(p)) {
		t.Error("a volume-only tick rebuilt the picker rows")
	}

	// A new default moves the selection: rebuild.
	srv.SetDefaults("bt", "mic")
	st = await("the new default", func(st audioState) bool {
		return st.defaultOut != nil && st.defaultOut.key.Index == 3
	})
	v.ctx.Invoke(func() { p.apply(st.outputs, st.defaultOut) })
	if sameWidgets(rows, pickerRows(p)) {
		t.Error("the new default did not rebuild the rows")
	}
	if !p.hasActive || p.active != st.defaultOut.key {
		t.Errorf("active = %v/%v, want the headset", p.active, p.hasActive)
	}

	// A new device grows the list: rebuild.
	srv.PutSink(pulsetest.Sink(5, "usb", "USB DAC", 2))
	st = await("the third sink", func(st audioState) bool { return len(st.outputs) == 3 })
	v.ctx.Invoke(func() { p.apply(st.outputs, st.defaultOut) })
	if got := len(pickerRows(p)); got != 3 {
		t.Errorf("picker rows = %d, want 3", got)
	}
}

// A device whose name is one long token still fits a narrow picker:
// the option's text ellipsizes, so the check stays inside the row.
func TestAudioOptionFitsANarrowPicker(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	v := audioDropdown(ctx).(*audioView)
	row := v.pickers[audioOutput].row(audioDevice{description: "alsa_output.pci-0000_00_1f.3.analog-stereo", icon: "ld-speaker-symbolic"}, true)
	ctx.Theme.Attach(row)
	const w = 240
	sz := row.Measure(widget.Constraints{Max: widget.Size{W: w, H: 400}})
	row.Arrange(render.Rect{W: w, H: sz.H})
	check := treetest.WithClass(row, "audio-device-option-check")[0].(*widget.Icon)
	if end := check.Bounds().X + check.Bounds().W; end > w {
		t.Errorf("the check ends at %d, past the %d row", end, w)
	}
}

// pickerRows is a device picker's option rows in order.
func pickerRows(p *devicePicker) []widget.Widget {
	var out []widget.Widget
	for _, it := range treetest.All[*widget.Item](p.list) {
		out = append(out, it)
	}
	return out
}

// A volume section is lys-ui's Item: the device's name over its kind,
// the whole row opening the kind's picker, and the mute button keeping
// its own click, which mutes without leaving the main page.
func TestAudioSectionIsAnItem(t *testing.T) {
	srv, svc := audioFixture(t)
	ctx := newTestContext(t, config.Defaults())
	ctx.Pulse = svc
	v := audioDropdown(ctx).(*audioView)
	defer v.dropdownClosed()
	if kind := itemText(v.input.item, "description"); kind != i18n.T("dropdown-audio-input") {
		t.Errorf("the input row's kind = %q", kind)
	}
	v.output.mute.OnClick()
	if v.pages.Visible() != "main" {
		t.Errorf("the mute button opened page %q, want main", v.pages.Visible())
	}
	waitHeadless(t, "the mute", func() bool { s, _ := srv.Sink(1); return s.Mute })
	v.input.item.ClickAt(widget.Point{})
	if v.pages.Visible() != "input" {
		t.Errorf("activating the input row opened page %q, want input", v.pages.Visible())
	}
}
