// Package shotthumb is the floating thumbnail a screenshot leaves in a
// screen corner: it counts down to its own dismissal, pausing while the
// pointer is over it; a click opens the markup editor, a drag carries the
// file and its PNG into another app, its delete button discards the
// capture, and a sideways swipe or Escape dismisses it.
package shotthumb

import (
	"io"
	"math"
	"os"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/transfer"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/clock"
	"github.com/stubbedev/lys-shell/shell/keys"
)

const (
	maxW         = 240
	maxH         = 160
	pad          = 6
	margin       = 16
	closePx      = 22
	swipeDismiss = 80
	namespace    = "lys-shell-screenshot-thumbnail"
)

var (
	backing   = render.RGBA(20, 20, 26, 235)
	closeFill = render.RGBA(0, 0, 0, 190)
	closeInk  = render.RGB(255, 255, 255)
)

// Shot is a saved capture: where it is and its pixels.
type Shot struct {
	Path  string
	Image render.StillImage
}

// Window is the mapped layer surface (app.LayerWindow).
type Window interface{ Close() }

// Deps are the thumbnail's collaborators.
type Deps struct {
	// Open maps the layer surface.
	Open func(app.LayerConfig) (Window, error)
	// KeySym reads a key code through the seat's keymap.
	KeySym keys.Translator
	// Invoke runs fn on the loop goroutine.
	Invoke func(fn func())
	Clock  clock.Clock
	// Edit opens the markup editor on the shot.
	Edit func(Shot)
	// Delete discards the shot: its file and its clipboard entry.
	Delete func(Shot)
	// Done runs once the thumbnail no longer shows the shot.
	Done func(Shot)
}

// Thumbnail shows one shot at a time; its methods run on the loop goroutine.
type Thumbnail struct {
	d   Deps
	cur *face
}

type face struct {
	shot      Shot
	win       Window
	view      *view
	remaining time.Duration
	started   time.Time
	stop      func() bool
	hovered   bool
}

// New builds the thumbnail; nothing maps until Show.
func New(d Deps) *Thumbnail { return &Thumbnail{d: d} }

// Show replaces whatever the thumbnail shows with s for cfg.ThumbnailMs,
// in cfg.ThumbnailPosition's corner of out (nil leaves the output to the
// compositor). A zero duration shows nothing.
func (t *Thumbnail) Show(s Shot, out *app.Output, cfg config.ScreenshotConfig) error {
	t.Dismiss()
	if cfg.ThumbnailMs == 0 {
		t.d.Done(s)
		return nil
	}
	f := &face{shot: s, remaining: time.Duration(cfg.ThumbnailMs) * time.Millisecond}
	f.view = &view{t: t, f: f}
	anchor := anchors(cfg.ThumbnailPosition)
	win, err := t.d.Open(app.LayerConfig{
		Output:    out,
		Layer:     app.LayerOverlay,
		Anchor:    anchor,
		Margin:    margins(anchor),
		Keyboard:  app.KeyboardOnDemand,
		Namespace: namespace,
		Root:      f.view,
		OnKey: keys.OnEscape(t.d.KeySym, func() {
			if t.cur == f {
				t.Dismiss()
			}
		}),
	})
	if err != nil {
		t.d.Done(s)
		return err
	}
	f.win = win
	t.cur = f
	t.arm(f)
	return nil
}

// Shown is the shot on screen, false when there is none.
func (t *Thumbnail) Shown() (Shot, bool) {
	if t.cur == nil {
		return Shot{}, false
	}
	return t.cur.shot, true
}

// Dismiss takes the thumbnail down, leaving the file as it is.
func (t *Thumbnail) Dismiss() {
	f := t.cur
	if f == nil {
		return
	}
	t.cur = nil
	if f.stop != nil {
		f.stop()
	}
	f.win.Close()
	f.view.t = nil
	t.d.Done(f.shot)
}

func (t *Thumbnail) arm(f *face) {
	f.started = t.d.Clock.Now()
	f.stop = t.d.Clock.AfterFunc(f.remaining, func() {
		t.d.Invoke(func() {
			if t.cur == f && !f.hovered {
				t.Dismiss()
			}
		})
	})
}

func (t *Thumbnail) hover(f *face, on bool) {
	if t.cur != f || f.hovered == on {
		return
	}
	f.hovered = on
	if on {
		if f.stop() {
			f.remaining -= t.d.Clock.Now().Sub(f.started)
		}
		return
	}
	t.arm(f)
}

func (t *Thumbnail) edit(f *face) {
	if t.cur != f {
		return
	}
	shot := f.shot
	t.d.Edit(shot)
	t.Dismiss()
}

func (t *Thumbnail) remove(f *face) {
	if t.cur != f {
		return
	}
	shot := f.shot
	t.Dismiss()
	t.d.Delete(shot)
}

func anchors(c config.ScreenshotCorner) app.Anchor {
	switch c {
	case config.ScreenshotTopLeft:
		return app.AnchorTop | app.AnchorLeft
	case config.ScreenshotTopRight:
		return app.AnchorTop | app.AnchorRight
	case config.ScreenshotBottomLeft:
		return app.AnchorBottom | app.AnchorLeft
	}
	return app.AnchorBottom | app.AnchorRight
}

func margins(a app.Anchor) app.Margins {
	var m app.Margins
	if a&app.AnchorTop != 0 {
		m.Top = margin
	} else {
		m.Bottom = margin
	}
	if a&app.AnchorLeft != 0 {
		m.Left = margin
	} else {
		m.Right = margin
	}
	return m
}

// Size is the logical size the thumbnail shows an image of w x h pixels
// at: shrunk to fit maxW x maxH, never enlarged, at least one pixel.
func Size(w, h int) (int, int) {
	s := min(1, float64(maxW)/float64(max(w, 1)), float64(maxH)/float64(max(h, 1)))
	return max(1, int(float64(w)*s)), max(1, int(float64(h)*s))
}

// DragContent is what dragging the shot offers: its file as a uri list,
// then its PNG, streamed from the saved file.
func DragContent(s Shot) *widget.DragContent {
	png := transfer.Content{
		Mimes: []string{transfer.MimePNG},
		Write: func(_ string, w io.Writer) error {
			f, err := os.Open(s.Path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(w, f)
			return err
		},
	}
	return &widget.DragContent{Content: transfer.Merge(transfer.Files(s.Path), png), Actions: transfer.ActionCopy}
}

type view struct {
	widget.Base
	t *Thumbnail
	f *face

	swipe float64
}

func (v *view) Measure(axis widget.Axis, _ int) widget.Sizes {
	b := v.f.shot.Image.Bounds()
	w, h := Size(b.Dx(), b.Dy())
	n := w + 2*pad
	if axis == widget.Column {
		n = h + 2*pad
	}
	return widget.Sizes{Min: n, Nat: n}
}

func (v *view) HitTest(p widget.Point) widget.Widget { return v.HitLeaf(v, p) }

func (v *view) closeRect() render.Rect {
	b := v.Bounds()
	return render.Rect{X: b.X + b.W - closePx - pad/2, Y: b.Y + pad/2, W: closePx, H: closePx}
}

func (v *view) Paint(cv *render.Canvas) {
	b := v.Bounds()
	cv.RoundedRect(b, 10, backing)
	cv.DrawImageCover(v.f.shot.Image, render.Rect{X: b.X + pad, Y: b.Y + pad, W: b.W - 2*pad, H: b.H - 2*pad}, render.UniformCorners(6))
	c := v.closeRect()
	cv.RoundedRect(c, c.W/2, closeFill)
	in := c.W / 4
	cv.Line(c.X+in, c.Y+in, c.X+c.W-in, c.Y+c.H-in, 2, closeInk)
	cv.Line(c.X+c.W-in, c.Y+in, c.X+in, c.Y+c.H-in, 2, closeInk)
}

func (v *view) SetHovered(on bool) {
	if v.t != nil {
		v.t.hover(v.f, on)
	}
}

func (v *view) ClickAt(p widget.Point) {
	if v.t == nil {
		return
	}
	c := v.closeRect()
	if p.X >= c.X && p.X < c.X+c.W && p.Y >= c.Y && p.Y < c.Y+c.H {
		v.t.remove(v.f)
		return
	}
	v.t.edit(v.f)
}

func (v *view) DragContent() *widget.DragContent {
	if v.t == nil {
		return nil
	}
	return DragContent(v.f.shot)
}

func (v *view) ClaimPixels(dx, dy float64) bool {
	if v.t == nil || (v.swipe == 0 && math.Abs(dx) <= math.Abs(dy)) {
		return false
	}
	v.swipe += dx
	if math.Abs(v.swipe) > swipeDismiss {
		v.t.Dismiss()
	}
	return true
}

func (v *view) ScrollEnd() { v.swipe = 0 }

func (v *view) KeyAction(a widget.KeyAction, _ widget.Mods) {
	if a == widget.KeyDismiss && v.t != nil {
		v.t.Dismiss()
	}
}
