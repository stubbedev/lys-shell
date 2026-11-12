// Package dialogsurface maps a portal dialog the way its request asks:
// transient for the app window that asked, when the request names it
// by an xdg-foreign handle the compositor can import, so the dialog is
// modal to that window only and the rest of the desktop stays usable;
// otherwise as an overlay over an output, which holds the keyboard
// while it is modal. The dialog builds its tree for the surface it is
// on: on the overlay it floats and frames itself, in a window the
// compositor frames and resizes it.
package dialogsurface

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-desktop/shell/apptheme"
	"github.com/stubbedev/lys-lib/intconv"
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

// Surface is what a dialog shows on, which decides what frames it.
type Surface int

const (
	// OnOverlay is an overlay layer over an output: the dialog floats
	// on it as a card with its own border, rounding and shadow.
	OnOverlay Surface = iota
	// OnWindow is a toplevel transient for the caller's window: the
	// compositor's decoration frames it and the compositor resizes it,
	// so the dialog fills the window and draws no frame of its own.
	OnWindow
)

// String names s.
func (s Surface) String() string {
	switch s {
	case OnOverlay:
		return "overlay"
	case OnWindow:
		return "window"
	}
	return "Surface(" + strconv.Itoa(int(s)) + ")"
}

// Spec is one dialog to map.
type Spec struct {
	// Caller is the request: its parent window and modality.
	Caller portaldialogs.Caller
	// Title names the dialog's window.
	Title string
	// Root builds the dialog's tree for the surface it shows on. It
	// runs again, for the overlay, when a window dialog moves there.
	Root func(Surface) widget.Widget
	// Layer is the dialog as an overlay, its Root left nil: Root fills
	// it. A window dialog takes its input hooks other than OnResize,
	// and its Namespace as its app id.
	Layer app.LayerConfig
	// Window sizes the dialog as a window.
	Window WindowSize
	// Failed runs when the dialog lost its parent and could not map as
	// an overlay instead: nothing shows it any more, and the host
	// answers the request.
	Failed func()
}

// WindowSize is a window dialog's size. Initial is the size it opens
// at, the tree's natural size on a zero axis; Min and Max bound how far
// the compositor resizes it, unbounded on a zero axis. OnResize, when
// set, hears every size the window takes, the first included.
type WindowSize struct {
	Initial, Min, Max widget.Size
	OnResize          func(widget.Size)
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

// LayerConfig is s as an overlay: its Layer, with the tree Root builds
// for the overlay.
func (s Spec) LayerConfig() app.LayerConfig {
	cfg := s.Layer
	cfg.Root = s.Root(OnOverlay)
	return cfg
}

// Open maps s on a: parented to the caller's window where it can be,
// else as the overlay. A parent the compositor cannot import (no
// xdg-foreign importer) falls back silently, any other failure to open
// with a warning. A spec without Root, with a Layer.Root, or with a
// window size out of range is an error.
func Open(a *app.Application, s Spec) (Window, error) {
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("portal dialog %q: %w", s.Title, err)
	}
	handle, ok := ParentHandle(s.Caller.ParentWindow)
	if !ok {
		return a.NewLayer(s.LayerConfig())
	}
	d := &parented{a: a, spec: s}
	win, err := a.NewWindow(s.windowConfig(handle, d.parentLost))
	switch {
	case errors.Is(err, app.ErrForeignParentUnsupported):
		return a.NewLayer(s.LayerConfig())
	case err != nil:
		log.Printf("portal dialog: cannot parent the dialog to %s, so it shows as an overlay: %v", s.Caller.ParentWindow, err)
		return a.NewLayer(s.LayerConfig())
	}
	d.cur = win
	return d, nil
}

func (s Spec) validate() error {
	switch {
	case s.Root == nil:
		return errors.New("no Root builds its tree")
	case s.Layer.Root != nil:
		return errors.New("Layer.Root is set: Root builds the tree for each surface")
	}
	w := s.Window
	for _, axis := range []struct {
		name                 string
		initial, least, most int
	}{
		{"width", w.Initial.W, w.Min.W, w.Max.W},
		{"height", w.Initial.H, w.Min.H, w.Max.H},
	} {
		switch {
		case axis.initial < 0 || axis.least < 0 || axis.most < 0:
			return fmt.Errorf("window %s %d, min %d, max %d: negative", axis.name, axis.initial, axis.least, axis.most)
		case axis.most > 0 && axis.least > axis.most:
			return fmt.Errorf("window %s min %d above its max %d", axis.name, axis.least, axis.most)
		case axis.initial > 0 && (axis.initial < axis.least || axis.most > 0 && axis.initial > axis.most):
			return fmt.Errorf("window %s %d outside its min %d and max %d", axis.name, axis.initial, axis.least, axis.most)
		}
	}
	return nil
}

func (s Spec) windowConfig(handle string, parentLost func()) app.WindowConfig {
	l, w := s.Layer, s.Window
	cfg := app.WindowConfig{
		Title: s.Title, AppID: l.Namespace, Root: s.Root(OnWindow),
		Width: intconv.Must[uint32](w.Initial.W), Height: intconv.Must[uint32](w.Initial.H),
		MinWidth: intconv.Must[uint32](w.Min.W), MinHeight: intconv.Must[uint32](w.Min.H),
		MaxWidth: intconv.Must[uint32](w.Max.W), MaxHeight: intconv.Must[uint32](w.Max.H),
		ForeignParent: handle, Modal: s.Caller.Modal, OnParentLost: parentLost,
		OnPress: l.OnPress, OnPointerMove: l.OnPointerMove,
		OnKey: l.OnKey, KeyCapture: l.KeyCapture, OnPresented: l.OnPresented,
	}
	if w.OnResize != nil {
		cfg.OnResize = func(width, height int) { w.OnResize(widget.Size{W: width, H: height}) }
	}
	return cfg
}

const unframed = "border: none; border-radius: 0px; box-shadow: none;"

// CardRoot is the root of a dialog that is one card, on s:
// window.<floatingClass> around rev, the revealer that plays the card's
// entry and exit, with sheet attached. On the overlay the window is
// transparent (the class's rule) and the card floats on it in its own
// frame; in a window the window's background fills it and the card
// draws no frame.
func CardRoot(s Surface, card *widget.Card, floatingClass string, sheet *widget.Stylesheet) (root *widget.Box, rev *widget.Revealer) {
	root = widget.NewBox(widget.Column, 0, 0)
	root.SetElement("window")
	switch s {
	case OnOverlay:
		root.AddClass(floatingClass)
		card.SetInlineStylePriority("", apptheme.ComponentPriority)
	case OnWindow:
		card.SetInlineStylePriority(unframed, apptheme.ComponentPriority)
	}
	if sheet != nil {
		root.AttachStylesheet(sheet)
	}
	rev = widget.NewRevealer(card)
	root.Append(rev, true)
	return root, rev
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
	layer, err := d.a.NewLayer(d.spec.LayerConfig())
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
