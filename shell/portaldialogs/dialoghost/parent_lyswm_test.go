package dialoghost

import (
	"testing"

	"github.com/stubbedev/lys-lib/portaldialogs"

	"github.com/stubbedev/lys-shell/shell/dialogsurface/surfacetest"
)

// On lys-wm, an Access request naming the app's window maps the dialog
// as that window's transient, on its output, and leaves the keyboard
// free for another window.
func TestAnAccessDialogIsTransientForTheAppWindow(t *testing.T) {
	r := surfacetest.Start(t)
	h := headlessDialogs(t, r.Shell)
	const handle = "/r/parented"
	answered := make(chan struct{})
	t.Cleanup(func() {
		h.Dismiss(handle)
		<-answered
	})
	go func() {
		defer close(answered)
		h.Access(portaldialogs.Caller{Handle: handle, ParentWindow: r.Parent, Modal: true}, portaldialogs.AccessRequest{Title: "Allow?", GrantLabel: "Allow", DenyLabel: "Deny"})
	}()
	r.AwaitParented(t, "lys-shell-portal-dialog")
	r.AwaitOtherFocused(t)
}
