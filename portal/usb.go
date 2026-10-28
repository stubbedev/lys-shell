package portal

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/shell/portaldialogs"
)

// usb is org.freedesktop.impl.portal.Usb: the frontend
// enumerates devices and the backend asks the user which of the ones
// an app requests it may open. The frontend remembers every grant as a
// lasting permission, so nothing is granted without the user's answer.
type usb struct{ dialogs }

func usbIface(conn *dbus.Conn) dbusx.Interface {
	return dbusx.Interface{
		Name:       "org.freedesktop.impl.portal.Usb",
		Methods:    usb{dialogs{conn}},
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

// AcquireDevices asks the user which of the devices the app may open,
// naming each and whether it would be writable, and grants the ones
// picked with the access asked for, never more.
func (u usb) AcquireDevices(handle dbus.ObjectPath, parent, appID string, devices []requestedDevice, _ Vardict) (uint32, Vardict, *dbus.Error) {
	asked := make([]portaldialogs.Device, len(devices))
	for i, d := range devices {
		writable, _ := optBool(d.Access, "writable")
		asked[i] = portaldialogs.Device{ID: d.ID, Name: usbDeviceName(d.ID, d.Info), Writable: writable}
	}
	client := u.client()
	c, ok := beginOr(u.conn, handle, appID, parent, client.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	allowed, picked, err := client.ChooseDevices(c.ctx, c.caller, asked)
	if code := c.response("usb", allowed, err); code != ResponseSuccess {
		return code, Vardict{}, nil
	}
	byID := make(map[string]portaldialogs.Device, len(asked))
	for _, d := range asked {
		byID[d.ID] = d
	}
	granted := make([]grantedDevice, 0, len(picked))
	for _, id := range picked {
		d, ok := byID[id]
		if !ok {
			warnf("usb: the shell granted %q, which %s did not request", id, appID)
			return ResponseOther, Vardict{}, nil
		}
		granted = append(granted, grantedDevice{ID: id, Access: Vardict{"writable": dbus.MakeVariant(d.Writable)}})
	}
	return ResponseSuccess, Vardict{"devices": dbus.MakeVariant(granted)}, nil
}

// usbDeviceName is how the dialog names a device, from the udev
// properties the frontend passes: "Vendor Model (vvvv:pppp)", with
// what is known of it, else its id.
func usbDeviceName(id string, info Vardict) string {
	props, _ := info["properties"].Value().(map[string]dbus.Variant)
	prop := func(key string) string { return stringOr(props, key, "") }
	named := func(field string) string {
		if s := prop("ID_" + field + "_FROM_DATABASE"); s != "" {
			return s
		}
		return udevUnescape(prop("ID_" + field + "_ENC"))
	}
	name := strings.Join(slices.DeleteFunc([]string{named("VENDOR"), named("MODEL")}, func(s string) bool { return s == "" }), " ")
	vendorID, modelID := prop("ID_VENDOR_ID"), prop("ID_MODEL_ID")
	switch {
	case vendorID != "" && modelID != "" && name != "":
		return fmt.Sprintf("%s (%s:%s)", name, vendorID, modelID)
	case vendorID != "" && modelID != "":
		return fmt.Sprintf("USB device %s:%s", vendorID, modelID)
	case name != "":
		return name
	}
	return id
}

// udevUnescape decodes udev's \xNN escapes in an ID_*_ENC property;
// any other backslash is kept as written.
func udevUnescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && s[i+1] == 'x' {
			if n, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return strings.TrimSpace(b.String())
}
