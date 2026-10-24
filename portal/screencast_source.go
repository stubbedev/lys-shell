package portal

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// Source types of ScreenCast (the AvailableSourceTypes bits). Virtual
// (4), a new virtual monitor, is not offered: there are none to make.
const (
	sourceMonitor uint32 = 1
	sourceWindow  uint32 = 2

	availableSources = sourceMonitor | sourceWindow
)

// Cursor modes of ScreenCast (AvailableCursorModes bits). Metadata (4)
// is not offered: the streams carry no cursor meta.
const (
	cursorHidden   uint32 = 1
	cursorEmbedded uint32 = 2

	availableCursorModes = cursorHidden | cursorEmbedded
)

// pickerSources is what the share picker offers for the source types:
// a monitor is a whole output or a region of one.
func pickerSources(types uint32) sharepicker.Sources {
	var s sharepicker.Sources
	if types&sourceMonitor != 0 {
		s |= sharepicker.SourceOutputs | sharepicker.SourceRegions
	}
	if types&sourceWindow != 0 {
		s |= sharepicker.SourceWindows
	}
	return s
}

// captureKind is what a captureTarget names.
type captureKind uint8

const (
	targetOutput captureKind = iota
	targetWindow
	targetRegion
)

// captureTarget is one source the user picked (source.rs
// CaptureTarget): an output by connector name, a window by its
// ext-foreign-toplevel identifier, or a region of an output in its
// logical coordinates.
type captureTarget struct {
	kind captureKind
	// name is the output connector, or the window identifier.
	name                string
	x, y, width, height int32
}

// sourceType is the target's AvailableSourceTypes bit: a region is
// part of a monitor.
func (t captureTarget) sourceType() uint32 {
	if t.kind == targetWindow {
		return sourceWindow
	}
	return sourceMonitor
}

// geometry is the target's place in the compositor's logical space: an
// output's, or a region's within its output; false for a window, whose
// place the capture does not know, or an output not listed.
func (t captureTarget) geometry(outputs []outputGeometry) (x, y, width, height int32, ok bool) {
	if t.kind == targetWindow {
		return 0, 0, 0, 0, false
	}
	i := slices.IndexFunc(outputs, func(o outputGeometry) bool { return o.name == t.name })
	if i < 0 {
		return 0, 0, 0, 0, false
	}
	o := outputs[i]
	if t.kind == targetRegion {
		return o.x + t.x, o.y + t.y, t.width, t.height, true
	}
	return o.x, o.y, o.width, o.height, true
}

// payload is the target as the share picker writes it (and a restore
// token keeps it): screen:<name>, window:<id>, region:<out>@x,y,w,h.
func (t captureTarget) payload() string {
	switch t.kind {
	case targetWindow:
		return "window:" + t.name
	case targetRegion:
		return fmt.Sprintf("region:%s@%d,%d,%d,%d", t.name, t.x, t.y, t.width, t.height)
	}
	return "screen:" + t.name
}

// parseTarget reads one payload; anything malformed is no target.
func parseTarget(payload string) (captureTarget, bool) {
	if name, ok := strings.CutPrefix(payload, "screen:"); ok {
		return captureTarget{kind: targetOutput, name: name}, name != ""
	}
	if id, ok := strings.CutPrefix(payload, "window:"); ok {
		return captureTarget{kind: targetWindow, name: id}, id != ""
	}
	spec, ok := strings.CutPrefix(payload, "region:")
	if !ok {
		return captureTarget{}, false
	}
	out, rect, ok := strings.Cut(spec, "@")
	nums := strings.Split(rect, ",")
	if !ok || out == "" || len(nums) != 4 {
		return captureTarget{}, false
	}
	var v [4]int32
	for i, n := range nums {
		parsed, err := strconv.ParseInt(n, 10, 32)
		if err != nil {
			return captureTarget{}, false
		}
		v[i] = int32(parsed)
	}
	if v[2] <= 0 || v[3] <= 0 {
		return captureTarget{}, false
	}
	return captureTarget{kind: targetRegion, name: out, x: v[0], y: v[1], width: v[2], height: v[3]}, true
}

// parsePickerReply reads the share picker's answer:
// `[r]/<payload>[;<payload>...]`, r granting a restore token. Empty is
// a cancel; any malformed piece rejects the whole reply.
func parsePickerReply(reply string) (allowToken bool, targets []captureTarget, ok bool) {
	flags, payload, found := strings.Cut(reply, "/")
	if reply == "" || !found {
		return false, nil, false
	}
	targets, ok = parseTargets(payload)
	return strings.Contains(flags, "r"), targets, ok
}

// parseTargets reads `<payload>[;<payload>...]`; any malformed piece
// rejects them all.
func parseTargets(payload string) ([]captureTarget, bool) {
	var targets []captureTarget
	for piece := range strings.SplitSeq(payload, ";") {
		t, ok := parseTarget(piece)
		if !ok {
			return nil, false
		}
		targets = append(targets, t)
	}
	return targets, true
}

// joinTargets writes targets as parseTargets reads them.
func joinTargets(targets []captureTarget) string {
	pieces := make([]string, len(targets))
	for i, t := range targets {
		pieces[i] = t.payload()
	}
	return strings.Join(pieces, ";")
}

// effectiveFPS clamps the requested rate to the output's refresh
// (unknown for a window: unclamped), never below 1.
func effectiveFPS(requested uint32, refreshMHz int32) uint32 {
	if refreshMHz > 0 {
		requested = min(requested, max(uint32(refreshMHz)/1000, 1))
	}
	return max(requested, 1)
}
