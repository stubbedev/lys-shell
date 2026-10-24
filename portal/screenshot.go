package portal

import (
	"context"
	"errors"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/internal/fileuri"
	"github.com/stubbedev/lys-shell/shell/screenshot"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// Screenshot targets (the AvailableTargets bits, and the target
// option's values).
const (
	targetScreen       uint32 = 1
	targetPickedWindow uint32 = 2
	targetArea         uint32 = 4
	targetActiveWindow uint32 = 8

	availableTargets = targetScreen | targetPickedWindow | targetArea | targetActiveWindow
)

// screenshotPortal bridges org.freedesktop.impl.portal.Screenshot to the
// shell's dev.stubbe.lys.Screenshot1: an app's capture is
// saved to a private file, none of the user's save, clipboard or
// notification settings applied.
type screenshotPortal struct{ conn *dbus.Conn }

func screenshotIface(conn *dbus.Conn) dbusx.Interface {
	return dbusx.Interface{
		Name:    "org.freedesktop.impl.portal.Screenshot",
		Methods: screenshotPortal{conn},
		Properties: dbusx.Getters{
			"AvailableTargets": func() any { return availableTargets },
			"version":          func() any { return uint32(3) },
		},
	}
}

// screenshotMode is the shell's capture mode for a target; false for
// one not offered. Without a target the version-2 behavior holds: an
// interactive request selects an area, any other takes the screen.
func screenshotMode(options Vardict) (mode string, ok bool) {
	target, given := optU32(options, "target")
	if !given {
		if interactive, _ := optBool(options, "interactive"); interactive {
			return screenshot.ModeRegion, true
		}
		return screenshot.ModeScreen, true
	}
	switch target {
	case targetScreen:
		return screenshot.ModeScreen, true
	case targetPickedWindow, targetActiveWindow:
		return screenshot.ModeWindow, true
	case targetArea:
		return screenshot.ModeRegion, true
	}
	return "", false
}

// Screenshot captures the target and replies the file's URI; an empty
// path from the shell, or no window picked, is the user cancelling. A
// window the user picks comes from the share picker's window page.
func (s screenshotPortal) Screenshot(handle dbus.ObjectPath, appID, parent string, options Vardict) (uint32, Vardict, *dbus.Error) {
	mode, ok := screenshotMode(options)
	if !ok {
		warnf("screenshot: target %v, of %#x offered", options["target"], availableTargets)
		return ResponseOther, Vardict{}, nil
	}
	client := screenshot.NewClient(s.conn)
	picker := sharepicker.NewClient(s.conn)
	c, ok := beginOr(s.conn, handle, appID, parent, func(ctx context.Context, h string) error {
		return errors.Join(client.Dismiss(ctx, h), picker.Dismiss(ctx, h))
	})
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	c.modal(options)
	window := ""
	if target, _ := optU32(options, "target"); target == targetPickedWindow {
		reply, err := picker.Pick(c.ctx, c.caller, sharepicker.Request{Sources: sharepicker.SourceWindows})
		if code := c.response("screenshot window picker", reply != "", err); code != ResponseSuccess {
			return code, Vardict{}, nil
		}
		_, targets, ok := parsePickerReply(reply)
		if !ok || len(targets) != 1 || targets[0].kind != targetWindow {
			warnf("screenshot: the window picker replied %q", reply)
			return ResponseOther, Vardict{}, nil
		}
		window = targets[0].name
	}
	path, err := client.CapturePrivate(c.ctx, c.caller, mode, window)
	if code := c.response("screenshot", path != "", err); code != ResponseSuccess {
		return code, Vardict{}, nil
	}
	return ResponseSuccess, Vardict{"uri": dbus.MakeVariant(fileuri.FromPath(path))}, nil
}

// PickColor samples one screen pixel as an sRGB (ddd): the user's
// cancel is cancelled, a failure other.
func (s screenshotPortal) PickColor(handle dbus.ObjectPath, appID, parent string, options Vardict) (uint32, Vardict, *dbus.Error) {
	client := screenshot.NewClient(s.conn)
	c, ok := beginOr(s.conn, handle, appID, parent, client.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	c.modal(options)
	color, picked, err := client.PickColor(c.ctx, c.caller)
	if code := c.response("color pick", picked, err); code != ResponseSuccess {
		return code, Vardict{}, nil
	}
	return ResponseSuccess, Vardict{"color": dbus.MakeVariant(colorTuple{color[0], color[1], color[2]})}, nil
}
