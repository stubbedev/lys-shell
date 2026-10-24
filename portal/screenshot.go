package portal

import (
	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/internal/fileuri"
	"github.com/stubbedev/lys-shell/shell/screenshot"
)

// screenshotPortal bridges org.freedesktop.impl.portal.Screenshot to the
// shell's dev.stubbe.lys.Screenshot1, the service `lys-shell screenshot` uses
// (screenshot.rs): an interactive request selects a region, any other
// grabs the whole multi-monitor screen as xdph's grim does.
type screenshotPortal struct{ conn *dbus.Conn }

func screenshotIface(conn *dbus.Conn) dbusx.Interface {
	return dbusx.Interface{
		Name:       "org.freedesktop.impl.portal.Screenshot",
		Methods:    screenshotPortal{conn},
		Properties: dbusx.Getters{"version": func() any { return uint32(2) }},
	}
}

// Screenshot captures and replies the saved file's URI; an empty path
// from the shell is the user cancelling the selection.
func (s screenshotPortal) Screenshot(handle dbus.ObjectPath, appID, parent string, options Vardict) (uint32, Vardict, *dbus.Error) {
	client := screenshot.NewClient(s.conn)
	c, ok := beginOr(s.conn, handle, appID, parent, client.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	mode := "screen"
	if interactive, _ := optBool(options, "interactive"); interactive {
		mode = "region"
	}
	path, err := client.Capture(c.ctx, c.caller, mode, "")
	if code := c.response("screenshot", path != "", err); code != ResponseSuccess {
		return code, Vardict{}, nil
	}
	return ResponseSuccess, Vardict{"uri": dbus.MakeVariant(fileuri.FromPath(path))}, nil
}

// PickColor samples one screen pixel as an sRGB (ddd). The shell errors
// on a cancel as on a failure, and both reply cancelled.
func (s screenshotPortal) PickColor(handle dbus.ObjectPath, appID, parent string, _ Vardict) (uint32, Vardict, *dbus.Error) {
	client := screenshot.NewClient(s.conn)
	c, ok := beginOr(s.conn, handle, appID, parent, client.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	r, g, b, err := client.PickColor(c.ctx, c.caller)
	if err != nil {
		warnf("screenshot: color pick failed/cancelled: %v", err)
		return ResponseCancelled, Vardict{}, nil
	}
	return ResponseSuccess, Vardict{"color": dbus.MakeVariant(colorTuple{r, g, b})}, nil
}
