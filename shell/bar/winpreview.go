package bar

import (
	"context"
	"errors"
	"image"
	"log"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/service/launcher/modes"
	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/service/sharepreview"
)

// The window preview's measures, in logical pixels at scale 1.
const (
	// hoverRest is how long the pointer rests on a workspace or a dock
	// entry before its windows preview: sweeping across the bar captures
	// nothing.
	hoverRest = 450 * time.Millisecond
	// previewLeave is how long the preview stays once the pointer has
	// left both it and what opened it.
	previewLeave = 300 * time.Millisecond
	// previewThumbPx is the smaller side a window is captured down to.
	previewThumbPx = 240
	previewAreaW   = 420
	previewAreaH   = 240
	previewCellW   = 132
	previewCellH   = 96
)

// previewWindow is one window a preview shows: the compositor's id, what
// names it, and where it sits when the compositor says.
type previewWindow struct {
	id, title, appID string
	rect             lyswm.Rect
	hasRect          bool
}

// previewActions are what a preview does with its windows: capture runs
// off the loop, focus and close on it.
type previewActions interface {
	capture(ctx context.Context, w previewWindow) (image.Image, error)
	focus(w previewWindow)
	close(w previewWindow)
}

// windowPreview is the hover preview a workspace button or a dock entry
// opens: after a rest, a popover with the windows at their relative
// positions, each captured once while it is open, a click focusing one
// and a middle click closing it. Its methods run on the loop.
type windowPreview struct {
	ctx    ModuleContext
	host   app.Host
	after  func(time.Duration, func()) func()
	spawn  func(func())
	invoke func(func())
	// open shows content in a popover at anchor; closed runs as it goes.
	open func(host app.Host, anchor widget.Boundser, content widget.Widget, closed func()) (dismiss func(), err error)
	// hold keeps the bar or dock the anchor is on revealed.
	hold func() (release func())

	resting  func()
	leaving  func()
	shown    *previewShown
	captures int
}

// previewShown is an open preview: its popover, the thumbnails captured
// for it, and what keeps it open.
type previewShown struct {
	dismiss       func()
	release       func()
	cancel        context.CancelFunc
	images        map[string]image.Image
	anchorHovered bool
	hovered       bool
}

// newWindowPreview is the preview of a module's or the dock's surface;
// hold keeps that surface revealed while it shows (nil: none to keep).
func newWindowPreview(ctx ModuleContext, hold func() func()) *windowPreview {
	p := &windowPreview{ctx: ctx, spawn: func(fn func()) { go fn() }, hold: hold}
	if ctx.App != nil {
		p.after, p.invoke = loopAfter(ctx.App), ctx.App.Invoke
		p.open = func(host app.Host, anchor widget.Boundser, content widget.Widget, closed func()) (func(), error) {
			pop, err := ctx.App.OpenPopover(host, app.PopoverConfig{
				Anchor: anchor, Content: content, Gap: widget.PopoverGap,
				Dismissal: app.PopoverPassive, OnClosed: closed,
			})
			if err != nil {
				return nil, err
			}
			return pop.Dismiss, nil
		}
	}
	return p
}

// Attach keeps the surface the popovers open from.
func (p *windowPreview) Attach(host app.Host) { p.host = host }

// hover is the pointer entering or leaving what opens the preview;
// gather lists the windows and their actions when it opens.
func (p *windowPreview) hover(on bool, anchor widget.Boundser, gather func() ([]previewWindow, previewActions)) {
	if p.open == nil {
		return
	}
	if p.shown != nil {
		p.shown.anchorHovered = on
		p.syncLeave()
		return
	}
	if !on {
		p.stopRest()
		return
	}
	p.stopRest()
	p.resting = p.after(hoverRest, func() {
		p.resting = nil
		p.show(anchor, gather)
	})
}

func (p *windowPreview) stopRest() {
	if p.resting != nil {
		p.resting()
		p.resting = nil
	}
}

// syncLeave closes the preview a moment after the pointer left both it
// and its anchor, and keeps it while the pointer is on either.
func (p *windowPreview) syncLeave() {
	s := p.shown
	if s == nil {
		return
	}
	if s.anchorHovered || s.hovered {
		if p.leaving != nil {
			p.leaving()
			p.leaving = nil
		}
		return
	}
	if p.leaving != nil {
		return
	}
	p.leaving = p.after(previewLeave, func() {
		p.leaving = nil
		p.close()
	})
}

// show opens the popover for the windows gather lists and captures each
// of them once, off the loop.
func (p *windowPreview) show(anchor widget.Boundser, gather func() ([]previewWindow, previewActions)) {
	windows, actions := gather()
	if len(windows) == 0 || p.host == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &previewShown{cancel: cancel, images: map[string]image.Image{}, anchorHovered: true}
	content, pictures := p.build(windows, actions, s)
	dismiss, err := p.open(p.host, anchor, content, func() { p.closed(s) })
	if err != nil {
		cancel()
		log.Printf("window preview: %v", err)
		return
	}
	s.dismiss = dismiss
	if p.hold != nil {
		s.release = p.hold()
	}
	p.shown = s
	for _, w := range windows {
		picture := pictures[w.id]
		p.captures++
		p.spawn(func() {
			img, err := actions.capture(ctx, w)
			if err != nil || ctx.Err() != nil {
				return
			}
			p.invoke(func() {
				if p.shown != s {
					return
				}
				s.images[w.id] = img
				picture.SetImage(img)
			})
		})
	}
}

// close dismisses the open preview.
func (p *windowPreview) close() {
	if s := p.shown; s != nil {
		s.dismiss()
		p.closed(s)
	}
}

// closed lets go of a preview that went: its captures stop, its
// thumbnails are dropped and the surface may hide again.
func (p *windowPreview) closed(s *previewShown) {
	if p.shown != s {
		return
	}
	p.shown = nil
	s.cancel()
	s.images = nil
	if s.release != nil {
		s.release()
	}
	if p.leaving != nil {
		p.leaving()
		p.leaving = nil
	}
}

// stop closes the preview and stops a rest under way.
func (p *windowPreview) stop() {
	p.stopRest()
	p.close()
}

// build lays the windows out: at their relative positions when the
// compositor gives them, in a row otherwise; each cell shows the app's
// icon until its thumbnail lands, over its title.
func (p *windowPreview) build(windows []previewWindow, actions previewActions, s *previewShown) (widget.Widget, map[string]*widget.Image) {
	px := func(v int) int { return int(float64(v) * float64(p.ctx.Config.Styling.Scale)) }
	pictures := make(map[string]*widget.Image, len(windows))
	cells := make([]*widget.Box, len(windows))
	for i, w := range windows {
		cell := widget.NewBox(widget.Column, 2, 0)
		cell.AddClass("window-preview-cell")
		picture := widget.NewImage(nil)
		picture.SetScale(widget.ImageFit)
		picture.AddClass("window-preview-thumbnail")
		cell.Append(picture, true)
		face, textPx := dropdownFont(p.ctx)
		title := widget.NewLabel(face, textPx, w.title, 0)
		title.SetEllipsize(widget.EllipsizeEnd)
		caption := widget.NewBox(widget.Row, 4, 0)
		icon := widget.NewThemeIconFallback(p.iconOf(w.appID), px(16))
		caption.AppendAligned(icon, false, widget.AlignCenter)
		caption.Append(title, true)
		cell.Append(caption, false)
		cell.SetTooltip(w.title)
		cell.SetOnPointer(func(e widget.PointerEvent) bool {
			switch e.Button {
			case widget.BTNLeft:
				actions.focus(w)
				p.close()
			case widget.BTNMiddle:
				actions.close(w)
				p.close()
			default:
				return false
			}
			return true
		})
		pictures[w.id] = picture
		cells[i] = cell
	}
	root := widget.NewBox(widget.Column, 0, 0)
	root.AddClass("window-preview")
	root.SetOnHoverWithin(func(on bool) {
		s.hovered = on
		p.syncLeave()
	})
	if placed := placeWindows(windows, px(previewAreaW), px(previewAreaH)); placed != nil {
		kids := make([]widget.Widget, len(cells))
		for i, c := range cells {
			kids[i] = c
		}
		root.Append(&previewCanvas{size: widget.Size{W: px(previewAreaW), H: px(previewAreaH)}, cells: kids, rects: placed}, false)
		return root, pictures
	}
	row := widget.NewBox(widget.Row, px(8), 0)
	for _, c := range cells {
		c.SetSizeRequest(px(previewCellW), px(previewCellH))
		row.Append(c, false)
	}
	root.Append(row, false)
	return root, pictures
}

// placeWindows scales the windows' rectangles into a w x h area, keeping
// their arrangement; nil when a window has no rectangle.
func placeWindows(windows []previewWindow, w, h int) []render.Rect {
	var box lyswm.Rect
	for i, win := range windows {
		if !win.hasRect || win.rect.Width <= 0 || win.rect.Height <= 0 {
			return nil
		}
		if i == 0 {
			box = win.rect
			continue
		}
		x0, y0 := min(box.X, win.rect.X), min(box.Y, win.rect.Y)
		x1, y1 := max(box.X+box.Width, win.rect.X+win.rect.Width), max(box.Y+box.Height, win.rect.Y+win.rect.Height)
		box = lyswm.Rect{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}
	}
	scale := min(float64(w)/float64(box.Width), float64(h)/float64(box.Height))
	offX := (w - int(float64(box.Width)*scale)) / 2
	offY := (h - int(float64(box.Height)*scale)) / 2
	out := make([]render.Rect, len(windows))
	for i, win := range windows {
		out[i] = render.Rect{
			X: offX + int(float64(win.rect.X-box.X)*scale), Y: offY + int(float64(win.rect.Y-box.Y)*scale),
			W: max(1, int(float64(win.rect.Width)*scale)), H: max(1, int(float64(win.rect.Height)*scale)),
		}
	}
	return out
}

// previewCanvas lays its cells at fixed rectangles in a fixed area.
type previewCanvas struct {
	widget.Base
	size  widget.Size
	cells []widget.Widget
	rects []render.Rect
}

// Measure is the area.
func (c *previewCanvas) Measure(axis widget.Axis, _ int) widget.Sizes {
	if axis == widget.Row {
		return widget.FixedSizes(c.size.W)
	}
	return widget.FixedSizes(c.size.H)
}

// Arrange lays each cell at its rectangle inside the area.
func (c *previewCanvas) Arrange(r render.Rect) {
	c.ArrangeSelf(r)
	for i, cell := range c.cells {
		rect := c.rects[i]
		rect.X, rect.Y = rect.X+r.X, rect.Y+r.Y
		cell.Arrange(rect)
	}
	widget.SetParents(c, c.cells...)
}

// ArrangeRoot mirrors Arrange for the root path.
func (c *previewCanvas) ArrangeRoot(r render.Rect) { c.Arrange(r) }

// Paint paints the cells.
func (c *previewCanvas) Paint(cv *render.Canvas) {
	for _, cell := range c.cells {
		widget.PaintChild(cv, cell)
	}
}

// Children is the cells, for the style cascade and hover.
func (c *previewCanvas) Children() []widget.Widget { return c.cells }

// HitTest descends into the cell under p.
func (c *previewCanvas) HitTest(p widget.Point) widget.Widget {
	for _, cell := range c.cells {
		if hit := cell.HitTest(p); hit != nil {
			return hit
		}
	}
	return c.HitLeaf(c, p)
}

// iconOf is the icon an app's windows show.
func (p *windowPreview) iconOf(appID string) string {
	const fallback = "ld-app-window-symbolic"
	if p.ctx.Tasks == nil {
		return fallback
	}
	return p.ctx.Tasks.appIcon(appID, fallback)
}

// errWindowGone is a window that closed before its capture.
var errWindowGone = errors.New("the window is gone")

// compositorActions previews the windows of the compositor's IPC: one
// window list per preview, read at its first capture.
type compositorActions struct {
	backend modes.WindowBackend
	once    sync.Once
	list    map[string]modes.WindowInfo
	err     error
}

func (a *compositorActions) capture(ctx context.Context, w previewWindow) (image.Image, error) {
	a.once.Do(func() {
		windows, err := a.backend.List(ctx)
		a.list, a.err = make(map[string]modes.WindowInfo, len(windows)), err
		for _, info := range windows {
			a.list[info.ID] = info
		}
	})
	if a.err != nil {
		return nil, a.err
	}
	info, ok := a.list[w.id]
	if !ok {
		return nil, errWindowGone
	}
	return info.CaptureAt(ctx, previewThumbPx)
}

func (a *compositorActions) focus(w previewWindow) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), previewIPCTimeout)
		defer cancel()
		if err := a.backend.Focus(ctx, w.id); err != nil {
			log.Printf("window preview: focus %s: %v", w.id, err)
		}
	}()
}

func (a *compositorActions) close(w previewWindow) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), previewIPCTimeout)
		defer cancel()
		if err := a.backend.Close(ctx, w.id); err != nil {
			log.Printf("window preview: close %s: %v", w.id, err)
		}
	}()
}

// previewIPCTimeout bounds a focus or close sent to the compositor.
const previewIPCTimeout = 5 * time.Second

// taskActions previews the foreign-toplevel windows the dock lists.
type taskActions struct{ windows map[string]taskWindow }

func (a taskActions) capture(_ context.Context, w previewWindow) (image.Image, error) {
	return sharepreview.WindowThumb(sharepreview.Toplevel{Class: w.appID, Title: w.title}, previewThumbPx)
}

func (a taskActions) focus(w previewWindow) {
	if tw, ok := a.windows[w.id]; ok {
		tw.Activate()
	}
}

func (a taskActions) close(w previewWindow) {
	if tw, ok := a.windows[w.id]; ok {
		tw.Close()
	}
}
