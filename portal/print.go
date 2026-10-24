package portal

import (
	"os"
	"slices"
	"strings"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/shell/printdialog"
)

// printPortal is org.freedesktop.impl.portal.Print over the shell's
// dev.stubbe.lys.Print1: PreparePrint shows the dialog and
// replies the settings and a token, Print spools the document to the
// printer prepared under it.
type printPortal struct{ conn *dbus.Conn }

func printIface(conn *dbus.Conn) dbusx.Interface {
	return dbusx.Interface{
		Name:    "org.freedesktop.impl.portal.Print",
		Methods: printPortal{conn},
	}
}

// PreparePrint shows the print dialog, starting from the app's
// settings and page setup, and replies the settings, the page setup
// and the token Print takes.
func (p printPortal) PreparePrint(handle dbus.ObjectPath, appID, parent, title string, settings, pageSetup, options Vardict) (uint32, Vardict, *dbus.Error) {
	client := printdialog.NewClient(p.conn)
	c, ok := beginOr(p.conn, handle, appID, parent, client.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	c.modal(options)
	req := printdialog.PrepareRequest{
		Title:         title,
		Settings:      printSettings(settings),
		PageSetup:     parsePageSetup(pageSetup),
		AcceptLabel:   stringOr(options, "accept_label", ""),
		OutputFormats: stringList(options, "supported_output_file_formats"),
	}
	req.HasCurrentPage, _ = optBool(options, "has_current_page")
	req.HasSelectedPages, _ = optBool(options, "has_selected_pages")
	granted, chosen, setup, token, err := client.Prepare(c.ctx, c.caller, req)
	if code := c.response("print prepare", granted, err); code != ResponseSuccess {
		return code, Vardict{}, nil
	}
	dict := Vardict{}
	for _, s := range chosen {
		dict[s.Key] = dbus.MakeVariant(s.Value)
	}
	return ResponseSuccess, Vardict{
		"settings":   dbus.MakeVariant(dict),
		"page-setup": dbus.MakeVariant(pageSetupDict(setup)),
		"token":      dbus.MakeVariant(token),
	}, nil
}

// printSettings are the app's GtkPrintSettings, each a string; a value
// of another type is skipped.
func printSettings(settings Vardict) []printdialog.Setting {
	out := make([]printdialog.Setting, 0, len(settings))
	for key, v := range settings {
		if s, ok := v.Value().(string); ok {
			out = append(out, printdialog.Setting{Key: key, Value: s})
		}
	}
	slices.SortFunc(out, func(a, b printdialog.Setting) int { return strings.Compare(a.Key, b.Key) })
	return out
}

// parsePageSetup reads the app's GtkPageSetup; missing keys stay zero.
func parsePageSetup(v Vardict) printdialog.PageSetup {
	num := func(key string) float64 { f, _ := v[key].Value().(float64); return f }
	return printdialog.PageSetup{
		PPDName: stringOr(v, "PPDName", ""), Name: stringOr(v, "Name", ""), DisplayName: stringOr(v, "DisplayName", ""),
		Width: num("Width"), Height: num("Height"),
		MarginTop: num("MarginTop"), MarginBottom: num("MarginBottom"), MarginLeft: num("MarginLeft"), MarginRight: num("MarginRight"),
		Orientation: stringOr(v, "Orientation", ""),
	}
}

// pageSetupDict is a GtkPageSetup as the portal returns it: the names
// and orientation as strings, the size and margins as doubles in
// millimetres.
func pageSetupDict(s printdialog.PageSetup) Vardict {
	return Vardict{
		"PPDName": dbus.MakeVariant(s.PPDName), "Name": dbus.MakeVariant(s.Name), "DisplayName": dbus.MakeVariant(s.DisplayName),
		"Width": dbus.MakeVariant(s.Width), "Height": dbus.MakeVariant(s.Height),
		"MarginTop": dbus.MakeVariant(s.MarginTop), "MarginBottom": dbus.MakeVariant(s.MarginBottom),
		"MarginLeft": dbus.MakeVariant(s.MarginLeft), "MarginRight": dbus.MakeVariant(s.MarginRight),
		"Orientation": dbus.MakeVariant(s.Orientation),
	}
}

// Print spools fd to the printer the app prepared under its token, or,
// without one, through the dialog first; a job not sent is other.
func (p printPortal) Print(handle dbus.ObjectPath, appID, parent, title string, fd dbus.UnixFD, options Vardict) (uint32, Vardict, *dbus.Error) {
	doc := os.NewFile(uintptr(fd), "document")
	defer func() { _ = doc.Close() }()
	client := printdialog.NewClient(p.conn)
	c, ok := beginOr(p.conn, handle, appID, parent, client.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	c.modal(options)
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
