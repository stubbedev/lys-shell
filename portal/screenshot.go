package portal

import (
	"context"
	"errors"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/internal/fileuri"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/shell/portaldialogs"
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

// shot is what a Screenshot request captures: a target (the
// AvailableTargets value) and whether the pointer is composited in.
type shot struct {
	target  uint32
	pointer bool
}

// requestedShot is the shot a request's options name; ask is set for
// an interactive request without a target, whose shot the user
// chooses; false for a target not offered.
func requestedShot(options Vardict) (s shot, ask, ok bool) {
	target, given := optU32(options, "target")
	if !given {
		interactive, _ := optBool(options, "interactive")
		return shot{target: targetScreen}, interactive, true
	}
	switch target {
	case targetScreen, targetPickedWindow, targetActiveWindow, targetArea:
		return shot{target: target}, false, true
	}
	return shot{}, false, false
}

// screenshotMode is the shell's capture mode for a target.
func screenshotMode(target uint32) string {
	switch target {
	case targetPickedWindow, targetActiveWindow:
		return screenshot.ModeWindow
	case targetArea:
		return screenshot.ModeRegion
	}
	return screenshot.ModeScreen
}

// The interactive chooser's questions and their answers.
const (
	choiceGrab    = "grab"
	choicePointer = "pointer"
)

// grabTargets are the chooser's grab options, by option id.
var grabTargets = map[string]uint32{"screen": targetScreen, "window": targetPickedWindow, "area": targetArea}

// screenshotChooser asks what to capture: the whole screen, a window
// the user then picks, or an area, and whether to show the pointer.
var screenshotChooser = portaldialogs.AccessRequest{
	Title: "Take a screenshot", Subtitle: "Choose what to capture.",
	GrantLabel: "Take Screenshot", DenyLabel: "Cancel", Icon: "applets-screenshooter-symbolic",
	Choices: []portalreq.Choice{
		{ID: choiceGrab, Label: "Grab", Initial: "screen", Options: []portalreq.Option{
			{ID: "screen", Label: "Whole screen"}, {ID: "window", Label: "A window"}, {ID: "area", Label: "An area"},
		}},
		{ID: choicePointer, Label: "Show the pointer"},
	},
}

// chosenShot reads the chooser's answers; false for a grab it did not
// offer.
func chosenShot(selections []portalreq.Selection) (shot, bool) {
	var s shot
	for _, sel := range selections {
		switch sel.ID {
		case choiceGrab:
			target, ok := grabTargets[sel.Value]
			if !ok {
				return shot{}, false
			}
			s.target = target
		case choicePointer:
			s.pointer = sel.Value == "true"
		}
	}
	return s, s.target != 0
}

// Screenshot captures the target and replies the file's URI; an empty
// path from the shell, or no window picked, is the user cancelling. An
// interactive request without a target asks the user what to capture
// first; a window the user picks comes from the share picker's window
// page.
func (s screenshotPortal) Screenshot(handle dbus.ObjectPath, appID, parent string, options Vardict) (uint32, Vardict, *dbus.Error) {
	sh, ask, ok := requestedShot(options)
	if !ok {
		warnf("screenshot: target %v, of %#x offered", options["target"], availableTargets)
		return ResponseOther, Vardict{}, nil
	}
	client := screenshot.NewClient(s.conn)
	picker := sharepicker.NewClient(s.conn)
	dialogs := portaldialogs.NewClient(s.conn)
	c, ok := beginOr(s.conn, handle, appID, parent, func(ctx context.Context, h string) error {
		return errors.Join(client.Dismiss(ctx, h), picker.Dismiss(ctx, h), dialogs.Dismiss(ctx, h))
	})
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	c.modal(options)
	if ask {
		allowed, selections, err := dialogs.Access(c.ctx, c.caller, screenshotChooser)
		if code := c.response("screenshot chooser", allowed, err); code != ResponseSuccess {
			return code, Vardict{}, nil
		}
		if sh, ok = chosenShot(selections); !ok {
			warnf("screenshot: the chooser answered %v", selections)
			return ResponseOther, Vardict{}, nil
		}
	}
	window := ""
	if sh.target == targetPickedWindow {
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
	path, err := client.CapturePrivate(c.ctx, c.caller, screenshotMode(sh.target), window, sh.pointer)
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
