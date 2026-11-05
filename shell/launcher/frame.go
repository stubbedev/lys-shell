package launcher

import (
	"math"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/anim"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/shell/reveal"
)

// The launcher's measures, in logical pixels at scale 1: every session is
// these, whichever mode it shows.
const (
	// panelTopFraction is how far down the output the field sits: above
	// centre, so results grow into the room below without the field
	// moving.
	panelTopFraction = 0.22
	panelEdgeGap     = 32
	inputRowHeight   = 62
	footerHeight     = 36
	searchRowHeight  = 46
	headerRowHeight  = 28
	searchIconSize   = 26
	entryFontFactor  = 1.45
	// glyphFontFactor sizes an emoji to read as large as an icon box.
	glyphFontFactor = 0.8
	rowEdgeGap      = 8
	rowHalfGap      = 2
)

// scaledPx is the styling scale's measure of logical pixels.
func scaledPx(cfg *config.Config) func(int) int {
	scale := float64(cfg.Styling.Scale)
	return func(v int) int { return int(math.Round(float64(v) * scale)) }
}

// launcherWidthPx is launcher.width in pixels.
func launcherWidthPx(cfg *config.Config) int {
	return int(math.Round(cfg.Launcher.Width.ResolvePx(config.LauncherWidthBaseRem*16, float64(cfg.Styling.Scale))))
}

// panelFrame is what every launcher session sits in: a surface covering
// the output, the rounded panel on it, the springs moving the panel, and
// the blur behind it.
type panelFrame struct {
	root    *panelRoot
	box     *widget.Box
	content *widget.Box
	motion  *panelMotion
	// win is the session's surface, nil before it maps and after it goes.
	win func() Window
	// target is the panel's size for what it shows, in an output.
	target func(out render.Rect) (w, h int)
	// place is where a panel of a size goes in an output.
	place func(out render.Rect, w, h int) render.Rect
}

func newPanelFrame(d Deps, content *widget.Box, w, h int, win func() Window) *panelFrame {
	f := &panelFrame{win: win, content: content}
	f.box = widget.NewBox(widget.Column, 0, 0)
	f.box.AddClass("launcher-panel")
	f.box.Append(&panelStage{f: f}, true)
	f.root = &panelRoot{f: f}
	f.root.SetElement("window")
	f.root.AddClass("launcher-window")
	if d.Sheet != nil {
		f.root.AttachStylesheet(d.Sheet)
	}
	f.motion = newPanelMotion(f, w, h)
	return f
}

// retarget sends the panel toward its size for what it now shows.
func (f *panelFrame) retarget() {
	f.motion.resize(f.target(f.root.bounds()))
}

// panelLayer is every session's layer surface over root; a press outside
// the panel runs outside.
func panelLayer(output *app.Output, f *panelFrame, keys func(app.Accel) bool, outside, closed func()) app.LayerConfig {
	return app.LayerConfig{
		Layer:          app.LayerOverlay,
		Anchor:         app.AnchorTop | app.AnchorBottom | app.AnchorLeft | app.AnchorRight,
		ExclusiveZone:  -1,
		Keyboard:       app.KeyboardExclusive,
		Namespace:      "lys-shell-launcher",
		Root:           f.root,
		Output:         output,
		OwnMotion:      true,
		BackgroundBlur: true,
		KeyCapture:     keys,
		OnPress: func(_ uint32, over widget.Widget) {
			if over == widget.Widget(f.root) {
				outside()
			}
		},
		OnClosed: closed,
	}
}

// placePanel is where a location puts a panel: centre is the search
// panel's place, above the middle with its top fixed so it grows down;
// the rest sit against their edges, offset by x and y (a positive x moves
// right, a positive y down).
func placePanel(out render.Rect, w, h int, loc config.LauncherLocation, x, y int) render.Rect {
	r := render.Rect{X: out.X + (out.W-w)/2, Y: out.Y + (out.H-h)/2, W: w, H: h}
	switch loc {
	case config.LauncherCenter:
		r.Y = out.Y + int(float64(out.H)*panelTopFraction)
		return r
	case config.LauncherNorth, config.LauncherNorthEast, config.LauncherNorthWest:
		r.Y = out.Y + y
	case config.LauncherSouth, config.LauncherSouthEast, config.LauncherSouthWest:
		r.Y = out.Y + out.H - h + y
	}
	switch loc {
	case config.LauncherWest, config.LauncherNorthWest, config.LauncherSouthWest:
		r.X = out.X + x
	case config.LauncherEast, config.LauncherNorthEast, config.LauncherSouthEast:
		r.X = out.X + out.W - w + x
	}
	return r
}

// panelWidthIn is width held inside an output, whose size may not be known
// yet.
func panelWidthIn(out render.Rect, width int) int {
	if out.W > 0 {
		width = min(width, out.W-2*panelEdgeGap)
	}
	return max(width, 1)
}

// panelChrome is every session's field and footer: the search icon, the
// chip (the level's title or the prompt), the entry, the message and the
// separator above the list, the notice and key hints below it.
type panelChrome struct {
	input      *widget.Box
	searchIcon *asyncIcon
	chip       *widget.Label
	entry      *widget.Entry
	message    *widget.RichLabel
	sep        *widget.Box
	footer     *widget.Box
	notice     *widget.Label
	hints      *widget.RichLabel
	hintBuf    []footerHint
	markup     []byte
}

func newPanelChrome(d Deps, cfg *config.Config, changed func(string)) *panelChrome {
	px := scaledPx(cfg)
	body := fontPx()
	c := &panelChrome{}
	c.input = widget.NewBox(widget.Row, px(10), 0)
	c.input.AddClass("launcher-search-input")
	c.input.SetSizeRequest(0, px(inputRowHeight))
	c.input.SetPadding(render.Insets{Left: px(18), Right: px(18)})
	c.searchIcon = newAsyncIcon(px(22), "launcher-search-icon")
	c.input.AppendAligned(c.searchIcon.slot, false, widget.AlignCenter)
	c.chip = widget.NewLabel(d.Font, body, "", 0)
	c.chip.AddClass("launcher-search-chip")
	c.chip.SetVisible(false)
	c.input.AppendAligned(c.chip, false, widget.AlignCenter)
	c.entry = widget.NewEntry(d.Font, math.Round(body*entryFontFactor), 0)
	c.entry.AddClass("launcher-search-entry")
	c.entry.OnChanged = changed
	c.input.AppendAligned(c.entry, true, widget.AlignCenter)

	c.message = widget.NewRichLabel(d.Font, body, "", 0)
	c.message.AddClass("launcher-search-message", css.DimLabel)
	c.message.SetMargin(render.Insets{Left: px(20), Right: px(20), Bottom: px(8)})
	c.message.SetVariants(d.Variants)
	c.message.SetWrap(true)
	c.message.SetVisible(false)

	c.sep = widget.NewBox(widget.Column, 0, 0)
	c.sep.AddClass("launcher-search-separator")
	c.sep.SetSizeRequest(0, 1)
	c.sep.SetVisible(false)

	c.footer = widget.NewBox(widget.Row, px(12), 0)
	c.footer.AddClass("launcher-search-footer")
	c.footer.SetSizeRequest(0, px(footerHeight))
	c.footer.SetPadding(render.Insets{Left: px(16), Right: px(16)})
	c.notice = widget.NewLabel(d.Font, widgetsSmallPx(), "", 0)
	c.notice.AddClass("launcher-search-notice")
	c.notice.SetEllipsize(widget.EllipsizeEnd)
	c.footer.AppendAligned(c.notice, true, widget.AlignCenter)
	c.hints = widget.NewRichLabel(d.Font, widgetsSmallPx(), "", 0)
	c.hints.AddClass("launcher-search-hints", css.DimLabel)
	c.hints.SetVariants(d.Variants)
	c.footer.AppendAligned(c.hints, false, widget.AlignCenter)
	c.footer.SetVisible(false)
	return c
}

// showHints writes the footer's hints from hintBuf.
func (c *panelChrome) showHints() {
	b := c.markup[:0]
	for i, h := range c.hintBuf {
		if i > 0 {
			b = append(b, "     "...)
		}
		b = append(b, "<b>"...)
		b = appendEscaped(b, h.key)
		b = append(b, "</b> "...)
		b = appendEscaped(b, h.does)
	}
	c.markup = b
	// The comparison allocates nothing, and the same footer is the usual case.
	if string(b) != c.hints.Markup() {
		c.hints.SetMarkup(string(b))
	}
}

// setMessage shows the message in markup, hidden without one.
func (c *panelChrome) setMessage(markup string) {
	if markup == "" {
		c.message.SetVisible(false)
		return
	}
	c.message.SetMarkup(messageMarkup(markup))
	c.message.SetVisible(true)
}

// setChip shows the title or prompt, hidden when empty.
func (c *panelChrome) setChip(text string) {
	c.chip.SetText(text)
	c.chip.SetVisible(text != "")
}

// closeHint is the footer's way out of a level.
func closeHint() footerHint { return footerHint{"Esc", i18n.T("launcher-footer-close")} }

func rowMargin(px func(int) int) render.Insets {
	return render.Insets{Top: px(rowHalfGap), Bottom: px(rowHalfGap), Left: px(rowEdgeGap), Right: px(rowEdgeGap)}
}

// newRowBox is a list row's box, in any session.
func newRowBox() *widget.Box {
	th := widget.Current()
	r := widget.NewBox(widget.Row, th.Spacing+4, 0)
	r.SetPadding(render.Insets{Left: th.Padding + 6, Right: th.Padding + 6})
	r.AddClass("launcher-search-row")
	return r
}

// panelRoot fills the output: transparent, the panel laid where the
// motion says, and a press on it (outside the panel) dismissing. The
// surface does not resize as the panel does, so its height can spring
// every frame with no round trip to the compositor, the field never
// moves, and the blur region follows the panel's own rect.
type panelRoot struct {
	widget.Base
	f    *panelFrame
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
		r.f.motion.snapSize(r.f.target(rect))
	}
	r.f.box.Arrange(r.f.motion.shownRect(rect))
	r.kids[0] = r.f.box
	widget.SetParents(r, r.kids[:]...)
}

// ArrangeRoot mirrors Arrange for the root path.
func (r *panelRoot) ArrangeRoot(rect render.Rect) { r.Arrange(rect) }

// Paint paints the panel, carrying the fade itself where the compositor
// cannot.
func (r *panelRoot) Paint(cv *render.Canvas) {
	if alpha := r.f.motion.paintAlpha(); alpha < 1 {
		prev := cv.PushAlpha(alpha)
		widget.PaintChild(cv, r.f.box)
		cv.PopAlpha(prev)
		return
	}
	widget.PaintChild(cv, r.f.box)
}

// Children is the panel.
func (r *panelRoot) Children() []widget.Widget { r.kids[0] = r.f.box; return r.kids[:] }

// BackdropRegion is what the compositor blurs: the panel's rounded box as
// it shows this frame, never the output around it.
func (r *panelRoot) BackdropRegion() []render.Rect {
	return render.RoundedRectRegion(r.f.box.Bounds(), widget.CornerRadii(r.f.box))
}

// HitTest is the panel's, else the root itself: a press to dismiss with.
func (r *panelRoot) HitTest(p widget.Point) widget.Widget {
	if hit := r.f.box.HitTest(p); hit != nil {
		return hit
	}
	return r.HitLeaf(r, p)
}

// panelStage clips the content to the panel while it moves: the content
// is laid out at its destination, so text never reflows or stretches.
type panelStage struct {
	widget.Base
	f    *panelFrame
	kids [1]widget.Widget
}

// Measure wants nothing: the motion sizes the panel.
func (s *panelStage) Measure(widget.Axis, int) widget.Sizes { return widget.Sizes{} }

// Arrange lays the content at the panel's resting place.
func (s *panelStage) Arrange(rect render.Rect) {
	s.ArrangeSelf(rect)
	s.f.content.Arrange(s.f.motion.contentRect(s.f.root.bounds()))
	s.kids[0] = s.f.content
	widget.SetParents(s, s.kids[:]...)
}

// ArrangeRoot mirrors Arrange for the root path.
func (s *panelStage) ArrangeRoot(rect render.Rect) { s.Arrange(rect) }

// Paint paints the content inside the panel.
func (s *panelStage) Paint(cv *render.Canvas) {
	prev := cv.PushClip(s.Bounds())
	widget.PaintChild(cv, s.f.content)
	cv.PopClip(prev)
}

// Children is the content.
func (s *panelStage) Children() []widget.Widget { s.kids[0] = s.f.content; return s.kids[:] }

// HitTest descends into the content.
func (s *panelStage) HitTest(p widget.Point) widget.Widget {
	b := s.Bounds()
	if p.X < b.X || p.Y < b.Y || p.X >= b.X+b.W || p.Y >= b.Y+b.H {
		return nil
	}
	return s.f.content.HitTest(p)
}

// panelMotion is the panel's movement: a spring for how far open it is
// and one for its size, each carrying its velocity when retargeted, so a
// close during an open or a list that changes mid-grow flows on. Reduced
// motion and disabled animations land at once (anim.Instant, the
// animations config).
type panelMotion struct {
	f     *panelFrame
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

func newPanelMotion(f *panelFrame, w, h int) *panelMotion {
	m := &panelMotion{f: f, alive: true, compositorFade: true}
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
			f.root.InvalidateLayout()
		}
	})
	m.sizeS.SetPrecision(0.2)
	m.w, m.h = float64(w), float64(h)
	m.targetW, m.targetH = w, h
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
		m.fade(reveal.FadeFrom, 1, d, nil)
		return
	}
	m.revS.SetParams(springFor(d, true))
	m.revS.Snap(reveal.FadeFrom)
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
	m.reveal = from
	m.applyReveal()
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
	if m.instant || m.reveal <= reveal.FadeFrom {
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

// reset readies the motion for another surface: closed, nothing moving,
// the size landed at w by h.
func (m *panelMotion) reset(w, h int) {
	m.stop()
	m.done = nil
	m.alive = true
	m.compositorFade = true
	m.revS.Snap(0)
	m.reveal = 0
	m.snapSize(w, h)
}

func (m *panelMotion) applyReveal() {
	if !m.alive {
		return
	}
	if win := m.f.win(); win != nil && m.compositorFade {
		if err := win.SetOpacity(min(max(m.reveal, 0), 1)); err != nil {
			m.compositorFade = false
		}
	}
	m.f.root.InvalidateLayout()
	m.f.root.Invalidate()
}

// paintAlpha is the fade the paint must carry.
func (m *panelMotion) paintAlpha() float64 {
	if m.compositorFade {
		return 1
	}
	return min(max(m.reveal, 0), 1)
}

// restRect is the panel's place at its current size in an output.
func (m *panelMotion) restRect(out render.Rect) render.Rect {
	return m.f.place(out, int(math.Round(m.w)), int(math.Round(m.h)))
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
