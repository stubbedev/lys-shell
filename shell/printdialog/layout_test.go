package printdialog

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/ipp"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// The layout dropdowns after the first six, in the form's order, and
// the check boxes.
const (
	dropNumberUp = iota + dropQuality + 1
	dropLayout
	dropPageSet
	dropScaling
	dropSource
	dropMediaType
	dropOutputBin
)

const (
	checkReverse = iota
	checkCollate
)

// The layout choices are GtkPrintSettings for the app and IPP job
// attributes for the printer.
func TestLayoutSettingsAndJobAttrs(t *testing.T) {
	base := form{copies: 2, paper: a4(), color: "color", side: "one-sided", quality: qualityNormal, numberUp: 1, layout: "lrtb", pageSet: "all", scale: 100}
	for _, tc := range []struct {
		name     string
		edit     func(*form)
		settings []Setting
		attrs    map[string]string
		absent   []string
	}{
		{
			"four up, even pages, reversed",
			func(f *form) { f.numberUp, f.pageSet, f.reverse = 4, "even", true },
			[]Setting{{"number-up", "4"}, {"page-set", "even"}, {"reverse", "true"}},
			map[string]string{"page-set": "even", "output-order": "reverse-order", "number-up-layout": "lrtb", "multiple-document-handling": "separate-documents-uncollated-copies"},
			nil,
		},
		{
			"collated, scaled, odd",
			func(f *form) { f.collate, f.scale, f.scaling, f.pageSet = true, 75, "fit", "odd" },
			[]Setting{{"collate", "true"}, {"scale", "75"}, {"page-set", "odd"}},
			map[string]string{"print-scaling": "fit", "page-set": "odd", "multiple-document-handling": "separate-documents-collated-copies"},
			[]string{"number-up", "output-order"},
		},
		{
			"defaults",
			func(*form) {},
			[]Setting{{"number-up", "1"}, {"page-set", "all"}, {"reverse", "false"}},
			map[string]string{"media": "iso_a4_210x297mm"},
			[]string{"number-up", "page-set", "output-order", "print-scaling", "media-col", "output-bin"},
		},
		{
			"tray, media type and output tray",
			func(f *form) { f.source, f.mediaType, f.outputBin = "tray-2", "photographic", "face-down" },
			[]Setting{{"default-source", "tray-2"}, {"media-type", "photographic"}, {"output-bin", "face-down"}},
			map[string]string{"output-bin": "face-down"},
			[]string{"media"},
		},
	} {
		f := base
		tc.edit(&f)
		settings := f.settings("office")
		for _, want := range tc.settings {
			if !slices.Contains(settings, want) {
				t.Errorf("%s: settings %v lack %v", tc.name, settings, want)
			}
		}
		g := ipp.Group{Attrs: f.jobAttrs(drawnToSettings)}
		for name, want := range tc.attrs {
			if got := g.Text(name); got != want {
				t.Errorf("%s: %s = %q, want %q", tc.name, name, got, want)
			}
		}
		for _, name := range tc.absent {
			if _, ok := g.Get(name); ok {
				t.Errorf("%s: sent %s", tc.name, name)
			}
		}
		if f.numberUp == 4 {
			if n, _ := g.Int("number-up"); n != 4 {
				t.Errorf("%s: number-up = %d", tc.name, n)
			}
		}
		if f.source != "" {
			col, _ := g.Get("media-col")
			members := ipp.Group{Attrs: col.Values[0].Members}
			size, _ := members.Get("media-size")
			dims := ipp.Group{Attrs: size.Values[0].Members}
			if x, _ := dims.Int("x-dimension"); x != 21000 || members.Text("media-source") != "tray-2" || members.Text("media-type") != "photographic" {
				t.Errorf("%s: media-col = %+v", tc.name, col)
			}
		}
	}
}

// The app draws a printer's document laying out nothing, the sheet
// layout kept under printerSide for its next request; Print to File's
// app lays out all of it.
func TestTheAppLaysOutOnlyForAFile(t *testing.T) {
	f := form{copies: 3, paper: a4(), color: "color", side: "one-sided", quality: qualityNormal, numberUp: 4, layout: "tblr", pageSet: "odd", reverse: true, collate: true, scale: 80}
	app := f.appSettings("office")
	for _, want := range []Setting{
		{"n-copies", "1"},
		{"number-up", "1"},
		{"number-up-layout", "lrtb"},
		{"page-set", "all"},
		{"reverse", "false"},
		{"collate", "false"},
		{"lys-printer-n-copies", "3"},
		{"lys-printer-number-up", "4"},
		{"lys-printer-number-up-layout", "tblr"},
		{"lys-printer-page-set", "odd"},
		{"lys-printer-reverse", "true"},
		{"lys-printer-collate", "true"},
		{"scale", "80"},
		{"printer", "office"},
	} {
		if !slices.Contains(app, want) {
			t.Errorf("a printer's app settings %v lack %v", app, want)
		}
	}
	if len(app) != len(f.settings("office"))+len(sheetLayout) {
		t.Errorf("a printer's app settings %v repeat or drop keys", app)
	}
	f.output = "/tmp/out.pdf"
	file := f.appSettings(fileQueue)
	if !slices.Equal(file, f.settings(fileQueue)) {
		t.Errorf("Print to File's app settings %v, want the form's %v", file, f.settings(fileQueue))
	}
	if slices.ContainsFunc(file, func(s Setting) bool { return strings.HasPrefix(s.Key, printerSide) }) {
		t.Errorf("Print to File's app settings %v carry a printer's layout", file)
	}
	back := initialForm(PrepareRequest{Settings: app}, capabilitiesOf(&ipp.Printer{NumberUp: allNumberUp}))
	if back.copies != 3 || back.numberUp != 4 || back.layout != "tblr" || back.pageSet != "odd" || !back.reverse || !back.collate {
		t.Errorf("the settings an app keeps reopen as %+v", back)
	}
}

// Through the dialog: four pages per sheet, even pages and reverse
// reach the printer as IPP, and the app is told to lay out none of it.
func TestTheLayoutReachesTheAppAndThePrinter(t *testing.T) {
	hs := newHarness(t)
	hs.printers = []ipp.Printer{{Name: "office", State: ipp.StateIdle, Accepting: true, NumberUp: []int32{1, 2, 4, 6, 9, 16}}}
	got, c := hs.prepare(t)
	dropdowns := treetest.All[*widget.Dropdown](c.Root)
	checks := treetest.All[*widget.CheckButton](c.Root)
	hs.do(func() {
		dropdowns[dropNumberUp].SetSelected(slices.Index(dropdowns[dropNumberUp].Items(), "4"))
		dropdowns[dropPageSet].SetSelected(slices.Index(dropdowns[dropPageSet].Items(), "Even pages only"))
		checks[checkReverse].SetChecked(true)
		treetest.Button(t, c.Root, "Print").OnClick()
	})
	r := <-got
	for _, want := range []Setting{{"number-up", "1"}, {"page-set", "all"}, {"reverse", "false"}, {"lys-printer-number-up", "4"}, {"lys-printer-page-set", "even"}, {"lys-printer-reverse", "true"}} {
		if !slices.Contains(r.settings, want) {
			t.Errorf("settings %v lack %v", r.settings, want)
		}
	}
	if !hs.h.Print(testCaller, "Doc", document(t, "%PDF"), r.token) {
		t.Fatal("the job did not spool")
	}
	g := ipp.Group{Attrs: hs.jobs[0].job.Attrs}
	if n, _ := g.Int("number-up"); n != 4 || g.Text("page-set") != "even" || g.Text("output-order") != "reverse-order" {
		t.Errorf("job attrs %+v", g)
	}
}

// A control shows only where the queue offers values: no tray control
// for a queue reporting no media-source-supported, one for a queue
// reporting trays, and Print to File has pages per sheet but no trays.
func TestOptionalControlsFollowTheQueue(t *testing.T) {
	hs := newHarness(t)
	hs.printers = []ipp.Printer{
		{Name: "plain", State: ipp.StateIdle, Accepting: true},
		{Name: "trays", State: ipp.StateIdle, Accepting: true, MediaSources: []string{"tray-1", "manual"}, OutputBins: []string{"face-up", "face-down"}, OutputBinDefault: "face-down"},
	}
	got, c := hs.prepare(t)
	dropdowns := treetest.All[*widget.Dropdown](c.Root)
	visible := func(i int) bool {
		var on bool
		hs.do(func() { on = widget.IsVisible(dropdowns[i]) })
		return on
	}
	if visible(dropSource) || visible(dropOutputBin) || visible(dropNumberUp) {
		t.Error("a queue offering no trays and no pages per sheet shows them")
	}
	hs.do(func() { treetest.First[*widget.ListBox](t, c.Root).Select(1) })
	if !visible(dropSource) || !visible(dropOutputBin) || visible(dropMediaType) {
		t.Error("the trays of a queue reporting them are not offered alone")
	}
	hs.do(func() {
		if items := dropdowns[dropSource].Items(); !slices.Equal(items, []string{"Automatic", "Tray 1", "Manual"}) {
			t.Errorf("trays %v", items)
		}
		if sel := dropdowns[dropOutputBin].Selection(); sel != "Face down" {
			t.Errorf("output tray %q, want the default", sel)
		}
		treetest.First[*widget.ListBox](t, c.Root).Select(2)
	})
	if visible(dropSource) || !visible(dropNumberUp) {
		t.Error("Print to File offers trays or no pages per sheet")
	}
	hs.do(func() { treetest.Button(t, c.Root, "Cancel").OnClick() })
	<-got
}

// The last form an app confirmed for a printer is where its next
// request to it starts, across restarts; another app starts from the
// queue's defaults, and Reset goes back to them.
func TestTheLastFormIsKeptPerAppAndPrinter(t *testing.T) {
	hs := newHarness(t)
	presets := filepath.Join(t.TempDir(), "state", "print-presets")
	hs.h.presets = loadPresets(presets)
	hs.printers = []ipp.Printer{{Name: "office", State: ipp.StateIdle, Accepting: true, NumberUp: []int32{1, 2, 4}}}
	got, c := hs.prepare(t)
	dropdowns := treetest.All[*widget.Dropdown](c.Root)
	copies := treetest.All[*widget.SpinButton](c.Root)[0]
	hs.do(func() {
		copies.SetValue(3)
		dropdowns[dropNumberUp].SetSelected(2)
		dropdowns[dropColor].SetSelected(1)
		treetest.Button(t, c.Root, "Print").OnClick()
	})
	<-got
	read := func(caller string) (float64, string, string) {
		got, c := hs.prepareWith(t, callerFor(caller), PrepareRequest{Title: "Doc"})
		dropdowns := treetest.All[*widget.Dropdown](c.Root)
		var n float64
		var up, color string
		hs.do(func() {
			n = treetest.All[*widget.SpinButton](c.Root)[0].Value()
			up, color = dropdowns[dropNumberUp].Selection(), dropdowns[dropColor].Selection()
			treetest.Button(t, c.Root, "Cancel").OnClick()
		})
		<-got
		return n, up, color
	}
	if n, up, color := read(testCaller.AppID); n != 3 || up != "4" || color != "Grayscale" {
		t.Errorf("the same app opens on %v copies, %s up, %s; want its last 3, 4, Grayscale", n, up, color)
	}
	if n, up, color := read("org.other"); n != 1 || up != "1" || color != "Color" {
		t.Errorf("another app opens on %v copies, %s up, %s; want the defaults", n, up, color)
	}
	hs.h.presets.file.Wait()
	if s := loadPresets(presets).settings(testCaller.AppID, "office"); !slices.Contains(s, Setting{"n-copies", "3"}) || slices.ContainsFunc(s, func(x Setting) bool { return x.Key == "printer" }) {
		t.Errorf("after a restart the preset reads %v", s)
	}
	got, c = hs.prepare(t)
	hs.do(func() {
		treetest.Button(t, c.Root, "Reset to printer defaults").OnClick()
		if n := treetest.All[*widget.SpinButton](c.Root)[0].Value(); n != 1 || treetest.All[*widget.Dropdown](c.Root)[dropNumberUp].Selection() != "1" {
			t.Errorf("after Reset %v copies, %s up", n, treetest.All[*widget.Dropdown](c.Root)[dropNumberUp].Selection())
		}
		treetest.Button(t, c.Root, "Cancel").OnClick()
	})
	<-got
}

// A Print to File whose document fails midway leaves the file that
// was there as it was, and no temporary beside it.
func TestPrintToFileIsWholeOrNothing(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(target, []byte("the old report"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := io.MultiReader(strings.NewReader("%PDF-1.7 half a docu"), iotest.ErrReader(errors.New("fd gone")))
	if toFile(target, broken) {
		t.Fatal("a failed copy reported written")
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "the old report" {
		t.Errorf("the target reads %q, %v", b, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the folder holds %d entries, want the report alone", len(entries))
	}
	if !toFile(target, strings.NewReader("the new report")) {
		t.Fatal("a whole copy failed")
	}
	if b, _ := os.ReadFile(target); string(b) != "the new report" {
		t.Errorf("the target reads %q after a whole copy", b)
	}
}

// callerFor is a request from app.
func callerFor(app string) portaldialogs.Caller {
	return portaldialogs.Caller{Handle: "/r/" + app, AppID: app}
}
