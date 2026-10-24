package portal

import (
	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
)

// usb is org.freedesktop.impl.portal.Usb (usb.rs): the frontend
// enumerates devices, the backend only grants, and grants every
// requested device its asked-for access.
type usb struct{ conn *dbus.Conn }

func usbIface(conn *dbus.Conn) dbusx.Interface {
	return dbusx.Interface{
		Name:       "org.freedesktop.impl.portal.Usb",
		Methods:    usb{conn},
		Properties: dbusx.Getters{"version": func() any { return uint32(1) }},
	}
}

// requestedDevice is AcquireDevices' (sa{sv}a{sv}): id, info, access.
type requestedDevice struct {
	ID     string
	Info   Vardict
	Access Vardict
}

// grantedDevice is the (sa{sv}) reply: id and access.
type grantedDevice struct {
	ID     string
	Access Vardict
}

// AcquireDevices grants the requested devices.
func (u usb) AcquireDevices(handle dbus.ObjectPath, appID, parent string, devices []requestedDevice, _ Vardict) (uint32, Vardict, *dbus.Error) {
	c, ok := beginOr(u.conn, handle, appID, parent, nil)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	granted := make([]grantedDevice, 0, len(devices))
	for _, d := range devices {
		granted = append(granted, grantedDevice{ID: d.ID, Access: d.Access})
	}
	return ResponseSuccess, Vardict{"devices": dbus.MakeVariant(granted)}, nil
}
