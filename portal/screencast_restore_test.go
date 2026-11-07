package portal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stubbedev/lys-lib/capture"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// grantOf is a ScreenCast grant keeping targets by their names alone.
func grantOf(targets ...sharepicker.Target) restoreGrant {
	g := restoreGrant{}
	for _, t := range targets {
		g.targets = append(g.targets, restoreTarget{picked: t})
	}
	return g
}

var (
	monitorA = displayID{pnp: "DEL", model: "U2720Q", serial: "ABC123"}
	monitorB = displayID{pnp: "GSM", model: "LG HDR 4K", serial: "XYZ789"}
	notesWin = restoreTarget{picked: sharepicker.Target{Kind: sharepicker.TargetWindow, Name: "old-1"}, appID: "org.app", title: "Notes"}
)

// restoreRig is a ScreenCast session of every source type replaying
// token, over these windows and displays; it returns what Start
// started, whether the picker asked, and the reissued grant.
func restoreRig(t *testing.T, token restoreToken, windows []capture.Toplevel, displays map[string]displayID) (started []string, asked bool, reissued restoreGrant) {
	t.Helper()
	r, f := castRig(t)
	f.toplevels.Store(windows)
	f.displays.Store(displays)
	p := serveSharePicker(t, r, "r/screen:DP-1")
	r.castSession(t, Vardict{
		"restore_data": dbus.MakeVariant(token), "persist_mode": dbus.MakeVariant(uint32(2)),
		"types": dbus.MakeVariant(sourceMonitor | sourceWindow), "multiple": dbus.MakeVariant(true),
	})
	code, res := r.castStart(t)
	if code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	reissued, ok := decodeRestore(res["restore_data"])
	if !ok {
		t.Fatalf("no token reissued: %v", res["restore_data"])
	}
	return f.started.Load(), len(p.asked.Load()) > 0, reissued
}

// A window token restores without the picker to the app's window with
// the stored title, whatever its new identifier, and the reissued token
// keeps the new identifier with the app id and title.
func TestRestoreFindsTheWindowByAppAndTitle(t *testing.T) {
	windows := []capture.Toplevel{
		{Identifier: "new-5", AppID: "org.app", Title: "Scratch"},
		{Identifier: "new-7", AppID: "org.app", Title: "Notes"},
		{Identifier: "new-9", AppID: "org.other", Title: "Notes"},
	}
	started, asked, reissued := restoreRig(t, encodeRestore(restoreGrant{targets: []restoreTarget{notesWin}}), windows, nil)
	if asked || !slices.Equal(started, []string{"window:new-7"}) {
		t.Fatalf("started %v, picker asked %v: want new-7 without asking", started, asked)
	}
	want := restoreTarget{picked: sharepicker.Target{Kind: sharepicker.TargetWindow, Name: "new-7"}, appID: "org.app", title: "Notes"}
	if len(reissued.targets) != 1 || reissued.targets[0] != want {
		t.Errorf("reissued %+v, want %+v", reissued.targets, want)
	}
}

// The app's only window restores even under another title; the same
// identifier wins over every other match.
func TestRestoreFallsBackToTheAppsOnlyWindow(t *testing.T) {
	started, asked, _ := restoreRig(t, encodeRestore(restoreGrant{targets: []restoreTarget{notesWin}}),
		[]capture.Toplevel{{Identifier: "new-5", AppID: "org.app", Title: "Notes — edited"}, {Identifier: "x", AppID: "org.other", Title: "Notes"}}, nil)
	if asked || !slices.Equal(started, []string{"window:new-5"}) {
		t.Errorf("only window: started %v, asked %v", started, asked)
	}
	started, asked, _ = restoreRig(t, encodeRestore(restoreGrant{targets: []restoreTarget{notesWin}}),
		[]capture.Toplevel{{Identifier: "new-7", AppID: "org.app", Title: "Notes"}, {Identifier: "old-1", AppID: "org.app", Title: "Renamed"}}, nil)
	if asked || !slices.Equal(started, []string{"window:old-1"}) {
		t.Errorf("same identifier: started %v, asked %v", started, asked)
	}
}

// Two windows of the app, neither with the stored title, are not
// guessed between: the picker asks. So does a window that is gone with
// no window of its app left, and two windows that both carry the title.
func TestRestoreAsksForAnAmbiguousWindow(t *testing.T) {
	for name, windows := range map[string][]capture.Toplevel{
		"no title matches": {{Identifier: "a", AppID: "org.app", Title: "One"}, {Identifier: "b", AppID: "org.app", Title: "Two"}},
		"two titles match": {{Identifier: "a", AppID: "org.app", Title: "Notes"}, {Identifier: "b", AppID: "org.app", Title: "Notes"}},
		"app gone":         {{Identifier: "c", AppID: "org.other", Title: "Notes"}},
	} {
		started, asked, _ := restoreRig(t, encodeRestore(restoreGrant{targets: []restoreTarget{notesWin}}), windows, nil)
		if !asked || !slices.Equal(started, []string{"screen:DP-1"}) {
			t.Errorf("%s: started %v, asked %v: want the picker's choice", name, started, asked)
		}
	}
}

// An output token follows its monitor's EDID from DP-1 to DP-2, and a
// region on it moves with it; another monitor now on DP-1 is not taken
// for it, so the picker asks.
func TestRestoreFindsTheOutputByEDID(t *testing.T) {
	screen := restoreTarget{picked: sharepicker.Target{Kind: sharepicker.TargetOutput, Name: "DP-1"}, display: monitorA}
	region := restoreTarget{picked: sharepicker.Target{Kind: sharepicker.TargetRegion, Name: "DP-1", X: 1, Y: 2, Width: 30, Height: 40}, display: monitorA}
	started, asked, reissued := restoreRig(t, encodeRestore(restoreGrant{targets: []restoreTarget{screen, region}}), nil,
		map[string]displayID{"DP-1": monitorB, "DP-2": monitorA})
	if asked || !slices.Equal(started, []string{"screen:DP-2", "region:DP-2@1,2,30,40"}) {
		t.Fatalf("moved monitor: started %v, asked %v", started, asked)
	}
	if len(reissued.targets) != 2 || reissued.targets[0].display != monitorA || reissued.targets[0].picked.Name != "DP-2" {
		t.Errorf("reissued %+v", reissued.targets)
	}
	started, asked, _ = restoreRig(t, encodeRestore(restoreGrant{targets: []restoreTarget{screen}}), nil, map[string]displayID{"DP-1": monitorB})
	if !asked || !slices.Equal(started, []string{"screen:DP-1"}) {
		t.Errorf("another monitor on DP-1: started %v, asked %v: want the picker", started, asked)
	}
}

// Without an EDID on either side (a nested or virtual output) an output
// restores by its connector.
func TestRestoreFallsBackToTheConnector(t *testing.T) {
	screen := restoreTarget{picked: sharepicker.Target{Kind: sharepicker.TargetOutput, Name: "DP-2"}, display: monitorA}
	started, asked, _ := restoreRig(t, encodeRestore(restoreGrant{targets: []restoreTarget{screen}}), nil, map[string]displayID{})
	if asked || !slices.Equal(started, []string{"screen:DP-2"}) {
		t.Errorf("no EDID now: started %v, asked %v", started, asked)
	}
	started, asked, _ = restoreRig(t, encodeRestore(grantOf(sharepicker.Target{Kind: sharepicker.TargetOutput, Name: "DP-2"})), nil, map[string]displayID{"DP-2": monitorB})
	if asked || !slices.Equal(started, []string{"screen:DP-2"}) {
		t.Errorf("no EDID kept: started %v, asked %v", started, asked)
	}
}

// A version 1 token still restores by identifier and connector.
func TestRestoreReadsVersion1Tokens(t *testing.T) {
	v1 := restoreToken{restoreVendor, restoreV1, dbus.MakeVariant("window:w1;region:DP-2@5,6,640,480")}
	started, asked, _ := restoreRig(t, v1, []capture.Toplevel{{Identifier: "w1", AppID: "org.app"}}, nil)
	if asked || !slices.Equal(started, []string{"window:w1", "region:DP-2@5,6,640,480"}) {
		t.Errorf("v1: started %v, asked %v", started, asked)
	}
	g, ok := decodeRestore(onWire(t, restoreToken{restoreVendor, restoreV1, dbus.MakeVariant("remote:3/screen:DP-1")}))
	if !ok || g.devices != 3 || !slices.Equal(payloads(pickedOf(g.targets)), []string{"screen:DP-1"}) {
		t.Errorf("v1 remote = %+v %v", g, ok)
	}
}

// A token of an unknown version, another vendor or another shape is
// none: the picker asks.
func TestRestoreIgnoresUnknownTokens(t *testing.T) {
	wire := encodeRestore(restoreGrant{targets: []restoreTarget{notesWin}}).Data
	v99 := restoreToken{restoreVendor, 99, wire}
	started, asked, _ := restoreRig(t, v99, []capture.Toplevel{{Identifier: "old-1", AppID: "org.app", Title: "Notes"}}, nil)
	if !asked || !slices.Equal(started, []string{"screen:DP-1"}) {
		t.Errorf("version 99: started %v, asked %v: want the picker", started, asked)
	}
	for name, v := range map[string]dbus.Variant{
		"foreign":       onWire(t, restoreToken{"other-portal", restoreV2, wire}),
		"not a struct":  dbus.MakeVariant(uint32(42)),
		"v2 string":     onWire(t, restoreToken{restoreVendor, restoreV2, dbus.MakeVariant("screen:DP-1")}),
		"v1 struct":     onWire(t, restoreToken{restoreVendor, restoreV1, wire}),
		"bad kind":      onWire(t, restoreToken{restoreVendor, restoreV2, dbus.MakeVariant(tokenGrant{Targets: []tokenTarget{{Kind: 7, Name: "x"}}})}),
		"empty name":    onWire(t, restoreToken{restoreVendor, restoreV2, dbus.MakeVariant(tokenGrant{Targets: []tokenTarget{{Kind: sharepicker.TargetWindow}}})}),
		"empty region":  onWire(t, restoreToken{restoreVendor, restoreV2, dbus.MakeVariant(tokenGrant{Targets: []tokenTarget{{Kind: sharepicker.TargetRegion, Name: "DP-1"}}})}),
		"devices":       onWire(t, restoreToken{restoreVendor, restoreV2, dbus.MakeVariant(tokenGrant{Devices: 64})}),
		"v1 bad remote": onWire(t, restoreToken{restoreVendor, restoreV1, dbus.MakeVariant("remote:0/screen:DP-1")}),
	} {
		if g, ok := decodeRestore(v); ok {
			t.Errorf("%s decoded: %+v", name, g)
		}
	}
}

// An ambiguous or gone source fails resolving with errAmbiguous or
// errGone; two outputs showing the same display resolve to the kept
// connector.
func TestResolveErrors(t *testing.T) {
	now := sources{
		windows:  []capture.Toplevel{{Identifier: "a", AppID: "org.app"}, {Identifier: "b", AppID: "org.app"}},
		outputs:  []string{"DP-1", "DP-2", "DP-3"},
		displays: map[string]displayID{"DP-1": monitorA, "DP-2": monitorA},
	}
	if _, err := resolve([]restoreTarget{notesWin}, now); !errors.Is(err, errAmbiguous) {
		t.Errorf("two windows: %v", err)
	}
	gone := restoreTarget{picked: sharepicker.Target{Kind: sharepicker.TargetOutput, Name: "DP-9"}, display: monitorB}
	if _, err := resolve([]restoreTarget{gone}, now); !errors.Is(err, errGone) {
		t.Errorf("gone output: %v", err)
	}
	twin := restoreTarget{picked: sharepicker.Target{Kind: sharepicker.TargetOutput, Name: "DP-2"}, display: monitorA}
	if got, err := resolve([]restoreTarget{twin}, now); err != nil || got[0].Name != "DP-2" {
		t.Errorf("twin monitors = %v, %v", got, err)
	}
	twin.picked.Name = "DP-3"
	if _, err := resolve([]restoreTarget{twin}, now); !errors.Is(err, errAmbiguous) {
		t.Errorf("twin monitors, neither on the kept connector: %v", err)
	}
}

// edidBlob is a valid EDID base block of PNP id pnp with a serial
// descriptor.
func edidBlob(pnp, serial string) []byte {
	b := make([]byte, 128)
	copy(b, []byte{0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0})
	id := uint16(pnp[0]-'A'+1)<<10 | uint16(pnp[1]-'A'+1)<<5 | uint16(pnp[2]-'A'+1)
	b[8], b[9] = byte(id>>8), byte(id)
	b[10], b[11] = 0x34, 0x12
	d := b[54:72]
	d[3] = 0xff
	copy(d[5:], serial+"\n")
	var sum byte
	for _, c := range b[:127] {
		sum += c
	}
	b[127] = -sum
	return b
}

// readDisplays reads each connected connector's EDID; a disconnected
// one (empty EDID), a non-connector, a name two cards share and a
// missing class directory name no display.
func TestReadDisplays(t *testing.T) {
	dir := t.TempDir()
	for entry, blob := range map[string][]byte{
		"card1-DP-1":     edidBlob("DEL", "ABC123"),
		"card1-HDMI-A-1": {},
		"card1-eDP-1":    edidBlob("BOE", "P1"),
		"card2-eDP-1":    edidBlob("AUO", "P2"),
		"renderD128":     edidBlob("GSM", "X"),
		"card1":          edidBlob("GSM", "Y"),
	} {
		if err := os.MkdirAll(filepath.Join(dir, entry), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, entry, "edid"), blob, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := readDisplays(dir)
	want := map[string]displayID{"DP-1": {pnp: "DEL", model: "0x1234", serial: "ABC123"}}
	if err != nil || len(got) != len(want) || got["DP-1"] != want["DP-1"] {
		t.Errorf("readDisplays = %v, %v, want %v", got, err, want)
	}
	if got, err := readDisplays(filepath.Join(dir, "absent")); err != nil || len(got) != 0 {
		t.Errorf("no DRM class = %v, %v, want none", got, err)
	}
}

// onWire is tok as a peer receives it: marshalled into a D-Bus message
// and read back, so its structs arrive as []any.
func onWire(t *testing.T, tok restoreToken) dbus.Variant {
	t.Helper()
	body := dbus.MakeVariant(tok)
	msg := &dbus.Message{Type: dbus.TypeSignal, Headers: map[dbus.HeaderField]dbus.Variant{
		dbus.FieldPath:      dbus.MakeVariant(dbus.ObjectPath("/token")),
		dbus.FieldInterface: dbus.MakeVariant("dev.stubbe.Token"),
		dbus.FieldMember:    dbus.MakeVariant("Token"),
		dbus.FieldSignature: dbus.MakeVariant(dbus.SignatureOf(body)),
	}, Body: []any{body}}
	var buf bytes.Buffer
	if err := msg.EncodeTo(&buf, binary.LittleEndian); err != nil {
		t.Fatal(err)
	}
	got, err := dbus.DecodeMessage(&buf)
	if err != nil {
		t.Fatal(err)
	}
	return got.Body[0].(dbus.Variant)
}
