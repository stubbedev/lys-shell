package styling

import (
	"io/fs"
	"path"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
)

// TestSheetOrderCoversEveryFile pins sheetOrder to the css/ tree: a
// file left out of the order would never load, and a listed file that
// is gone would panic at startup.
func TestSheetOrderCoversEveryFile(t *testing.T) {
	if gaps := orderGaps(sheets, "css", sheetOrder); gaps != nil {
		t.Errorf("sheetOrder against css/: %v", gaps)
	}
}

func TestOrderGapsNamesEachMismatch(t *testing.T) {
	fsys := fstest.MapFS{
		"css/a.css":     {},
		"css/sub/b.css": {},
		"css/notes.txt": {},
	}
	if gaps := orderGaps(fsys, "css", []string{"a.css", "sub/b.css"}); gaps != nil {
		t.Errorf("a matching order reported %v", gaps)
	}
	gaps := orderGaps(fsys, "css", []string{"a.css", "a.css", "gone.css"})
	want := []string{"listed twice: a.css", "listed but absent: gone.css", "not listed: sub/b.css"}
	if !slices.Equal(gaps, want) {
		t.Errorf("gaps = %q, want %q", gaps, want)
	}
}

// orderGaps lists every way order disagrees with the .css files under
// root in fsys, nil when it lists each exactly once.
func orderGaps(fsys fs.FS, root string, order []string) []string {
	files := map[string]bool{}
	_ = fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && path.Ext(p) == ".css" {
			files[strings.TrimPrefix(p, root+"/")] = true
		}
		return err
	})
	var gaps []string
	seen := map[string]bool{}
	for _, name := range order {
		switch {
		case seen[name]:
			gaps = append(gaps, "listed twice: "+name)
		case !files[name]:
			gaps = append(gaps, "listed but absent: "+name)
		}
		seen[name] = true
	}
	var unlisted []string
	for name := range files {
		if !seen[name] {
			unlisted = append(unlisted, "not listed: "+name)
		}
	}
	slices.Sort(unlisted)
	return append(gaps, unlisted...)
}

// TestStaticCSSIsTheBundle spot-checks the concatenation: the
// non-color tokens and the bar rules the Go bar relies on, and no
// palette or color token, which ThemeCSS and TokensCSS declare from the
// configured palette. No rule resets every node: lys-ui's theme layer
// styles the widgets the shell does not, and a universal `all: unset`
// would erase it.
func TestStaticCSSIsTheBundle(t *testing.T) {
	if strings.Contains(StaticCSS, "--palette-bg:") {
		t.Error("StaticCSS declares a palette of its own")
	}
	for _, tok := range config.CssTokens() {
		if strings.Contains(StaticCSS, "--"+string(tok)+":") {
			t.Errorf("StaticCSS restates the %s token", tok)
		}
	}
	if strings.Contains(StaticCSS, "* {\n  all: unset;") {
		t.Error("StaticCSS resets every node, erasing lys-ui's theme layer")
	}
	for _, want := range []string{
		"--space-sm: calc(0.5rem * var(--global-scale));",
		"button.bar-button {",
		".bar-group {",
	} {
		if !strings.Contains(StaticCSS, want) {
			t.Errorf("StaticCSS lacks %q", want)
		}
	}
}

// TestStaticCSSParsesClean pins engine completeness: the built-in
// stylesheet goes through lys-ui's CSS engine with zero parse warnings.
// Every declaration the engine cannot honor warns — a property the
// stylesheet starts using before the engine grows it, or a rule regression —
// so this fails naming it instead of silently rendering wrong.
func TestStaticCSSParsesClean(t *testing.T) {
	var warns []string
	widget.SetParseWarn(func(msg string) { warns = append(warns, msg) })
	defer widget.SetParseWarn(nil)
	widget.NewStylesheet(StaticCSS, 200)
	if len(warns) == 0 {
		return
	}
	t.Fatalf("the built-in stylesheet trips the CSS engine %d times:", len(warns))
	for _, w := range warns {
		t.Errorf("  %s", w)
	}
}
