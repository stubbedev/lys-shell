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

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/ipp"
	"github.com/stubbedev/lys-shell/internal/portalreq"
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
	out      string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	face := treetest.Font(t)
	cfg := config.Defaults()
	cfg.Animations.Enabled = false
	hs := &harness{opened: make(chan app.LayerConfig, 4), out: filepath.Join(t.TempDir(), "output.pdf")}
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
		OutputFile: func() string { return hs.out },
		User:       "u",
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
	token    uint32
}

func (hs *harness) prepare(t *testing.T) (chan prepareResult, app.LayerConfig) {
	t.Helper()
	got := make(chan prepareResult, 1)
	go func() {
		ok, s, tok := hs.h.Prepare(testCaller, "Doc")
		got <- prepareResult{ok, s, tok}
	}()
	select {
	case c := <-hs.opened:
		return got, c
	case <-time.After(treetest.Liveness):
		t.Fatal("no dialog opened")
		return nil, app.LayerConfig{}
	}
}

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
		dropdowns[0].SetSelected(1) // landscape
		dropdowns[2].SetSelected(1) // grayscale
		treetest.Button(t, c.Root, "Print").OnClick()
	})
	r := <-got
	if !r.ok || r.token == 0 {
		t.Fatalf("prepare = %+v", r)
	}
	for _, want := range []Setting{{"printer", "office"}, {"n-copies", "2"}, {"orientation", "landscape"}, {"use-color", "false"}, {"page-ranges", "1-2"}} {
		if !slices.Contains(r.settings, want) {
			t.Errorf("settings %v lack %v", r.settings, want)
		}
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
		c.Root.Measure(widget.Constraints{Max: widget.Size{W: 1280, H: 800}})
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
		ok, s, tok := hs.h.Prepare(portalreq.Caller{Handle: "/r/second"}, "Doc 2")
		second <- prepareResult{ok, s, tok}
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

func TestPrintToFileAndAMissingScheduler(t *testing.T) {
	hs := newHarness(t)
	hs.listErr = errors.New("connect: no such file")
	hs.printers = nil
	got, c := hs.prepare(t)
	if q := hs.h.queues(); len(q) != 1 || q[0].name != fileQueue {
		t.Errorf("without CUPS the queues are %+v, want only %s", q, fileQueue)
	}
	_ = c
	hs.do(func() { treetest.Button(t, c.Root, "Print").OnClick() })
	r := <-got
	if !slices.Contains(r.settings, Setting{"printer", fileQueue}) {
		t.Fatalf("settings = %v", r.settings)
	}
	if !hs.h.Print(testCaller, "Doc", document(t, "%PDF to file"), r.token) {
		t.Fatal("print to file failed")
	}
	if b, err := os.ReadFile(hs.out); err != nil || string(b) != "%PDF to file" {
		t.Errorf("output = %q %v", b, err)
	}
	if len(hs.jobs) != 0 {
		t.Error("print to file reached the scheduler")
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
	got, c = hs.prepare(t)
	hs.do(func() {
		treetest.First[*widget.ListBox](t, c.Root).Select(1) // Print to File
		treetest.Button(t, c.Root, "Print").OnClick()
	})
	r = <-got
	hs.out = filepath.Join(t.TempDir(), "missing-dir", "output.pdf")
	if hs.h.Print(testCaller, "Doc", document(t, "x"), r.token) {
		t.Error("an unwritable output reported sent")
	}
}

// testCaller is the request the tests' dialogs serve.
var testCaller = portalreq.Caller{Handle: "/r/test", AppID: "org.app"}

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
