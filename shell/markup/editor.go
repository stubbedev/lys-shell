package markup

import (
	"image"
	"log"
	"path/filepath"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-lib/xkb"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/i18n"
)

var sizes = []int{2, 4, 8}

const (
	editorW     = 1000
	editorH     = 700
	editorAppID = "lys-shell-markup"
	swatchPx    = 18
	canvasPad   = 16
)

var (
	canvasBacking = render.RGB(24, 24, 30)
	cropShade     = render.RGBA(0, 0, 0, 150)
	cropEdge      = render.RGB(255, 255, 255)
	caretInk      = render.RGB(255, 255, 255)
)

// Deps are what the editor needs from the shell.
type Deps struct {
	App *app.Application
	// Font draws the toolbar, text marks and marker numbers; Px is the
	// toolbar's size.
	Font render.Font
	Px   float64
	// Palette is the colors the toolbar offers, the first one picked.
	Palette []render.Color
	// Save writes the flattened image over path, atomically, and copies it.
	Save func(img *image.RGBA, path string) error
	// SaveAs writes the flattened image to a path the user picked.
	SaveAs func(img *image.RGBA, path string) error
	// Closed runs once the window is gone, whatever closed it.
	Closed func()
}

// Editor is one open markup window. Its methods run on the loop goroutine.
type Editor struct {
	d     Deps
	doc   *Doc
	path  string
	win   *app.Window
	view  *canvasView
	tool  Tool
	color render.Color
	size  int

	tools  map[Tool]*widget.ToggleButton
	closed bool
}

// Open shows the editor over base, the captured image saved at path. It
// draws base as it is in memory: the file is never read.
func Open(d Deps, base render.StillImage, path string) (*Editor, error) {
	e := &Editor{d: d, doc: NewDoc(base), path: path, tool: ToolArrow, size: sizes[1], tools: map[Tool]*widget.ToggleButton{}}
	if len(d.Palette) > 0 {
		e.color = d.Palette[0]
	}
	e.view = &canvasView{e: e}
	root := widget.NewBox(widget.Column, 0, 0)
	root.Append(e.toolbar(), false)
	root.Append(e.view, true)
	win, err := d.App.NewWindow(app.WindowConfig{
		Title:      i18n.T("screenshot-editor-title", i18n.Str("file", filepath.Base(path))),
		AppID:      editorAppID,
		Width:      editorW,
		Height:     editorH,
		Root:       root,
		Background: canvasBacking,
		KeyCapture: e.key,
		OnClosed:   e.gone,
	})
	if err != nil {
		return nil, err
	}
	e.win = win
	if err := win.SetFocus(e.view); err != nil {
		log.Printf("markup: focusing the canvas: %v", err)
	}
	return e, nil
}

// Doc is the document under edit.
func (e *Editor) Doc() *Doc { return e.doc }

// Close closes the window without saving.
func (e *Editor) Close() {
	if e.win != nil {
		e.win.Close()
	}
	e.gone()
}

func (e *Editor) gone() {
	if e.closed {
		return
	}
	e.closed = true
	e.doc = nil
	e.view.e = nil
	if e.d.Closed != nil {
		e.d.Closed()
	}
}

// SetTool picks what the next drag draws.
func (e *Editor) SetTool(t Tool) {
	e.view.commitText()
	e.tool = t
	if b, ok := e.tools[t]; ok && !b.Active() {
		b.SetActive(true)
	}
}

// Save flattens the marks into the image, writes it over the capture and
// closes the editor; a failed write keeps it open and is returned.
func (e *Editor) Save() error {
	e.view.commitText()
	if err := e.d.Save(Flatten(e.doc, e.d.Font), e.path); err != nil {
		return err
	}
	e.Close()
	return nil
}

// SaveAs asks where to save a copy and writes it there.
func (e *Editor) SaveAs() {
	e.view.commitText()
	img := Flatten(e.doc, e.d.Font)
	_, err := e.d.App.SaveFileDialog(e.win, app.SaveDialogConfig{
		Title:     i18n.T("screenshot-editor-save-as"),
		StartDir:  filepath.Dir(e.path),
		StartName: filepath.Base(e.path),
		OnSave: func(path string) {
			if err := e.d.SaveAs(img, path); err != nil {
				log.Printf("markup: %v", err)
				return
			}
			e.Close()
		},
	})
	if err != nil {
		log.Printf("markup: save as: %v", err)
	}
}

// Undo takes the newest mark back.
func (e *Editor) Undo() {
	e.view.commitText()
	if e.doc.Undo() {
		e.view.Invalidate()
	}
}

// Redo reapplies the last mark undo took back.
func (e *Editor) Redo() {
	if e.doc.Redo() {
		e.view.Invalidate()
	}
}

func (e *Editor) key(a app.Accel) bool {
	ctrl := a.Mods&app.ModCtrl != 0
	shift := a.Mods&app.ModShift != 0
	switch {
	case ctrl && (a.Sym == 'z' || a.Sym == 'Z') && shift, ctrl && (a.Sym == 'y' || a.Sym == 'Y'):
		e.Redo()
	case ctrl && (a.Sym == 'z' || a.Sym == 'Z'):
		e.Undo()
	case ctrl && (a.Sym == 's' || a.Sym == 'S'):
		e.SaveAs()
	case e.view.typing != nil:
		return false
	case a.Sym == xkb.KeyReturn || a.Sym == xkb.KeyKPEnter:
		if err := e.Save(); err != nil {
			log.Printf("markup: %v", err)
		}
	case a.Sym == xkb.KeyEscape:
		e.Close()
	default:
		return false
	}
	return true
}

func (e *Editor) toolbar() widget.Widget {
	bar := widget.NewBox(widget.Row, 4, 6)
	bar.AddClass("markup-toolbar")
	px := int(e.d.Px * 1.25)
	var first *widget.ToggleButton
	for _, t := range Tools {
		b := widget.NewToggleButton(widget.NewThemeIcon(toolIcons[t], px), 6, 6)
		b.SetTooltip(i18n.T(toolLabels[t]))
		b.OnToggled = func(on bool) {
			if on && e.tool != t {
				e.SetTool(t)
			}
		}
		if first == nil {
			first = b
		} else {
			b.SetGroup(first)
		}
		e.tools[t] = b
		bar.Append(b, false)
	}
	e.tools[e.tool].SetActive(true)
	bar.Append(separator(), false)
	var firstColor *widget.ToggleButton
	for i, c := range e.d.Palette {
		b := widget.NewToggleButton(&swatch{color: c, d: swatchPx}, 4, 4)
		b.OnToggled = func(on bool) {
			if on {
				e.color = c
			}
		}
		if firstColor == nil {
			firstColor = b
		} else {
			b.SetGroup(firstColor)
		}
		b.SetActive(i == 0)
		bar.Append(b, false)
	}
	bar.Append(separator(), false)
	var firstSize *widget.ToggleButton
	for _, s := range sizes {
		b := widget.NewToggleButton(&swatch{color: caretInk, d: 4 + s, round: true, box: swatchPx}, 4, 4)
		b.OnToggled = func(on bool) {
			if on {
				e.size = s
			}
		}
		if firstSize == nil {
			firstSize = b
		} else {
			b.SetGroup(firstSize)
		}
		b.SetActive(s == e.size)
		bar.Append(b, false)
	}
	bar.Append(separator(), false)
	bar.Append(actionButton("ld-undo-2-symbolic", "screenshot-editor-undo", px, e.Undo), false)
	bar.Append(actionButton("ld-redo-2-symbolic", "screenshot-editor-redo", px, e.Redo), false)
	bar.Append(widget.NewBox(widget.Row, 0, 0), true)
	bar.Append(actionButton("ld-save-symbolic", "screenshot-editor-save", px, func() {
		if err := e.Save(); err != nil {
			log.Printf("markup: %v", err)
		}
	}), false)
	return bar
}

var toolIcons = map[Tool]string{
	ToolCrop:      "ld-crop-symbolic",
	ToolArrow:     "ld-move-up-right-symbolic",
	ToolRect:      "ld-square-symbolic",
	ToolEllipse:   "ld-circle-symbolic",
	ToolFreehand:  "ld-pencil-symbolic",
	ToolText:      "ld-type-symbolic",
	ToolHighlight: "ld-highlighter-symbolic",
	ToolMarker:    "ld-list-ordered-symbolic",
	ToolPixelate:  "ld-grid-3x3-symbolic",
}

var toolLabels = map[Tool]string{
	ToolCrop:      "screenshot-tool-crop",
	ToolArrow:     "screenshot-tool-arrow",
	ToolRect:      "screenshot-tool-rectangle",
	ToolEllipse:   "screenshot-tool-ellipse",
	ToolFreehand:  "screenshot-tool-freehand",
	ToolText:      "screenshot-tool-text",
	ToolHighlight: "screenshot-tool-highlight",
	ToolMarker:    "screenshot-tool-marker",
	ToolPixelate:  "screenshot-tool-pixelate",
}

func actionButton(icon, tooltip string, px int, fn func()) *widget.Button {
	b := widget.NewButton(widget.NewThemeIcon(icon, px), 6, 6)
	b.SetTooltip(i18n.T(tooltip))
	b.OnClick = fn
	return b
}

func separator() widget.Widget { return &swatch{d: 0, box: 8} }

type swatch struct {
	widget.Base
	color render.Color
	d     int
	box   int
	round bool
}

func (s *swatch) Measure(widget.Axis, int) widget.Sizes {
	n := max(s.d, s.box)
	return widget.Sizes{Min: n, Nat: n}
}

func (s *swatch) HitTest(widget.Point) widget.Widget { return nil }

func (s *swatch) Paint(cv *render.Canvas) {
	if s.d == 0 {
		return
	}
	b := s.Bounds()
	r := render.Rect{X: b.X + (b.W-s.d)/2, Y: b.Y + (b.H-s.d)/2, W: s.d, H: s.d}
	radius := 3
	if s.round {
		radius = s.d / 2
	}
	cv.RoundedRect(r, radius, s.color)
}

type canvasView struct {
	widget.Base
	e       *Editor
	pending *Mark
	typing  *Mark
}

func (c *canvasView) Measure(widget.Axis, int) widget.Sizes { return widget.Sizes{} }

func (c *canvasView) HitTest(p widget.Point) widget.Widget { return c.HitLeaf(c, p) }

func (c *canvasView) CursorName() string { return "crosshair" }

func (c *canvasView) KeyAction(a widget.KeyAction, _ widget.Mods) {
	if c.typing == nil {
		return
	}
	switch a {
	case widget.KeyEnter:
		c.commitText()
	case widget.KeyDismiss:
		c.typing = nil
	case widget.KeyBackspace:
		if r := []rune(c.typing.Text); len(r) > 0 {
			c.typing.Text = string(r[:len(r)-1])
		}
	default:
		return
	}
	c.Invalidate()
}

func (c *canvasView) InsertRune(r rune) {
	if c.typing == nil {
		return
	}
	c.typing.Text += string(r)
	c.Invalidate()
}

func (c *canvasView) commitText() {
	if c.typing == nil || c.e == nil {
		return
	}
	c.e.doc.Push(*c.typing)
	c.typing = nil
	c.Invalidate()
}

func (c *canvasView) fit() View {
	b := c.Bounds()
	ib := c.e.doc.Base().Bounds()
	aw, ah := float64(b.W-2*canvasPad), float64(b.H-2*canvasPad)
	s := min(1, aw/float64(max(ib.Dx(), 1)), ah/float64(max(ib.Dy(), 1)))
	if s <= 0 {
		s = 1
	}
	w, h := float64(float64(ib.Dx())*s), float64(float64(ib.Dy())*s)
	return View{
		X:     float64(b.X) + float64((float64(b.W)-w)/2) - float64(float64(ib.Min.X)*s),
		Y:     float64(b.Y) + float64((float64(b.H)-h)/2) - float64(float64(ib.Min.Y)*s),
		Scale: s,
	}
}

func (c *canvasView) imagePoint(p widget.Point) image.Point {
	ip := c.fit().Image(float64(p.X), float64(p.Y))
	b := c.e.doc.Base().Bounds()
	return image.Pt(min(max(ip.X, b.Min.X), b.Max.X), min(max(ip.Y, b.Min.Y), b.Max.Y))
}

func (c *canvasView) PressAt(p widget.Point) {
	if c.e == nil {
		return
	}
	c.commitText()
	ip := c.imagePoint(p)
	m := Mark{Tool: c.e.tool, Points: []image.Point{ip, ip}, Color: c.e.color, Size: c.e.size}
	switch c.e.tool {
	case ToolText:
		m.Points = m.Points[:1]
		c.typing = &m
	case ToolMarker:
		m.Points = m.Points[:1]
		c.e.doc.Push(m)
	case ToolFreehand:
		m.Points = m.Points[:1]
		c.pending = &m
	default:
		c.pending = &m
	}
	c.Invalidate()
}

func (c *canvasView) DragMove(p widget.Point) {
	if c.pending == nil {
		return
	}
	ip := c.imagePoint(p)
	if c.pending.Tool == ToolFreehand {
		if c.pending.Points[len(c.pending.Points)-1] != ip {
			c.pending.Points = append(c.pending.Points, ip)
		}
	} else {
		c.pending.Points[1] = ip
	}
	c.Invalidate()
}

func (c *canvasView) PressEnd() {
	if c.pending == nil || c.e == nil {
		return
	}
	c.e.doc.Push(*c.pending)
	c.pending = nil
	c.Invalidate()
}

func (c *canvasView) Paint(cv *render.Canvas) {
	if c.e == nil {
		return
	}
	doc := c.e.doc
	v := c.fit()
	base := doc.Base()
	cv.DrawImageRect(base, v.rect(base.Bounds()))
	Paint(cv, doc.Marks(), v, c.e.d.Font)
	if c.pending != nil && c.pending.Tool != ToolCrop {
		PaintMark(cv, doc.Prepare(*c.pending), v, c.e.d.Font)
	}
	if c.typing != nil {
		PaintMark(cv, *c.typing, v, c.e.d.Font)
		c.paintCaret(cv, v)
	}
	crop := doc.Crop()
	if c.pending != nil && c.pending.Tool == ToolCrop {
		crop = c.pending.Rect()
	}
	if crop != base.Bounds() {
		shadeOutside(cv, v.rect(base.Bounds()), v.rect(crop))
	}
}

func (c *canvasView) paintCaret(cv *render.Canvas, v View) {
	x, y := v.point(c.typing.Points[0])
	px := v.length(TextPx(c.typing.Size))
	if f := c.e.d.Font; f != nil && c.typing.Text != "" {
		x += int(f.Shape(c.typing.Text, px).Advance())
	}
	cv.FillRect(render.Rect{X: x, Y: y, W: 2, H: int(px * 1.2)}, caretInk)
}

func shadeOutside(cv *render.Canvas, all, keep render.Rect) {
	cv.FillRect(render.Rect{X: all.X, Y: all.Y, W: all.W, H: keep.Y - all.Y}, cropShade)
	cv.FillRect(render.Rect{X: all.X, Y: keep.Y + keep.H, W: all.W, H: all.Y + all.H - keep.Y - keep.H}, cropShade)
	cv.FillRect(render.Rect{X: all.X, Y: keep.Y, W: keep.X - all.X, H: keep.H}, cropShade)
	cv.FillRect(render.Rect{X: keep.X + keep.W, Y: keep.Y, W: all.X + all.W - keep.X - keep.W, H: keep.H}, cropShade)
	cv.BorderRect(keep, 1, cropEdge)
}
