package dialoghost

import (
	"testing"

	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/shell/dialogsurface"
	"github.com/stubbedev/lys-shell/shell/dialogsurface/surfacetest"
)

const dialogAppID = "lys-shell-portal-dialog"

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
	r.AwaitParented(t, dialogAppID)
	r.AwaitOtherFocused(t)
}

// On lys-wm, an authentication dialog shown as the app window's
// transient grows its window, at the same width, to fit the error rows
// a failed attempt adds.
func TestAWindowedAuthDialogGrowsWithItsErrors(t *testing.T) {
	r := surfacetest.Start(t)
	var (
		root widget.Widget
		size widget.Size
	)
	h := headlessDialogs(t, r.Shell, func(s *dialogsurface.Spec) {
		build := s.Root
		s.Root = func(surface dialogsurface.Surface) widget.Widget {
			root = build(surface)
			return root
		}
		s.Window.OnResize = func(sz widget.Size) { size = sz }
	})
	caller := portaldialogs.Caller{Handle: authHandle, AppID: "polkit", ParentWindow: r.Parent, Modal: true}
	t.Cleanup(func() { h.Dismiss(authHandle) })
	h.ShowAuthentication(caller, authStep())
	r.AwaitParented(t, dialogAppID)
	apptest.Until(t, r.Shell, "the window sized", func() bool { return size != widget.Size{} })
	apptest.Dispatched(t, r.Shell)
	before := apptest.On(r.Shell, func() widget.Size { return size })
	failed := portaldialogs.AuthNote{Error: true, Text: "Sorry, that did not work. Please try again."}
	h.ShowAuthentication(caller, authStep(failed, failed, failed))
	apptest.Until(t, r.Shell, "the window grown to its error rows", func() bool {
		need := root.Measure(widget.Column, size.W).Min
		return size.W == before.W && size.H > before.H && size.H == need
	})
}
