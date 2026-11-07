package portal

import (
	"slices"

	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// Source types of ScreenCast (the AvailableSourceTypes bits). Virtual,
// a new output the desktop extends onto, is offered only where lys-wm
// makes virtual outputs.
const (
	sourceMonitor uint32 = 1
	sourceWindow  uint32 = 2
	sourceVirtual uint32 = 4

	pickedSources = sourceMonitor | sourceWindow
)

// cursorMode is a ScreenCast cursor mode, one AvailableCursorModes bit:
// the pointer left out, painted into the frames, or carried beside them
// as a cursor meta.
type cursorMode uint32

const (
	cursorHidden   cursorMode = 1
	cursorEmbedded cursorMode = 2
	cursorMetadata cursorMode = 4

	availableCursorModes = cursorHidden | cursorEmbedded | cursorMetadata
)

// parseCursorMode is the one mode a cursor_mode option names; false for
// none, several, or one not offered.
func parseCursorMode(v uint32) (cursorMode, bool) {
	switch m := cursorMode(v); m {
	case cursorHidden, cursorEmbedded, cursorMetadata:
		return m, true
	}
	return 0, false
}

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

// sourceTypeOf is the target's AvailableSourceTypes bit: a region is
// part of a monitor.
func sourceTypeOf(t sharepicker.Target) uint32 {
	if t.Kind == sharepicker.TargetWindow {
		return sourceWindow
	}
	return sourceMonitor
}

// targetGeometry is the target's place in the compositor's logical
// space: an output's, or a region's within its output; false for a
// window, whose place the capture does not know, or an output not listed.
func targetGeometry(t sharepicker.Target, outputs []outputGeometry) (x, y, width, height int32, ok bool) {
	if t.Kind == sharepicker.TargetWindow {
		return 0, 0, 0, 0, false
	}
	i := slices.IndexFunc(outputs, func(o outputGeometry) bool { return o.name == t.Name })
	if i < 0 {
		return 0, 0, 0, 0, false
	}
	o := outputs[i]
	if t.Kind == sharepicker.TargetRegion {
		return o.x + t.X, o.y + t.Y, t.Width, t.Height, true
	}
	return o.x, o.y, o.width, o.height, true
}

// effectiveFPS clamps the requested rate to the output's refresh
// (unknown for a window or an output reporting none: unclamped). A
// refresh below 1 Hz still allows 1 frame per second, so a requested
// rate never clamps to 0; a requested 0 is passed on for the producer
// to refuse.
func effectiveFPS(requested uint32, refreshMHz int32) uint32 {
	if refreshMHz <= 0 {
		return requested
	}
	return min(requested, max(uint32(refreshMHz)/1000, 1))
}
