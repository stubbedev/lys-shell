package filechooser

import (
	"testing"

	"github.com/stubbedev/lys-lib/portaldialogs"

	"github.com/stubbedev/lys-shell/shell/dialogsurface/surfacetest"
)

// On lys-wm, an Open request naming the app's window maps the chooser as
// that window's transient, on its output, and leaves the keyboard free
// for another window. The chooser is closed once the loop has ended, as
// Close requires.
func TestTheChooserIsTransientForTheAppWindow(t *testing.T) {
	var c *Chooser
	t.Cleanup(func() {
		if c != nil {
			c.Close()
		}
	})
	r := surfacetest.Start(t)
	home := t.TempDir()
	c = headlessChooser(t, r.Shell, home)
	const handle = "/r/parented"
	answered := make(chan struct{})
	t.Cleanup(func() {
		c.Dismiss(handle)
		<-answered
	})
	go func() {
		defer close(answered)
		c.Open(portaldialogs.Caller{Handle: handle, ParentWindow: r.Parent, Modal: true}, OpenRequest{Title: "Open", CurrentFolder: home})
	}()
	r.AwaitParented(t, "lys-shell-file-chooser")
	r.AwaitOtherFocused(t)
}
