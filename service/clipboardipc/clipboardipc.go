// Package clipboardipc is dev.stubbe.lys.Clipboard1: the running shell
// taking text onto the clipboard for another process (`lys-shell icons
// list -i`), so the shell's clipboard owns the selection and its history
// records it, and no wl-copy is needed.
package clipboardipc

import (
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// D-Bus identity.
const (
	ServiceName = "dev.stubbe.lys.Clipboard1"
	ServicePath = dbus.ObjectPath("/dev/stubbe/lys/Clipboard")
)

// Daemon is the dev.stubbe.lys.Clipboard1 object.
type Daemon struct {
	copyText func(text string) error
}

// NewDaemon serves copies through copyText, which owns the selection
// until another client takes it.
func NewDaemon(copyText func(text string) error) *Daemon { return &Daemon{copyText: copyText} }

// Export serves the interface under its well-known name; the returned
// release drops the name.
func (d *Daemon) Export(conn *dbus.Conn) (func(), error) {
	return dbusx.Serve(conn, dbusx.Service{
		Name:      ServiceName,
		Path:      ServicePath,
		Interface: ServiceName,
		Methods:   d,
	})
}

// CopyText puts text on the clipboard; empty text is refused, as there
// is nothing to offer.
func (d *Daemon) CopyText(text string) *dbus.Error {
	if text == "" {
		return dbusx.InvalidArgs("no text to copy")
	}
	if err := d.copyText(text); err != nil {
		return dbusx.Failed("cannot copy to the clipboard: " + err.Error())
	}
	return nil
}
