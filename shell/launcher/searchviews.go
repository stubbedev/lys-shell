package launcher

import (
	"fmt"
	"image"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/anim"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	engine "github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/search"
)

// The panel's measures, in logical pixels at scale 1.
const (
	// panelTopFraction is how far down the output the field sits: above
	// centre, so results grow into the room below without the field
	// moving.
	panelTopFraction = 0.22
	panelEdgeGap     = 32
	// previewMinRows is the least the list area is tall beside a preview:
	// a pane one row high shows nothing.
	previewMinRows   = 7
	panelWidthFactor = 1.2
	previewWidth     = 360
	inputRowHeight   = 62
	footerHeight     = 36
	searchRowHeight  = 46
	headerRowHeight  = 28
	searchIconSize   = 26
	entryFontFactor  = 1.45
)

func hexColor(c render.Color) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R(), c.G(), c.B())
}

// panelViews is the panel's widget tree.
type panelViews struct {
	p          *panel
	root       *panelRoot
	box        *widget.Box
	content    *widget.Box
	input      *widget.Box
	chip       *widget.Label
	entry      *widget.Entry
	message    *widget.RichLabel
	sep        *widget.Box
	body       *widget.Box
	list       *searchList
	pane       *widget.Box
	image      *widget.Image
	text       *widget.Label
	empty      *widget.Label
	footer     *widget.Box
	notice     *widget.Label
	hints      *widget.RichLabel
	motion     *panelMotion
	searchIcon *asyncIcon
	font       render.Font
	scale      float64
	lines      int
	// baseW is the width without the preview pane, previewW the pane's.
	baseW, previewW int
	hintBuf         []footerHint
	markup          []byte
	withPreview     bool
}

func newPanelViews(p *panel, cfg *config.Config) *panelViews {
	d := p.s.d
	scale := float64(cfg.Styling.Scale)
	px := func(v int) int { return int(math.Round(float64(v) * scale)) }
	v := &panelViews{
		p: p, font: d.Font, scale: scale, lines: max(int(cfg.Launcher.Lines), 1),
		baseW:    int(math.Round(cfg.Launcher.Width.ResolvePx(config.LauncherWidthBaseRem*16, scale) * panelWidthFactor)),
		previewW: px(previewWidth),
	}
	body := fontPx()
	v.input = widget.NewBox(widget.Row, px(10), 0)
	v.input.AddClass("launcher-search-input")
	v.input.SetSizeRequest(0, px(inputRowHeight))
	v.input.SetPadding(render.Insets{Left: px(18), Right: px(18)})
	v.searchIcon = newAsyncIcon(px(22), "launcher-search-icon")
	v.input.AppendAligned(v.searchIcon.slot, false, widget.AlignCenter)
	v.chip = widget.NewLabel(d.Font, body, "", 0)
	v.chip.AddClass("launcher-search-chip")
	v.chip.SetVisible(false)
	v.input.AppendAligned(v.chip, false, widget.AlignCenter)
	v.entry = widget.NewEntry(d.Font, math.Round(body*entryFontFactor), 0)
	v.entry.AddClass("launcher-search-entry")
	v.entry.OnChanged = func(text string) {
		if p.eng != nil && !p.closing {
			p.querySeq++
			p.eng.SetQuery(text, p.querySeq)
		}
	}
	v.input.AppendAligned(v.entry, true, widget.AlignCenter)

	v.message = widget.NewRichLabel(d.Font, body, "", 0)
	v.message.AddClass("launcher-search-message", css.DimLabel)
	v.message.SetMargin(render.Insets{Left: px(20), Right: px(20), Bottom: px(8)})
	v.message.SetVariants(d.Variants)
	v.message.SetWrap(true)
	v.message.SetVisible(false)

	v.sep = widget.NewBox(widget.Column, 0, 0)
	v.sep.AddClass("launcher-search-separator")
	v.sep.SetSizeRequest(0, 1)
	v.sep.SetVisible(false)

	v.list = newSearchList(p, cfg, d.Font, d.Variants, px(searchRowHeight), px(headerRowHeight), px(searchIconSize))
	v.list.bindAsync(v.searchIcon, "ld-search-symbolic")
	v.pane = widget.NewBox(widget.Column, 0, px(12))
	v.pane.AddClass("launcher-search-preview")
	v.pane.SetSizeRequest(v.previewW, 0)
	v.image = widget.NewImage(nil)
	v.image.SetScale(widget.ImageFit)
	v.image.AddClass("launcher-search-preview-image")
	v.text = widget.NewLabel(d.Font, body, "", 0)
	v.text.AddClass("launcher-search-preview-text")
	v.text.SetWrap(true)
	v.empty = widget.NewLabel(d.Font, body, i18n.T("launcher-preview-unavailable"), 0)
	v.empty.AddClass("launcher-search-preview-empty", css.DimLabel)
	v.pane.Append(v.image, true)
	v.pane.Append(v.text, false)
	v.pane.Append(v.empty, false)
	v.showPreview(search.PreviewContent{}, nil, p)
	v.pane.SetVisible(false)
	v.body = widget.NewBox(widget.Row, 0, 0)
	v.body.Append(v.list, true)
	v.body.Append(v.pane, false)

	v.footer = widget.NewBox(widget.Row, px(12), 0)
	v.footer.AddClass("launcher-search-footer")
	v.footer.SetSizeRequest(0, px(footerHeight))
	v.footer.SetPadding(render.Insets{Left: px(16), Right: px(16)})
	v.notice = widget.NewLabel(d.Font, widgetsSmallPx(), "", 0)
	v.notice.AddClass("launcher-search-notice")
	v.notice.SetEllipsize(widget.EllipsizeEnd)
	v.footer.AppendAligned(v.notice, true, widget.AlignCenter)
	v.hints = widget.NewRichLabel(d.Font, widgetsSmallPx(), "", 0)
	v.hints.AddClass("launcher-search-hints", css.DimLabel)
	v.hints.SetVariants(d.Variants)
	v.footer.AppendAligned(v.hints, false, widget.AlignCenter)
	v.footer.SetVisible(false)

	v.content = widget.NewBox(widget.Column, 0, 0)
	v.content.Append(v.input, false)
	v.content.Append(v.message, false)
	v.content.Append(v.sep, false)
	v.content.Append(v.body, true)
	v.content.Append(v.footer, false)

	v.box = widget.NewBox(widget.Column, 0, 0)
	v.box.AddClass("launcher-panel")
	v.box.Append(&panelStage{v: v}, true)

	v.root = &panelRoot{v: v}
	v.root.SetElement("window")
	v.root.AddClass("launcher-window")
	if d.Sheet != nil {
		v.root.AttachStylesheet(d.Sheet)
	}
	v.motion = newPanelMotion(v)
	return v
}

// widgetsSmallPx is the footer's text size.
func widgetsSmallPx() float64 { return math.Round(fontPx() * 0.85) }

// fieldH is the fixed height above the list.
func (v *panelViews) fieldH() int { return int(math.Round(inputRowHeight * v.scale)) }

func (v *panelViews) footerH() int { return int(math.Round(footerHeight * v.scale)) }

// messageH is the message's height at the panel's width.
func (v *panelViews) messageH() int {
	if !v.message.Visible() {
		return 0
	}
	return v.message.Measure(widget.Column, v.baseW).Nat
}

// targetH is the height the panel springs to for what it now shows.
func (v *panelViews) targetH() int {
	h := v.fieldH() + v.messageH()
	listH := v.list.contentH()
	if listH == 0 {
		if v.notice.Text() != "" {
			return h + v.footerH()
		}
		return h
	}
	if v.withPreview {
		listH = max(listH, previewMinRows*v.list.rowH)
	}
	limit := v.lines * v.list.rowH
	if v.withPreview {
		limit = max(limit, previewMinRows*v.list.rowH)
	}
	if v.root.bounds().H > 0 {
		limit = min(limit, int(float64(v.root.bounds().H)*(1-panelTopFraction))-h-v.footerH()-panelEdgeGap)
	}
	return h + 1 + min(listH, max(limit, v.list.rowH)) + v.footerH()
}

// targetW is the width it springs to.
func (v *panelViews) targetW() int {
	w := v.baseW
	if v.withPreview {
		w += v.previewW
	}
	if out := v.root.bounds().W; out > 0 {
		w = min(w, out-2*panelEdgeGap)
	}
	return max(w, 1)
}

// pageRows is how many rows PageUp and PageDown move.
func (v *panelViews) pageRows() int { return max(v.lines-1, 1) }

// retarget sends the panel toward its size for the current content.
func (v *panelViews) retarget(*panel) {
	v.motion.resize(v.targetW(), v.targetH())
}

func (v *panelViews) setField(text string) {
	stop := v.entry.OnChanged
	v.entry.OnChanged = nil
	v.entry.SetText(text)
	v.entry.OnChanged = stop
}

// setLevel shows what the level says about itself.
func (v *panelViews) setLevel(f search.Frame) {
	v.chip.SetText(f.Title)
	v.chip.SetVisible(f.Title != "")
	v.entry.SetPlaceholder(f.Placeholder)
	echo := widget.EchoNormal
	if f.Mask {
		echo = widget.EchoPassword
	}
	v.entry.SetEcho(echo)
	if f.Message == "" {
		v.message.SetVisible(false)
	} else {
		v.message.SetMarkup(messageMarkup(f.Message))
		v.message.SetVisible(true)
	}
	v.sep.SetVisible(len(f.Rows) > 0)
}

func (v *panelViews) setHasPreview(on bool, p *panel) {
	on = on && p.s.d.Config().Launcher.Search.Previews
	if on == v.withPreview {
		return
	}
	v.withPreview = on
	v.pane.SetVisible(on)
}

// setFooter shows the hints, or a failure said out loud.
func (v *panelViews) setFooter(p *panel) {
	v.notice.SetText(p.notice)
	shown := len(p.frame.Rows) > 0 || p.notice != "" || p.depth > 0
	v.footer.SetVisible(shown)
	if !shown {
		return
	}
	v.hintBuf = hintFor(p, v.hintBuf[:0])
	b := v.markup[:0]
	for i, h := range v.hintBuf {
		if i > 0 {
			b = append(b, "     "...)
		}
		b = append(b, "<b>"...)
		b = appendEscaped(b, h.key)
		b = append(b, "</b> "...)
		b = appendEscaped(b, h.does)
	}
	v.markup = b
	// The comparison allocates nothing, and the same footer is the usual case.
	if string(b) != v.hints.Markup() {
		v.hints.SetMarkup(string(b))
	}
}

// showPreview shows content; err or nothing shows the empty note.
func (v *panelViews) showPreview(c search.PreviewContent, err error, _ *panel) {
	var img image.Image
	text := ""
	if err == nil {
		img, text = c.Image, c.Text
	}
	v.image.SetVisible(img != nil)
	if img != nil {
		v.image.SetImage(img)
	}
	v.text.SetVisible(text != "")
	if text != "" {
		v.text.SetText(previewLines(text))
	}
	v.empty.SetVisible(img == nil && text == "")
}

// previewLines cuts text to what a pane can show, so laying it out stays
// cheap whatever was copied.
func previewLines(text string) string {
	const maxLines, maxBytes = 40, 4096
	if len(text) > maxBytes {
		text = strings.ToValidUTF8(text[:maxBytes], "")
	}
	lines := strings.SplitN(text, "\n", maxLines+1)
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return strings.Join(lines, "\n")
}

// panelRoot fills the output: transparent, the panel laid where the
// motion says, and a press on it (outside the panel) dismissing. The
// surface does not resize as the panel does, so its height can spring
// every frame with no round trip to the compositor, the field never
// moves, and the blur region follows the panel's own rect.
type panelRoot struct {
	widget.Base
	v    *panelViews
	kids [1]widget.Widget
}

func (r *panelRoot) bounds() render.Rect { return r.Bounds() }

// Measure wants nothing: the surface is the output.
func (r *panelRoot) Measure(widget.Axis, int) widget.Sizes { return widget.Sizes{} }

// Arrange lays the panel at its shown rect, and retargets when the
// output's size first arrives.
func (r *panelRoot) Arrange(rect render.Rect) {
	first := r.Bounds().W != rect.W || r.Bounds().H != rect.H
	r.ArrangeSelf(rect)
	if first {
		r.v.motion.snapSize(r.v.targetW(), r.v.targetH())
	}
	r.v.box.Arrange(r.v.motion.shownRect(rect))
	r.kids[0] = r.v.box
	widget.SetParents(r, r.kids[:]...)
}

// ArrangeRoot mirrors Arrange for the root path.
func (r *panelRoot) ArrangeRoot(rect render.Rect) { r.Arrange(rect) }

// Paint paints the panel, carrying the fade itself where the compositor
// cannot.
func (r *panelRoot) Paint(cv *render.Canvas) {
	r.v.list.setScale(cv.DeviceScale())
	if alpha := r.v.motion.paintAlpha(); alpha < 1 {
		prev := cv.PushAlpha(alpha)
		widget.PaintChild(cv, r.v.box)
		cv.PopAlpha(prev)
		return
	}
	widget.PaintChild(cv, r.v.box)
}

// Children is the panel.
func (r *panelRoot) Children() []widget.Widget { r.kids[0] = r.v.box; return r.kids[:] }

// BackdropRegion is what the compositor blurs: the panel's rounded box as
// it shows this frame, never the output around it.
func (r *panelRoot) BackdropRegion() []render.Rect {
	return render.RoundedRectRegion(r.v.box.Bounds(), widget.CornerRadii(r.v.box))
}

// HitTest is the panel's, else the root itself: a press to dismiss with.
func (r *panelRoot) HitTest(p widget.Point) widget.Widget {
	if hit := r.v.box.HitTest(p); hit != nil {
		return hit
	}
	return r.HitLeaf(r, p)
}

// panelStage clips the content to the panel while it moves: the content
// is laid out at its destination, so text never reflows or stretches.
type panelStage struct {
	widget.Base
	v    *panelViews
	kids [1]widget.Widget
}

// Measure wants nothing: the motion sizes the panel.
func (s *panelStage) Measure(widget.Axis, int) widget.Sizes { return widget.Sizes{} }

// Arrange lays the content at the panel's resting place.
func (s *panelStage) Arrange(rect render.Rect) {
	s.ArrangeSelf(rect)
	s.v.content.Arrange(s.v.motion.contentRect(s.v.root.bounds()))
	s.kids[0] = s.v.content
	widget.SetParents(s, s.kids[:]...)
}

// ArrangeRoot mirrors Arrange for the root path.
func (s *panelStage) ArrangeRoot(rect render.Rect) { s.Arrange(rect) }

// Paint paints the content inside the panel.
func (s *panelStage) Paint(cv *render.Canvas) {
	prev := cv.PushClip(s.Bounds())
	widget.PaintChild(cv, s.v.content)
	cv.PopClip(prev)
}

// Children is the content.
func (s *panelStage) Children() []widget.Widget { s.kids[0] = s.v.content; return s.kids[:] }

// HitTest descends into the content.
func (s *panelStage) HitTest(p widget.Point) widget.Widget {
	b := s.Bounds()
	if p.X < b.X || p.Y < b.Y || p.X >= b.X+b.W || p.Y >= b.Y+b.H {
		return nil
	}
	return s.v.content.HitTest(p)
}

// panelMotion is the panel's movement: a spring for how far open it is
// and one for its size, each carrying its velocity when retargeted, so a
// close during an open or a list that changes mid-grow flows on. Reduced
// motion and disabled animations land at once (anim.Instant, the
// animations config).
type panelMotion struct {
	v     *panelViews
	alive bool
	// reveal is 0 closed through 1 open, past 1 on the overshoot.
	reveal float64
	w, h   float64
	revS   *anim.Spring
	sizeS  *anim.Spring
	style  panelStyle
	done   func()
	// compositorFade is the surface's opacity carrying the fade; false
	// where the compositor has no alpha modifier.
	compositorFade bool
	fadeStop       func()
	// instant is animations being off for this surface: nothing springs.
	instant bool
	targetW int
	targetH int
}

// panelStyle is how a configured transition moves the panel.
type panelStyle struct {
	grow  bool
	shift int
}

func panelStyleFor(t config.AnimationType) panelStyle {
	const slide = 24
	switch t {
	case config.AnimationFade:
		return panelStyle{}
	case config.AnimationSlideUp, config.AnimationSwingUp:
		return panelStyle{shift: slide}
	case config.AnimationSlideDown, config.AnimationSwingDown:
		return panelStyle{shift: -slide}
	}
	return panelStyle{grow: true}
}

// panelSizeSpring is a touch quicker than a window's: the list follows
// typing, and a slow height reads as lag.
var panelSizeSpring = anim.SpringParams{Response: 220 * time.Millisecond, Damping: 1}

func newPanelMotion(v *panelViews) *panelMotion {
	m := &panelMotion{v: v, alive: true, compositorFade: true}
	m.revS = anim.NewSpring(anim.SpringSnappy, []float64{0}, func(pos []float64) {
		m.reveal = pos[0]
		m.applyReveal()
		if m.done != nil && pos[0] <= 0 {
			done := m.done
			m.done = nil
			m.alive = false
			done()
		}
	})
	m.revS.SetPrecision(0.002)
	m.sizeS = anim.NewSpring(panelSizeSpring, []float64{0, 0}, func(pos []float64) {
		m.w, m.h = pos[0], pos[1]
		if m.alive {
			v.root.InvalidateLayout()
		}
	})
	m.sizeS.SetPrecision(0.2)
	m.w, m.h = float64(v.baseW), float64(v.fieldH())
	m.targetW, m.targetH = v.baseW, v.fieldH()
	return m
}

// springFor shapes a reveal from the configured duration: the duration
// is the spring's response, overshoot only on the way in.
func springFor(d uint32, entering bool) anim.SpringParams {
	damping := 1.0
	if entering {
		damping = anim.SpringSnappy.Damping
	}
	return anim.SpringParams{Response: time.Duration(d) * time.Millisecond, Damping: damping}
}

// open plays the enter.
func (m *panelMotion) open(anims config.AnimationsConfig) {
	t := anims.TransitionFor(config.AnimLauncher, false)
	m.style = panelStyleFor(t)
	d := anims.DurationFor(config.AnimLauncher, false)
	m.instant = t == config.AnimationNone || d == 0 || anim.NoFades()
	if m.instant {
		m.revS.Snap(1)
		return
	}
	if anim.Reduced() {
		m.fade(0, 1, d, nil)
		return
	}
	m.revS.SetParams(springFor(d, true))
	m.revS.Snap(0)
	m.revS.SetTarget(1)
}

// close plays the exit, then runs done. Done runs at once when there is
// nothing to play.
func (m *panelMotion) close(anims config.AnimationsConfig, done func()) {
	t := anims.TransitionFor(config.AnimLauncher, true)
	d := anims.DurationFor(config.AnimLauncher, true)
	if t == config.AnimationNone || d == 0 || anim.NoFades() {
		m.stop()
		m.alive = false
		done()
		return
	}
	if anim.Reduced() {
		m.fade(m.reveal, 0, d, func() {
			m.alive = false
			done()
		})
		return
	}
	m.style = panelStyleFor(t)
	m.done = done
	m.revS.SetParams(springFor(d, false))
	m.revS.SetTarget(0)
}

// stop halts everything moving.
func (m *panelMotion) stop() {
	m.revS.Stop()
	m.sizeS.Stop()
	if m.fadeStop != nil {
		m.fadeStop()
		m.fadeStop = nil
	}
}

// fade is reduced motion's reveal: a tween of the opacity alone, the
// panel where it rests, which is what the preference keeps.
func (m *panelMotion) fade(from, to float64, d uint32, done func()) {
	m.style = panelStyle{}
	if m.fadeStop != nil {
		m.fadeStop()
	}
	m.fadeStop = anim.Start(time.Duration(d)*time.Millisecond, func(t float64) {
		m.reveal = from + float64((to-from)*t)
		m.applyReveal()
		if t >= 1 && done != nil {
			done()
		}
	})
}

// resize retargets the size.
func (m *panelMotion) resize(w, h int) {
	if w == m.targetW && h == m.targetH {
		return
	}
	m.targetW, m.targetH = w, h
	if m.instant || m.reveal <= 0.01 {
		m.snapSize(w, h)
		return
	}
	m.sizeS.SetTarget(float64(w), float64(h))
}

// snapSize lands the size at once: before anything is showing.
func (m *panelMotion) snapSize(w, h int) {
	m.targetW, m.targetH = w, h
	m.sizeS.Snap(float64(w), float64(h))
}

func (m *panelMotion) applyReveal() {
	if !m.alive {
		return
	}
	if win := m.v.p.win; win != nil && m.compositorFade {
		if err := win.SetOpacity(min(max(m.reveal, 0), 1)); err != nil {
			m.compositorFade = false
		}
	}
	m.v.root.InvalidateLayout()
	m.v.root.Invalidate()
}

// paintAlpha is the fade the paint must carry.
func (m *panelMotion) paintAlpha() float64 {
	if m.compositorFade {
		return 1
	}
	return min(max(m.reveal, 0), 1)
}

// restRect is the panel's place at its current size in an output: the
// top fixed, so growing goes down.
func (m *panelMotion) restRect(out render.Rect) render.Rect {
	w, h := int(math.Round(m.w)), int(math.Round(m.h))
	return render.Rect{X: out.X + (out.W-w)/2, Y: out.Y + int(float64(out.H)*panelTopFraction), W: w, H: h}
}

// shownRect is the panel's rect this frame.
func (m *panelMotion) shownRect(out render.Rect) render.Rect {
	r := m.restRect(out)
	if m.style.shift != 0 {
		r.Y += int(math.Round((1 - m.reveal) * float64(m.style.shift)))
		return r
	}
	if !m.style.grow {
		return r
	}
	const from = 0.94
	s := from + float64((1-from)*m.reveal)
	w, h := int(math.Round(float64(r.W)*s)), int(math.Round(float64(r.H)*s))
	return render.Rect{X: r.X + (r.W-w)/2, Y: r.Y + (r.H-h)/2, W: w, H: h}
}

// contentRect is the content's rect: at rest, whatever the panel is
// doing around it.
func (m *panelMotion) contentRect(out render.Rect) render.Rect {
	r := m.restRect(out)
	if m.style.shift != 0 {
		r.Y += int(math.Round((1 - m.reveal) * float64(m.style.shift)))
	}
	return r
}

// searchList is the virtualized result list: a fixed height per row kind,
// so nothing shifts as rows change text, and a pool of row widgets
// rebound in place, so typing costs a few labels' worth of work.
type searchList struct {
	widget.Base
	p        *panel
	font     render.Font
	variants widget.VariantFunc
	rowH     int
	headerH  int
	iconSize int
	icons    bool
	rows     []search.Row
	selected int
	// offset is the scroll position in pixels; scroll its spring.
	offset float64
	scroll *anim.Spring
	pool   []*searchRow
	shown  []widget.Widget
	loader *iconLoader
	// num and denom are the device scale icons are decoded for.
	num, denom int
	// waiting are the icons shown once decoded.
	waiting []*asyncIcon
	// openTag is the highlight's markup, for the theme's accent.
	openTag   string
	openColor render.Color
}

func newSearchList(p *panel, cfg *config.Config, font render.Font, variants widget.VariantFunc, rowH, headerH, iconSize int) *searchList {
	l := &searchList{p: p, font: font, variants: variants, rowH: rowH, headerH: headerH, iconSize: iconSize, icons: cfg.Launcher.ShowIcons, selected: -1}
	l.num, l.denom = p.s.scale()
	l.loader = newIconLoader(p.ctx, p.s.d.Invoke, l.iconReady)
	l.SetElement("listview")
	l.AddClass("launcher-search-list")
	l.scroll = anim.NewSpring(anim.SpringParams{Response: 160 * time.Millisecond, Damping: 1}, []float64{0}, func(pos []float64) {
		l.offset = pos[0]
		l.InvalidateLayout()
	})
	l.scroll.SetPrecision(0.5)
	l.SetOnScroll(func(e widget.ScrollEvent) bool {
		l.scrollTo(l.scrollTarget() + float64(e.DY*rowH))
		return true
	})
	return l
}

// contentH is every row's height together.
func (l *searchList) contentH() int {
	h := 0
	for i := range l.rows {
		h += l.heightOf(i)
	}
	return h
}

func (l *searchList) heightOf(i int) int {
	if l.rows[i].Header != "" {
		return l.headerH
	}
	return l.rowH
}

func (l *searchList) topOf(i int) int {
	y := 0
	for k := range i {
		y += l.heightOf(k)
	}
	return y
}

func (l *searchList) scrollTarget() float64 { return l.offset }

func (l *searchList) scrollTo(y float64) {
	limit := float64(max(l.contentH()-l.Bounds().H, 0))
	y = min(max(y, 0), limit)
	if l.p.v.motion.instant {
		l.scroll.Snap(y)
		return
	}
	l.scroll.SetTarget(y)
}

// setRows swaps the list. The scroll goes back to the top for a new
// list, except when the selected row stays in view.
func (l *searchList) setRows(rows []search.Row, selected int) {
	l.rows = rows
	l.selected = selected
	l.scroll.Snap(0)
	l.reveal(selected)
	l.InvalidateLayout()
}

func (l *searchList) setSelected(i int) {
	l.selected = i
	l.reveal(i)
	l.InvalidateLayout()
}

// reveal scrolls row i into view, a heading above it too.
func (l *searchList) reveal(i int) {
	if i < 0 || i >= len(l.rows) || l.Bounds().H == 0 {
		return
	}
	top := l.topOf(i)
	if i > 0 && l.rows[i-1].Header != "" {
		top -= l.headerH
	}
	bottom := l.topOf(i) + l.rowH
	switch {
	case float64(top) < l.offset:
		l.scrollTo(float64(top))
	case float64(bottom) > l.offset+float64(l.Bounds().H):
		l.scrollTo(float64(bottom - l.Bounds().H))
	}
}

// firstShown is the first row index in view.
func (l *searchList) firstShown() int {
	y := 0
	for i := range l.rows {
		y += l.heightOf(i)
		if float64(y) > l.offset {
			return i
		}
	}
	return 0
}

// Measure is flexible: the body gives it its room.
func (l *searchList) Measure(axis widget.Axis, _ int) widget.Sizes {
	if axis == widget.Column {
		return widget.Sizes{}
	}
	return widget.Sizes{}
}

// Arrange binds and lays the rows in view; the rest cost nothing.
func (l *searchList) Arrange(r render.Rect) {
	l.ArrangeSelf(r)
	l.shown = l.shown[:0]
	off := int(math.Round(l.offset))
	y := 0
	used := 0
	number := 0
	for i := range l.rows {
		h := l.heightOf(i)
		isItem := l.rows[i].Header == ""
		if y+h > off && y < off+r.H {
			if isItem {
				number++
			}
			if used == len(l.pool) {
				l.pool = append(l.pool, newSearchRow(l))
			}
			row := l.pool[used]
			used++
			row.bind(i, l.rows[i], i == l.selected, number)
			row.Arrange(render.Rect{X: r.X, Y: r.Y + y - off, W: r.W, H: h})
			l.shown = append(l.shown, row)
		}
		y += h
	}
	widget.SetParents(l, l.shown...)
}

// ArrangeRoot mirrors Arrange for the root path.
func (l *searchList) ArrangeRoot(r render.Rect) { l.Arrange(r) }

// Paint paints the rows in view.
func (l *searchList) Paint(cv *render.Canvas) {
	prev := cv.PushClip(l.Bounds())
	for _, w := range l.shown {
		widget.PaintChild(cv, w)
	}
	cv.PopClip(prev)
}

// Children are the rows in view.
func (l *searchList) Children() []widget.Widget { return l.shown }

// HitTest descends into the row under p.
func (l *searchList) HitTest(p widget.Point) widget.Widget {
	b := l.Bounds()
	if p.X < b.X || p.Y < b.Y || p.X >= b.X+b.W || p.Y >= b.Y+b.H {
		return nil
	}
	for _, w := range l.shown {
		if hit := w.HitTest(p); hit != nil {
			return hit
		}
	}
	return l.HitLeaf(l, p)
}

// ScrollBy scrolls by whole rows.
func (l *searchList) ScrollBy(_, dy int) { l.scrollTo(l.offset + float64(dy*l.rowH)) }

// searchRow is one row of the pool, rebound as the list changes.
type searchRow struct {
	*widget.Box
	l     *searchList
	index int
	icon  *asyncIcon
	glyph *widget.Label
	title *widget.RichLabel
	// positions and markup are the highlight's memory.
	positions []int
	markup    []byte
	subtitle  *widget.Label
	hint      *widget.Label
	header    *widget.Label
}

func newSearchRow(l *searchList) *searchRow {
	th := widget.Current()
	px := fontPx()
	r := &searchRow{Box: widget.NewBox(widget.Row, th.Spacing+4, 0), l: l}
	r.SetPadding(render.Insets{Left: th.Padding + 6, Right: th.Padding + 6})
	r.AddClass("launcher-search-row")
	r.icon = newAsyncIcon(l.iconSize, "launcher-search-row-icon")
	r.AppendAligned(r.icon.slot, false, widget.AlignCenter)
	r.glyph = widget.NewLabel(l.font, float64(l.iconSize)*0.8, "", 0)
	r.glyph.SetVisible(false)
	r.glyph.SetSizeRequest(l.iconSize, l.iconSize)
	r.AppendAligned(r.glyph, false, widget.AlignCenter)
	r.title = widget.NewRichLabel(l.font, px, "", 0)
	r.title.AddClass("launcher-search-row-title")
	r.title.SetVariants(l.variants)
	r.title.SetEllipsize(widget.EllipsizeEnd)
	r.AppendAligned(r.title, false, widget.AlignCenter)
	r.subtitle = widget.NewLabel(l.font, widgetsSmallPx(), "", 0)
	r.subtitle.AddClass("launcher-search-row-subtitle", css.DimLabel)
	r.subtitle.SetEllipsize(widget.EllipsizeEnd)
	r.AppendAligned(r.subtitle, true, widget.AlignCenter)
	r.hint = widget.NewLabel(l.font, widgetsSmallPx(), "", 0)
	r.hint.AddClass("launcher-search-row-hint", css.DimLabel)
	r.AppendAligned(r.hint, false, widget.AlignCenter)
	r.header = widget.NewLabel(l.font, widgetsSmallPx(), "", 0)
	r.header.AddClass("launcher-search-header")
	r.AppendAligned(r.header, true, widget.AlignCenter)
	r.SetOnPointer(func(e widget.PointerEvent) bool {
		if e.Button == widget.BTNLeft || e.Button == widget.BTNRight {
			l.p.rowPressed(r.index, e.Button)
		}
		return true
	})
	return r
}

// HitTest makes the row the leaf the pointer lands on.
func (r *searchRow) HitTest(p widget.Point) widget.Widget { return r.HitLeaf(r, p) }

// bind shows row i. Text that did not change costs a comparison.
func (r *searchRow) bind(i int, row search.Row, selected bool, number int) {
	r.index = i
	r.SetState(widget.StateSelected, selected)
	isHeader := row.Header != ""
	r.header.SetVisible(isHeader)
	r.icon.slot.SetVisible(!isHeader && r.l.icons && row.Entry != nil && row.Entry.Glyph == "")
	r.glyph.SetVisible(!isHeader && r.l.icons && row.Entry != nil && row.Entry.Glyph != "")
	r.title.SetVisible(!isHeader)
	r.subtitle.SetVisible(!isHeader)
	r.hint.SetVisible(!isHeader)
	if isHeader {
		r.header.SetText(row.Header)
		return
	}
	e := row.Entry
	r.positions = row.AppendHighlights(r.positions[:0], &r.l.p.hl)
	r.markup = appendHighlightMarkup(r.markup[:0], e.Title, r.positions, r.l.highlightTag())
	if string(r.markup) != r.title.Markup() {
		r.title.SetMarkup(string(r.markup))
	}
	r.subtitle.SetText(e.Subtitle)
	hint := e.Hint
	if hint == "" && number >= 1 && number <= 9 {
		hint = numberHints[number-1]
	}
	r.hint.SetText(hint)
	if !r.l.icons {
		return
	}
	if e.Glyph != "" {
		r.glyph.SetText(e.Glyph)
		return
	}
	r.l.bindAsync(r.icon, iconSource(e.Icon))
}

// numberHints are the rows' shortcut labels.
var numberHints = [...]string{"Ctrl+1", "Ctrl+2", "Ctrl+3", "Ctrl+4", "Ctrl+5", "Ctrl+6", "Ctrl+7", "Ctrl+8", "Ctrl+9"}

// iconSource is what names an entry's icon: a theme name or a file.
func iconSource(icon engine.Icon) string {
	switch ic := icon.(type) {
	case engine.IconName:
		return string(ic)
	case engine.IconFile:
		return string(ic)
	case engine.IconThumbnail:
		return ic.Fallback
	}
	return ""
}

// highlightTag is the highlight's markup for the theme's accent, built
// when the accent changes rather than per row.
func (l *searchList) highlightTag() string {
	accent := widget.Current().Accent
	if l.openTag == "" || accent != l.openColor {
		l.openColor = accent
		l.openTag = "<span weight=\"700\" foreground=\"" + hexColor(accent) + "\">"
	}
	return l.openTag
}

// asyncIcon is a fixed-size slot for an icon that may not be decoded yet.
// The slot is its size from the start, so an icon landing moves nothing,
// and the one Icon widget is renamed rather than rebuilt.
type asyncIcon struct {
	slot *widget.Box
	icon *widget.Icon
	size int
	// name is what the slot should show, shown what it does.
	name, shown string
}

func newAsyncIcon(size int, class string) *asyncIcon {
	a := &asyncIcon{slot: widget.NewBox(widget.Row, 0, 0), size: size}
	a.slot.SetSizeRequest(size, size)
	a.slot.AddClass(class)
	return a
}

// show puts the decoded icon in the slot. Only this touches a theme name
// on the UI loop, once the cache holds its raster.
func (a *asyncIcon) show(name string) {
	a.shown = name
	if a.icon == nil {
		a.icon = widget.NewThemeIconFallback(name, a.size)
		a.slot.Append(a.icon, false)
		return
	}
	a.icon.SetThemeName(name)
	a.icon.SetVisible(true)
}

func (a *asyncIcon) blank() {
	a.shown = ""
	if a.icon != nil {
		a.icon.SetVisible(false)
	}
}

// bindAsync makes a show name: at once when its raster is decoded (the
// cache answers without waiting), else blank until the loader has it.
func (l *searchList) bindAsync(a *asyncIcon, name string) {
	if a.name == name && (a.shown == name || name == "") {
		return
	}
	a.name = name
	if name == "" {
		a.blank()
		return
	}
	if widget.ThemeIconPreloaded(name, a.size, l.num, l.denom) {
		a.show(name)
		return
	}
	a.blank()
	if !slices.Contains(l.waiting, a) {
		l.waiting = append(l.waiting, a)
	}
	l.loader.request(iconRequest{name: name, size: a.size, num: l.num, denom: l.denom})
}

// iconReady runs on the UI loop when a decode finished.
func (l *searchList) iconReady(req iconRequest) {
	kept := l.waiting[:0]
	for _, a := range l.waiting {
		switch {
		case a.name == req.name && a.size == req.size:
			a.show(req.name)
		case a.name != "" && a.shown != a.name:
			kept = append(kept, a)
		}
	}
	l.waiting = kept
}

// setScale follows the canvas: another device scale is another raster.
func (l *searchList) setScale(num, denom int) {
	if num == l.num && denom == l.denom {
		return
	}
	l.num, l.denom = num, denom
	l.p.s.setScale(num, denom)
	for _, row := range l.pool {
		row.icon.name = ""
	}
	l.p.v.searchIcon.name = ""
	l.bindAsync(l.p.v.searchIcon, "ld-search-symbolic")
	l.InvalidateLayout()
}
