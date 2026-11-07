package launcher

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/apptheme"
	"github.com/stubbedev/lys-shell/shell/treetest"
	"github.com/stubbedev/lys-shell/styling"
)

// codeOn is a code view under the shell's stylesheet for cfg's palette,
// laid out w by h.
func codeOn(t *testing.T, cfg *config.Config, text, name string, w, h int) (*widget.Box, *codeView) {
	t.Helper()
	c := newCodeView(treetest.Font(t), 13)
	root := widget.NewBox(widget.Column, 0, 0)
	root.Append(c, true)
	root.AttachStylesheet(apptheme.New(cfg).Sheet)
	c.setText(text, name)
	layout(root, w, h)
	return root, c
}

func paletteOf(t *testing.T, cfg *config.Config) *styling.Palette {
	t.Helper()
	p, err := styling.ConfigPalette(cfg.Styling)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// lineMarkup is the markup line i of the view was bound with.
func lineMarkup(t *testing.T, c *codeView, i int) string {
	t.Helper()
	if i >= len(c.rows) || c.rows[i].line != i {
		t.Fatalf("line %d is not shown", i)
	}
	return c.rows[i].code.Markup()
}

// spanOf is the span tag the markup opens before word.
func spanOf(markup, word string) string {
	at := strings.Index(markup, ">"+word+"<")
	if at < 0 {
		return ""
	}
	return markup[strings.LastIndex(markup[:at], "<span"):at]
}

// Tokens take their colors from the theme's palette: the same Go source
// is painted in the dark palette's colors and in a light palette's, and
// each role in its own.
func TestTokensTakeThePalettesColors(t *testing.T) {
	src := "package main\n\n// greet says hello.\nfunc greet(n int) string { return \"hi\" + strconv.Itoa(42) }\n"
	dark := config.Defaults()
	light := config.Defaults()
	light.Styling.Appearance = config.AppearanceLight
	light.Styling.PaletteBaseTheme = "gruvbox-dark"
	for _, cfg := range []*config.Config{dark, light} {
		p := paletteOf(t, cfg)
		_, c := codeOn(t, cfg, src, "greet.go", 360, 400)
		line := lineMarkup(t, c, 3)
		for word, want := range map[string]render.Color{"func": p.Primary, "int": p.Blue, `&quot;hi&quot;`: p.Green, "42": p.Yellow} {
			if tag := spanOf(line, word); !strings.Contains(tag, hexColor(want)) {
				t.Errorf("%s: %q is %q, want %s", cfg.Styling.Appearance, word, tag, hexColor(want))
			}
		}
		if tag := spanOf(lineMarkup(t, c, 2), "// greet says hello."); !strings.Contains(tag, hexColor(p.FgMuted)) || !strings.Contains(tag, "italic") {
			t.Errorf("%s: the comment is %q", cfg.Styling.Appearance, tag)
		}
	}
	if paletteOf(t, dark).Primary == paletteOf(t, light).Primary {
		t.Fatal("the two palettes share their accent: the test proves nothing")
	}
}

// A text with nothing to say what it is shows plain, line numbers and
// all; copied source whose first line gives it away, and a script whose
// #! line names its interpreter, are highlighted.
func TestALanguageIsFoundByNameShebangOrNotAtAll(t *testing.T) {
	cfg := config.Defaults()
	_, plain := codeOn(t, cfg, "just some words\nand more", "", 360, 400)
	if m := lineMarkup(t, plain, 0); strings.Contains(m, "<span") || plain.rows[1].num.Text() != "2" {
		t.Errorf("plain text: %q, gutter %q", m, plain.rows[1].num.Text())
	}
	_, copied := codeOn(t, cfg, "package main\n\nfunc main() {}\n", "", 360, 400)
	if tag := spanOf(lineMarkup(t, copied, 2), "func"); !strings.Contains(tag, hexColor(paletteOf(t, cfg).Primary)) {
		t.Errorf("copied Go source: the keyword is %q", tag)
	}
	_, script := codeOn(t, cfg, "#!/bin/sh\nif true; then echo hi; fi\n", "", 360, 400)
	if tag := spanOf(lineMarkup(t, script, 1), "if"); !strings.Contains(tag, hexColor(paletteOf(t, cfg).Primary)) {
		t.Errorf("the shell keyword is %q", tag)
	}
}

// Only the lines that fit are found, highlighted and bound: a long file
// costs the pane's height, laying it out again costs nothing, and a
// taller pane highlights only the lines it adds.
func TestOnlyTheLinesThatFitAreHighlighted(t *testing.T) {
	var b strings.Builder
	for i := range 5000 {
		b.WriteString("x := " + strconv.Itoa(i) + " // line\n")
	}
	root, c := codeOn(t, config.Defaults(), b.String(), "big.go", 360, 300)
	shown := len(c.shown) / 2
	if shown == 0 || shown > 40 {
		t.Fatalf("%d lines shown in 300px", shown)
	}
	if c.highlighted != shown {
		t.Errorf("%d lines highlighted for %d shown", c.highlighted, shown)
	}
	if found := len(c.lines); found > shown+1 {
		t.Errorf("%d lines found for %d shown", found, shown)
	}
	allocs := testing.AllocsPerRun(20, func() { layout(root, 360, 300) })
	if c.highlighted != shown {
		t.Errorf("laying the same text out again highlighted %d more lines", c.highlighted-shown)
	}
	if allocs > 0 {
		t.Errorf("laying the same text out again allocates %.0f times", allocs)
	}
	layout(root, 360, 600)
	taller := len(c.shown) / 2
	if taller <= shown || c.highlighted != taller {
		t.Errorf("a taller pane shows %d lines and highlighted %d in all", taller, c.highlighted)
	}
}

// A construct spanning lines carries on when the lines below it are
// first shown later.
func TestALineShownLaterCarriesTheStateAbove(t *testing.T) {
	src := "/* a comment\nstill the comment\n*/ int x;\n"
	root, c := codeOn(t, config.Defaults(), src, "a.c", 360, 1)
	layout(root, 360, 400)
	if tag := spanOf(lineMarkup(t, c, 1), "still the comment"); !strings.Contains(tag, "italic") {
		t.Errorf("the second line of the comment is %q", tag)
	}
}

// Tabs advance to the next stop of four, a carriage return is dropped,
// and markup in the text stays text.
func TestLinesAreShownAsTheyRead(t *testing.T) {
	_, c := codeOn(t, config.Defaults(), "\ta\tb\r\n<i>x</i> & y\n", "", 360, 400)
	if got := c.rows[0].code.Text(); got != "    a   b" {
		t.Errorf("line 1 reads %q", got)
	}
	if got := c.rows[1].code.Text(); got != "<i>x</i> & y" {
		t.Errorf("line 2 reads %q", got)
	}
}

// A line longer than a pane could ever show is cut, so a minified file
// lays out a bounded amount.
func TestAHugeLineIsCut(t *testing.T) {
	_, c := codeOn(t, config.Defaults(), strings.Repeat("é", codeLineBytes), "", 360, 400)
	if got := len(c.rows[0].code.Text()); got > codeLineBytes {
		t.Errorf("the line holds %d bytes, want at most %d", got, codeLineBytes)
	}
}

// Lines and their numbers shape in the configured monospace family from
// their first layout on: "iiii" and "MMMM" are equally wide.
func TestLinesAreMonospaced(t *testing.T) {
	widget.SetFaceResolver(apptheme.FontResolver)
	t.Cleanup(func() { widget.SetFaceResolver(nil) })
	_, c := codeOn(t, config.Defaults(), "iiii\nMMMM\n", "", 360, 400)
	narrow := c.rows[0].code.Measure(widget.Row, -1).Nat
	wide := c.rows[1].code.Measure(widget.Row, -1).Nat
	if narrow != wide {
		t.Errorf("iiii is %dpx and MMMM %dpx: the lines are not monospaced", narrow, wide)
	}
}
