// Package colorpicker is the magnifier-loupe color picker
// (crates/lys-shell/src/shell/color_picker): a frozen-frame layer
// surface per monitor with a zoom loupe that follows the pointer - the
// magnified pixels around it, a crosshair on the exact pixel a click
// picks, and the color's readout in the chosen format, which Tab cycles -
// and a strip of recently picked swatches, a click on one picking it
// again. A click samples that pixel; Escape cancels. The screenshot host
// captures the frames and hands them over (it owns the capture path), and
// the portal's Screenshot.PickColor and `lys-shell colorpicker` reach it
// over dev.stubbe.lys.Screenshot1.
package colorpicker

import (
	"context"
	"fmt"
	"image"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-lib/xdg"
	"github.com/stubbedev/lys-lib/xkb"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/version"
	"github.com/stubbedev/lys-shell/shell/keys"
	"github.com/stubbedev/lys-shell/shell/loupe"
	"github.com/stubbedev/lys-shell/shell/regionoverlay"
)

// Card geometry of color_picker/mod.rs, around the loupe's grid.
const (
	// historyMax is the most swatches kept.
	historyMax = 8

	panelPad  = 10
	readoutH  = 28
	swatchGap = 4
	hexPx     = 13.0
	// stripSwatch and stripMargin size the recent swatches' strip at the
	// bottom of each output.
	stripSwatch = 28
	stripMargin = 24
)

// Colors of the loupe card.
var (
	cardBacking   = render.RGBA(20, 20, 26, 235)
	hexColor      = render.RGBA(255, 255, 255, 242)
	swatchOutline = render.RGBA(255, 255, 255, 77)
	swatchHover   = render.RGBA(255, 255, 255, 242)
)

// RGB is one 8-bit color.
type RGB struct{ R, G, B uint8 }

// Hex is the color as #RRGGBB.
func (c RGB) Hex() string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }

// Float is the color as sRGB channels in [0, 1], the PickColor reply.
func (c RGB) Float() (r, g, b float64) {
	return float64(c.R) / 255, float64(c.G) / 255, float64(c.B) / 255
}

// Options shape one pick.
type Options struct {
	// Format is the readout's first format; empty starts in the one the
	// last pick used, else the configured one.
	Format config.ColorFormat
	// NoLoupe shows a crosshair pointer over the frozen screen instead of
	// the magnifier.
	NoLoupe bool
}

// Pick is a picked color and the format the readout showed at the click.
type Pick struct {
	Color  RGB
	Format config.ColorFormat
}

// Text is the color written in the format shown.
func (p Pick) Text() string { return p.Color.Format(p.Format) }

// result is one request's answer: a pick, a cancel, or a failure.
type result struct {
	pick Pick
	ok   bool
	err  error
}

// Picker is the picker component. Request is its entry point; the rest
// runs on the lys-ui loop goroutine.
type Picker struct {
	app     *app.Application
	gate    *regionoverlay.Gate
	outputs func() []*app.Output
	font    render.Font
	// historyPath is where the swatches persist; empty disables it.
	historyPath string
	// configured is the format a pick starts in until one is used.
	configured func() config.ColorFormat

	history []RGB
	// used is the format the last pick was made in, empty before one.
	used     config.ColorFormat
	reply    chan result
	surfaces []*app.LayerWindow
	views    []*surface
	// format and noLoupe are the open request's readout and look.
	format  config.ColorFormat
	noLoupe bool
}

// New builds the picker. gate is the shell's one capture gate; font is
// the readout's monospace bold face; the history loads from historyPath
// (HistoryPath for the real one); configured reads colorpicker.format.
func New(a *app.Application, gate *regionoverlay.Gate, outputs func() []*app.Output, font render.Font, historyPath string, configured func() config.ColorFormat) *Picker {
	return &Picker{app: a, gate: gate, outputs: outputs, font: font, historyPath: historyPath, configured: configured, history: loadHistory(historyPath)}
}

// HistoryPath is the persisted swatch file:
// $XDG_DATA_HOME/lys-shell/color-history, else ~/.local/share/lys-shell/color-history.
// Empty when neither is an absolute path.
func HistoryPath() string {
	dir, ok := xdg.OS.DataDir(version.Name)
	if !ok {
		return ""
	}
	return filepath.Join(dir, "color-history")
}

// Request opens the picker over the frozen frames (keyed by connector),
// which frames captures once the gate admits the request, and blocks until
// a click picks a pixel or a recent swatch (ok), or the user cancels or
// ctx ends (not ok, no error). A monitor without a frame gets no surface;
// when no frame matches any monitor there is nothing to pick from, and that
// is an error naming both, not a cancel. While another session holds the
// gate the request opens nothing, takes no frames and answers
// regionoverlay.ErrBusy. Call it from any goroutine but the loop's.
func (p *Picker) Request(ctx context.Context, frames regionoverlay.Frames, opts Options) (Pick, bool, error) {
	show := func(frames map[string]render.StillImage, ch chan result) { p.show(frames, ch, opts) }
	r, err := regionoverlay.OpenSession(ctx, p.gate, p.app.Invoke, frames, show, p.cancel)
	if err != nil {
		return Pick{}, false, err
	}
	return r.pick, r.ok, r.err
}

// cancel answers the request at ch, if it is still the open one, as
// cancelled.
func (p *Picker) cancel(ch chan result) {
	if p.reply == ch {
		p.finish(RGB{}, false)
	}
}

// startFormat is the readout's first format for a request asking for f.
func (p *Picker) startFormat(f config.ColorFormat) config.ColorFormat {
	switch {
	case f != "":
		return f
	case p.used != "":
		return p.used
	}
	return p.configured()
}

// key is the picker's keyboard: Escape cancels, Tab cycles the readout's
// format.
func (p *Picker) key(_ *widget.Router, sym xkb.Keysym, _ app.Mods) {
	switch sym {
	case xkb.KeyEscape:
		p.finish(RGB{}, false)
	case xkb.KeyTab:
		p.format = p.format.Next()
		for _, v := range p.views {
			v.Invalidate()
		}
	}
}

func (p *Picker) show(frames map[string]render.StillImage, ch chan result, opts Options) {
	p.reply = ch
	p.format, p.noLoupe = p.startFormat(opts.Format), opts.NoLoupe
	for _, out := range p.outputs() {
		mon := regionoverlay.MonitorOf(out)
		frame, ok := frames[mon.Connector]
		if !ok || frame.RGBA == nil {
			continue
		}
		l := &surface{p: p, frame: frame, logicalW: mon.Width, logicalH: mon.Height, hoverSwatch: -1}
		layer, err := regionoverlay.OpenSurface(p.app, out, "lys-shell-color-picker", l, keys.OnKey(p.app.KeySym, p.key))
		if err != nil {
			log.Printf("color picker: surface on %s: %v", mon.Connector, err)
			continue
		}
		p.surfaces = append(p.surfaces, layer)
		p.views = append(p.views, l)
	}
	if len(p.surfaces) == 0 {
		names := make([]string, 0, len(p.outputs()))
		for _, out := range p.outputs() {
			names = append(names, out.Name)
		}
		p.answer(result{err: fmt.Errorf("color picker: no captured frame matches an output (frames %v, outputs %v)", slices.Sorted(maps.Keys(frames)), names)})
	}
}

// finish answers the open request, remembering a pick in the history
// and its format for the next pick, and tears the surfaces down.
func (p *Picker) finish(c RGB, ok bool) {
	if !ok {
		p.answer(result{})
		return
	}
	p.history = pushHistory(p.history, c)
	saveHistory(p.historyPath, p.history)
	p.used = p.format
	p.answer(result{pick: Pick{Color: c, Format: p.format}, ok: true})
}

// answer tears the surfaces down and then replies to the open request,
// if any: once the requester has its reply nothing draws its frames, so
// it may release them.
func (p *Picker) answer(r result) {
	for _, s := range p.surfaces {
		s.Close()
	}
	p.surfaces, p.views = nil, nil
	if p.reply != nil {
		p.reply <- r
		p.reply = nil
	}
}

// pushHistory moves c to the front, dropping its older copy and
// anything past historyMax.
func pushHistory(h []RGB, c RGB) []RGB {
	out := []RGB{c}
	for _, old := range h {
		if old != c && len(out) < historyMax {
			out = append(out, old)
		}
	}
	return out
}

// loadHistory reads the swatches, newest first; an unreadable file is
// an empty history.
func loadHistory(path string) []RGB {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // the path is HistoryPath's fixed data-dir file
	if err != nil {
		return nil
	}
	var out []RGB
	for line := range strings.SplitSeq(string(data), "\n") {
		if c, ok := parseHex(line); ok && len(out) < historyMax {
			out = append(out, c)
		}
	}
	return out
}

// saveHistory persists the swatches, one #RRGGBB per line, best
// effort.
func saveHistory(path string, h []RGB) {
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755) //nolint:gosec // a plain data directory, like the Rust create_dir_all
	var b strings.Builder
	for _, c := range h {
		b.WriteString(c.Hex())
		b.WriteByte('\n')
	}
	_ = os.WriteFile(path, []byte(b.String()), 0o644) //nolint:gosec // the history is not secret
}

// parseHex reads one "#RRGGBB" line.
func parseHex(line string) (RGB, bool) {
	hex, ok := strings.CutPrefix(strings.TrimSpace(line), "#")
	if !ok || len(hex) != 6 {
		return RGB{}, false
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return RGB{}, false
	}
	return RGB{uint8(v >> 16 & 0xff), uint8(v >> 8 & 0xff), uint8(v & 0xff)}, true
}

// pixel reads img at frame coordinates as an opaque 8-bit color.
func pixel(img *image.RGBA, x, y int) RGB {
	c := loupe.Pixel(img, x, y)
	return RGB{c.R, c.G, c.B}
}

// surface is one monitor's picker surface.
type surface struct {
	widget.Base
	p                  *Picker
	frame              render.StillImage
	logicalW, logicalH int

	cursorX, cursorY float64
	hasCursor        bool
	// hoverSwatch is the recent swatch under the pointer, -1 for none.
	hoverSwatch int
}

// Measure asks for nothing: the surface takes the whole output.
func (l *surface) Measure(widget.Axis, int) widget.Sizes { return widget.Sizes{} }

func (l *surface) HitTest(p widget.Point) widget.Widget { return l.HitLeaf(l, p) }

// CursorName implements widget.CursorNamer: the loupe replaces the
// pointer, a crosshair stands in without one, and over the recent
// swatches the pointer is the arrow that clicks one.
func (l *surface) CursorName() string {
	switch {
	case l.hoverSwatch >= 0:
		return "default"
	case l.p.noLoupe:
		return "crosshair"
	}
	return "none"
}

// HoverMove implements widget.HoverMover.
func (l *surface) HoverMove(p widget.Point) {
	b := l.Bounds()
	l.cursorX, l.cursorY, l.hasCursor = float64(p.X-b.X), float64(p.Y-b.Y), true
	l.hoverSwatch = swatchAt(stripRects(b, len(l.p.history)), p)
	l.Invalidate()
}

// SetHovered implements widget.HoverSetter: leaving hides the loupe.
func (l *surface) SetHovered(on bool) {
	if !on {
		l.hasCursor, l.hoverSwatch = false, -1
		l.Invalidate()
	}
}

// ClickAt implements widget.Clicker: the release picks the recent swatch
// under it, else the pixel under it.
func (l *surface) ClickAt(p widget.Point) {
	b := l.Bounds()
	if i := swatchAt(stripRects(b, len(l.p.history)), p); i >= 0 {
		l.p.finish(l.p.history[i], true)
		return
	}
	x, y := loupe.SampleAt(l.frame.RGBA, l.logicalW, l.logicalH, float64(p.X-b.X), float64(p.Y-b.Y))
	l.p.finish(pixel(l.frame.RGBA, x, y), true)
}

func (l *surface) Paint(cv *render.Canvas) {
	b := l.Bounds()
	loupe.DrawFrame(cv, l.frame, b)
	drawStrip(cv, l.p.history, stripRects(b, len(l.p.history)), l.hoverSwatch)
	if l.hasCursor && l.hoverSwatch < 0 && !l.p.noLoupe {
		drawLoupe(cv, l.p.font, l.frame.RGBA, l.logicalW, l.logicalH, b, l.cursorX, l.cursorY, l.p.format)
	}
}

// drawLoupe paints the loupe card near the cursor (flipping to stay on
// screen): the magnified grid, the crosshair on the center pixel, and the
// color chip with its readout in format. box is the surface's logical
// rect, (cx, cy) the cursor inside it.
func drawLoupe(cv *render.Canvas, font render.Font, img *image.RGBA, logicalW, logicalH int, box render.Rect, cx, cy float64, format config.ColorFormat) {
	centerX, centerY := loupe.SampleAt(img, logicalW, logicalH, cx, cy)
	picked := pixel(img, centerX, centerY)
	var shaped *render.ShapedText
	panelW := loupe.Size
	if font != nil {
		shaped = font.Shape(picked.Format(format), hexPx)
		panelW = max(panelW, 28+int(shaped.Advance()+0.5)+panelPad)
	}
	panelH := loupe.Size + readoutH
	x0, y0 := loupe.Place(box, cx, cy, panelW, panelH)

	cv.FillRect(render.Rect{X: x0 - 4, Y: y0 - 4, W: panelW + 8, H: panelH + 8}, cardBacking)
	loupe.Draw(cv, img, centerX, centerY, x0+(panelW-loupe.Size)/2, y0)

	ry := y0 + loupe.Size
	cv.FillRect(render.Rect{X: x0, Y: ry + 4, W: 20, H: 20}, render.RGB(picked.R, picked.G, picked.B))
	if shaped != nil {
		font.Draw(cv, shaped, x0+28, ry+19, hexColor)
	}
}

// stripRects are the n recent swatches' rects, centered along the bottom
// of box.
func stripRects(box render.Rect, n int) []render.Rect {
	if n == 0 {
		return nil
	}
	w := n*stripSwatch + (n-1)*swatchGap
	x := box.X + (box.W-w)/2
	y := box.Y + box.H - stripMargin - stripSwatch
	rects := make([]render.Rect, n)
	for i := range rects {
		rects[i] = render.Rect{X: x + i*(stripSwatch+swatchGap), Y: y, W: stripSwatch, H: stripSwatch}
	}
	return rects
}

// swatchAt is the index of the rect holding p, -1 for none.
func swatchAt(rects []render.Rect, p widget.Point) int {
	return slices.IndexFunc(rects, func(r render.Rect) bool {
		return p.X >= r.X && p.X < r.X+r.W && p.Y >= r.Y && p.Y < r.Y+r.H
	})
}

// drawStrip paints the recent swatches on a card, the hovered one ringed.
func drawStrip(cv *render.Canvas, history []RGB, rects []render.Rect, hover int) {
	if len(rects) == 0 {
		return
	}
	first, last := rects[0], rects[len(rects)-1]
	cv.FillRect(render.Rect{X: first.X - panelPad, Y: first.Y - panelPad, W: last.X + last.W - first.X + 2*panelPad, H: stripSwatch + 2*panelPad}, cardBacking)
	for i, r := range rects {
		c := history[i]
		cv.FillRect(r, render.RGB(c.R, c.G, c.B))
		if i == hover {
			cv.BorderRect(r, 2, swatchHover)
			continue
		}
		cv.BorderRect(r, 1, swatchOutline)
	}
}
