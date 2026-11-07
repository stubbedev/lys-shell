package widgets

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/highlight"
	"github.com/stubbedev/lys-ui/widget"
)

const (
	// codeTabWidth is the columns a tab advances to the next stop of.
	codeTabWidth = 4
	// codeLineBytes cuts a line longer than this: a pane shows a few
	// hundred characters of one, and a minified file is one line.
	codeLineBytes = 4096
	// codeGutterDigits is the gutter's least width, in digits.
	codeGutterDigits = 2
)

// codeRole is one color the highlighting paints with, which the
// stylesheet gives through the role's class, and the style ids it
// colors. The ids follow the scheme fallbacks (def:decimal to
// def:number to def:constant), so each names only where a family
// starts.
type codeRole struct {
	class        string
	ids          []string
	bold, italic bool
}

// codeRoles are the palette's roles: comments muted, keywords in the
// accent, types blue, strings green, constants yellow, and escapes,
// preprocessor lines, errors and removed lines red.
var codeRoles = [...]codeRole{
	{class: "hl-comment", ids: []string{"def:comment", "def:net-address", "def:blockquote-marker", "def:thematic-break"}, italic: true},
	{class: "hl-keyword", ids: []string{"def:keyword", "def:statement", "def:heading", "def:list-marker"}, bold: true},
	{class: "hl-type", ids: []string{"def:type", "def:identifier", "def:link-text"}},
	{class: "hl-string", ids: []string{"def:string", "def:character", "def:inline-code", "def:preformatted-section", "def:inserted"}},
	{class: "hl-constant", ids: []string{"def:constant"}},
	{class: "hl-special", ids: []string{"def:special-char", "def:preprocessor", "def:error", "def:deleted"}},
}

// CodeView shows text as bat does: line numbers in a gutter and tokens
// colored from the theme's palette. Only the lines that fit are found,
// highlighted and laid out, so a large file costs a pane's worth; the
// row widgets are pooled and rebound as the text changes.
type CodeView struct {
	widget.Base
	font render.Font
	px   float64
	// text and name are what shows and the file it came from.
	text, name string
	hl         widget.Highlighter
	// lines are the [start, end) byte ranges of the lines found so far,
	// next where finding goes on, -1 past the text's end.
	lines [][2]int
	next  int
	// states are the highlighter's state at the start of each line
	// highlighted so far and the one after it.
	states []int
	// gen counts the text and palette changes a row's binding answers to.
	gen   uint64
	rows  []*codeRow
	shown []widget.Widget
	kids  []widget.Widget
	// probes carry the roles' classes, so the stylesheet colors them, and
	// gutter measures the gutter's digits.
	probes [len(codeRoles)]*widget.Box
	gutter *widget.Label
	colors [len(codeRoles)]render.Color
	scheme widget.TextScheme
	// highlighted counts the lines handed to the highlighter: the work the
	// tests gate.
	highlighted int
	runes       []rune
	markup      []byte
}

// codeRow is one line: its number and its text.
type codeRow struct {
	num  *widget.Label
	code *widget.RichLabel
	line int
	gen  uint64
}

// NewCodeView is an empty code view setting text at px.
func NewCodeView(font render.Font, px float64) *CodeView {
	c := &CodeView{font: font, px: px, next: -1}
	c.AddClass("code-view")
	for i, r := range codeRoles {
		c.probes[i] = widget.NewBox(widget.Row, 0, 0)
		c.probes[i].AddClass(r.class)
		c.probes[i].SetVisible(false)
	}
	c.gutter = widget.NewLabel(font, px, "", 0)
	c.gutter.AddClass("code-view-gutter")
	c.gutter.SetVisible(false)
	return c
}

// SetText shows text, highlighted as the language its name, #! line or
// modeline says, plain when none does.
func (c *CodeView) SetText(text, name string) {
	c.text, c.name = text, name
	c.hl, _ = highlight.Detect(name, text)
	c.lines = c.lines[:0]
	c.next = 0
	c.states = append(c.states[:0], 0)
	c.gen++
	c.InvalidateLayout()
}

// Text is the text shown.
func (c *CodeView) Text() string { return c.text }

// Name is the file name the text was highlighted for.
func (c *CodeView) Name() string { return c.name }

// HexColor is c as a #rrggbb markup color.
func HexColor(c render.Color) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R(), c.G(), c.B())
}

// line is line i's byte range, found as far as i and no further; false
// past the text's end.
func (c *CodeView) line(i int) (start, end int, ok bool) {
	for len(c.lines) <= i && c.next >= 0 {
		start := c.next
		nl := strings.IndexByte(c.text[start:], '\n')
		end := len(c.text)
		c.next = -1
		if nl >= 0 {
			end = start + nl
			c.next = end + 1
		}
		if start == len(c.text) && nl < 0 {
			break
		}
		c.lines = append(c.lines, [2]int{start, end})
	}
	if i >= len(c.lines) {
		return 0, 0, false
	}
	return c.lines[i][0], c.lines[i][1], true
}

// lineRunes is line i as runes, cut to codeLineBytes, a carriage return
// dropped and tabs expanded to their stops.
func (c *CodeView) lineRunes(i int) []rune {
	start, end, _ := c.line(i)
	s := strings.TrimSuffix(c.text[start:end], "\r")
	if len(s) > codeLineBytes {
		s = strings.ToValidUTF8(s[:codeLineBytes], "")
	}
	c.runes = c.runes[:0]
	for _, r := range s {
		if r != '\t' {
			c.runes = append(c.runes, r)
			continue
		}
		for n := codeTabWidth - len(c.runes)%codeTabWidth; n > 0; n-- {
			c.runes = append(c.runes, ' ')
		}
	}
	return c.runes
}

// lineSpans is line i's runes and their spans. The lines before it are
// highlighted first when they have not been: a construct spanning lines
// carries its state forward.
func (c *CodeView) lineSpans(i int) ([]rune, []widget.TextSpan) {
	if c.hl == nil {
		return c.lineRunes(i), nil
	}
	for k := len(c.states) - 1; k < i; k++ {
		c.highlighted++
		_, end := c.hl.Highlight(c.lineRunes(k), c.states[k])
		c.states = append(c.states, end)
	}
	rs := c.lineRunes(i)
	c.highlighted++
	spans, end := c.hl.Highlight(rs, c.states[i])
	if len(c.states) == i+1 {
		c.states = append(c.states, end)
	}
	return rs, spans
}

// restyle reads the roles' colors from the stylesheet and rebuilds the
// scheme when the palette changed; true when it did.
func (c *CodeView) restyle() bool {
	var colors [len(codeRoles)]render.Color
	for i, p := range c.probes {
		colors[i] = widget.CascadeColor(p)
	}
	if colors == c.colors && c.scheme != nil {
		return false
	}
	c.colors = colors
	c.scheme = widget.TextScheme{"def:emphasis": {Italic: true}, "def:strong-emphasis": {Bold: true}}
	for i, r := range codeRoles {
		for _, id := range r.ids {
			c.scheme[id] = widget.SchemeStyle{Color: colors[i], Bold: r.bold && id == r.ids[0], Italic: r.italic}
		}
	}
	c.gen++
	return true
}

// bind shows line i in row, unless it already shows it for this text
// and palette.
func (c *CodeView) bind(row *codeRow, i int) {
	if row.line == i && row.gen == c.gen {
		return
	}
	row.line, row.gen = i, c.gen
	row.num.SetText(strconv.Itoa(i + 1))
	rs, spans := c.lineSpans(i)
	c.markup = c.appendMarkup(c.markup[:0], rs, spans)
	row.code.SetMarkup(string(c.markup))
}

// appendMarkup is line rs in lys-ui markup, each span in its scheme
// style and the rest in the text's own color.
func (c *CodeView) appendMarkup(dst []byte, rs []rune, spans []widget.TextSpan) []byte {
	at := 0
	plain := func(to int) {
		for _, r := range rs[at:to] {
			dst = appendEscapedRune(dst, r)
		}
		at = to
	}
	for _, sp := range spans {
		plain(sp.Start)
		st, ok := c.scheme.Style(sp.Class)
		if !ok {
			plain(sp.End)
			continue
		}
		dst = append(dst, "<span"...)
		if st.Color != 0 {
			dst = append(dst, ` foreground="`...)
			dst = append(dst, HexColor(st.Color)...)
			dst = append(dst, '"')
		}
		if st.Bold {
			dst = append(dst, ` weight="bold"`...)
		}
		if st.Italic {
			dst = append(dst, ` style="italic"`...)
		}
		dst = append(dst, '>')
		plain(sp.End)
		dst = append(dst, "</span>"...)
	}
	plain(len(rs))
	return dst
}

// appendEscapedRune appends r escaped for lys-ui markup, as appendEscaped
// does a string.
func appendEscapedRune(dst []byte, r rune) []byte {
	switch r {
	case '&':
		return append(dst, "&amp;"...)
	case '<':
		return append(dst, "&lt;"...)
	case '>':
		return append(dst, "&gt;"...)
	case '\'':
		return append(dst, "&apos;"...)
	case '"':
		return append(dst, "&quot;"...)
	}
	return utf8.AppendRune(dst, r)
}

// row is the pooled row for the n-th line shown, made the first time a
// pane is that tall and parented at once, so the stylesheet reaches it
// before it is first measured.
func (c *CodeView) row(n int) *codeRow {
	for len(c.rows) <= n {
		r := &codeRow{line: -1}
		r.num = widget.NewLabel(c.font, c.px, "", 0)
		r.num.AddClass("code-view-gutter")
		r.num.SetAlignment(render.AlignEnd)
		r.code = widget.NewRichLabel(c.font, c.px, "", 0)
		r.code.AddClass("code-view-line")
		r.code.SetWrap(true)
		widget.SetParents(c, r.num, r.code)
		c.rows = append(c.rows, r)
	}
	return c.rows[n]
}

// Measure is flexible: the pane gives the view its room.
func (c *CodeView) Measure(widget.Axis, int) widget.Sizes { return widget.Sizes{} }

// Arrange finds, highlights and lays out the lines that fit, top down.
func (c *CodeView) Arrange(r render.Rect) {
	c.ArrangeSelf(r)
	c.setKids()
	c.restyle()
	c.shown = c.shown[:0]
	lineH := c.gutter.Measure(widget.Column, -1).Nat
	if lineH <= 0 || c.text == "" {
		c.setKids()
		return
	}
	digits := max(len(strconv.Itoa(r.H/lineH+1)), codeGutterDigits)
	if len(c.gutter.Text()) != digits {
		c.gutter.SetText(strings.Repeat("0", digits))
	}
	gutterW := c.gutter.Measure(widget.Row, -1).Nat
	gap := lineH / 2
	codeW := max(r.W-gutterW-gap, 1)
	y := r.Y
	for i := 0; y < r.Y+r.H; i++ {
		if _, _, ok := c.line(i); !ok {
			break
		}
		row := c.row(i)
		c.bind(row, i)
		h := max(row.code.Measure(widget.Column, codeW).Nat, lineH)
		row.num.Arrange(render.Rect{X: r.X, Y: y, W: gutterW, H: lineH})
		row.code.Arrange(render.Rect{X: r.X + gutterW + gap, Y: y, W: codeW, H: h})
		c.shown = append(c.shown, row.num, row.code)
		y += h
	}
	c.setKids()
}

// setKids makes the rows shown and the style probes the view's children:
// every widget the cascade reaches.
func (c *CodeView) setKids() {
	c.kids = append(c.kids[:0], c.shown...)
	for _, p := range c.probes {
		c.kids = append(c.kids, p)
	}
	c.kids = append(c.kids, c.gutter)
	widget.SetParents(c, c.kids...)
}

// ArrangeRoot mirrors Arrange for the root path.
func (c *CodeView) ArrangeRoot(r render.Rect) { c.Arrange(r) }

// Paint paints the rows shown, clipped to the view. A palette that
// changed since they were bound lays them out again for the next frame.
func (c *CodeView) Paint(cv *render.Canvas) {
	if c.restyle() {
		c.InvalidateLayout()
	}
	prev := cv.PushClip(c.Bounds())
	for _, w := range c.shown {
		widget.PaintChild(cv, w)
	}
	cv.PopClip(prev)
}

// Children are the rows shown and the style probes.
func (c *CodeView) Children() []widget.Widget { return c.kids }

// HitTest makes the view the leaf the pointer lands on.
func (c *CodeView) HitTest(p widget.Point) widget.Widget { return c.HitLeaf(c, p) }
