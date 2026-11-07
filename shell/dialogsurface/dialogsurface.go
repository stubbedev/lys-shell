// Package dialogsurface maps a portal dialog the way its request asks:
// transient for the app window that asked, when the request names it
// by an xdg-foreign handle the compositor can import, so the dialog is
// modal to that window only and the rest of the desktop stays usable;
// otherwise as an overlay over an output, which holds the keyboard
// while it is modal.
package dialogsurface

import (
	"errors"
	"log"
	"strings"

	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-lib/third_party/wayland/wl"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
)

// Window is a mapped dialog, either kind.
type Window interface {
	Close()
	SetFocus(widget.Widget) error
	HostSurface() *wl.Surface
}

// Spec is one dialog to map.
type Spec struct {
	// Caller is the request: its parent window and modality.
	Caller portaldialogs.Caller
	// Title names the dialog's window.
	Title string
	// Overlay is the dialog as an overlay. A parented dialog takes its
	// Root, its input hooks and its Namespace, as its app id.
	Overlay app.LayerConfig
	// Failed runs when the dialog lost its parent and could not map as
	// an overlay instead: nothing shows it any more, and the host
	// answers the request.
	Failed func()
}

// waylandPrefix marks a parent_window naming a Wayland toplevel by its
// xdg-foreign handle; x11:<xid> names an X11 window, which a Wayland
// compositor cannot parent to.
const waylandPrefix = "wayland:"

// ParentHandle is the xdg-foreign handle a request's parent_window
// names, false when it names none a Wayland dialog can be parented to.
func ParentHandle(parentWindow string) (string, bool) {
	handle, ok := strings.CutPrefix(parentWindow, waylandPrefix)
	if !ok || handle == "" {
		return "", false
	}
	return handle, true
}

// Open maps s on a: parented to the caller's window where it can be,
// else as the overlay. A parent the compositor cannot import (no
// xdg-foreign importer) falls back silently, any other failure to open
// with a warning.
func Open(a *app.Application, s Spec) (Window, error) {
	handle, ok := ParentHandle(s.Caller.ParentWindow)
	if !ok {
		return a.NewLayer(s.Overlay)
	}
	d := &parented{a: a, spec: s}
	win, err := a.NewWindow(app.WindowConfig{
		Title: s.Title, AppID: s.Overlay.Namespace, Root: s.Overlay.Root,
		ForeignParent: handle, Modal: s.Caller.Modal, OnParentLost: d.parentLost,
		OnPress: s.Overlay.OnPress, OnPointerMove: s.Overlay.OnPointerMove, OnResize: s.Overlay.OnResize,
		OnKey: s.Overlay.OnKey, KeyCapture: s.Overlay.KeyCapture, OnPresented: s.Overlay.OnPresented,
	})
	switch {
	case errors.Is(err, app.ErrForeignParentUnsupported):
		return a.NewLayer(s.Overlay)
	case err != nil:
		log.Printf("portal dialog: cannot parent the dialog to %s, so it shows as an overlay: %v", s.Caller.ParentWindow, err)
		return a.NewLayer(s.Overlay)
	}
	d.cur = win
	return d, nil
}

// parented is a dialog mapped transient for the caller's window, which
// moves to the overlay should that parent go away or never have
// existed.
type parented struct {
	a      *app.Application
	spec   Spec
	cur    Window
	focus  widget.Widget
	closed bool
}

func (d *parented) Close() {
	d.closed = true
	d.cur.Close()
}

func (d *parented) HostSurface() *wl.Surface { return d.cur.HostSurface() }

func (d *parented) SetFocus(w widget.Widget) error {
	d.focus = w
	return d.cur.SetFocus(w)
}

// parentLost moves the dialog to the overlay, its focus with it.
func (d *parented) parentLost() {
	if d.closed {
		return
	}
	log.Printf("portal dialog: %s names no window, or it closed, so the dialog shows as an overlay", d.spec.Caller.ParentWindow)
	d.cur.Close()
	layer, err := d.a.NewLayer(d.spec.Overlay)
	if err != nil {
		log.Printf("portal dialog: cannot map the overlay either: %v", err)
		d.closed = true
		if d.spec.Failed != nil {
			d.spec.Failed()
		}
		return
	}
	d.cur = layer
	if d.focus == nil {
		return
	}
	if err := layer.SetFocus(d.focus); err != nil {
		log.Printf("portal dialog: cannot focus %T on the overlay: %v", d.focus, err)
	}
}
