package portal

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/edid"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/drmconn"
	"github.com/stubbedev/lys-shell/internal/version"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// Restore tokens are (suv): vendor, version, and the grant. Version 1
// kept the picker's reply as a string; version 2 keeps each source's
// identity, so a token outlives the window mapping and the connector it
// was issued for.
const (
	restoreVendor           = version.Name
	restoreV1        uint32 = 1
	restoreV2        uint32 = 2
	remoteTokenV1Tag        = "remote:"
)

// restoreToken is the (suv) restore_data.
type restoreToken struct {
	Vendor  string
	Version uint32
	Data    dbus.Variant
}

// displayID is a monitor's EDID identity: its PNP manufacturer, its
// model and its serial.
type displayID struct{ pnp, model, serial string }

func displayIDOf(i edid.Info) displayID { return displayID{i.PNP, i.Model, i.Serial} }

// restoreTarget is one source a token keeps: the target as it was
// picked, and what identifies it once that name is stale: a window's
// app id and title, an output's (or a region's output's) display.
type restoreTarget struct {
	picked       sharepicker.Target
	appID, title string
	display      displayID
}

// restoreGrant is what a token replays: the RemoteDesktop device types
// granted (none for a ScreenCast token) and the sources shared.
type restoreGrant struct {
	devices uint32
	targets []restoreTarget
}

// pickedOf is every kept target as it was picked.
func pickedOf(kept []restoreTarget) []sharepicker.Target {
	out := make([]sharepicker.Target, len(kept))
	for i, t := range kept {
		out[i] = t.picked
	}
	return out
}

// tokenGrant is a version 2 grant on the wire: (ua(yssssssiiii)).
type tokenGrant struct {
	Devices uint32
	Targets []tokenTarget
}

// tokenTarget is one source on the wire. Name is the connector or the
// toplevel identifier; the app id and title are a window's, the display
// an output's or a region's, the rectangle a region's.
type tokenTarget struct {
	Kind                sharepicker.TargetKind
	Name                string
	AppID, Title        string
	PNP, Model, Serial  string
	X, Y, Width, Height int32
}

func encodeRestore(g restoreGrant) restoreToken {
	wire := tokenGrant{Devices: g.devices, Targets: make([]tokenTarget, len(g.targets))}
	for i, t := range g.targets {
		wire.Targets[i] = tokenTarget{
			Kind: t.picked.Kind, Name: t.picked.Name, AppID: t.appID, Title: t.title,
			PNP: t.display.pnp, Model: t.display.model, Serial: t.display.serial,
			X: t.picked.X, Y: t.picked.Y, Width: t.picked.Width, Height: t.picked.Height,
		}
	}
	return restoreToken{restoreVendor, restoreV2, dbus.MakeVariant(wire)}
}

// decodeRestore reads a token this portal issued, of a version it
// knows; any other is none, so the picker asks.
func decodeRestore(v dbus.Variant) (restoreGrant, bool) {
	fields, ok := v.Value().([]any)
	if !ok || len(fields) != 3 || fields[0] != restoreVendor {
		return restoreGrant{}, false
	}
	data, ok := fields[2].(dbus.Variant)
	if !ok {
		return restoreGrant{}, false
	}
	switch fields[1] {
	case restoreV1:
		payload, ok := data.Value().(string)
		if !ok {
			return restoreGrant{}, false
		}
		return decodeRestoreV1(payload)
	case restoreV2:
		return decodeRestoreV2(data)
	}
	return restoreGrant{}, false
}

// decodeRestoreV1 reads a version 1 payload: the picker's targets, after
// remote:<devices>/ for a RemoteDesktop token.
func decodeRestoreV1(payload string) (restoreGrant, bool) {
	var g restoreGrant
	if rest, remote := strings.CutPrefix(payload, remoteTokenV1Tag); remote {
		devices, targets, found := strings.Cut(rest, "/")
		n, err := strconv.ParseUint(devices, 10, 32)
		if !found || err != nil || n == 0 || uint32(n)&^availableDevices != 0 {
			return restoreGrant{}, false
		}
		g.devices, payload = uint32(n), targets
		if payload == "" {
			return g, true
		}
	}
	targets, ok := sharepicker.ParseTargets(payload)
	if !ok {
		return restoreGrant{}, false
	}
	for _, t := range targets {
		g.targets = append(g.targets, restoreTarget{picked: t})
	}
	return g, true
}

func decodeRestoreV2(data dbus.Variant) (restoreGrant, bool) {
	var wire tokenGrant
	if err := dbus.Store([]any{data.Value()}, &wire); err != nil || wire.Devices&^availableDevices != 0 {
		return restoreGrant{}, false
	}
	g := restoreGrant{devices: wire.Devices, targets: make([]restoreTarget, len(wire.Targets))}
	for i, w := range wire.Targets {
		picked := sharepicker.Target{Kind: w.Kind, Name: w.Name, X: w.X, Y: w.Y, Width: w.Width, Height: w.Height}
		if _, ok := sharepicker.ParseTarget(picked.Payload()); !ok || w.Kind > sharepicker.TargetRegion {
			return restoreGrant{}, false
		}
		g.targets[i] = restoreTarget{picked: picked, appID: w.AppID, title: w.Title, display: displayID{w.PNP, w.Model, w.Serial}}
	}
	return g, true
}

// sources is what a token is resolved against: the windows, the
// outputs' connectors and the displays behind them by connector.
type sources struct {
	windows  []capture.Toplevel
	outputs  []string
	displays map[string]displayID
}

// errAmbiguous is a kept source more than one current one matches: the
// picker asks rather than guess.
var errAmbiguous = errors.New("matches more than one source")

// errGone is a kept source no current one matches.
var errGone = errors.New("matches no source")

// resolve is the current target each kept one names, best match first.
func resolve(kept []restoreTarget, now sources) ([]sharepicker.Target, error) {
	out := make([]sharepicker.Target, len(kept))
	for i, k := range kept {
		t := k.picked
		var err error
		if t.Kind == sharepicker.TargetWindow {
			t.Name, err = resolveWindow(k, now.windows)
		} else {
			t.Name, err = resolveOutput(k, now)
		}
		if err != nil {
			return nil, fmt.Errorf("restore %s: %w", k.picked.Payload(), err)
		}
		out[i] = t
	}
	return out, nil
}

// resolveWindow is the window a kept one names: the same toplevel, else
// the one window of its app with its exact title, else the app's only
// window.
func resolveWindow(k restoreTarget, windows []capture.Toplevel) (string, error) {
	var ofApp, titled []string
	for _, w := range windows {
		if w.Identifier == k.picked.Name {
			return w.Identifier, nil
		}
		if k.appID == "" || w.AppID != k.appID {
			continue
		}
		ofApp = append(ofApp, w.Identifier)
		if w.Title == k.title {
			titled = append(titled, w.Identifier)
		}
	}
	for _, match := range [][]string{titled, ofApp} {
		switch len(match) {
		case 0:
			continue
		case 1:
			return match[0], nil
		}
		return "", errAmbiguous
	}
	return "", errGone
}

// resolveOutput is the output a kept one names: the one showing its
// display, else its connector while that shows no display the kept one
// is not.
func resolveOutput(k restoreTarget, now sources) (string, error) {
	if k.display != (displayID{}) {
		var showing []string
		for _, name := range now.outputs {
			if now.displays[name] == k.display {
				showing = append(showing, name)
			}
		}
		switch len(showing) {
		case 0:
		case 1:
			return showing[0], nil
		default:
			if i := slices.Index(showing, k.picked.Name); i >= 0 {
				return showing[i], nil
			}
			return "", errAmbiguous
		}
	}
	if slices.Index(now.outputs, k.picked.Name) < 0 {
		return "", errGone
	}
	if shown, known := now.displays[k.picked.Name]; known && k.display != (displayID{}) && shown != k.display {
		return "", fmt.Errorf("%w: %s shows another display", errGone, k.picked.Name)
	}
	return k.picked.Name, nil
}

// describe keeps targets with what identifies them beyond their names.
func describe(targets []sharepicker.Target, now sources) []restoreTarget {
	out := make([]restoreTarget, len(targets))
	for i, t := range targets {
		out[i] = restoreTarget{picked: t}
		if t.Kind != sharepicker.TargetWindow {
			out[i].display = now.displays[t.Name]
			continue
		}
		for _, w := range now.windows {
			if w.Identifier == t.Name {
				out[i].appID, out[i].title = w.AppID, w.Title
				break
			}
		}
	}
	return out
}

// listToplevels is the compositor's windows, on a capture connection of
// its own.
func listToplevels() ([]capture.Toplevel, error) {
	c, err := capture.Connect()
	if err != nil {
		return nil, fmt.Errorf("connect to list the windows: %w", err)
	}
	defer func() { _ = c.Close() }()
	return c.Toplevels()
}

// readDisplays is the display behind each connected DRM connector under
// dir, by its EDID. A connector name two cards share names no display.
func readDisplays(dir string) (map[string]displayID, error) {
	displays := map[string]displayID{}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return displays, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list DRM connectors in %s: %w", dir, err)
	}
	shared := map[string]bool{}
	for _, e := range entries {
		name, ok := drmconn.Name(e.Name())
		if !ok {
			continue
		}
		blob, err := os.ReadFile(filepath.Join(dir, e.Name(), "edid")) //nolint:gosec // a connector's EDID in the DRM class directory
		if err != nil {
			warnf("screencast: connector %s: %v", e.Name(), err)
			continue
		}
		if len(blob) == 0 {
			continue
		}
		info, err := edid.Parse(blob, nil)
		if err != nil {
			warnf("screencast: connector %s: %v", e.Name(), err)
			continue
		}
		if _, seen := displays[name]; seen {
			shared[name] = true
		}
		displays[name] = displayIDOf(info)
	}
	for name := range shared {
		delete(displays, name)
	}
	return displays, nil
}
