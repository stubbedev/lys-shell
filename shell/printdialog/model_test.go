package printdialog

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/stubbedev/lys-lib/xdg"

	"github.com/stubbedev/lys-shell/internal/ipp"
)

func TestPageRanges(t *testing.T) {
	for in, want := range map[string]string{
		"1-5, 8": "0-4,7-7",
		"3":      "2-2",
		"5-2":    "1-4", // reversed reads as written forwards
		"0":      "0-0", // page numbers start at 1
		" 2 ,,4": "1-1,3-3",
	} {
		rs, ok := pageRanges(in)
		if !ok || gtkRanges(rs) != want {
			t.Errorf("pageRanges(%q) = %v %v, want %s", in, rs, ok, want)
		}
	}
	for _, in := range []string{"", "   ", "abc", "1-x", "-3", "1-2-3", "99999999999"} {
		if rs, ok := pageRanges(in); ok {
			t.Errorf("pageRanges(%q) = %v, want all pages", in, rs)
		}
	}
	if rs, _ := pageRanges("1-5, 8"); !reflect.DeepEqual(rs, [][2]int32{{1, 5}, {8, 8}}) {
		t.Errorf("1-based ranges = %v", rs)
	}
}

// GTK's zero-based ranges read back as the form's 1-based text.
func TestRangesFromGTK(t *testing.T) {
	for in, want := range map[string]string{"0-4,7-7": "1-5,8", "2": "3", "": "", "x-1": ""} {
		if got := rangesFromGTK(in); got != want {
			t.Errorf("rangesFromGTK(%q) = %q, want %q", in, got, want)
		}
	}
}

// A PWG media name reads as its size in millimetres, with GTK's name
// and the common label (or the size name's).
func TestParseMedia(t *testing.T) {
	for media, want := range map[string]paper{
		"iso_a4_210x297mm":   {"A4", "iso_a4", 210, 297, "iso_a4_210x297mm"},
		"na_letter_8.5x11in": {"Letter", "na_letter", 215.9, 279.4, "na_letter_8.5x11in"},
		"iso_a6_105x148mm":   {"A6", "iso_a6", 105, 148, "iso_a6_105x148mm"},
		"na_index-4x6_4x6in": {"Index-4x6", "na_index-4x6", 101.6, 152.4, "na_index-4x6_4x6in"},
	} {
		if got, ok := parseMedia(media); !ok || got != want {
			t.Errorf("parseMedia(%q) = %+v, %v; want %+v", media, got, ok, want)
		}
	}
	for _, bad := range []string{"", "a4", "iso_a4_210x297", "iso_a4_axbmm", "iso_a4_0x297mm", "custom_min_1x1pt"} {
		if p, ok := parseMedia(bad); ok {
			t.Errorf("parseMedia(%q) = %+v", bad, p)
		}
	}
}

// A queue's capabilities are what it reports and the form knows, its
// defaults chosen; where it reports nothing the common choices stand.
func TestCapabilitiesOf(t *testing.T) {
	c := capabilitiesOf(&ipp.Printer{
		Media: []string{"na_letter_8.5x11in", "garbage", "iso_a4_210x297mm"}, MediaDefault: "iso_a4_210x297mm",
		Sides: []string{"one-sided", "two-sided-long-edge"}, SidesDefault: "two-sided-long-edge",
		ColorModes: []string{"monochrome", "auto"}, Qualities: []int32{qualityDraft, qualityNormal},
	})
	var papers []string
	for _, p := range c.papers {
		papers = append(papers, p.gtk)
	}
	if !slices.Equal(papers, []string{"na_letter", "iso_a4"}) || c.paper != "iso_a4" {
		t.Errorf("papers %v (default %s)", papers, c.paper)
	}
	if !slices.Equal(c.sides, []string{"one-sided", "two-sided-long-edge"}) || c.side != "two-sided-long-edge" {
		t.Errorf("sides %v (default %s)", c.sides, c.side)
	}
	if !slices.Equal(c.colors, []string{"monochrome"}) || c.color != "monochrome" {
		t.Errorf("colors %v (default %s)", c.colors, c.color)
	}
	if !slices.Equal(c.qualities, []int32{qualityNormal, qualityDraft}) || c.quality != qualityNormal {
		t.Errorf("qualities %v (default %d)", c.qualities, c.quality)
	}
	file := capabilitiesOf(nil)
	if len(file.papers) != len(commonPapers) || file.paper != "iso_a4" || !slices.Equal(file.sides, allSides) || file.color != "color" {
		t.Errorf("Print to File offers %+v", file)
	}
}

func a4() paper { return commonPapers[0] }

func TestFormSettings(t *testing.T) {
	f := form{copies: 2, pages: pagesRanges, ranges: "1-3", landscape: true, paper: commonPapers[1], color: "monochrome", side: "two-sided-short-edge", quality: qualityDraft}
	want := []Setting{
		{"printer", "office"},
		{"n-copies", "2"},
		{"orientation", "landscape"},
		{"paper-format", "na_letter"},
		{"paper-width", "215.9"},
		{"paper-height", "279.4"},
		{"use-color", "false"},
		{"duplex", "vertical"},
		{"quality", "draft"},
		{"print-pages", "ranges"},
		{"page-ranges", "0-2"},
	}
	if got := f.settings("office"); !slices.Equal(got, want) {
		t.Errorf("settings =\n %v\nwant\n %v", got, want)
	}
	plain := form{copies: 1, paper: a4(), color: "color", side: "one-sided", quality: qualityNormal}.settings("x")
	if !slices.Contains(plain, Setting{"print-pages", "all"}) || !slices.Contains(plain, Setting{"paper-format", "iso_a4"}) ||
		!slices.Contains(plain, Setting{"use-color", "true"}) || !slices.Contains(plain, Setting{"orientation", "portrait"}) {
		t.Errorf("defaults = %v", plain)
	}
	if slices.ContainsFunc(plain, func(s Setting) bool { return s.Key == "page-ranges" || s.Key == "output-uri" }) {
		t.Errorf("all pages to a printer carried %v", plain)
	}
	for kind, want := range map[pagesKind]string{pagesCurrent: "current", pagesSelection: "selection"} {
		if got := (form{pages: kind, paper: a4()}).settings("x"); !slices.Contains(got, Setting{"print-pages", want}) {
			t.Errorf("%s pages = %v", want, got)
		}
	}
	file := form{copies: 1, paper: a4(), output: "/home/u/My docs/out.pdf"}.settings(fileQueue)
	if !slices.Contains(file, Setting{"output-uri", "file:///home/u/My%20docs/out.pdf"}) || !slices.Contains(file, Setting{"output-file-format", "pdf"}) {
		t.Errorf("Print to File = %v", file)
	}
}

// The page setup is the chosen paper's GTK and display names and size,
// GTK's default margins, and the orientation.
func TestFormPageSetup(t *testing.T) {
	got := form{paper: commonPapers[1], landscape: true}.pageSetup()
	want := PageSetup{
		Name: "na_letter", DisplayName: "Letter", Width: 215.9, Height: 279.4,
		MarginTop: 6.35, MarginBottom: 6.35, MarginLeft: 6.35, MarginRight: 6.35, Orientation: "landscape",
	}
	if got != want {
		t.Errorf("pageSetup = %+v, want %+v", got, want)
	}
}

func TestFormJobAttrs(t *testing.T) {
	g := ipp.Group{Attrs: form{copies: 3, pages: pagesRanges, ranges: "2", landscape: true, paper: commonPapers[3], color: "monochrome", side: "two-sided-long-edge", quality: qualityHigh}.jobAttrs()}
	if c, _ := g.Int("copies"); c != 3 {
		t.Errorf("copies = %d", c)
	}
	if o, _ := g.Int("orientation-requested"); o != 4 {
		t.Errorf("orientation = %d, want landscape 4", o)
	}
	for name, want := range map[string]string{"media": "iso_a3_297x420mm", "print-color-mode": "monochrome", "sides": "two-sided-long-edge"} {
		if got := g.Text(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if q, _ := g.Int("print-quality"); q != qualityHigh {
		t.Errorf("quality = %d, want high 5", q)
	}
	if a, ok := g.Get("page-ranges"); !ok || a.Values[0].Low != 2 || a.Values[0].Int != 2 {
		t.Errorf("page-ranges = %+v", a)
	}
	// The current page or selection is the whole document the app
	// sends: no ranges.
	current := ipp.Group{Attrs: form{copies: 1, pages: pagesCurrent, ranges: "4", paper: a4(), color: "color", side: "one-sided", quality: qualityNormal}.jobAttrs()}
	if _, ok := current.Get("page-ranges"); ok {
		t.Errorf("current page = %+v", current)
	}
}

// The form opens on the queue's defaults, overridden by the app's
// settings and page setup where the queue offers what they name.
func TestInitialForm(t *testing.T) {
	c := capabilitiesOf(nil)
	f := initialForm(PrepareRequest{
		Settings: []Setting{
			{"n-copies", "3"},
			{"use-color", "false"},
			{"duplex", "horizontal"},
			{"quality", "high"},
			{"print-pages", "ranges"},
			{"page-ranges", "0-1,4"},
			{"paper-format", "iso_a3"},
		},
		PageSetup: PageSetup{Name: "na_letter", Orientation: "landscape"},
	}, c)
	if f.copies != 3 || f.color != "monochrome" || f.side != "two-sided-long-edge" || f.quality != qualityHigh {
		t.Errorf("form %+v", f)
	}
	if f.pages != pagesRanges || f.ranges != "1-2,5" || !f.landscape || f.paper.gtk != "na_letter" {
		t.Errorf("form %+v: the page setup's paper wins over paper-format", f)
	}
	plain := initialForm(PrepareRequest{Settings: []Setting{{"n-copies", "5000"}, {"print-pages", "current"}, {"paper-format", "iso_b9"}}}, c)
	if plain.copies != maxCopies || plain.pages != pagesAll || plain.paper.gtk != "iso_a4" {
		t.Errorf("unoffered values = %+v: copies clamped, current page and an unknown paper ignored", plain)
	}
	if cur := initialForm(PrepareRequest{Settings: []Setting{{"print-pages", "current"}}, HasCurrentPage: true}, c); cur.pages != pagesCurrent {
		t.Errorf("an offered current page = %v", cur.pages)
	}
	mono := capabilitiesOf(&ipp.Printer{ColorModes: []string{"monochrome"}})
	if f := initialForm(PrepareRequest{Settings: []Setting{{"use-color", "true"}}}, mono); f.color != "monochrome" {
		t.Errorf("color on a monochrome queue = %s", f.color)
	}
}

func TestPrinterStatusAndDetail(t *testing.T) {
	for _, tc := range []struct {
		p    ipp.Printer
		want string
	}{
		{ipp.Printer{State: ipp.StateStopped, Accepting: true}, "Paused"},
		{ipp.Printer{State: ipp.StateIdle, Accepting: false}, "Not accepting jobs"},
		{ipp.Printer{State: ipp.StateProcessing, Accepting: true, StateMessage: "Warming"}, "Warming"},
		{ipp.Printer{State: ipp.StateIdle, Accepting: true}, "Ready"},
	} {
		if got := printerStatus(tc.p); got != tc.want {
			t.Errorf("printerStatus(%+v) = %q, want %q", tc.p, got, tc.want)
		}
	}
	if got := printerDetail("Hall", "Ready"); got != "Hall · Ready" {
		t.Errorf("detail = %q", got)
	}
	if got := printerDetail("", "Ready"); got != "Ready" {
		t.Errorf("no location = %q", got)
	}
}

func TestSuggestedOutput(t *testing.T) {
	home, cfg := t.TempDir(), t.TempDir()
	env := xdg.Env(func(name string) string {
		return map[string]string{"HOME": home, "XDG_CONFIG_HOME": cfg}[name]
	})
	userDirs := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(cfg, "user-dirs.dirs"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := SuggestedOutput(env, home, "pdf"); err != nil || got != filepath.Join(home, "output.pdf") {
		t.Errorf("no documents dir = %s, %v", got, err)
	}
	userDirs("XDG_DOCUMENTS_DIR=\"$HOME/Docs\"\n")
	if got, err := SuggestedOutput(env, home, "ps"); err != nil || got != filepath.Join(home, "Docs", "output.ps") {
		t.Errorf("documents dir = %s, %v", got, err)
	}
	userDirs("XDG_DOCUMENTS_DIR=Docs\n")
	if got, err := SuggestedOutput(env, home, "pdf"); err == nil || got != filepath.Join(home, "output.pdf") {
		t.Errorf("an invalid documents dir = %s, %v; want the home path and an error", got, err)
	}
	if got := outputFormat([]string{"ps", "svg"}); got != "ps" {
		t.Errorf("without pdf the format = %s", got)
	}
	if got := outputFormat(nil); got != "pdf" {
		t.Errorf("no formats = %s", got)
	}
}
