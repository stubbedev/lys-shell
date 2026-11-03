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
	engine "github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/search"
	"github.com/stubbedev/lys-shell/styling"
)

// The panel's measures of its own, in logical pixels at scale 1; frame.go
// holds what every session shares.
const (
	// previewMinRows is the least the list area is tall beside a preview:
	// a pane one row high shows nothing.
	previewMinRows = 7
	previewWidth   = 360
)

func hexColor(c render.Color) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R(), c.G(), c.B())
}

// panelViews is the panel's widget tree.
type panelViews struct {
	*panelFrame
	*panelChrome
	p       *panel
	body    *widget.Box
	list    *searchList
	pane    *widget.Box
	image   *widget.Image
	text    *widget.Label
	summary *rowSummary
	font    render.Font
	scale   float64
	lines   int
	// baseW is the width without the preview pane, previewW the pane's.
	baseW, previewW int
	withPreview     bool
}

func newPanelViews(p *panel, cfg *config.Config) *panelViews {
	d := p.s.d
	scale := float64(cfg.Styling.Scale)
	px := scaledPx(cfg)
	body := fontPx()
	v := &panelViews{
		p: p, font: d.Font, scale: scale, lines: max(int(cfg.Launcher.Lines), 1),
		baseW: launcherWidthPx(cfg), previewW: px(previewWidth),
	}
	v.panelChrome = newPanelChrome(d, cfg, func(text string) {
		if p.eng != nil && !p.closing {
			p.querySeq++
			p.eng.SetQuery(text, p.querySeq)
		}
	})

	v.list = newSearchList(p, cfg, d.Font, d.Variants, px(searchRowHeight), px(headerRowHeight), px(searchIconSize))
	v.list.bindAsync(v.searchIcon, "ld-search-symbolic")
	v.pane = widget.NewBox(widget.Column, 0, px(12))
	v.pane.AddClass("launcher-search-preview")
	v.pane.SetInlineStyle(styling.WidthStyle(v.previewW))
	v.image = widget.NewImage(nil)
	v.image.SetScale(widget.ImageFit)
	v.image.AddClass("launcher-search-preview-image")
	v.text = widget.NewLabel(d.Font, body, "", 0)
	v.text.AddClass("launcher-search-preview-text")
	v.text.SetWrap(true)
	v.summary = newRowSummary(d.Font, px)
	v.pane.Append(v.image, true)
	v.pane.Append(v.text, false)
	v.pane.Append(v.summary.box, false)
	v.showPreview(search.PreviewContent{}, nil, nil)
	v.pane.SetVisible(false)
	v.body = widget.NewBox(widget.Row, 0, 0)
	v.body.Append(v.list, true)
	v.body.Append(v.pane, false)

	content := widget.NewBox(widget.Column, 0, 0)
	content.Append(v.input, false)
	content.Append(v.message, false)
	content.Append(v.sep, false)
	content.Append(v.body, true)
	content.Append(v.footer, false)

	v.panelFrame = newPanelFrame(d, content, v.baseW, v.fieldH(), func() Window { return p.win })
	v.target = func(render.Rect) (int, int) { return v.targetW(), v.targetH() }
	v.place = func(out render.Rect, w, h int) render.Rect { return placePanel(out, w, h, config.LauncherCenter, 0, 0) }
	v.scaledTo = func(num, denom int) { v.list.setScale(num, denom) }
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
	return panelWidthIn(v.root.bounds(), w)
}

// pageRows is how many rows PageUp and PageDown move.
func (v *panelViews) pageRows() int { return max(v.lines-1, 1) }

// retarget sends the panel toward its size for the current content.
func (v *panelViews) retarget(*panel) {
	v.panelFrame.retarget()
}

func (v *panelViews) setField(text string) {
	stop := v.entry.OnChanged
	v.entry.OnChanged = nil
	v.entry.SetText(text)
	v.entry.OnChanged = stop
}

// setLevel shows what the level says about itself.
func (v *panelViews) setLevel(f search.Frame) {
	v.setChip(f.Title)
	v.entry.SetPlaceholder(f.Placeholder)
	echo := widget.EchoNormal
	if f.Mask {
		echo = widget.EchoPassword
	}
	v.entry.SetEcho(echo)
	v.setMessage(f.Message)
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
	v.showHints()
}

// showPreview shows content; err or nothing shows what the row says of
// itself, so the pane is never blank while a row is selected.
func (v *panelViews) showPreview(c search.PreviewContent, err error, entry *search.Entry) {
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
	v.summary.box.SetVisible(img == nil && text == "" && entry != nil)
	if img == nil && text == "" && entry != nil {
		v.summary.show(v.list, entry)
	}
}

// heroIconSize is the summary's picture, in logical pixels at scale 1.
const heroIconSize = 72

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
	s.box.AddClass("launcher-search-preview-summary")
	s.box.SetPadding(render.Insets{Top: px(24)})
	s.icon = newAsyncIcon(px(heroIconSize), "launcher-search-preview-icon")
	s.box.AppendAligned(s.icon.slot, false, widget.AlignCenter)
	s.glyph = widget.NewLabel(font, float64(px(heroIconSize))*glyphFontFactor, "", 0)
	s.box.AppendAligned(s.glyph, false, widget.AlignCenter)
	s.title = widget.NewLabel(font, math.Round(fontPx()*1.15), "", 0)
	s.title.AddClass("launcher-search-preview-title")
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

func (s *rowSummary) show(l *searchList, e *search.Entry) {
	s.glyph.SetVisible(e.Glyph != "")
	s.glyph.SetText(e.Glyph)
	s.icon.slot.SetVisible(e.Glyph == "")
	if e.Glyph == "" {
		l.bindAsync(s.icon, iconSource(e.Icon))
	}
	s.title.SetText(e.Title)
	s.subtitle.SetText(e.Subtitle)
	s.subtitle.SetVisible(e.Subtitle != "")
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
	px := fontPx()
	r := &searchRow{Box: newRowBox(), l: l}
	r.icon = newAsyncIcon(l.iconSize, "launcher-search-row-icon")
	r.AppendAligned(r.icon.slot, false, widget.AlignCenter)
	r.glyph = widget.NewLabel(l.font, float64(l.iconSize)*glyphFontFactor, "", 0)
	r.glyph.SetVisible(false)
	r.glyph.SetSizeRequest(l.iconSize, 0)
	r.glyph.SetAlignment(render.AlignCenter)
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
	hero := l.p.v.summary.icon
	name := hero.name
	hero.name = ""
	l.bindAsync(hero, name)
	l.InvalidateLayout()
}
