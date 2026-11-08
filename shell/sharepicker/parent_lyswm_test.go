package sharepicker

import (
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-lib/portaldialogs"

	"github.com/stubbedev/lys-shell/shell/dialogsurface/surfacetest"
)

// On lys-wm, a share request naming the app's window maps the picker as
// that window's transient, on its output, and leaves the keyboard free
// for another window.
func TestThePickerIsTransientForTheAppWindow(t *testing.T) {
	r := surfacetest.Start(t)
	p, _ := headlessPicker(t, r.Shell, config.SharePickerWindows)
	const handle = "/r/parented"
	answered := make(chan struct{})
	t.Cleanup(func() {
		p.Dismiss(handle)
		<-answered
	})
	go func() {
		defer close(answered)
		p.Pick(portaldialogs.Caller{Handle: handle, ParentWindow: r.Parent, Modal: true}, Request{Sources: SourceWindows})
	}()
	r.AwaitParented(t, "lys-shell-share-picker")
	r.AwaitOtherFocused(t)
}
