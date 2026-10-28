package bar

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/desktopnotify"
	"github.com/stubbedev/lys-shell/service/bluetooth"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

const (
	pathHeadset = dbus.ObjectPath("/org/bluez/hci0/dev_01")
	pathSpeaker = dbus.ObjectPath("/org/bluez/hci0/dev_02")
	pathMouse   = dbus.ObjectPath("/org/bluez/hci0/dev_03")
)

// btWorld is a powered adapter with a connected headset, a paired
// mouse, and an unpaired speaker nearby.
func btWorld() bluetooth.State {
	battery := uint8(64)
	class := uint32(0x240418)
	return bluetooth.State{
		Available: true, Enabled: true, Primary: &bluetooth.Adapter{Powered: true},
		Devices: []bluetooth.Device{
			{Path: pathSpeaker, Alias: "Speaker", Name: new("Speaker"), Icon: new("audio-speakers")},
			{Path: pathHeadset, Alias: "Headset", Name: new("Headset"), Icon: new("audio-headphones"), Connected: true, Paired: true, BatteryPercentage: &battery},
			{Path: pathMouse, Alias: "Mouse", Name: new("Mouse"), Class: &class, Paired: true},
		},
	}
}

func newTestBtDropdown(t *testing.T, st bluetooth.State) (*btDropdown, *fakeBluetooth) {
	t.Helper()
	src := newFakeBluetooth(st)
	return newBtDropdown(newTestContext(t, config.Defaults()), src), src
}

func visible(w widget.Widget) bool {
	v, ok := w.(interface{ Visible() bool })
	return !ok || v.Visible()
}

func rows(list *widget.ItemGroup) []*btDeviceRow {
	var out []*btDeviceRow
	treetest.Walk(list, func(w widget.Widget) {
		if r, ok := w.(*btDeviceRow); ok {
			out = append(out, r)
		}
	})
	return out
}

// itemText is the text of the item's part labelled class (title,
// description).
func itemText(it widget.Widget, class string) string {
	for _, w := range treetest.WithClass(it, class) {
		if l, ok := w.(*widget.Label); ok {
			return l.Text()
		}
	}
	return ""
}

func rowNamed(t *testing.T, d *btDropdown, name string) *btDeviceRow {
	t.Helper()
	for _, list := range []*widget.ItemGroup{d.myList, d.availList} {
		for _, r := range rows(list) {
			if itemText(r, "title") == name {
				return r
			}
		}
	}
	t.Fatalf("no row %q", name)
	return nil
}

func TestBtDropdownListsDevices(t *testing.T) {
	d, _ := newTestBtDropdown(t, btWorld())
	mine, avail := rows(d.myList), rows(d.availList)
	if len(mine) != 2 || len(avail) != 1 {
		t.Fatalf("rows mine %d avail %d, want 2 and 1", len(mine), len(avail))
	}
	// Connected sorts before paired.
	if got := mine[0].status.Text(); got != "Connected" {
		t.Errorf("first row status = %q, want the connected headset", got)
	}
	if got := mine[1].status.Text(); got != "Paired" {
		t.Errorf("second row status = %q", got)
	}
	if !visible(d.mySection) || !visible(d.availSection) || !visible(d.availCard) {
		t.Error("the device sections are hidden")
	}
	for _, empty := range []widget.Widget{d.emptyNoDevices, d.emptyOff, d.emptyNoAdapter, d.scanningHint} {
		if visible(empty) {
			t.Error("an empty state shows beside devices")
		}
	}
	// The description: the type, then the battery behind the separator.
	if got := plain(itemText(mine[0], "description")); got != "Headphones "+btDetailSeparator+" 64%" {
		t.Errorf("headset description = %q, want type and battery", got)
	}
	if got := plain(itemText(mine[1], "description")); strings.Contains(got, "%") {
		t.Errorf("a device without a battery reads %q", got)
	}
	// Available rows carry no status/actions slot and no hover swap.
	if avail[0].actions != nil || avail[0].hoverSwaps {
		t.Error("an available row has the my-device slot")
	}
}

func TestBtDropdownEmptyStates(t *testing.T) {
	d, src := newTestBtDropdown(t, bluetooth.State{})
	if !visible(d.emptyNoAdapter) || visible(d.toggle) || visible(d.scanBtn) {
		t.Error("no adapter: want the no-adapter state without switch or scan")
	}
	src.setState(bluetooth.State{Available: true, Primary: &bluetooth.Adapter{}})
	d.sync()
	if !visible(d.emptyOff) || visible(d.emptyNoAdapter) || !visible(d.toggle) || visible(d.scanBtn) {
		t.Error("adapter off: want the off state and the switch")
	}
	if d.toggle.On() {
		t.Error("the switch reads on for a powered-off adapter")
	}
	src.setState(btState())
	d.sync()
	if !visible(d.emptyNoDevices) || visible(d.emptyOff) || !visible(d.scanBtn) || !d.toggle.On() {
		t.Error("on with no devices: want the no-devices state and scan")
	}
	if d.headerIcon.Name() != "ld-bluetooth-symbolic" {
		t.Errorf("header icon = %q", d.headerIcon.Name())
	}
	// A state sync does not call the service back through the switch.
	if calls := src.Calls(); len(calls) != 0 {
		t.Errorf("syncs made calls: %v", calls)
	}
}

func TestBtDropdownScan(t *testing.T) {
	d, src := newTestBtDropdown(t, btState())
	d.scanBtn.OnClick()
	if !slices.Equal(src.Calls(), []string{"scan 30s"}) {
		t.Errorf("calls = %v, want a 30s timed discovery", src.Calls())
	}
	if !d.scanning || d.scanBtn.Enabled() || !widget.HasClass(d.scanBtn, "scanning") {
		t.Error("the scan button is not held in its scanning state")
	}
	if !visible(d.scanningHint) || !visible(d.availSection) || visible(d.availCard) || visible(d.emptyNoDevices) {
		t.Error("scanning with no devices: want the no-new hint under the available label")
	}
	// A second press while scanning does nothing.
	d.onScan()
	if len(src.Calls()) != 1 {
		t.Error("a second scan started while scanning")
	}
	d.finishScan(d.scanGen)
	if d.scanning || !d.scanBtn.Enabled() || widget.HasClass(d.scanBtn, "scanning") {
		t.Error("the scan did not complete")
	}
}

// A section shows only with devices to list: no paired device hides
// My Devices, and a powered-off adapter hides both sections behind
// its empty state.
func TestBtDropdownSectionsFollowTheirDevices(t *testing.T) {
	st := btWorld()
	st.Devices = st.Devices[:1]
	d, src := newTestBtDropdown(t, st)
	if visible(d.mySection) || !visible(d.availSection) {
		t.Errorf("only an unpaired device: my devices %v, available %v; want only available", visible(d.mySection), visible(d.availSection))
	}
	off := btWorld()
	off.Enabled = false
	src.setState(off)
	d.sync()
	if visible(d.mySection) || visible(d.availSection) || !visible(d.emptyOff) {
		t.Errorf("powered off: my devices %v, available %v, off state %v; want only the off state", visible(d.mySection), visible(d.availSection), visible(d.emptyOff))
	}
}

func TestBtDropdownToggleOffCancelsTheScan(t *testing.T) {
	d, src := newTestBtDropdown(t, btState())
	d.onScan()
	gen := d.scanGen
	d.toggle.SetOn(false) // the user flips the switch
	if !slices.Contains(src.Calls(), "disable") {
		t.Errorf("calls = %v, want disable", src.Calls())
	}
	if d.scanning || d.enabled {
		t.Error("toggle off left the scan or the enabled view")
	}
	if !visible(d.emptyOff) {
		t.Error("toggle off does not show the off state")
	}
	// A sync with the service still reporting on keeps the switch where
	// the user put it until the service's value changes.
	d.sync()
	if d.toggle.On() {
		t.Error("a sync with an unchanged service value flipped the switch back")
	}
	src.setState(bluetooth.State{Available: true, Primary: &bluetooth.Adapter{}})
	d.sync()
	src.setState(btState())
	d.sync()
	if !d.toggle.On() {
		t.Error("a service change to on does not show")
	}
	// The old scan's timer no longer ends anything.
	d.onScan()
	d.finishScan(gen)
	if !d.scanning {
		t.Error("a stale scan timer ended the new scan")
	}
	d.toggle.SetOn(true)
	if slices.Contains(src.Calls(), "enable") {
		t.Error("setting the switch to its current state called the service")
	}
}

func TestBtDropdownConnectAvailableDevice(t *testing.T) {
	d, src := newTestBtDropdown(t, btWorld())
	rowNamed(t, d, "Speaker").clickRow(t)
	want := []string{"connect " + string(pathSpeaker), "trust-paired " + string(pathSpeaker)}
	if !slices.Equal(src.Calls(), want) {
		t.Fatalf("calls = %v, want %v", src.Calls(), want)
	}
	row := rowNamed(t, d, "Speaker")
	if !row.pending || row.status == nil || row.status.Text() != "Connecting..." || !row.HasClass("pending") {
		t.Error("a connecting row does not show its pending state")
	}
	// A click while pending does nothing.
	row.clickRow(t)
	if len(src.Calls()) != 2 {
		t.Error("a pending row took a second click")
	}
	// The state catching up settles it.
	st := btWorld()
	st.Devices[0].Connected, st.Devices[0].Paired = true, true
	src.setState(st)
	d.sync()
	if row := rowNamed(t, d, "Speaker"); row.pending || row.status.Text() != "Connected" {
		t.Error("the completed connect is still pending")
	}
}

func TestBtDropdownFailedConnectClearsPending(t *testing.T) {
	d, src := newTestBtDropdown(t, btWorld())
	src.fail["connect"] = errors.New("page timeout")
	rowNamed(t, d, "Speaker").clickRow(t)
	if slices.Contains(src.Calls(), "trust-paired "+string(pathSpeaker)) {
		t.Error("a failed connect still trusted the device")
	}
	if row := rowNamed(t, d, "Speaker"); row.pending {
		t.Error("a failed connect left the row pending")
	}
}

func TestBtDropdownDisconnectAndForget(t *testing.T) {
	d, src := newTestBtDropdown(t, btWorld())
	rowNamed(t, d, "Headset").clickRow(t)
	if got := src.Calls(); len(got) != 1 || got[0] != "disconnect "+string(pathHeadset) {
		t.Fatalf("calls = %v, want a disconnect", got)
	}
	if got := rowNamed(t, d, "Headset").status.Text(); got != "Disconnecting..." {
		t.Errorf("status = %q", got)
	}
	mouse := rowNamed(t, d, "Mouse")
	forget := mouse.actions.Children()[1].(*widget.Button)
	forget.OnClick()
	if got := src.Calls(); len(got) != 2 || got[1] != "forget "+string(pathMouse) {
		t.Fatalf("calls = %v, want a forget", got)
	}
	if got := rowNamed(t, d, "Mouse").status.Text(); got != "Removing..." {
		t.Errorf("status = %q", got)
	}
	// The row vanishing completes the forget.
	st := btWorld()
	st.Devices = st.Devices[:2]
	src.setState(st)
	d.sync()
	if _, ok := d.pending[pathMouse]; ok {
		t.Error("the forgotten device is still pending")
	}
}

func TestBtDeviceRowHoverSwapsStatusForActions(t *testing.T) {
	d, _ := newTestBtDropdown(t, btWorld())
	row := rowNamed(t, d, "Headset")
	if row.slot.Visible() != btSlotStatus || !visible(row.status) {
		t.Fatal("at rest the row shows its status")
	}
	// One pointer: the router moves over the row, onto a button, then
	// off the row onto the list's empty space below it, as in the
	// dropdown, where the row is never the surface's root.
	list := widget.NewBox(widget.Column, 0, 0).Append(row, false)
	widget.SizeWithin(list, widget.Size{W: 400, H: 200})
	list.Arrange(render.Rect{X: 0, Y: 0, W: 400, H: 200})
	router := &widget.Router{Root: list}
	router.Move(widget.Point{X: 10, Y: 10})
	if row.slot.Visible() != btSlotActions || !row.slot.Switching() {
		t.Fatal("hovered, the row crossfades to its actions")
	}
	toggle := row.actions.Children()[0].(*widget.Button)
	b := toggle.Bounds()
	// Moving onto a button keeps the actions: the button is a
	// descendant, so the row's hover-within holds.
	router.Move(widget.Point{X: b.X + b.W/2, Y: b.Y + b.H/2})
	if row.slot.Visible() != btSlotActions {
		t.Error("moving onto an action hid the actions")
	}
	router.Move(widget.Point{X: 10, Y: 190})
	if row.slot.Visible() != btSlotStatus || !visible(row.status) {
		t.Error("leaving the row keeps the actions")
	}
	// Hovering an available row never swaps.
	speaker := rowNamed(t, d, "Speaker")
	widget.SizeWithin(speaker, widget.Size{W: 400, H: 200})
	speaker.Arrange(render.Rect{X: 0, Y: 0, W: 400, H: 40})
	(&widget.Router{Root: speaker}).Move(widget.Point{X: 10, Y: 10})
	if speaker.actions != nil {
		t.Error("an available row grew actions")
	}
}

// clickRow presses and releases through a Router, the real input path:
// the hit lands inside the row and the row's SetOnClickWithin hook
// hears it.
func (r *btDeviceRow) clickRow(t *testing.T) {
	t.Helper()
	widget.SizeWithin(r, widget.Size{W: 400, H: 200})
	r.Arrange(render.Rect{X: 0, Y: 0, W: 400, H: 40})
	router := &widget.Router{Root: r}
	p := widget.Point{X: 10, Y: 10}
	router.Move(p)
	router.Press(widget.BTNLeft, p, 0)
	router.Release(widget.BTNLeft, p)
}

func TestBtPairingConfirmation(t *testing.T) {
	st := btWorld()
	st.Pairing = bluetooth.RequestConfirmation{Device: pathHeadset, Passkey: 42}
	d, src := newTestBtDropdown(t, st)
	c := d.card
	if !visible(c.root) || c.variant != variantRequestConfirmation {
		t.Fatal("the card does not show the confirmation")
	}
	if name, kind := itemText(c.device, "title"), itemText(c.device, "description"); name != "Headset" || kind != "Headphones" || c.icon.Name() != "ld-headphones-symbolic" {
		t.Errorf("header = %q %q %q", name, kind, c.icon.Name())
	}
	if c.pinCode[1].Text() != "000042" {
		t.Errorf("code = %q, want the zero-padded passkey", c.pinCode[1].Text())
	}
	if c.leftLabel.Text() != "Reject" || c.rightLabel.Text() != "Confirm" || !visible(c.right) {
		t.Errorf("actions = %q/%q", c.leftLabel.Text(), c.rightLabel.Text())
	}
	c.right.OnClick()
	if !slices.Equal(src.Calls(), []string{"confirmation true"}) {
		t.Errorf("calls = %v", src.Calls())
	}
	if visible(c.root) {
		t.Error("an answered card stays")
	}
}

func TestBtPairingRejectAndClose(t *testing.T) {
	for _, c := range []struct {
		req  bluetooth.PairingRequest
		want string
	}{
		{bluetooth.RequestConfirmation{Device: pathHeadset, Passkey: 1}, "confirmation false"},
		{bluetooth.RequestAuthorization{Device: pathHeadset}, "authorization false"},
		{bluetooth.RequestServiceAuthorization{Device: pathHeadset, UUID: "0000110b-0000-1000-8000-00805f9b34fb"}, "service-authorization false"},
		{bluetooth.RequestPasskey{Device: pathHeadset}, "cancel"},
		{bluetooth.RequestPinCode{Device: pathHeadset}, "cancel"},
		{bluetooth.DisplayPinCode{Device: pathHeadset, PinCode: "123456"}, "cancel"},
	} {
		st := btWorld()
		st.Pairing = c.req
		d, src := newTestBtDropdown(t, st)
		// The left action rejects the yes/no prompts, cancels the rest.
		d.card.emit(d.card.rejectOutput())
		if got := src.Calls(); len(got) != 1 || got[0] != c.want {
			t.Errorf("%T reject: calls = %v, want %q", c.req, got, c.want)
		}
		// The close button always cancels.
		d2, src2 := newTestBtDropdown(t, st)
		d2.card.emit(pairingOutput{kind: outputCancelled})
		if got := src2.Calls(); len(got) != 1 || got[0] != "cancel" {
			t.Errorf("%T close: calls = %v", c.req, got)
		}
	}
}

func TestBtPairingPasskeyEntry(t *testing.T) {
	st := btWorld()
	st.Pairing = bluetooth.RequestPasskey{Device: pathSpeaker}
	d, src := newTestBtDropdown(t, st)
	c := d.card
	if c.rightLabel.Text() != "Pair" || c.leftLabel.Text() != "Reject" {
		t.Errorf("actions = %q/%q", c.leftLabel.Text(), c.rightLabel.Text())
	}
	// Nothing typed: nothing to send.
	c.right.OnClick()
	if len(src.Calls()) != 0 {
		t.Errorf("an empty passkey was sent: %v", src.Calls())
	}
	// The code input takes digits only, six at most.
	c.passkey.SetCode("12a3456789")
	if got := c.passkey.Code(); got != "123456" {
		t.Errorf("passkey = %q, want the first six digits", got)
	}
	c.right.OnClick()
	if !slices.Equal(src.Calls(), []string{"passkey 123456"}) {
		t.Errorf("calls = %v", src.Calls())
	}
	// A partial passkey still submits, the Rust build_confirm_output's
	// PinSubmitted(passkey) over whatever the boxes spell — its provider
	// does the refusing.
	st.Pairing = bluetooth.RequestPasskey{Device: pathSpeaker}
	src.setState(st)
	d.sync()
	c = d.card
	c.passkey.SetCode("9")
	c.right.OnClick()
	if !slices.Contains(src.Calls(), "passkey 9") {
		t.Errorf("a partial passkey is not submitted: %v", src.Calls())
	}
}

func TestBtPairingLegacyPin(t *testing.T) {
	st := btWorld()
	st.Pairing = bluetooth.RequestPinCode{Device: pathSpeaker}
	d, src := newTestBtDropdown(t, st)
	c := d.card
	if c.legacyPinEntry.Placeholder() != "PIN" {
		t.Errorf("placeholder = %q", c.legacyPinEntry.Placeholder())
	}
	c.legacyPinEntry.SetText("00000000000000000000")
	if got := c.legacyPinEntry.Text(); len(got) != 16 {
		t.Errorf("entry = %q, want 16 characters at most", got)
	}
	c.legacyPinEntry.SetText("0000")
	c.right.OnClick()
	if !slices.Equal(src.Calls(), []string{"pin 0000"}) {
		t.Errorf("calls = %v", src.Calls())
	}
}

func TestBtPairingServiceAuthorizationTrusts(t *testing.T) {
	st := btWorld()
	st.Pairing = bluetooth.RequestServiceAuthorization{Device: pathHeadset, UUID: "0000111e-0000-1000-8000-00805f9b34fb"}
	d, src := newTestBtDropdown(t, st)
	if got := d.card.serviceName.Text(); got != "Hands-Free" {
		t.Errorf("service = %q", got)
	}
	if d.card.rightLabel.Text() != "Allow" || d.card.leftLabel.Text() != "Deny" {
		t.Error("service authorization labels")
	}
	d.card.right.OnClick()
	want := []string{"service-authorization true", "trust-paired " + string(pathHeadset)}
	if !slices.Equal(src.Calls(), want) {
		t.Errorf("calls = %v, want %v", src.Calls(), want)
	}
}

func TestBtPairingAuthorizationAllowDoesNotTrust(t *testing.T) {
	st := btWorld()
	st.Pairing = bluetooth.RequestAuthorization{Device: pathSpeaker}
	d, src := newTestBtDropdown(t, st)
	d.card.right.OnClick()
	if !slices.Equal(src.Calls(), []string{"authorization true"}) {
		t.Errorf("calls = %v", src.Calls())
	}
}

func TestBtPairingDisplayRequests(t *testing.T) {
	st := btWorld()
	st.Pairing = bluetooth.DisplayPasskey{Device: pathHeadset, Passkey: 7, Entered: 2}
	d, src := newTestBtDropdown(t, st)
	c := d.card
	if visible(c.right) || c.leftLabel.Text() != "Cancel" {
		t.Error("a display request offers a confirm")
	}
	if plain(c.progress.Text()) != "2 of 6 digits entered" || c.pinCode[2].Text() != "000007" {
		t.Errorf("progress %q code %q", c.progress.Text(), c.pinCode[2].Text())
	}
	// The typing progress updates in place.
	st.Pairing = bluetooth.DisplayPasskey{Device: pathHeadset, Passkey: 7, Entered: 5}
	src.setState(st)
	d.sync()
	if plain(c.progress.Text()) != "5 of 6 digits entered" {
		t.Errorf("progress = %q", c.progress.Text())
	}
	// BlueZ withdrawing it clears the card.
	st.Pairing = nil
	src.setState(st)
	d.sync()
	if visible(c.root) {
		t.Error("a withdrawn request stays on the card")
	}
}

func TestBtPairingUnknownDevice(t *testing.T) {
	st := btWorld()
	st.Pairing = bluetooth.RequestAuthorization{Device: "/org/bluez/hci0/dev_NEW"}
	d, _ := newTestBtDropdown(t, st)
	if name, kind := itemText(d.card.device, "title"), itemText(d.card.device, "description"); name != "-" || kind != "Bluetooth device" {
		t.Errorf("unknown device header = %q %q", name, kind)
	}
}

func TestBtPairingRefusedAnswerShowsTheRequestAgain(t *testing.T) {
	st := btWorld()
	st.Pairing = bluetooth.RequestPinCode{Device: pathSpeaker}
	d, src := newTestBtDropdown(t, st)
	src.fail["pin"] = errors.New("bluetooth: cannot provide pin: 0 characters")
	d.card.right.OnClick()
	if !visible(d.card.root) || d.card.variant != variantRequestPinCode {
		t.Error("a refused answer hid the still-pending request")
	}
}

func TestBtDropdownClosedStopsFollowing(t *testing.T) {
	src := newFakeBluetooth(btWorld())
	ctx := newTestContext(t, config.Defaults())
	ctx.Bluetooth = src
	content := bluetoothDropdown(ctx)
	closer, ok := content.(dropdownCloser)
	if !ok {
		t.Fatal("the bluetooth dropdown does not release its subscription")
	}
	if !btDropdownVisible(src) {
		t.Error("an open dropdown reads as closed")
	}
	closer.dropdownClosed()
	if btDropdownVisible(src) {
		t.Error("a closed dropdown reads as open")
	}
	// Closing twice does not underflow.
	closer.dropdownClosed()
	btDropdownOpened(src)
	if !btDropdownVisible(src) {
		t.Error("the open count underflowed")
	}
	btDropdownClosed(src)
}

func TestBtDropdownWithoutServiceShowsNoAdapter(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	content := bluetoothDropdown(ctx)
	// The full shell builds without a service; closing it is a no-op,
	// not a panic, and nothing subscribed.
	if c, ok := content.(dropdownCloser); ok {
		c.dropdownClosed()
	}
	if findBox(content, "dropdown-header") == nil {
		t.Error("the no-adapter dropdown dropped the header")
	}
}

// fakeNotifier records notifications and answers Ask with key.
type fakeNotifier struct {
	sent, asked []string
	key         string
	answered    bool
}

func (f *fakeNotifier) Send(_ context.Context, _, summary, body, _ string) (uint32, error) {
	f.sent = append(f.sent, summary+": "+body)
	return 1, nil
}

func (f *fakeNotifier) Ask(_ context.Context, _, _, body, _ string, actions []desktopnotify.Action, timeout time.Duration) (string, bool, error) {
	f.asked = append(f.asked, body)
	if len(actions) != 2 || actions[0].Key != "deny" || actions[1].Key != "allow" || timeout != 60*time.Second {
		return "", false, errors.New("unexpected actions")
	}
	return f.key, f.answered, nil
}

func newTestNotifier(st bluetooth.State, key string, answered bool) (*btPairingNotifier, *fakeBluetooth, *fakeNotifier) {
	src := newFakeBluetooth(st)
	sender := &fakeNotifier{key: key, answered: answered}
	return &btPairingNotifier{src: src, sender: sender, spawn: func(fn func()) { fn() }}, src, sender
}

func TestBtNotifierAsksForConfirmation(t *testing.T) {
	st := btWorld()
	st.Pairing = bluetooth.RequestConfirmation{Device: pathHeadset, Passkey: 99}
	n, src, sender := newTestNotifier(st, "allow", true)
	n.check()
	want := "Headset wants to pair with passkey 000099 — open the Bluetooth menu to respond"
	if len(sender.asked) != 1 || plain(sender.asked[0]) != want {
		t.Fatalf("asked = %v", sender.asked)
	}
	wantCalls := []string{"confirmation true", "trust-paired " + string(pathHeadset)}
	if !slices.Equal(src.Calls(), wantCalls) {
		t.Errorf("calls = %v, want %v", src.Calls(), wantCalls)
	}
	// The same request does not notify twice.
	n.check()
	if len(sender.asked) != 1 {
		t.Error("an unchanged request notified again")
	}
}

func TestBtNotifierDenyDoesNotTrust(t *testing.T) {
	st := btWorld()
	st.Pairing = bluetooth.RequestServiceAuthorization{Device: pathHeadset, UUID: "x"}
	n, src, _ := newTestNotifier(st, "deny", true)
	n.check()
	if !slices.Equal(src.Calls(), []string{"service-authorization false"}) {
		t.Errorf("calls = %v", src.Calls())
	}
}

func TestBtNotifierIgnoredNotificationAnswersNothing(t *testing.T) {
	st := btWorld()
	st.Pairing = bluetooth.RequestAuthorization{Device: pathSpeaker}
	n, src, sender := newTestNotifier(st, "", false)
	n.check()
	if len(sender.asked) != 1 || len(src.Calls()) != 0 {
		t.Errorf("asked %v calls %v, want one ask and no answer", sender.asked, src.Calls())
	}
}

func TestBtNotifierLinksInputPrompts(t *testing.T) {
	st := btWorld()
	st.Pairing = bluetooth.RequestPasskey{Device: "/org/bluez/hci0/dev_NEW"}
	n, src, sender := newTestNotifier(st, "allow", true)
	n.check()
	if len(sender.asked) != 0 || len(sender.sent) != 1 {
		t.Fatalf("sent %v asked %v, want one plain notification", sender.sent, sender.asked)
	}
	if plain(sender.sent[0]) != "Bluetooth pairing request: New device wants to connect — open the Bluetooth menu to respond" {
		t.Errorf("notification = %q", sender.sent[0])
	}
	if len(src.Calls()) != 0 {
		t.Error("a link-only notification answered the agent")
	}
}

func TestBtNotifierQuietWhileTheDropdownIsOpen(t *testing.T) {
	st := btWorld()
	st.Pairing = bluetooth.RequestAuthorization{Device: pathSpeaker}
	n, src, sender := newTestNotifier(st, "allow", true)
	btDropdownOpened(src)
	defer btDropdownClosed(src)
	n.check()
	if len(sender.asked)+len(sender.sent) != 0 {
		t.Error("notified while the dropdown shows the request")
	}
}

// findBox walks the tree for the first box carrying class.
func findBox(w widget.Widget, class string) *widget.Box {
	var found *widget.Box
	walkTree(w, func(k widget.Widget) bool {
		if found != nil {
			return false
		}
		if b, ok := k.(*widget.Box); ok && widget.HasClass(b, class) {
			found = b
		}
		return found == nil
	})
	return found
}

// findButton walks the tree for the first button carrying class.
func findButton(w widget.Widget, class string) *widget.Button {
	var found *widget.Button
	walkTree(w, func(k widget.Widget) bool {
		if found != nil {
			return false
		}
		if b, ok := k.(*widget.Button); ok && widget.HasClass(b, class) {
			found = b
		}
		return found == nil
	})
	return found
}

// The no-adapter state is the whole dropdown shell — the header stays
// above the empty state (mod.rs:73-147,303-326); the early return of a
// bare empty state is the regression.
func TestBtNoAdapterDropdownKeepsTheHeader(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	content := bluetoothDropdown(ctx)
	if c, ok := content.(dropdownCloser); ok {
		c.dropdownClosed()
	}
	if findBox(content, "dropdown-header") == nil {
		t.Error("the no-adapter dropdown dropped the header")
	}
	empty := findByClass(content, "empty-state")
	if empty == nil {
		t.Fatal("the no-adapter dropdown lost its empty state")
	}
	if !visible(empty) {
		t.Error("the no-adapter empty state is hidden")
	}
	// The shell is the dropdown frame, not a bare box.
	if root, ok := content.(*btDropdownRoot); !ok || !root.HasClass("bluetooth-dropdown") {
		t.Fatal("the no-adapter state is not the dropdown frame")
	}
	// A live dropdown with an adapter keeps the same header; the
	// no-adapter state never grows device rows.
	d, _ := newTestBtDropdown(t, btWorld())
	if findBox(d.root, "dropdown-header") == nil {
		t.Error("the live dropdown lost its header")
	}
	if len(treetest.WithClass(content, "bluetooth-device")) != 0 {
		t.Error("the no-adapter state shows a device row")
	}
}

// The passkey prompt is lys-ui's code input of six digit slots
// (pairing_card/mod.rs:147-190), which only the RequestPasskey variant
// shows; the legacy PIN is a plain entry.
func TestBtPairingCardPasskeyInput(t *testing.T) {
	st := btWorld()
	st.Pairing = bluetooth.RequestPasskey{Device: pathSpeaker}
	d, src := newTestBtDropdown(t, st)
	c := d.card
	if c.passkey.Len() != passkeyTotal || !visible(c.passkey) {
		t.Fatalf("code input = %d slots (visible %v), want %d showing", c.passkey.Len(), visible(c.passkey), passkeyTotal)
	}
	if visible(c.legacyPinEntry) {
		t.Error("the legacy PIN entry shows for a passkey prompt")
	}
	c.passkey.SetCode("12")
	st.Pairing = bluetooth.RequestConfirmation{Device: pathSpeaker, Passkey: 1}
	src.setState(st)
	d.sync()
	if visible(c.passkey) {
		t.Error("the code input shows for a confirmation prompt")
	}
	st.Pairing = bluetooth.RequestPasskey{Device: pathSpeaker}
	src.setState(st)
	d.sync()
	if !visible(c.passkey) || c.passkey.Code() != "" {
		t.Errorf("a new passkey prompt: visible %v, code %q, want an empty input showing", visible(c.passkey), c.passkey.Code())
	}
}

// fluent renders placeables wrapped in Unicode isolation marks (the
// Rust fluent-rs defaults to isolating): tests compare the plain text.
func plain(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\u2068' || r == '\u2069' {
			return -1
		}
		return r
	}, s)
}

// Icon buttons are lys-ui's ghost icon square (mod.rs:95-121,
// pairing_card/mod.rs:94-100).
func TestBtGhostClassesAreExact(t *testing.T) {
	d, _ := newTestBtDropdown(t, btWorld())
	for _, class := range []string{css.Ghost, css.Icon, "bluetooth-scan-btn"} {
		if !widget.HasClass(d.scanBtn, class) {
			t.Errorf("the scan button lacks %s", class)
		}
	}
	closeBtn := findButton(d.card.root, "form-close")
	if closeBtn == nil {
		t.Fatal("no pairing close button")
	}
	if !widget.HasClass(closeBtn, css.Ghost) || !widget.HasClass(closeBtn, css.Icon) {
		t.Error("the close button is not a ghost icon button")
	}
	// The row actions are small ghost icon buttons named by their
	// tooltips, so the slot they size stays narrow.
	mouse := rowNamed(t, d, "Mouse")
	for i, want := range []string{btText("dropdown-bluetooth-connect"), btText("dropdown-bluetooth-forget")} {
		b := mouse.actions.Children()[i].(*widget.Button)
		if !widget.HasClass(b, css.Ghost) || !widget.HasClass(b, css.Icon) || !widget.HasClass(b, css.Small) {
			t.Errorf("row action %d is not a small ghost icon button", i)
		}
		if b.TooltipText() != want {
			t.Errorf("row action %d tooltip = %q, want %q", i, b.TooltipText(), want)
		}
	}
	if !widget.HasClass(mouse.actions.Children()[1], "bluetooth-forget") {
		t.Error("the forget action lacks its class")
	}
}

// The device rows live in the panel: each list sits in its card, so the
// rows are laid out and painted, not just built.
func TestBluetoothRowsAreInThePanel(t *testing.T) {
	d, _ := newTestBtDropdown(t, btWorld())
	widget.SizeWithin(d.root, widget.Size{W: 420, H: 900})
	d.root.Arrange(render.Rect{W: 420, H: 900})
	inTree := map[widget.Widget]bool{}
	treetest.Walk(d.root, func(w widget.Widget) { inTree[w] = true })
	mine, avail := rows(d.myList), rows(d.availList)
	if len(mine) == 0 || len(avail) == 0 {
		t.Fatalf("rows: %d mine, %d available", len(mine), len(avail))
	}
	for _, r := range append(mine, avail...) {
		if !inTree[r] || r.Bounds().W == 0 || r.Bounds().H == 0 {
			t.Errorf("row at %+v, in the panel %v: want it laid out inside the panel", r.Bounds(), inTree[r])
		}
	}
}

// At rest the status hugs the row's end: the slot is as wide as the
// actions page, and the status page sits at its end (the Rust status
// box's halign End), so a short "Paired" never floats mid-row.
func TestBtDeviceStatusSitsAtTheRowsEnd(t *testing.T) {
	d, _ := newTestBtDropdown(t, btWorld())
	row := rowNamed(t, d, "Mouse")
	widget.SizeWithin(row, widget.Size{W: 400, H: 200})
	row.Arrange(render.Rect{X: 0, Y: 0, W: 400, H: 40})
	slot, status := boundsOf(row.slot), boundsOf(row.status)
	if status.W >= slot.W {
		t.Fatalf("status %+v fills its slot %+v: the test proves nothing", status, slot)
	}
	if end, slotEnd := status.X+status.W, slot.X+slot.W; end != slotEnd {
		t.Errorf("the status ends at %d, want the slot's end %d", end, slotEnd)
	}
	if d := midY(status) - midY(slot); d < -1 || d > 1 {
		t.Errorf("the status sits %dpx off the slot's middle", d)
	}
}

// A device row fits a narrow dropdown: the name and detail ellipsize
// on one line instead of holding the row open, so the status slot
// stays inside it.
func TestBtRowFitsANarrowDropdown(t *testing.T) {
	d, _ := newTestBtDropdown(t, btWorld())
	r := rows(d.myList)[0]
	styledContext(t, d.ctx.Config).Theme.Attach(r)
	const w = 280
	sz := widget.SizeWithin(r, widget.Size{W: w, H: 400})
	r.Arrange(render.Rect{W: w, H: sz.H})
	if end := r.slot.Bounds().X + r.slot.Bounds().W; end > w {
		t.Errorf("the status slot ends at %d, past the %d row", end, w)
	}
	for _, part := range treetest.WithClass(r, "title") {
		l := part.(*widget.Label)
		if one := widget.SizeWithinAsChild(l, widget.Size{W: 1000, H: 400}).H; l.Bounds().H != one {
			t.Errorf("the name spans %dpx, want one %dpx line", l.Bounds().H, one)
		}
	}
}

// At a dropdown's usual width a device's detail shows in full: the
// slot the actions reserve is two small icon buttons, not two text
// buttons' width taken from the text.
func TestBtRowDetailFitsTheUsualWidth(t *testing.T) {
	d, _ := newTestBtDropdown(t, btWorld())
	r := rowNamed(t, d, "Headset")
	styledContext(t, d.ctx.Config).Theme.Attach(r)
	const w = 360
	sz := widget.SizeWithin(r, widget.Size{W: w, H: 400})
	r.Arrange(render.Rect{W: w, H: sz.H})
	desc := treetest.WithClass(r, "description")[0].(*widget.Label)
	if natural := widget.SizeWithinAsChild(desc, widget.Size{W: 2000, H: 400}).W; desc.Bounds().W < natural {
		t.Errorf("the detail %q gets %dpx of its %dpx", desc.Text(), desc.Bounds().W, natural)
	}
}

// A connected device that reports its battery wears it as a ring
// around its icon; one without a battery, or not connected, keeps the
// plain icon.
func TestConnectedDeviceBatteryIsARing(t *testing.T) {
	d, _ := newTestBtDropdown(t, btWorld())
	headset := treetest.All[*glyphRing](rowNamed(t, d, "Headset"))
	if len(headset) != 1 {
		t.Fatalf("the connected headset has %d battery rings, want one", len(headset))
	}
	if got := headset[0].Value(); got != 0.64 || !headset[0].glyph.HasClass("bluetooth-icon") || headset[0].glyph.Name() == "" {
		t.Errorf("the headset's ring is at %v around %q, want 0.64 around its icon", got, headset[0].glyph.Name())
	}
	for _, name := range []string{"Mouse", "Speaker"} {
		row := rowNamed(t, d, name)
		if rings := treetest.All[*glyphRing](row); len(rings) != 0 {
			t.Errorf("%s, with no battery or not connected, has a ring", name)
		}
		if icons := treetest.WithClass(row, "bluetooth-icon"); len(icons) != 1 {
			t.Errorf("%s shows %d device icons, want its plain icon", name, len(icons))
		}
	}
}
