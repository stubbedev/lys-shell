package launcher

import (
	"image"
	"log"
	"math"
	"path/filepath"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/launcheripc"
	engine "github.com/stubbedev/lys-shell/service/launcher"
)

// WallpaperMode is the mode name that opens the wallpaper picker rather
// than a list.
const WallpaperMode = "wallpaper"

// Wallpapers is what the picker shows and sets wallpapers through.
type Wallpapers interface {
	// Images lists the pictures the picker offers.
	Images() ([]string, error)
	// Shown is the image monitor shows now, "" for none.
	Shown(monitor string) string
	// Preview shows path on monitor ("" every monitor) without setting it.
	Preview(path, monitor string) error
	// EndPreview shows the set images again.
	EndPreview()
	// Commit sets path on monitor ("" every monitor) and saves it to the
	// config.
	Commit(path, monitor string) error
	// LoadThumbnail is path's thumbnail, made off the loop.
	LoadThumbnail(path string) (image.Image, error)
}

// The picker's measures, in logical pixels at scale 1.
const (
	pickerColumns    = 4
	pickerLines      = 3
	pickerCellWidth  = 200
	pickerCellHeight = 156
	pickerThumbH     = 120
	pickerCached     = 64
)

// wallpaperSettle is how long a selection rests before the monitor
// previews it: arrowing through the grid changes no wallpaper.
const wallpaperSettle = 150 * time.Millisecond

func wantsPicker(o launcheripc.SessionOptions) bool {
	return !o.Dmenu && o.ErrorMessage == nil && o.Mode != nil && *o.Mode == WallpaperMode
}

// pickerImage is one picture the picker offers.
type pickerImage struct {
	path, name string
	hay        []rune
}

// picker is one wallpaper picker session on the UI loop.
type picker struct {
	s       *Surface
	sess    *launcheripc.Session
	walls   Wallpapers
	win     Window
	frame   *panelFrame
	chrome  *panelChrome
	grid    *widget.GridView
	images  []pickerImage
	shown   []int
	query   *engine.FuzzyQuery
	monitor string
	// current is what the monitor showed when the picker opened.
	current    string
	previewing string
	thumbs     *thumbQueue
	cache      *thumbCache
	thumbPx    int
	cellPx     [2]int
	stopSettle func()
	stopScale  func()
	closing    bool
}

// openPicker starts a wallpaper picker session on the monitor it sets
// wallpapers for: launcher.monitor, else the focused one.
func (s *Surface) openPicker(sess *launcheripc.Session) {
	if s.d.Wallpapers == nil {
		log.Print("launcher: the wallpaper picker needs the wallpaper service, which is not running")
		sess.Reply(launcheripc.ServerFrame{Type: launcheripc.FrameCancelled, Code: 1})
		return
	}
	cfg := s.d.Config()
	monitor := cfg.Launcher.Monitor
	if monitor == "" && s.d.FocusedOutput != nil {
		monitor, _ = s.d.FocusedOutput()
	}
	p := &picker{s: s, sess: sess, walls: s.d.Wallpapers, monitor: monitor, query: engine.NewFuzzyQuery(""), cache: newThumbCache(pickerCached)}
	p.current = p.walls.Shown(monitor)
	paths, err := p.walls.Images()
	if err != nil {
		log.Printf("launcher: %v", err)
	}
	p.images = make([]pickerImage, len(paths))
	for i, path := range paths {
		name := filepath.Base(path)
		p.images[i] = pickerImage{path: path, name: name, hay: engine.Haystack(name)}
	}
	p.build(cfg)
	p.thumbs = newThumbQueue(pickerColumns*(pickerLines+1), p.walls.LoadThumbnail, s.spawn, s.d.Invoke)
	s.picker = p
	win, err := s.d.Open(panelLayer(cfg.Styling.Translucent(), s.outputFor(monitor), p.frame, p.keyCaptured, func() { p.finish(false, "") }, func() {
		if s.picker == p {
			p.win = nil
			p.finish(false, "")
		}
	}))
	if err != nil {
		log.Printf("launcher: cannot map the surface: %v", err)
		p.finish(false, "")
		return
	}
	p.win = win
	p.stopScale = win.OnScaleChanged(func(int, int) { p.grid.Refresh() })
	if err := win.SetFocus(p.chrome.entry); err != nil {
		log.Printf("launcher: cannot focus the entry: %v", err)
		p.finish(false, "")
		return
	}
	if sess.Options.Filter != nil {
		p.chrome.entry.SetText(*sess.Options.Filter)
	}
	p.filter(p.chrome.entry.Text())
	p.frame.motion.open(cfg.Animations)
}

// build makes the picker's widgets: the field, the grid and the footer.
func (p *picker) build(cfg *config.Config) {
	px := scaledPx(cfg)
	p.thumbPx = px(pickerThumbH)
	p.cellPx = [2]int{px(pickerCellWidth), px(pickerCellHeight)}
	p.chrome = newPanelChrome(p.s.d, cfg, p.filter)
	p.chrome.entry.SetPlaceholder(i18n.T("launcher-wallpaper-placeholder"))
	p.chrome.setChip(i18n.T("launcher-wallpaper-title"))
	p.grid = widget.NewGridView(pickerModel{p}, p.cellPx[0], p.cellPx[1])
	p.grid.SetMinColumns(pickerColumns)
	p.grid.SetMaxColumns(pickerColumns)
	p.grid.SetMaxHeight(pickerLines * p.cellPx[1])
	p.grid.AddClass("launcher-wallpaper-grid")
	p.grid.OnSelect = p.selected
	p.grid.OnActivate = func(int) { p.commit(p.monitor) }
	p.chrome.hintBuf = append(p.chrome.hintBuf[:0],
		footerHint{"Enter", i18n.T("launcher-wallpaper-set-on", i18n.Str("monitor", p.monitorName()))},
		footerHint{"Ctrl+Enter", i18n.T("launcher-wallpaper-set-all")},
		closeHint())
	p.chrome.showHints()
	p.chrome.footer.SetVisible(true)
	content := widget.NewBox(widget.Column, 0, 0)
	content.Append(p.chrome.input, false)
	content.Append(p.chrome.message, false)
	content.Append(p.grid, true)
	content.Append(p.chrome.footer, false)
	width := pickerColumns*p.cellPx[0] + px(2*rowEdgeGap)
	height := int(math.Round(inputRowHeight*float64(cfg.Styling.Scale))) + pickerLines*p.cellPx[1] + int(math.Round(footerHeight*float64(cfg.Styling.Scale)))
	p.frame = newPanelFrame(p.s.d, content, width, height, func() Window { return p.win })
	p.frame.target = func(out render.Rect) (int, int) { return panelWidthIn(out, width), height }
	p.frame.place = func(out render.Rect, w, h int) render.Rect { return placePanel(out, w, h, config.LauncherCenter, 0, 0) }
}

func (p *picker) monitorName() string {
	if p.monitor == "" {
		return i18n.T("launcher-wallpaper-every-display")
	}
	return p.monitor
}

// filter narrows the grid to the images whose names match text.
func (p *picker) filter(text string) {
	if p.closing {
		return
	}
	p.query.Set(text)
	p.shown = p.shown[:0]
	for i, im := range p.images {
		if p.query.Empty() {
			p.shown = append(p.shown, i)
			continue
		}
		if _, ok := p.query.Score(im.hay); ok {
			p.shown = append(p.shown, i)
		}
	}
	p.grid.Reset()
	switch {
	case len(p.images) == 0:
		p.chrome.setMessage(i18n.T("launcher-wallpaper-none"))
	case len(p.shown) == 0:
		p.chrome.setMessage(i18n.T("launcher-wallpaper-no-match"))
	default:
		p.chrome.setMessage("")
	}
	if len(p.shown) == 0 {
		return
	}
	p.grid.Select(max(0, p.indexOf(p.current)))
}

// indexOf is the grid position showing path, -1 when it is filtered out.
func (p *picker) indexOf(path string) int {
	for pos, i := range p.shown {
		if p.images[i].path == path {
			return pos
		}
	}
	return -1
}

// path is the image at grid position pos.
func (p *picker) path(pos int) (string, bool) {
	if pos < 0 || pos >= len(p.shown) {
		return "", false
	}
	return p.images[p.shown[pos]].path, true
}

// selected previews the selection once it rests.
func (p *picker) selected(pos int) {
	if p.stopSettle != nil {
		p.stopSettle()
		p.stopSettle = nil
	}
	path, ok := p.path(pos)
	if !ok || p.closing {
		return
	}
	p.stopSettle = p.s.d.After(wallpaperSettle, func() {
		p.stopSettle = nil
		p.preview(path)
	})
}

// preview shows path on the monitor, unless it already shows it.
func (p *picker) preview(path string) {
	if p.closing || path == p.previewing || (p.previewing == "" && path == p.current) {
		return
	}
	if err := p.walls.Preview(path, p.monitor); err != nil {
		log.Printf("launcher: previewing %s: %v", path, err)
		return
	}
	p.previewing = path
}

// commit sets the selected image on monitor ("" every monitor) and ends
// the session.
func (p *picker) commit(monitor string) {
	path, ok := p.path(p.grid.Selected())
	if !ok {
		return
	}
	if err := p.walls.Commit(path, monitor); err != nil {
		p.chrome.setMessage(i18n.T("launcher-failed", i18n.Str("what", i18n.T("launcher-wallpaper-title")), i18n.Str("error", err.Error())))
		return
	}
	p.finish(true, path)
}

// keyCaptured is the picker's keyboard: the grid's motion and the
// accepts here, everything else to the field.
func (p *picker) keyCaptured(press app.Accel) bool {
	if p.closing {
		return false
	}
	action, ok := pickerKeys[press]
	if !ok {
		return false
	}
	switch action {
	case pickerCommit:
		p.commit(p.monitor)
	case pickerCommitAll:
		p.commit("")
	case pickerCancel:
		p.finish(false, "")
	default:
		p.grid.KeyAction(widget.KeyAction(action), 0)
	}
	return true
}

// pickerAction is a picker key: a grid motion (its widget.KeyAction) or
// one of the picker's own.
type pickerAction uint8

const (
	pickerCommit pickerAction = iota + 64
	pickerCommitAll
	pickerCancel
)

var pickerKeys = func() map[app.Accel]pickerAction {
	table := map[string]pickerAction{
		"Up": pickerAction(widget.KeyUp), "Down": pickerAction(widget.KeyDown),
		"Left": pickerAction(widget.KeyLeft), "Right": pickerAction(widget.KeyRight),
		"Page_Up": pickerAction(widget.KeyPriorPage), "Page_Down": pickerAction(widget.KeyNextPage),
		"Return": pickerCommit, "KP_Enter": pickerCommit,
		"Control+Return": pickerCommitAll, "Control+KP_Enter": pickerCommitAll,
		"Escape": pickerCancel,
	}
	out := make(map[app.Accel]pickerAction, len(table))
	for spec, a := range table {
		accel, err := app.ParseAccel(spec)
		if err != nil {
			panic("launcher: bad picker key " + spec + ": " + err.Error())
		}
		out[accel] = a
	}
	return out
}()

// finish ends the session: a cancel puts the set images back, an accept
// keeps the one committed.
func (p *picker) finish(accepted bool, path string) {
	if p.closing {
		return
	}
	p.closing = true
	if p.stopSettle != nil {
		p.stopSettle()
		p.stopSettle = nil
	}
	p.walls.EndPreview()
	frame := launcheripc.ServerFrame{Type: launcheripc.FrameCancelled, Code: 1}
	if accepted {
		frame = launcheripc.ServerFrame{Type: launcheripc.FrameResult, Selected: []launcheripc.Selected{{Index: 0, Text: path}}}
	}
	p.teardown()
	p.sess.Reply(frame)
}

// release ends the session for a client that went away.
func (p *picker) release() {
	if p.closing {
		return
	}
	p.closing = true
	if p.stopSettle != nil {
		p.stopSettle()
		p.stopSettle = nil
	}
	p.walls.EndPreview()
	p.teardown()
	p.sess.Release()
}

func (p *picker) teardown() {
	if p.s.picker == p {
		p.s.picker = nil
	}
	if p.thumbs != nil {
		p.thumbs.close()
	}
	if p.stopScale != nil {
		p.stopScale()
		p.stopScale = nil
	}
	if p.win == nil {
		return
	}
	win := p.win
	p.frame.motion.close(p.s.d.Config().Animations, win.Close)
}

// pickerModel is the grid's cells: the shown images, each a thumbnail
// over its name.
type pickerModel struct{ p *picker }

func (m pickerModel) Len() int { return len(m.p.shown) }

func (m pickerModel) Row(pos int) widget.Widget {
	p := m.p
	im := p.images[p.shown[pos]]
	cell := widget.NewBox(widget.Column, 4, 0)
	cell.AddClass("launcher-wallpaper-cell")
	pic := widget.NewImage(nil)
	pic.SetScale(widget.ImageFit)
	pic.SetSizeRequest(0, p.thumbPx)
	pic.AddClass("launcher-wallpaper-thumbnail")
	label := widget.NewLabel(p.s.d.Font, widgetsSmallPx(), im.name, 0)
	label.SetEllipsize(widget.EllipsizeMiddle)
	label.AddClass("launcher-wallpaper-name")
	cell.Append(pic, true)
	cell.Append(label, false)
	if img, ok := p.cache.get(im.path); ok {
		pic.SetImage(img)
		return cell
	}
	p.thumbs.request(im.path, func(img image.Image) {
		if p.closing {
			return
		}
		p.cache.put(im.path, img)
		pic.SetImage(img)
	})
	return cell
}
