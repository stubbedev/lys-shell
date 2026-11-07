package printdialog

import (
	"testing"

	"github.com/stubbedev/lys-lib/portaldialogs"

	"github.com/stubbedev/lys-shell/shell/dialogsurface/surfacetest"
)

// On lys-wm, a print request naming the app's window maps the dialog as
// that window's transient, on its output, and leaves the keyboard free
// for another window.
func TestThePrintDialogIsTransientForTheAppWindow(t *testing.T) {
	r := surfacetest.Start(t)
	h := headlessDialog(t, r.Shell)
	const handle = "/r/parented"
	answered := make(chan struct{})
	t.Cleanup(func() {
		h.Dismiss(handle)
		<-answered
	})
	go func() {
		defer close(answered)
		h.Prepare(portaldialogs.Caller{Handle: handle, ParentWindow: r.Parent, Modal: true}, PrepareRequest{Title: "Doc"})
	}()
	r.AwaitParented(t, "lys-shell-print")
	r.AwaitOtherFocused(t)
}
