package portal

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
)

// autostartDBusActivatable is EnableAutostart's flag for an app that
// is D-Bus activatable.
const autostartDBusActivatable = 1

// background is org.freedesktop.impl.portal.Background (background.rs):
// apps may always keep running, and autostart is an entry under
// $XDG_CONFIG_HOME/autostart.
type background struct{ conn *dbus.Conn }

func backgroundIface(conn *dbus.Conn) dbusx.Interface {
	return dbusx.Interface{
		Name:       "org.freedesktop.impl.portal.Background",
		Methods:    background{conn},
		Properties: dbusx.Getters{"version": func() any { return uint32(2) }},
	}
}

// GetAppState reports no per-app state: windows are not tracked.
func (background) GetAppState() (Vardict, *dbus.Error) { return Vardict{}, nil }

// NotifyBackground allows the app to keep running (result 1).
func (b background) NotifyBackground(handle dbus.ObjectPath, appID, _ string) (uint32, Vardict, *dbus.Error) {
	c, ok := beginOr(b.conn, handle, appID, "", nil)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	return ResponseSuccess, Vardict{"result": dbus.MakeVariant(uint32(1))}, nil
}

// EnableAutostart writes or removes the app's autostart entry and
// replies whether autostart is now enabled.
func (background) EnableAutostart(appID string, enable bool, commandline []string, flags uint32) (bool, *dbus.Error) {
	path, ok := autostartPath(appID)
	if !ok {
		warnf("background: no autostart directory (XDG_CONFIG_HOME/HOME unset)")
		return false, nil
	}
	if !enable {
		_ = os.Remove(path)
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // ~/.config/autostart is a normal readable config dir
		warnf("background: cannot create autostart dir: %v", err)
		return false, nil
	}
	if err := os.WriteFile(path, []byte(autostartDesktop(appID, commandline, flags&autostartDBusActivatable != 0)), 0o644); err != nil { //nolint:gosec // an autostart entry is world-readable like any desktop file
		warnf("background: cannot write autostart entry: %v", err)
		return false, nil
	}
	return true, nil
}

// autostartPath is $XDG_CONFIG_HOME/autostart/<app_id>.desktop, or
// under ~/.config.
func autostartPath(appID string) (string, bool) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, ok := os.LookupEnv("HOME")
		if !ok {
			return "", false
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "autostart", appID+".desktop"), true
}

func autostartDesktop(appID string, commandline []string, dbusActivatable bool) string {
	var b strings.Builder
	b.WriteString("[Desktop Entry]\nType=Application\n")
	b.WriteString("Name=" + appID + "\n")
	b.WriteString("Exec=" + strings.Join(commandline, " ") + "\n")
	b.WriteString("X-Flatpak=" + appID + "\n")
	if dbusActivatable {
		b.WriteString("DBusActivatable=true\n")
	}
	b.WriteString("X-GNOME-Autostart-enabled=true\n")
	return b.String()
}
