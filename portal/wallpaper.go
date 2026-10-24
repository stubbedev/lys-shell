package portal

import (
	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/internal/fileuri"
	"github.com/stubbedev/lys-shell/service/wallpaper"
	"github.com/stubbedev/lys-shell/shell/portaldialogs"
)

// wallpaperPortal maps SetWallpaperURI onto the shell's
// dev.stubbe.lys.Wallpaper1, on every monitor; only file:// URIs are
// supported (wallpaper.rs).
type wallpaperPortal struct{ conn *dbus.Conn }

func wallpaperIface(conn *dbus.Conn) dbusx.Interface {
	return dbusx.Interface{
		Name:       "org.freedesktop.impl.portal.Wallpaper",
		Methods:    wallpaperPortal{conn},
		Properties: dbusx.Getters{"version": func() any { return uint32(1) }},
	}
}

// SetWallpaperURI sets the wallpaper, after the preview dialog when
// show-preview asks for one.
func (w wallpaperPortal) SetWallpaperURI(handle dbus.ObjectPath, appID, parent, uri string, options Vardict) (uint32, *dbus.Error) {
	path, ok := fileuri.Path(uri)
	if !ok {
		warnf("wallpaper: only file:// URIs are supported: %s", uri)
		return ResponseOther, nil
	}
	dialogs := portaldialogs.NewClient(w.conn)
	c, ok := beginOr(w.conn, handle, appID, parent, dialogs.Dismiss)
	if !ok {
		return ResponseOther, nil
	}
	defer c.end()
	if preview, _ := optBool(options, "show-preview"); preview {
		set, err := dialogs.ConfirmWallpaper(c.ctx, c.caller, uri)
		if code := c.response("wallpaper preview", set, err); code != ResponseSuccess {
			return code, nil
		}
	}
	if err := wallpaper.NewClient(w.conn).SetWallpaper(c.ctx, path, ""); err != nil {
		warnf("wallpaper: set_wallpaper failed: %v", err)
		return ResponseOther, nil
	}
	return ResponseSuccess, nil
}
