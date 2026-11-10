package bar

import (
	"log"

	"github.com/stubbedev/lys-desktop/service/clipboard"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/transfer"

	"github.com/stubbedev/lys-shell/service/clipboardipc"
)

// copyTextOffLoop copies text from any goroutine but the loop's, through
// the clipboard history when the shell keeps one, else the plain device.
func copyTextOffLoop(application *app.Application, clip *clipboard.Clipboard) func(text string) error {
	put := awaitLoop(application, func(content transfer.Content) (func(), error) {
		return putOnClipboard(application, clip, content)
	})
	return func(text string) error {
		_, err := put(transfer.Text(text))
		return err
	}
}

// serveClipboardCopy exports dev.stubbe.lys.Clipboard1 on the session
// bus, copying through copyText; the returned func stops serving.
func serveClipboardCopy(conn *dbus.Conn, copyText func(text string) error) func() {
	if conn == nil {
		log.Print("clipboard: no session bus; other processes cannot copy through the shell")
		return func() {}
	}
	release, err := clipboardipc.NewDaemon(copyText).Export(conn)
	if err != nil {
		log.Printf("clipboard: daemon: %v", err)
		return func() {}
	}
	return release
}
