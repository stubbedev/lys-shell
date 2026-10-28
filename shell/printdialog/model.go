package printdialog

import (
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/xdg"

	"github.com/stubbedev/lys-shell/internal/fileuri"
	"github.com/stubbedev/lys-shell/internal/ipp"
)

// paper is one size the form offers: its label, GTK's paper name (the
// settings the portal returns), its size in millimetres, and the PWG
// media name IPP takes.
type paper struct {
	label, gtk    string
	width, height float64
	media         string
}

// commonPapers are the sizes offered by a queue that names none (and
// Print to File), A4 first (shell/print PAPER_SIZES).
var commonPapers = []paper{
	{"A4", "iso_a4", 210, 297, "iso_a4_210x297mm"},
	{"Letter", "na_letter", 215.9, 279.4, "na_letter_8.5x11in"},
	{"Legal", "na_legal", 215.9, 355.6, "na_legal_8.5x14in"},
	{"A3", "iso_a3", 297, 420, "iso_a3_297x420mm"},
	{"A5", "iso_a5", 148, 210, "iso_a5_148x210mm"},
	{"Executive", "na_executive", 184.15, 266.7, "na_executive_7.25x10.5in"},
}

// mmPerInch converts PWG inch sizes.
const mmPerInch = 25.4

// parseMedia reads a PWG self-describing media name ("iso_a4_210x297mm",
// "na_letter_8.5x11in") as a paper: GTK's name is the class and size
// name, the label the common one or the size name capitalized; false
// for a name that is not one.
func parseMedia(media string) (paper, bool) {
	cut := strings.LastIndexByte(media, '_')
	if cut <= 0 {
		return paper{}, false
	}
	name, dims := media[:cut], media[cut+1:]
	scale, unit := 1.0, ""
	switch {
	case strings.HasSuffix(dims, "mm"):
		unit = "mm"
	case strings.HasSuffix(dims, "in"):
		unit, scale = "in", mmPerInch
	default:
		return paper{}, false
	}
	w, h, ok := strings.Cut(strings.TrimSuffix(dims, unit), "x")
	width, werr := strconv.ParseFloat(w, 64)
	height, herr := strconv.ParseFloat(h, 64)
	if !ok || werr != nil || herr != nil || width <= 0 || height <= 0 {
		return paper{}, false
	}
	p := paper{gtk: name, width: hundredths(width * scale), height: hundredths(height * scale), media: media}
	if i := slices.IndexFunc(commonPapers, func(c paper) bool { return c.gtk == name }); i >= 0 {
		p.label = commonPapers[i].label
	} else {
		_, size, _ := strings.Cut(name, "_")
		if size == "" {
			size = name
		}
		p.label = strings.ToUpper(size[:1]) + size[1:]
	}
	return p, true
}

// hundredths rounds a size to a hundredth of a millimetre, past the
// noise of the inch conversion.
func hundredths(v float64) float64 { return math.Round(v*100) / 100 }

// The sides, color modes and qualities a queue that names none offers,
// in their dropdown order, and their labels.
var (
	allSides     = []string{"one-sided", "two-sided-long-edge", "two-sided-short-edge"}
	allColors    = []string{"color", "monochrome"}
	allQualities = []int32{qualityNormal, qualityDraft, qualityHigh}

	sideLabels    = map[string]string{"one-sided": "One-sided", "two-sided-long-edge": "Two-sided (long edge)", "two-sided-short-edge": "Two-sided (short edge)"}
	colorLabels   = map[string]string{"color": "Color", "monochrome": "Grayscale"}
	qualityLabels = map[int32]string{qualityDraft: "Draft", qualityNormal: "Normal", qualityHigh: "High"}
	orientations  = []string{"Portrait", "Landscape"}
)

// The print-quality enums.
const (
	qualityDraft  int32 = 3
	qualityNormal int32 = 4
	qualityHigh   int32 = 5
)

// capabilities are what a queue offers, its defaults first-chosen:
// what it reports, the common choices where it reports nothing, and
// only the choices the form knows.
type capabilities struct {
	papers    []paper
	sides     []string
	colors    []string
	qualities []int32
	// The defaults: a paper's GTK name, a sides keyword, a color mode,
	// a quality.
	paper, side, color string
	quality            int32
}

// capabilitiesOf is a printer's capabilities; nil is Print to File's.
func capabilitiesOf(p *ipp.Printer) capabilities {
	c := capabilities{papers: commonPapers, sides: allSides, colors: allColors, qualities: allQualities}
	if p == nil {
		return c.withDefaults("", "", "", 0)
	}
	var papers []paper
	for _, m := range p.Media {
		if pp, ok := parseMedia(m); ok && !slices.ContainsFunc(papers, func(o paper) bool { return o.gtk == pp.gtk }) {
			papers = append(papers, pp)
		}
	}
	if len(papers) > 0 {
		c.papers = papers
	}
	if sides := known(p.Sides, allSides); len(sides) > 0 {
		c.sides = sides
	}
	if colors := known(p.ColorModes, allColors); len(colors) > 0 {
		c.colors = colors
	}
	if qualities := known(p.Qualities, allQualities); len(qualities) > 0 {
		c.qualities = qualities
	}
	defaultPaper := ""
	if pp, ok := parseMedia(p.MediaDefault); ok {
		defaultPaper = pp.gtk
	}
	return c.withDefaults(defaultPaper, p.SidesDefault, p.ColorModeDefault, p.QualityDefault)
}

// known keeps the offered values the form knows, in the form's order.
func known[T comparable](offered, all []T) []T {
	var out []T
	for _, v := range all {
		if slices.Contains(offered, v) {
			out = append(out, v)
		}
	}
	return out
}

// withDefaults sets the defaults the queue names, where it offers them,
// else its first choices (normal quality when offered).
func (c capabilities) withDefaults(paperName, side, color string, quality int32) capabilities {
	c.paper = c.papers[0].gtk
	if slices.ContainsFunc(c.papers, func(p paper) bool { return p.gtk == paperName }) {
		c.paper = paperName
	}
	c.side = pick(c.sides, side, c.sides[0])
	c.color = pick(c.colors, color, c.colors[0])
	c.quality = pick(c.qualities, quality, pick(c.qualities, qualityNormal, c.qualities[0]))
	return c
}

// pick is want when offered, else fallback.
func pick[T comparable](offered []T, want, fallback T) T {
	if slices.Contains(offered, want) {
		return want
	}
	return fallback
}

// pagesKind is which pages print.
type pagesKind uint8

const (
	pagesAll pagesKind = iota
	pagesCurrent
	pagesSelection
	pagesRanges
)

// form is what the user chose.
type form struct {
	copies    int
	pages     pagesKind
	ranges    string
	landscape bool
	paper     paper
	color     string
	side      string
	quality   int32
	// output is the file Print to File writes, "" for a printer.
	output string
}

// The copies bounds (the Rust spin button's adjustment).
const (
	minCopies = 1
	maxCopies = 999
)

// pageRanges parses "1-5, 8" into 1-based ranges, each low to high;
// false for an empty input or one that does not parse
// (shell/print's parse_page_ranges).
func pageRanges(input string) ([][2]int32, bool) {
	var out [][2]int32
	for tok := range strings.SplitSeq(input, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(tok, "-")
		a, err := strconv.ParseUint(strings.TrimSpace(lo), 10, 31)
		if err != nil {
			return nil, false
		}
		b := a
		if isRange {
			if b, err = strconv.ParseUint(strings.TrimSpace(hi), 10, 31); err != nil {
				return nil, false
			}
		}
		first, last := int32(max(a, 1)), int32(max(b, 1))
		out = append(out, [2]int32{min(first, last), max(first, last)})
	}
	return out, len(out) > 0
}

// gtkRanges is GTK's zero-based page-ranges value ("0-4,7-7").
func gtkRanges(rs [][2]int32) string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = strconv.Itoa(int(r[0]-1)) + "-" + strconv.Itoa(int(r[1]-1))
	}
	return strings.Join(parts, ",")
}

// rangesFromGTK reads GTK's zero-based page-ranges ("0-4,7-7") back
// as the form's 1-based text ("1-5,8"); "" for a value that does not
// parse.
func rangesFromGTK(v string) string {
	var parts []string
	for tok := range strings.SplitSeq(v, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(tok, "-")
		a, err := strconv.ParseUint(strings.TrimSpace(lo), 10, 31)
		if err != nil {
			return ""
		}
		b := a
		if isRange {
			if b, err = strconv.ParseUint(strings.TrimSpace(hi), 10, 31); err != nil {
				return ""
			}
		}
		if a == b {
			parts = append(parts, strconv.FormatUint(a+1, 10))
		} else {
			parts = append(parts, strconv.FormatUint(a+1, 10)+"-"+strconv.FormatUint(b+1, 10))
		}
	}
	return strings.Join(parts, ",")
}

// pageList is the ranges the form prints, false for every page of the
// document (all of it, or the current page or selection the app
// renders as the whole document).
func (f form) pageList() ([][2]int32, bool) {
	if f.pages != pagesRanges {
		return nil, false
	}
	return pageRanges(f.ranges)
}

func mm(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// GTK's duplex and quality values for the IPP ones.
var (
	gtkDuplex  = map[string]string{"one-sided": "simplex", "two-sided-long-edge": "horizontal", "two-sided-short-edge": "vertical"}
	gtkQuality = map[int32]string{qualityDraft: "draft", qualityNormal: "normal", qualityHigh: "high"}
)

// settings are the GtkPrintSettings keys the portal returns, the
// printer GTK's dialog records included, and Print to File's output.
func (f form) settings(printer string) []Setting {
	orientation := "portrait"
	if f.landscape {
		orientation = "landscape"
	}
	s := []Setting{
		{"printer", printer},
		{"n-copies", strconv.Itoa(f.copies)},
		{"orientation", orientation},
		{"paper-format", f.paper.gtk},
		{"paper-width", mm(f.paper.width)},
		{"paper-height", mm(f.paper.height)},
		{"use-color", strconv.FormatBool(f.color != "monochrome")},
		{"duplex", gtkDuplex[f.side]},
		{"quality", gtkQuality[f.quality]},
	}
	if f.output != "" {
		format := strings.TrimPrefix(filepath.Ext(f.output), ".")
		s = append(s, Setting{"output-uri", fileuri.FromPath(f.output)}, Setting{"output-file-format", format})
	}
	switch f.pages {
	case pagesCurrent:
		return append(s, Setting{"print-pages", "current"})
	case pagesSelection:
		return append(s, Setting{"print-pages", "selection"})
	}
	if rs, ok := f.pageList(); ok {
		return append(s, Setting{"print-pages", "ranges"}, Setting{"page-ranges", gtkRanges(rs)})
	}
	return append(s, Setting{"print-pages", "all"})
}

// defaultMargin is GTK's default margin of a sheet of paper, a quarter
// inch.
const defaultMargin = 6.35

// pageSetup is the GtkPageSetup the portal returns for the form.
func (f form) pageSetup() PageSetup {
	orientation := "portrait"
	if f.landscape {
		orientation = "landscape"
	}
	return PageSetup{
		Name: f.paper.gtk, DisplayName: f.paper.label, Width: f.paper.width, Height: f.paper.height,
		MarginTop: defaultMargin, MarginBottom: defaultMargin, MarginLeft: defaultMargin, MarginRight: defaultMargin,
		Orientation: orientation,
	}
}

// jobAttrs are the form as IPP job template attributes (what GTK's
// CUPS backend maps the settings to).
func (f form) jobAttrs() []ipp.Attr {
	orientation := int32(3)
	if f.landscape {
		orientation = 4
	}
	attrs := []ipp.Attr{
		ipp.Int("copies", intconv.Clamp[int32](f.copies)),
		ipp.Enum("orientation-requested", orientation),
		ipp.String(ipp.TagKeyword, "media", f.paper.media),
		ipp.String(ipp.TagKeyword, "print-color-mode", f.color),
		ipp.String(ipp.TagKeyword, "sides", f.side),
		ipp.Enum("print-quality", f.quality),
	}
	if rs, ok := f.pageList(); ok {
		attrs = append(attrs, ipp.Ranges("page-ranges", rs))
	}
	return attrs
}

// initialForm is the form a request opens on: the queue's defaults,
// overridden by the app's settings and page setup where the queue
// offers what they name.
func initialForm(r PrepareRequest, c capabilities) form {
	in := map[string]string{}
	for _, s := range r.Settings {
		in[s.Key] = s.Value
	}
	f := form{copies: minCopies, side: c.side, color: c.color, quality: c.quality}
	f.paper = c.papers[slices.IndexFunc(c.papers, func(p paper) bool { return p.gtk == c.paper })]
	if n, err := strconv.Atoi(in["n-copies"]); err == nil {
		f.copies = min(max(n, minCopies), maxCopies)
	}
	f.landscape = in["orientation"] == "landscape" || r.PageSetup.Orientation == "landscape"
	for _, name := range []string{r.PageSetup.Name, in["paper-format"]} {
		if i := slices.IndexFunc(c.papers, func(p paper) bool { return p.gtk == name }); name != "" && i >= 0 {
			f.paper = c.papers[i]
			break
		}
	}
	if v, ok := in["use-color"]; ok {
		f.color = pick(c.colors, map[bool]string{true: "color", false: "monochrome"}[v == "true"], f.color)
	}
	for side, gtk := range gtkDuplex {
		if in["duplex"] == gtk {
			f.side = pick(c.sides, side, f.side)
		}
	}
	for q, gtk := range gtkQuality {
		if in["quality"] == gtk {
			f.quality = pick(c.qualities, q, f.quality)
		}
	}
	switch in["print-pages"] {
	case "current":
		if r.HasCurrentPage {
			f.pages = pagesCurrent
		}
	case "selection":
		if r.HasSelectedPages {
			f.pages = pagesSelection
		}
	case "ranges":
		if f.ranges = rangesFromGTK(in["page-ranges"]); f.ranges != "" {
			f.pages = pagesRanges
		}
	}
	return f
}

// printerStatus is the row's status: GTK's paused, not accepting, the
// state message, else ready.
func printerStatus(p ipp.Printer) string {
	switch {
	case p.State == ipp.StateStopped:
		return "Paused"
	case !p.Accepting:
		return "Not accepting jobs"
	case p.StateMessage != "":
		return p.StateMessage
	}
	return "Ready"
}

// printerDetail is the row's muted line: location · status.
func printerDetail(location, status string) string {
	var parts []string
	for _, s := range []string{location, status} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " · ")
}

// fileQueue is GTK's always-present file backend printer.
const fileQueue = "Print to File"

// SuggestedOutput is where Print to File proposes to write, GTK's
// default: output.<format> in env's documents directory, else the home
// directory. The error reports a user-dirs.dirs that cannot say where
// the documents directory is; the path is then the one in home.
func SuggestedOutput(env xdg.Env, home, format string) (string, error) {
	name := "output." + format
	dir, ok, err := env.UserDir("DOCUMENTS")
	if err != nil || !ok {
		return filepath.Join(home, name), err
	}
	return filepath.Join(dir, name), nil
}
