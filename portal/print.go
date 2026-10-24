package portal

import (
	"os"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/shell/printdialog"
)

// printPortal is org.freedesktop.impl.portal.Print over the shell's
// dev.stubbe.lys.Print1 (print.rs): PreparePrint shows the dialog and
// replies the settings and a token, Print spools the document to the
// printer prepared under it.
type printPortal struct{ conn *dbus.Conn }

func printIface(conn *dbus.Conn) dbusx.Interface {
	return dbusx.Interface{
		Name:       "org.freedesktop.impl.portal.Print",
		Methods:    printPortal{conn},
		Properties: dbusx.Getters{"version": func() any { return uint32(1) }},
	}
}

// PreparePrint shows the print dialog. The page setup rides inside the
// settings, so page_setup is empty: an a{sv}, as the spec types it
// (Rust sent an a{ss}, which a typed lookup does not find).
func (p printPortal) PreparePrint(handle dbus.ObjectPath, appID, parent, title string, _, _, _ Vardict) (uint32, Vardict, *dbus.Error) {
	client := printdialog.NewClient(p.conn)
	c, ok := beginOr(p.conn, handle, appID, parent, client.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	granted, settings, token, err := client.Prepare(c.ctx, c.caller, title)
	if code := c.response("print prepare", granted, err); code != ResponseSuccess {
		return code, Vardict{}, nil
	}
	dict := Vardict{}
	for _, s := range settings {
		dict[s.Key] = dbus.MakeVariant(s.Value)
	}
	return ResponseSuccess, Vardict{
		"settings":   dbus.MakeVariant(dict),
		"page_setup": dbus.MakeVariant(Vardict{}),
		"token":      dbus.MakeVariant(token),
	}, nil
}

// Print spools fd to the prepared printer; a job not sent is other.
func (p printPortal) Print(handle dbus.ObjectPath, appID, parent, title string, fd dbus.UnixFD, options Vardict) (uint32, Vardict, *dbus.Error) {
	doc := os.NewFile(uintptr(fd), "document")
	defer func() { _ = doc.Close() }()
	client := printdialog.NewClient(p.conn)
	c, ok := beginOr(p.conn, handle, appID, parent, client.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	token, _ := optU32(options, "token")
	sent, err := client.Print(c.ctx, c.caller, title, fd, token)
	switch {
	case c.closed():
		return ResponseCancelled, Vardict{}, nil
	case err != nil:
		warnf("print: spooling failed: %v", err)
		return ResponseOther, Vardict{}, nil
	case !sent:
		return ResponseOther, Vardict{}, nil
	}
	return ResponseSuccess, Vardict{}, nil
}
