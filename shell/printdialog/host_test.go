package printdialog

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/ipp"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

type fakeWindow struct{ closed bool }

func (w *fakeWindow) Close() { w.closed = true }

// spooled is one job the fake scheduler took.
type spooled struct {
	job ipp.Job
	doc string
}

type harness struct {
	mu       sync.Mutex
	h        *Dialog
	opened   chan app.LayerConfig
	printers []ipp.Printer
	listErr  error
	spoolErr error
	jobs     []spooled
	home     string
	// picked is the file the file chooser answers, "" for a cancel.
	picked string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	face := treetest.Font(t)
	cfg := config.Defaults()
	cfg.Animations.Enabled = false
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	hs := &harness{opened: make(chan app.LayerConfig, 4), home: t.TempDir()}
	hs.printers = []ipp.Printer{{Name: "office", Location: "Hall", State: ipp.StateIdle, Accepting: true}}
	hs.h = New(Deps{
		Config: func() *config.Config { return cfg },
		Open: func(c app.LayerConfig) (Window, error) {
			hs.opened <- c
			return &fakeWindow{}, nil
		},
		Invoke: hs.do,
		Font:   face,
		Printers: func(context.Context) ([]ipp.Printer, error) {
			hs.mu.Lock()
			defer hs.mu.Unlock()
			return hs.printers, hs.listErr
		},
		Spool: func(_ context.Context, j ipp.Job) error {
			b, _ := io.ReadAll(j.Document)
			hs.mu.Lock()
			defer hs.mu.Unlock()
			hs.jobs = append(hs.jobs, spooled{j, string(b)})
			return hs.spoolErr
		},
		PickOutput: func(_ string, chosen func(string)) {
			hs.mu.Lock()
			picked := hs.picked
			hs.mu.Unlock()
			if picked != "" {
				chosen(picked)
			}
		},
		Home: hs.home,
		User: "u",
	})
	return hs
}

func (hs *harness) do(fn func()) {
	treetest.Loop.Lock()
	defer treetest.Loop.Unlock()
	fn()
}

type prepareResult struct {
	ok       bool
	settings []Setting
	setup    PageSetup
	token    uint32
}

func (hs *harness) prepare(t *testing.T) (chan prepareResult, app.LayerConfig) {
	t.Helper()
	return hs.prepareWith(t, testCaller, PrepareRequest{Title: "Doc"})
}

func (hs *harness) prepareWith(t *testing.T, c portaldialogs.Caller, r PrepareRequest) (chan prepareResult, app.LayerConfig) {
	t.Helper()
	got := make(chan prepareResult, 1)
	go func() {
		ok, s, setup, tok := hs.h.Prepare(c, r)
		got <- prepareResult{ok, s, setup, tok}
	}()
	return got, hs.open(t)
}

// open waits for the next dialog.
func (hs *harness) open(t *testing.T) app.LayerConfig {
	t.Helper()
	select {
	case c := <-hs.opened:
		return c
	case <-time.After(treetest.Liveness):
		t.Fatal("no dialog opened")
		return app.LayerConfig{}
	}
}

// The dropdowns of the form, in its order.
const (
	dropPages = iota
	dropOrientation
	dropPaper
	dropColor
	dropSides
	dropQuality
)

func document(t *testing.T, body string) *os.File {
	t.Helper()
	p := filepath.Join(t.TempDir(), "doc.pdf")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestPrepareListsQueuesAndAnswersTheForm(t *testing.T) {
	hs := newHarness(t)
	got, c := hs.prepare(t)
	if c.Namespace != "lys-shell-print" || c.Layer != app.LayerOverlay {
		t.Errorf("surface = %+v", c)
	}
	labels := treetest.Labels(c.Root)
	for _, want := range []string{"office", "Hall · Ready", fileQueue, "Copies", "Paper size"} {
		if !slices.Contains(labels, want) {
			t.Errorf("no %q in %q", want, labels)
		}
	}
	dropdowns := treetest.All[*widget.Dropdown](c.Root)
	pages := treetest.All[*widget.Entry](c.Root)[0]
	copies := treetest.All[*widget.SpinButton](c.Root)[0]
	hs.do(func() {
		copies.SetValue(2)
		pages.SetText("2-3")
		dropdowns[dropOrientation].SetSelected(1) // landscape
		dropdowns[dropColor].SetSelected(1)       // grayscale
		treetest.Button(t, c.Root, "Print").OnClick()
	})
	r := <-got
	if !r.ok || r.token == 0 {
		t.Fatalf("prepare = %+v", r)
	}
	for _, want := range []Setting{{"printer", "office"}, {"n-copies", "2"}, {"orientation", "landscape"}, {"use-color", "false"}, {"print-pages", "ranges"}, {"page-ranges", "1-2"}} {
		if !slices.Contains(r.settings, want) {
			t.Errorf("settings %v lack %v", r.settings, want)
		}
	}
	if r.setup.Name != "iso_a4" || r.setup.Width != 210 || r.setup.Orientation != "landscape" {
		t.Errorf("page setup = %+v, want the chosen A4 in landscape", r.setup)
	}

	if !hs.h.Print(testCaller, "Doc", document(t, "%PDF body"), r.token) {
		t.Fatal("the prepared job did not spool")
	}
	hs.mu.Lock()
	j := hs.jobs[0]
	hs.mu.Unlock()
	g := ipp.Group{Attrs: j.job.Attrs}
	if j.job.Printer != "office" || j.job.Title != "Doc" || j.job.User != "u" || j.doc != "%PDF body" {
		t.Errorf("spooled %+v", j)
	}
	if c, _ := g.Int("copies"); c != 2 || g.Text("print-color-mode") != "monochrome" {
		t.Errorf("job attrs = %+v", g)
	}
	// A token spools once.
	if hs.h.Print(testCaller, "Doc", document(t, "again"), r.token) {
		t.Error("a used token spooled again")
	}
}

// The copies spin button keeps the count within 1..999.
func TestCopiesStayInBounds(t *testing.T) {
	hs := newHarness(t)
	got, c := hs.prepare(t)
	copies := treetest.All[*widget.SpinButton](c.Root)[0]
	hs.do(func() {
		copies.SetValue(0)
		if copies.Value() != 1 {
			t.Errorf("zero copies = %v, want 1", copies.Value())
		}
		copies.SetValue(5000)
		treetest.Button(t, c.Root, "Print").OnClick()
	})
	r := <-got
	if !slices.Contains(r.settings, Setting{"n-copies", "999"}) {
		t.Errorf("past the top = %v, want 999", r.settings)
	}
}

// The sheet is the print view!: the .print-form grid with a label
// column, and the printer list in a scroll at least 64px tall that
// grows with its rows to 240.
func TestSheetIsTheRustLayout(t *testing.T) {
	hs := newHarness(t)
	_, c := hs.prepare(t)
	grids := treetest.All[*widget.Grid](c.Root)
	if len(grids) != 1 || !slices.Contains(grids[0].Classes(), "print-form") {
		t.Fatalf("grids %v, want the one .print-form", grids)
	}
	scrolls := treetest.All[*widget.Scroll](c.Root)
	if len(scrolls) == 0 || scrolls[0].SizeRequest().H != listMinHeight || !scrolls[0].PropagateNaturalHeight {
		t.Errorf("printer scroll %+v, want a propagating scroll requesting %d", scrolls, listMinHeight)
	}
	// The rows span the sheet, as the GTK viewport gives its child the
	// whole width.
	var listW, scrollW int
	hs.do(func() {
		widget.SizeWithin(c.Root, widget.Size{W: 1280, H: 800})
		c.Root.Arrange(render.Rect{W: 1280, H: 800})
		listW = treetest.All[*widget.ListBox](c.Root)[0].Bounds().W
		scrollW = scrolls[0].Bounds().W
	})
	if listW < scrollW-20 {
		t.Errorf("printer list %dpx in a %dpx scroll, want it filling the width", listW, scrollW)
	}
	if len(treetest.All[*widget.ListBox](c.Root)) != 1 {
		t.Error("no printer ListBox")
	}
	// An unanchored, unsized layer: the compositor centers the
	// content-sized sheet, as the GTK window is.
	if c.Anchor != 0 || c.Width != 0 || c.Height != 0 {
		t.Errorf("layer anchor %v size %dx%d, want unanchored and content-sized", c.Anchor, c.Width, c.Height)
	}
	if root, ok := c.Root.(*widget.Box); !ok || root.Element() != "window" || !widget.HasClass(root, "print-window") {
		t.Errorf("root %T, want window.print-window", c.Root)
	}
}

func TestCancelEscapeAndQueueing(t *testing.T) {
	hs := newHarness(t)
	got, c := hs.prepare(t)
	hs.do(func() { treetest.Button(t, c.Root, "Cancel").OnClick() })
	if r := <-got; r.ok || r.token != 0 {
		t.Errorf("cancel = %+v", r)
	}
	got, c = hs.prepare(t)
	hs.do(func() { c.OnKey(nil, escapeKeycode, 0) })
	if r := <-got; r.ok {
		t.Errorf("escape = %+v", r)
	}
	// A second prepare waits for the first instead of cancelling it.
	first, c := hs.prepare(t)
	second := make(chan prepareResult, 1)
	go func() {
		ok, s, setup, tok := hs.h.Prepare(portaldialogs.Caller{Handle: "/r/second"}, PrepareRequest{Title: "Doc 2"})
		second <- prepareResult{ok, s, setup, tok}
	}()
	select {
	case r := <-first:
		t.Fatalf("a second prepare answered the first %+v", r)
	case <-hs.opened:
		t.Fatal("the second dialog mapped over the first")
	case <-time.After(50 * time.Millisecond):
	}
	hs.do(func() { treetest.Button(t, c.Root, "Print").OnClick() })
	if r := <-first; !r.ok {
		t.Error("the first prepare lost its answer")
	}
	var next app.LayerConfig
	select {
	case next = <-hs.opened:
	case <-time.After(treetest.Liveness):
		t.Fatal("the waiting prepare never showed")
	}
	hs.do(func() { treetest.Button(t, next.Root, "Print").OnClick() })
	if r := <-second; !r.ok {
		t.Error("the second prepare did not answer")
	}
}

// Print to File writes the file the user picked; one that exists is
// replaced only after a second confirm. Without CUPS it is the only
// queue.
func TestPrintToFileAndAMissingScheduler(t *testing.T) {
	hs := newHarness(t)
	hs.listErr = errors.New("connect: no such file")
	hs.printers = nil
	if q := hs.h.queues(); len(q) != 1 || q[0].name != fileQueue {
		t.Errorf("without CUPS the queues are %+v, want only %s", q, fileQueue)
	}
	out := filepath.Join(t.TempDir(), "report.pdf")
	hs.picked = out
	got, c := hs.prepare(t)
	if !slices.Contains(treetest.Labels(c.Root), filepath.Join(hs.home, "output.pdf")) {
		t.Errorf("labels %q lack the suggested output", treetest.Labels(c.Root))
	}
	hs.do(func() {
		treetest.Button(t, c.Root, "Choose…").OnClick()
		treetest.Button(t, c.Root, "Print").OnClick()
	})
	r := <-got
	if !slices.Contains(r.settings, Setting{"printer", fileQueue}) || !slices.Contains(r.settings, Setting{"output-uri", "file://" + out}) {
		t.Fatalf("settings = %v", r.settings)
	}
	if !hs.h.Print(testCaller, "Doc", document(t, "%PDF to file"), r.token) {
		t.Fatal("print to file failed")
	}
	if b, err := os.ReadFile(out); err != nil || string(b) != "%PDF to file" {
		t.Errorf("output = %q %v", b, err)
	}
	if len(hs.jobs) != 0 {
		t.Error("print to file reached the scheduler")
	}

	// The file exists now: the first Print asks, the second replaces.
	got, c = hs.prepare(t)
	hs.do(func() {
		treetest.Button(t, c.Root, "Choose…").OnClick()
		treetest.Button(t, c.Root, "Print").OnClick()
	})
	select {
	case r := <-got:
		t.Fatalf("an existing file was replaced without asking: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}
	if !slices.Contains(treetest.Labels(c.Root), "report.pdf exists. Print again to replace it.") {
		t.Errorf("labels %q lack the replace note", treetest.Labels(c.Root))
	}
	hs.do(func() { treetest.Button(t, c.Root, "Print").OnClick() })
	if r := <-got; !r.ok {
		t.Error("the second Print did not confirm")
	}
}

func TestPrintFailures(t *testing.T) {
	hs := newHarness(t)
	doc := document(t, "x")
	if hs.h.Print(testCaller, "Doc", doc, 77) {
		t.Error("an unknown token spooled")
	}
	if _, err := doc.Read(make([]byte, 1)); err == nil {
		t.Error("an unspooled document was left open")
	}
	got, c := hs.prepare(t)
	hs.do(func() { treetest.Button(t, c.Root, "Print").OnClick() })
	r := <-got
	hs.spoolErr = errors.New("printer on fire")
	if hs.h.Print(testCaller, "Doc", document(t, "x"), r.token) {
		t.Error("a refused job reported sent")
	}
	hs.picked = filepath.Join(t.TempDir(), "missing-dir", "output.pdf")
	got, c = hs.prepare(t)
	hs.do(func() {
		treetest.First[*widget.ListBox](t, c.Root).Select(1) // Print to File
		treetest.Button(t, c.Root, "Choose…").OnClick()
		treetest.Button(t, c.Root, "Print").OnClick()
	})
	r = <-got
	if hs.h.Print(testCaller, "Doc", document(t, "x"), r.token) {
		t.Error("an unwritable output reported sent")
	}
}

// A token prints once, for the app that prepared it, until it expires;
// tokens are not sequential.
func TestTokensBelongToTheirApp(t *testing.T) {
	hs := newHarness(t)
	confirm := func() uint32 {
		got, c := hs.prepare(t)
		hs.do(func() { treetest.Button(t, c.Root, "Print").OnClick() })
		return (<-got).token
	}
	first, second := confirm(), confirm()
	if first == 0 || second == 0 || second == first+1 {
		t.Errorf("tokens %d then %d, want random non-zero ones", first, second)
	}
	other := portaldialogs.Caller{Handle: "/r/other", AppID: "org.other"}
	if hs.h.Print(other, "Doc", document(t, "x"), first) {
		t.Error("another app printed with the token")
	}
	if !hs.h.Print(testCaller, "Doc", document(t, "x"), first) {
		t.Error("the preparing app could not print")
	}
	hs.h.now = func() time.Time { return time.Now().Add(tokenLifetime + time.Minute) }
	if hs.h.Print(testCaller, "Doc", document(t, "x"), second) {
		t.Error("an expired token printed")
	}
}

// Print without a token shows the dialog first.
func TestPrintWithoutATokenAsks(t *testing.T) {
	hs := newHarness(t)
	sent := make(chan bool, 1)
	go func() { sent <- hs.h.Print(testCaller, "Doc", document(t, "%PDF"), 0) }()
	c := hs.open(t)
	hs.do(func() { treetest.Button(t, c.Root, "Print").OnClick() })
	if !<-sent {
		t.Fatal("Print without a token did not spool after the dialog")
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if len(hs.jobs) != 1 || hs.jobs[0].job.Printer != "office" {
		t.Errorf("spooled %+v", hs.jobs)
	}
}

// The form starts from the app's settings, page setup and options:
// the printer, copies, paper, pages, the confirm label, and the pages
// choices it can print.
func TestPrepareStartsFromTheApp(t *testing.T) {
	hs := newHarness(t)
	hs.printers = append(hs.printers, ipp.Printer{Name: "lab", State: ipp.StateIdle, Accepting: true})
	got, c := hs.prepareWith(t, testCaller, PrepareRequest{
		Title:          "Doc",
		Settings:       []Setting{{"printer", "lab"}, {"n-copies", "4"}, {"print-pages", "ranges"}, {"page-ranges", "1-2"}},
		PageSetup:      PageSetup{Name: "na_letter"},
		AcceptLabel:    "Export",
		HasCurrentPage: true,
	})
	if sel := treetest.First[*widget.ListBox](t, c.Root).Selected(); sel != 1 {
		t.Errorf("printer row %d selected, want lab", sel)
	}
	if v := treetest.All[*widget.SpinButton](c.Root)[0].Value(); v != 4 {
		t.Errorf("copies = %v", v)
	}
	dropdowns := treetest.All[*widget.Dropdown](c.Root)
	if items := dropdowns[dropPages].Items(); !slices.Equal(items, []string{"All pages", "Current page", "Range"}) || dropdowns[dropPages].Selection() != "Range" {
		t.Errorf("pages %v on %q", items, dropdowns[dropPages].Selection())
	}
	if p := dropdowns[dropPaper].Selection(); p != "Letter" {
		t.Errorf("paper = %q", p)
	}
	if e := treetest.All[*widget.Entry](c.Root)[0].Text(); e != "2-3" {
		t.Errorf("range = %q", e)
	}
	hs.do(func() { treetest.Button(t, c.Root, "Export").OnClick() })
	if r := <-got; !slices.Contains(r.settings, Setting{"printer", "lab"}) || !slices.Contains(r.settings, Setting{"n-copies", "4"}) {
		t.Errorf("settings = %v", r.settings)
	}
}

// The paper, sides, color and quality offered are the selected
// printer's; switching printers keeps a choice the next one offers.
func TestFormFollowsThePrinter(t *testing.T) {
	hs := newHarness(t)
	hs.printers = []ipp.Printer{
		{
			Name: "mono", State: ipp.StateIdle, Accepting: true, Media: []string{"iso_a4_210x297mm", "iso_a5_148x210mm"},
			Sides: []string{"one-sided"}, ColorModes: []string{"monochrome"}, Qualities: []int32{qualityNormal},
		},
		{Name: "photo", State: ipp.StateIdle, Accepting: true, Media: []string{"iso_a5_148x210mm", "na_index-4x6_4x6in"}, MediaDefault: "na_index-4x6_4x6in"},
	}
	got, c := hs.prepare(t)
	dropdowns := treetest.All[*widget.Dropdown](c.Root)
	if items := dropdowns[dropPaper].Items(); !slices.Equal(items, []string{"A4", "A5"}) {
		t.Errorf("mono papers %v", items)
	}
	if items := dropdowns[dropColor].Items(); !slices.Equal(items, []string{"Grayscale"}) {
		t.Errorf("mono colors %v", items)
	}
	if items := dropdowns[dropSides].Items(); !slices.Equal(items, []string{"One-sided"}) {
		t.Errorf("mono sides %v", items)
	}
	hs.do(func() {
		dropdowns[dropPaper].SetSelected(1) // A5
		treetest.First[*widget.ListBox](t, c.Root).Select(1)
	})
	if items := dropdowns[dropPaper].Items(); !slices.Equal(items, []string{"A5", "Index-4x6"}) || dropdowns[dropPaper].Selection() != "A5" {
		t.Errorf("photo papers %v on %q, want A5 kept", items, dropdowns[dropPaper].Selection())
	}
	if items := dropdowns[dropColor].Items(); !slices.Equal(items, []string{"Color", "Grayscale"}) {
		t.Errorf("photo colors %v", items)
	}
	hs.do(func() { treetest.Button(t, c.Root, "Print").OnClick() })
	r := <-got
	if !slices.Contains(r.settings, Setting{"paper-format", "iso_a5"}) || !slices.Contains(r.settings, Setting{"printer", "photo"}) {
		t.Errorf("settings = %v", r.settings)
	}
}

// testCaller is the request the tests' dialogs serve.
var testCaller = portaldialogs.Caller{Handle: "/r/test", AppID: "org.app"}

// Dismissing the request the dialog shows cancels it; another request's
// dismissal leaves it up.
func TestDismissCancelsItsRequestOnly(t *testing.T) {
	hs := newHarness(t)
	got, _ := hs.prepare(t)
	hs.h.Dismiss("/r/other")
	select {
	case r := <-got:
		t.Fatalf("another request's dismissal answered %+v", r)
	case <-time.After(50 * time.Millisecond):
	}
	hs.h.Dismiss(testCaller.Handle)
	select {
	case r := <-got:
		if r.ok || r.token != 0 {
			t.Errorf("a dismissed dialog prepared %+v", r)
		}
	case <-time.After(treetest.Liveness):
		t.Fatal("Dismiss left the dialog waiting")
	}
}

// The sheet stands on lys-ui's parts, as the portal dialogs do: its
// card, the heading title, the form's labels dim, a printer's detail a
// dim caption, the replace note a warning, Cancel outline and Print the
// suggested action.
func TestSheetTakesLysUiParts(t *testing.T) {
	hs := newHarness(t)
	_, c := hs.prepare(t)
	has := func(class string, want ...string) {
		t.Helper()
		ws := treetest.WithClass(c.Root, class)
		if len(ws) == 0 {
			t.Fatalf("no .%s", class)
		}
		for _, w := range ws {
			for _, cl := range want {
				if !widget.HasClass(w, cl) {
					t.Errorf(".%s lacks .%s", class, cl)
				}
			}
		}
	}
	if card := treetest.First[*widget.Card](t, c.Root); !widget.HasClass(card, "print-surface") || card.Title() != "Print" {
		t.Errorf("the surface is card %q with classes %v, want lys-ui's card titled Print", card.Title(), card.Classes())
	}
	has("print-form-label", css.DimLabel)
	has("print-printer-detail", css.Caption, css.DimLabel)
	has("print-output-replace", css.Warning)
	has("portal-dialog-cancel", css.Outline)
	has("portal-dialog-confirm", css.SuggestedAction)
	for _, w := range treetest.WithClass(c.Root, "print-printer-name") {
		if widget.HasClass(w, css.DimLabel) {
			t.Error("a printer's name is dim")
		}
	}
}
