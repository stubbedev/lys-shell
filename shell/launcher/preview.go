package launcher

import (
	"context"
	"errors"
	"image"
	"log"
	"math"
	"strings"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	engine "github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/styling"
)

const (
	// previewWidth is the pane's width, in logical pixels at scale 1.
	previewWidth = 360
	// previewMinRows is the least the list area is tall beside a preview:
	// a pane one row high shows nothing.
	previewMinRows = 7
	// heroIconSize is the summary's picture, in logical pixels at scale 1.
	heroIconSize = 72
)

// previewSettle is how long the selection must rest before its preview
// loads: arrowing through a list reads no files and captures no windows.
const previewSettle = 40 * time.Millisecond

// previewPane is the side pane every kind of session shows its selected
// row in: a picture, text, the facts that describe the row, or, with
// nothing to load, the row's own summary. It is built once; showing
// another row rebinds its widgets.
type previewPane struct {
	box     *paneBox
	image   *widget.Image
	text    *widget.Label
	facts   *widget.Box
	rows    []factRow
	summary *rowSummary
	font    render.Font
	// shown is the picture the image widget holds.
	shown image.Image
	// width is the pane's width in pixels at the session's scale.
	width int
}

// paneBox is the pane's column, height tall whatever it shows: what is
// previewed never resizes the panel around it.
type paneBox struct {
	*widget.Box
	height int
}

// Measure is the content's width and the fixed height.
func (b *paneBox) Measure(axis widget.Axis, forSize int) widget.Sizes {
	if axis == widget.Column {
		return widget.FixedSizes(b.height)
	}
	return b.Box.Measure(axis, forSize)
}

// factRow is one labelled line under the content.
type factRow struct {
	box   *widget.Box
	label *widget.Label
	value *widget.Label
}

func newPreviewPane(font render.Font, px func(int) int, height int) *previewPane {
	p := &previewPane{box: &paneBox{Box: widget.NewBox(widget.Column, 0, px(12)), height: height}, font: font, width: px(previewWidth)}
	p.box.AddClass("launcher-preview")
	p.box.SetInlineStyle(styling.WidthStyle(p.width))
	p.image = widget.NewImage(nil)
	p.image.SetScale(widget.ImageFit)
	p.image.AddClass("launcher-preview-image")
	p.text = widget.NewLabel(font, fontPx(), "", 0)
	p.text.AddClass("launcher-preview-text")
	p.text.SetWrap(true)
	p.facts = widget.NewBox(widget.Column, px(4), 0)
	p.facts.AddClass("launcher-preview-facts")
	p.summary = newRowSummary(font, px)
	p.box.Append(p.image, true)
	p.box.Append(p.text, false)
	p.box.Append(p.facts, false)
	p.box.Append(p.summary.box, false)
	p.show(nil, engine.PreviewContent{}, nil)
	p.box.SetVisible(false)
	return p
}

// show binds a row's facts and loaded content; err shows the facts
// alone. It reports whether the pane shows anything, so the caller can
// fall back to the row's summary.
func (p *previewPane) show(facts []engine.PreviewFact, c engine.PreviewContent, err error) bool {
	var img image.Image
	text := ""
	if err == nil {
		img, text = c.Image, c.Text
	}
	p.image.SetVisible(img != nil)
	if img != nil && img != p.shown {
		p.shown = img
		p.image.SetImage(img)
	}
	p.text.SetVisible(text != "")
	if text != "" {
		p.text.SetText(previewLines(text))
	}
	p.showFacts(facts)
	p.summary.box.SetVisible(false)
	return img != nil || text != "" || len(facts) > 0
}

// showFacts binds facts to the pooled rows, growing the pool only past
// the most facts any row had.
func (p *previewPane) showFacts(facts []engine.PreviewFact) {
	for len(p.rows) < len(facts) {
		r := factRow{box: widget.NewBox(widget.Row, 8, 0)}
		r.label = widget.NewLabel(p.font, widgetsSmallPx(), "", 0)
		r.label.AddClass("launcher-preview-fact-label", css.DimLabel)
		r.value = widget.NewLabel(p.font, widgetsSmallPx(), "", 0)
		r.value.AddClass("launcher-preview-fact-value")
		r.value.SetWrap(true)
		r.box.AppendAligned(r.label, false, widget.AlignStart)
		r.box.Append(r.value, true)
		p.facts.Append(r.box, false)
		p.rows = append(p.rows, r)
	}
	for i, r := range p.rows {
		r.box.SetVisible(i < len(facts))
		if i < len(facts) {
			r.label.SetText(facts[i].Label)
			r.value.SetText(facts[i].Value)
		}
	}
	p.facts.SetVisible(len(facts) > 0)
}

// showSummary shows what a row says of itself, in place of content.
func (p *previewPane) showSummary(bind func(*asyncIcon, string), icon, glyph, title, subtitle string) {
	p.show(nil, engine.PreviewContent{}, nil)
	p.summary.box.SetVisible(true)
	p.summary.show(bind, icon, glyph, title, subtitle)
}

// widgetsSmallPx is the small text size: footers, subtitles, facts.
func widgetsSmallPx() float64 { return math.Round(fontPx() * 0.85) }

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

// rowSummary is the pane for a row with nothing to load: its picture
// large, its title and its subtitle.
type rowSummary struct {
	box      *widget.Box
	icon     *asyncIcon
	glyph    *widget.Label
	title    *widget.Label
	subtitle *widget.Label
}

func newRowSummary(font render.Font, px func(int) int) *rowSummary {
	s := &rowSummary{box: widget.NewBox(widget.Column, px(8), 0)}
	s.box.AddClass("launcher-preview-summary")
	s.box.SetPadding(render.Insets{Top: px(24)})
	s.icon = newAsyncIcon(px(heroIconSize), "launcher-preview-icon")
	s.box.AppendAligned(s.icon.slot, false, widget.AlignCenter)
	s.glyph = widget.NewLabel(font, float64(px(heroIconSize))*glyphFontFactor, "", 0)
	s.box.AppendAligned(s.glyph, false, widget.AlignCenter)
	s.title = widget.NewLabel(font, math.Round(fontPx()*1.15), "", 0)
	s.title.AddClass("launcher-preview-title")
	s.title.SetWrap(true)
	s.title.SetAlignment(render.AlignCenter)
	s.box.Append(s.title, false)
	s.subtitle = widget.NewLabel(font, widgetsSmallPx(), "", 0)
	s.subtitle.AddClass(css.DimLabel)
	s.subtitle.SetWrap(true)
	s.subtitle.SetAlignment(render.AlignCenter)
	s.box.Append(s.subtitle, false)
	return s
}

func (s *rowSummary) show(bind func(*asyncIcon, string), icon, glyph, title, subtitle string) {
	s.glyph.SetVisible(glyph != "")
	s.glyph.SetText(glyph)
	s.icon.slot.SetVisible(glyph == "")
	if glyph == "" {
		bind(s.icon, icon)
	}
	s.title.SetText(title)
	s.subtitle.SetText(subtitle)
	s.subtitle.SetVisible(subtitle != "")
}

// previewLoader waits for the selection to rest and loads the selected
// row's preview off the UI loop. Starting another row drops the last
// one's wait and load, so only the row the selection rests on loads.
type previewLoader struct {
	after  func(time.Duration, func()) func()
	invoke func(func())
	thumbs engine.Thumbnailing
	// stopWait and stopLoad end the current row's wait and load.
	stopWait func()
	stopLoad context.CancelFunc
	// loads counts the previews loaded: the reads and captures asked for.
	loads int
}

func newPreviewLoader(d Deps, thumbs engine.Thumbnailing) *previewLoader {
	return &previewLoader{after: d.After, invoke: d.Invoke, thumbs: thumbs}
}

// start drops the last row's wait and load, and returns the context the
// next row loads under.
func (l *previewLoader) start(parent context.Context) context.Context {
	l.stop()
	ctx, cancel := context.WithCancel(parent)
	l.stopLoad = cancel
	return ctx
}

// stop drops the current row's wait and load.
func (l *previewLoader) stop() {
	if l.stopWait != nil {
		l.stopWait()
		l.stopWait = nil
	}
	if l.stopLoad != nil {
		l.stopLoad()
		l.stopLoad = nil
	}
}

// settle runs fn once the selection has rested.
func (l *previewLoader) settle(fn func()) { l.stopWait = l.after(previewSettle, fn) }

// fetch loads p off the loop and hands it to done on the loop, unless ctx
// ended first. Nothing to load is no failure; anything else is logged
// and handed on, so the pane shows what it still can.
func (l *previewLoader) fetch(ctx context.Context, p engine.Preview, done func(engine.PreviewContent, error)) {
	l.loads++
	go func() {
		content, err := engine.LoadPreview(ctx, p, l.thumbs)
		l.invoke(func() {
			if ctx.Err() != nil {
				return
			}
			if err != nil && !errors.Is(err, engine.ErrNoPreview) {
				log.Printf("launcher: preview: %v", err)
			}
			done(content, err)
		})
	}()
}
