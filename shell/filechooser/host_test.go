package filechooser

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/transfer"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/fileuri"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/shell/apptheme"
	"github.com/stubbedev/lys-shell/shell/reveal"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

type fakeWindow struct {
	closed bool
	focus  widget.Widget
	// refuse fails every SetFocus, as an overlay whose surface is gone.
	refuse error
}

func (w *fakeWindow) Close() { w.closed = true }

func (w *fakeWindow) SetFocus(f widget.Widget) error {
	if w.refuse != nil {
		return w.refuse
	}
	w.focus = f
	return nil
}

// lay lays root out on a w x h output, as its window does.
func lay(root widget.Widget, w, h int) {
	root.Measure(widget.Constraints{Max: widget.Size{W: w, H: h}})
	root.Arrange(render.Rect{W: w, H: h})
}

// drag presses at from, moves to to and releases there.
func drag(r *widget.Router, from, to widget.Point) {
	r.Move(from)
	r.Press(widget.BTNLeft, from, 0)
	r.Move(to)
	r.Release(widget.BTNLeft, to)
}

// panel is the request's floating panel, which the overlay's root
// reveals.
func (hs *harness) panel(t *testing.T) *widget.FloatingPanel {
	t.Helper()
	if _, ok := hs.cfg.Root.(*widget.Revealer); !ok {
		t.Fatalf("root %T, want the revealer around the panel", hs.cfg.Root)
	}
	return treetest.First[*widget.FloatingPanel](t, hs.cfg.Root)
}

// center is the middle of w's bounds.
func center(w widget.Boundser) widget.Point {
	b := w.Bounds()
	return widget.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}
}

// harness runs the chooser with a lock-step loop: Invoke and the
// test's gestures take turns, as on the one UI goroutine.
type harness struct {
	t       *testing.T
	mu      sync.Mutex
	c       *Chooser
	root    string
	opened  chan app.LayerConfig
	windows []*fakeWindow
	openErr error
	// focusErr fails every opened overlay's SetFocus.
	focusErr error
	cfg      app.LayerConfig
	// conf is the live config; the chooser animates as it says.
	conf *config.Config
	// theme is the stylesheet and palette the chooser paints with.
	theme   *apptheme.Theme
	queue   chan func()
	qmu     sync.Mutex
	stopped bool
}

func newHarness(t *testing.T, viewFile string) *harness {
	t.Helper()
	face := treetest.Font(t)
	conf := config.Defaults()
	conf.Animations.Enabled = false
	hs := &harness{t: t, root: tree(t), opened: make(chan app.LayerConfig, 4), conf: conf, theme: apptheme.New(conf)}
	hs.queue = make(chan func(), 256)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for fn := range hs.queue {
			hs.run(fn)
		}
	}()
	// The loop ends with the test: work already queued runs out before
	// the next test touches the widget package's loop state.
	t.Cleanup(func() {
		hs.qmu.Lock()
		hs.stopped = true
		close(hs.queue)
		hs.qmu.Unlock()
		<-drained
	})
	hs.c = New(Deps{
		Config: func() *config.Config { return hs.conf },
		Open: func(c app.LayerConfig) (Window, error) {
			hs.mu.Lock()
			defer hs.mu.Unlock()
			if hs.openErr != nil {
				return nil, hs.openErr
			}
			w := &fakeWindow{refuse: hs.focusErr}
			hs.windows = append(hs.windows, w)
			hs.opened <- c
			return w, nil
		},
		Invoke:    hs.invoke,
		Font:      face,
		Sheet:     hs.theme.Sheet,
		MIME:      testDB(t),
		Home:      hs.root,
		Places:    func() []Place { return []Place{{"Home", hs.root, "user-home-symbolic"}} },
		Locations: func() []Place { return []Place{{"Computer", "/", "drive-harddisk-symbolic"}} },
		ViewFile:  viewFile,
	})
	return hs
}

// flush waits until the loop ran everything queued so far.
func (hs *harness) flush() {
	done := make(chan struct{})
	hs.invoke(func() { close(done) })
	<-done
}

// invoke queues fn for the loop, in order, as Application.Invoke does;
// after the test, like Invoke after Run, it drops fn.
func (hs *harness) invoke(fn func()) {
	hs.qmu.Lock()
	defer hs.qmu.Unlock()
	if !hs.stopped {
		hs.queue <- fn
	}
}

// run runs fn as the loop: the dialog's work and the test's gestures
// take turns, as on the one UI goroutine.
func (hs *harness) run(fn func()) {
	treetest.Loop.Lock()
	defer treetest.Loop.Unlock()
	fn()
}

// do runs a gesture as the loop and waits for what it set off to
// settle: a folder listing being read lands before the test goes on.
func (hs *harness) do(fn func()) {
	hs.run(fn)
	hs.settle()
}

// settle waits until no listing is being read and the loop ran what
// that queued.
func (hs *harness) settle() {
	for deadline := time.Now().Add(treetest.Liveness); ; time.Sleep(time.Millisecond) {
		var loading bool
		hs.run(func() { loading = hs.c.s != nil && hs.c.s.loading })
		if !loading {
			hs.flush()
			return
		}
		if time.Now().After(deadline) {
			hs.t.Fatal("a listing never arrived")
		}
	}
}

// ask starts a request and waits for its surface.
func (hs *harness) ask(t *testing.T, fn func() []string) chan []string {
	t.Helper()
	got := make(chan []string, 1)
	go func() { got <- fn() }()
	select {
	case hs.cfg = <-hs.opened:
	case <-time.After(treetest.Liveness):
		t.Fatal("no chooser opened")
	}
	hs.settle()
	return got
}

func (hs *harness) open(t *testing.T, r OpenRequest) chan []string {
	t.Helper()
	if r.CurrentFolder == "" {
		r.CurrentFolder = hs.root
	}
	return hs.ask(t, func() []string { return hs.c.Open(testCaller, r).URIs })
}

func answer(t *testing.T, got chan []string) []string {
	t.Helper()
	select {
	case uris := <-got:
		return uris
	case <-time.After(treetest.Liveness):
		t.Fatal("the request was never answered")
		return nil
	}
}

func pending(t *testing.T, got chan []string) {
	t.Helper()
	select {
	case uris := <-got:
		t.Fatalf("answered %v while it should still be open", uris)
	case <-time.After(20 * time.Millisecond):
	}
}

// click finds the named button as the loop, which owns the tree a
// listing may be rebuilding, and clicks it.
func (hs *harness) click(t *testing.T, name string) {
	t.Helper()
	var b *widget.Button
	hs.run(func() { b = treetest.Button(t, hs.cfg.Root, name) })
	hs.do(b.OnClick)
}

// shown is the active view's labels, in order.
func (hs *harness) shown() []string {
	var out []string
	hs.do(func() {
		for _, e := range hs.c.s.entries {
			out = append(out, hs.c.s.displayName(e))
		}
	})
	return out
}

// pick selects the named entry in the active view.
func (hs *harness) pick(t *testing.T, name string) {
	t.Helper()
	hs.do(func() {
		j := slices.Index(names(hs.c.s.entries), name)
		if j < 0 {
			t.Fatalf("no %s in %v", name, names(hs.c.s.entries))
		}
		hs.c.activeView().Select(j)
	})
}

func (hs *harness) uri(rel string) string { return "file://" + filepath.Join(hs.root, rel) }

func TestOpenAnswersTheSelectedFile(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.open(t, OpenRequest{Title: "Pick one"})
	if hs.cfg.Layer != app.LayerOverlay || hs.cfg.Namespace != "lys-shell-file-chooser" || hs.cfg.Keyboard != app.KeyboardExclusive {
		t.Errorf("surface = %+v", hs.cfg)
	}
	var l []string
	hs.run(func() { l = treetest.Labels(hs.cfg.Root) })
	if !slices.Contains(l, "Pick one") {
		t.Errorf("no title in %q", l)
	}
	if got := hs.shown(); !slices.Equal(got, []string{"docs", "pics", "a.png", "b.txt"}) {
		t.Errorf("listing = %v", got)
	}
	// Nothing selected: Open keeps the dialog.
	hs.click(t, "Open")
	pending(t, got)
	hs.pick(t, "b.txt")
	hs.click(t, "Open")
	if uris := answer(t, got); !slices.Equal(uris, []string{hs.uri("b.txt")}) {
		t.Errorf("answered %v", uris)
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if !hs.windows[0].closed {
		t.Error("an answered chooser stayed mapped")
	}
}

func TestOpenMultipleAnswersEveryFile(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.open(t, OpenRequest{Multiple: true})
	hs.do(func() {
		v := hs.c.activeView()
		if v.SelectionMode() != widget.SelectionMultiple {
			t.Errorf("selection mode = %v, want multiple", v.SelectionMode())
		}
		v.SelectAll()
	})
	hs.click(t, "Open")
	if uris := answer(t, got); !slices.Equal(uris, []string{hs.uri("a.png"), hs.uri("b.txt")}) {
		t.Errorf("answered %v, want the two files and no folders", uris)
	}
}

func TestActivatingAFileOpensItAndAFolderEntersIt(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.open(t, OpenRequest{})
	hs.do(func() { hs.c.activate(slices.Index(names(hs.c.s.entries), "pics")) })
	if got := hs.shown(); !slices.Equal(got, []string{"a.png"}) {
		t.Fatalf("after entering pics = %v", got)
	}
	hs.run(func() {
		crumbs := treetest.WithClass(hs.cfg.Root, "file-chooser-crumb")
		if last := crumbs[len(crumbs)-1]; widget.Describe(last).Name != "pics" || !widget.HasClass(last, "current") {
			t.Errorf("last crumb = %q, want the current pics", widget.Describe(last).Name)
		}
	})
	// One gesture's later activations index the listing that is gone.
	hs.do(func() {
		hs.c.activate(0)
		hs.c.navigating = true
		hs.c.activate(0)
		hs.c.navigating = false
	})
	pending(t, got)
	hs.do(func() {
		hs.c.activeView().Select(0)
		hs.c.activate(0)
	})
	if uris := answer(t, got); !slices.Equal(uris, []string{hs.uri("pics/a.png")}) {
		t.Errorf("activating a file answered %v", uris)
	}
}

func TestHistoryButtonsAndUp(t *testing.T) {
	hs := newHarness(t, "")
	hs.open(t, OpenRequest{})
	hs.do(func() { hs.c.goTo(filepath.Join(hs.root, "docs")) })
	hs.do(func() { hs.cfg.OnPress(btnBack, nil) })
	if dir := hs.c.s.dir; dir != hs.root {
		t.Errorf("back = %s", dir)
	}
	hs.do(func() { hs.cfg.OnPress(btnForward, nil) })
	if dir := hs.c.s.dir; dir != filepath.Join(hs.root, "docs") {
		t.Errorf("forward = %s", dir)
	}
	hs.do(func() { hs.cfg.OnPress(widget.BTNRight, nil) })
	if dir := hs.c.s.dir; dir != filepath.Join(hs.root, "docs") {
		t.Errorf("another button moved to %s", dir)
	}
	hs.do(hs.c.goUp)
	if dir := hs.c.s.dir; dir != hs.root {
		t.Errorf("up = %s", dir)
	}
}

func TestCancelEscapeAndQueueing(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.open(t, OpenRequest{Filters: []Filter{{"Images", []Rule{{RuleGlob, "*.png"}}}}})
	// Escape closes quick look first, then the dialog.
	hs.pick(t, "a.png")
	hs.do(func() { hs.cfg.OnKey(&widget.Router{}, keySpace, 0) })
	if !hs.c.ui.quicklook.Visible() {
		t.Fatal("space did not open quick look")
	}
	hs.do(func() { hs.cfg.OnKey(&widget.Router{}, keyEscape, 0) })
	pending(t, got)
	hs.do(func() { hs.cfg.OnKey(&widget.Router{}, keyEscape, 0) })
	if uris := answer(t, got); uris != nil {
		t.Errorf("Escape answered %v", uris)
	}

	// A second request waits for the first rather than cancelling it.
	first := hs.open(t, OpenRequest{})
	second := make(chan []string, 1)
	go func() {
		second <- hs.c.Save(portalreq.Caller{Handle: "/r/second", Modal: true}, SaveRequest{CurrentFolder: hs.root, CurrentName: "x"}).URIs
	}()
	pending(t, first)
	hs.click(t, "Cancel")
	if uris := answer(t, first); uris != nil {
		t.Errorf("Cancel answered %v", uris)
	}
	select {
	case hs.cfg = <-hs.opened:
	case <-time.After(treetest.Liveness):
		t.Fatal("the waiting request never showed")
	}
	hs.click(t, "Cancel")
	if uris := answer(t, second); uris != nil {
		t.Errorf("Cancel answered %v", uris)
	}
}

func TestSpaceInATextFieldTypes(t *testing.T) {
	hs := newHarness(t, "")
	hs.open(t, OpenRequest{})
	hs.pick(t, "a.png")
	r := &widget.Router{Root: hs.cfg.Root}
	hs.do(func() {
		r.SetFocus(hs.c.ui.search)
		hs.cfg.OnKey(r, keySpace, 0)
	})
	if hs.c.ui.quicklook.Visible() {
		t.Error("space in the search box opened quick look")
	}
}

func TestSaveAnswersTheNameInTheFolder(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.ask(t, func() []string {
		return hs.c.Save(testCaller, SaveRequest{Title: "Save", CurrentName: "draft.txt", CurrentFolder: hs.root}).URIs
	})
	var focused bool
	var text string
	hs.do(func() { focused, text = hs.windows[0].focus == hs.c.ui.name, hs.c.ui.name.Text() })
	if !focused || text != "draft.txt" {
		t.Errorf("name entry focused %v, text %q", focused, text)
	}
	// Activating a file takes its name.
	hs.do(func() { hs.c.activate(slices.Index(names(hs.c.s.entries), "b.txt")) })
	if hs.c.ui.name.Text() != "b.txt" {
		t.Errorf("name = %q after activating b.txt", hs.c.ui.name.Text())
	}
	hs.do(func() { hs.c.ui.name.SetText("") })
	hs.click(t, "Save")
	pending(t, got)
	hs.do(func() {
		hs.c.ui.name.SetText("new.txt")
		hs.c.ui.name.OnActivate("new.txt")
	})
	if uris := answer(t, got); !slices.Equal(uris, []string{hs.uri("new.txt")}) {
		t.Errorf("save answered %v", uris)
	}
}

func TestFolderPickAnswersTheFolder(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.open(t, OpenRequest{Directory: true})
	if got := hs.shown(); !slices.Equal(got, []string{"docs", "pics"}) {
		t.Errorf("folder pick lists %v", got)
	}
	hs.click(t, "Select")
	if uris := answer(t, got); !slices.Equal(uris, []string{"file://" + hs.root}) {
		t.Errorf("answered %v", uris)
	}
}

func TestFiltersHiddenAndSort(t *testing.T) {
	viewFile := filepath.Join(t.TempDir(), "state", "file-chooser-view")
	hs := newHarness(t, viewFile)
	filtered := hs.open(t, OpenRequest{Filters: []Filter{{"Images", []Rule{{RuleGlob, "*.png"}}}}})
	if got := hs.shown(); !slices.Equal(got, []string{"docs", "pics", "a.png"}) {
		t.Errorf("filtered = %v", got)
	}
	hs.do(func() { hs.c.ui.filter.SetSelected(1) })
	if got := hs.shown(); !slices.Equal(got, []string{"docs", "pics", "a.png", "b.txt"}) {
		t.Errorf("all files = %v", got)
	}
	if got := hs.c.ui.filter.Selection(); got != "All Files" {
		t.Errorf("filter shows %q", got)
	}
	hs.click(t, "Show hidden files")
	if got := hs.shown(); !slices.Contains(got, ".dot.txt") {
		t.Errorf("hidden shown = %v", got)
	}
	// The header toggles are gtk::ToggleButtons: pressed while on.
	if st := widget.Describe(hs.c.ui.hiddenTg); st.Role != widget.RoleToggleButton || !st.Pressed {
		t.Errorf("hidden toggle reads %s pressed %v, want a pressed toggle button", st.Role, st.Pressed)
	}
	if widget.Describe(hs.c.ui.recursiveToggle).Pressed {
		t.Error("the untouched subfolder toggle reads pressed")
	}
	hs.click(t, "Name ↑")
	if got := hs.shown(); !slices.Equal(got[:2], []string{"pics", "docs"}) {
		t.Errorf("name descending = %v", got)
	}
	if b, _ := os.ReadFile(viewFile); string(b) != "name desc list\n" {
		t.Errorf("persisted view = %q", b)
	}
	hs.click(t, "Toggle grid view")
	if !widget.IsVisible(hs.c.ui.grid) || widget.IsVisible(hs.c.ui.list) || widget.IsVisible(hs.c.ui.colHeader) {
		t.Error("grid view did not swap the views")
	}
	// The next chooser starts as this one was left.
	again := newHarness(t, viewFile)
	if again.c.view != (view{SortName, false, true}) {
		t.Errorf("reloaded view = %+v", again.c.view)
	}
	// And the hidden toggle sticks to the next request.
	hs.click(t, "Cancel")
	answer(t, filtered)
	got := hs.open(t, OpenRequest{})
	if !slices.Contains(hs.shown(), ".dot.txt") || !hs.c.ui.hiddenTg.Active() {
		t.Error("the hidden toggle did not stick")
	}
	hs.click(t, "Cancel")
	answer(t, got)
}

func TestRecursiveSearchStreamsMatchesForTheLiveQuery(t *testing.T) {
	hs := newHarness(t, "")
	touch(t, hs.root, ".hidden/a.png")
	hs.open(t, OpenRequest{})
	hs.do(func() { hs.c.ui.search.SetText("a.p") })
	if got := hs.shown(); !slices.Equal(got, []string{"a.png"}) {
		t.Errorf("in-folder search = %v", got)
	}
	hs.click(t, "Search subfolders")
	deadline := time.Now().Add(2 * time.Second)
	want := []string{"a.png", filepath.Join("pics", "a.png")}
	for !slices.Equal(hs.shown(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("recursive search = %v, want %v", hs.shown(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The walk skipped the dot folder; showing hidden files walks again.
	hs.click(t, "Show hidden files")
	want = []string{"a.png", filepath.Join(".hidden", "a.png"), filepath.Join("pics", "a.png")}
	for !slices.Equal(hs.shown(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("with hidden files = %v, want %v", hs.shown(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Navigating drops the query and its walk.
	hs.do(func() { hs.c.goTo(filepath.Join(hs.root, "docs")) })
	if hs.c.ui.search.Text() != "" || hs.c.stopWalk != nil || len(hs.shown()) != 0 {
		t.Errorf("after navigating: search %q, walk %v, rows %v", hs.c.ui.search.Text(), hs.c.stopWalk != nil, hs.shown())
	}
	if !hs.c.ui.empty.Visible() {
		t.Error("an empty folder hides the empty label")
	}
}

// A file dropped on the panel's body, over the files or the footer,
// opens its folder; a drag without files, a remote file, or a drop
// beside the panel does not.
func TestDropNavigates(t *testing.T) {
	hs := newHarness(t, "")
	hs.open(t, OpenRequest{})
	var r *widget.Router
	var body, footer, outside widget.Point
	hs.run(func() {
		r, body = treetest.Pointer(hs.cfg.Root, hs.c.ui.list, 1280, 720)
		footer = center(treetest.WithClass(hs.cfg.Root, "file-chooser-footer")[0].(widget.Boundser))
		outside = widget.Point{X: 2, Y: 2}
	})
	drop := func(p widget.Point, rel string) {
		t.Helper()
		hs.do(func() {
			if got := r.DragEnter([]string{"text/plain"}, p); got != "" {
				t.Errorf("a text drag at %v was accepted as %q", p, got)
			}
			if got := r.DragEnter([]string{"text/plain", uriListMime}, p); got != uriListMime {
				t.Fatalf("a file drag at %v = %q", p, got)
			}
			r.Drop(uriListMime, []byte("# comment\r\n"+hs.uri(rel)+"\r\n"), p)
		})
	}
	drop(body, "pics/a.png")
	if hs.dir() != filepath.Join(hs.root, "pics") {
		t.Errorf("a drop on the body navigated to %s", hs.dir())
	}
	drop(footer, "b.txt")
	if hs.dir() != hs.root {
		t.Errorf("a drop on the footer navigated to %s", hs.dir())
	}
	hs.do(func() {
		r.DragEnter([]string{uriListMime}, body)
		r.Drop(uriListMime, []byte("https://example.com/x\n"), body)
	})
	if hs.dir() != hs.root {
		t.Errorf("a remote drop navigated to %s", hs.dir())
	}
	hs.run(func() {
		if got := r.DragEnter([]string{uriListMime}, outside); got != "" {
			t.Errorf("a file drag beside the panel was accepted as %q", got)
		}
	})
}

func TestOpenFailureAnswersNothing(t *testing.T) {
	hs := newHarness(t, "")
	hs.openErr = errors.New("no layer shell")
	if uris := hs.c.Open(testCaller, OpenRequest{CurrentFolder: hs.root}).URIs; uris != nil {
		t.Errorf("an unmapped chooser answered %v", uris)
	}
}

// An overlay whose first control cannot take focus could not be typed
// into: the request answers nothing and the overlay closes.
func TestUnfocusableOverlayAnswersNothing(t *testing.T) {
	hs := newHarness(t, "")
	hs.focusErr = app.ErrClosed
	if uris := hs.c.Open(testCaller, OpenRequest{CurrentFolder: hs.root}).URIs; uris != nil {
		t.Errorf("an unfocusable chooser answered %v", uris)
	}
	var closed bool
	hs.do(func() { closed = len(hs.windows) == 1 && hs.windows[0].closed })
	if !closed {
		t.Error("the unfocusable overlay stayed mapped")
	}
}

// waitThumb waits until the thumbnail lookup for path at px ended.
func (hs *harness) waitThumb(t *testing.T, path string, px int) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		var ok bool
		hs.do(func() { _, ok = hs.c.thumbs[thumbKey{path, px}] })
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the thumbnail lookup never ended")
		}
	}
}

// A row shows its type's icon until its thumbnail is found, then the
// thumbnail; a rebuilt row shows it at once, and a file with none keeps
// the icon.
func TestImageRowsGetThumbnails(t *testing.T) {
	hs := newHarness(t, "")
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	real := filepath.Join(hs.root, "pics", "real.png")
	f, err := os.Create(real)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	hs.open(t, OpenRequest{CurrentFolder: filepath.Join(hs.root, "pics")})
	var row widget.Widget
	hs.run(func() { row = rows{hs.c, false}.Row(slices.Index(names(hs.c.s.entries), "real.png")) })
	hs.waitThumb(t, real, rowIconPx)
	if thumbs := treetest.All[*widget.Image](row); len(thumbs) != 1 || !thumbs[0].Loaded() {
		t.Errorf("the waiting row holds %d images, want its filled thumbnail", len(thumbs))
	}
	hs.do(func() {
		again := treetest.All[*widget.Image](rows{hs.c, false}.Row(slices.Index(names(hs.c.s.entries), "real.png")))
		if len(again) != 1 || !again[0].Loaded() {
			t.Error("a rebuilt row looked the thumbnail up again")
		}
	})
	// The empty a.png cannot decode: its row keeps the icon.
	broken := filepath.Join(hs.root, "pics", "a.png")
	hs.run(func() { row = rows{hs.c, false}.Row(slices.Index(names(hs.c.s.entries), "a.png")) })
	hs.waitThumb(t, broken, rowIconPx)
	if len(treetest.All[*widget.Image](row)) != 0 || len(treetest.All[*widget.Icon](row)) == 0 {
		t.Error("an undecodable image lost its icon")
	}
}

// A file with a thumbnail in the freedesktop cache gets it while its
// Thumb::MTime matches; a stale one is no thumbnail.
func TestThumbnailFromTheCache(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	doc := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(doc, []byte("%PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(doc)
	if err != nil {
		t.Fatal(err)
	}
	put := func(mtime int64) {
		var buf bytes.Buffer
		if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
			t.Fatal(err)
		}
		withText := insertText(t, buf.Bytes(), "Thumb::MTime", strconv.FormatInt(mtime, 10))
		sum := md5.Sum([]byte(fileuri.FromPath(doc)))
		path := filepath.Join(cache, "thumbnails", "large", hex.EncodeToString(sum[:])+".png")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, withText, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put(info.ModTime().Unix())
	if got, err := thumbnail(doc, 48); err != nil || got.Bounds().Dx() != 48 {
		t.Errorf("a cached thumbnail = %v, %v", got, err)
	}
	put(info.ModTime().Unix() - 60)
	if _, err := thumbnail(doc, 48); !errors.Is(err, errNoThumb) {
		t.Errorf("a stale cached thumbnail = %v, want none", err)
	}
	if got := pngText([]byte("not a png"), "Thumb::MTime"); got != "" {
		t.Errorf("garbage carries %q", got)
	}
}

// insertText puts a tEXt chunk right after a PNG's header chunk.
func insertText(t *testing.T, data []byte, key, value string) []byte {
	t.Helper()
	const ihdrEnd = 8 + 8 + 13 + 4
	body := append([]byte(key+"\x00"), value...)
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
	chunk = append(chunk, "tEXt"...)
	chunk = append(chunk, body...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(append([]byte("tEXt"), body...)))
	out := append([]byte{}, data[:ihdrEnd]...)
	out = append(out, chunk...)
	return append(out, data[ihdrEnd:]...)
}

// A column's grip resizes it by its left edge through the pointer,
// within the column bounds; hovering does not, and the rows re-lay on
// release, not on every motion.
func TestColumnGripResizesAndRelaysRows(t *testing.T) {
	hs := newHarness(t, "")
	hs.c.size = widget.Size{W: panelMaxW, H: panelMaxH}
	hs.open(t, OpenRequest{})
	const W, H = 1800, 1200
	var grips []*columnGrip
	hs.run(func() { grips = treetest.All[*columnGrip](hs.cfg.Root) })
	if len(grips) != 3 {
		t.Fatalf("%d column grips, want one before each of size, modified and kind", len(grips))
	}
	col := grips[2]
	// paint renders the list's rows as they are cached, after laying
	// out the whole surface as its window does, so the rows inherit
	// the stylesheet's ink through their ancestors.
	paint := func() []uint8 {
		var px []uint8
		hs.do(func() {
			lay(hs.cfg.Root, W, H)
			l := hs.c.ui.list
			l.Measure(widget.Constraints{Max: widget.Size{W: 600, H: 200}})
			l.Arrange(render.Rect{W: 600, H: 200})
			px = make([]uint8, 600*200*4)
			l.Paint(render.New(px, 600*4, 600, 200))
		})
		return px
	}
	before := paint()
	var r *widget.Router
	var at widget.Point
	hs.run(func() { r, at = treetest.Pointer(hs.cfg.Root, col, W, H) })
	if got := r.Hovered(); got != widget.Widget(col) {
		t.Fatalf("the pointer rests on %T, want the kind column's grip", got)
	}
	hs.do(func() { r.Move(widget.Point{X: at.X - 30, Y: at.Y}) })
	if hs.c.columns[2] != 90 {
		t.Errorf("kind column = %d after hovering across its grip, want the 90 it had", hs.c.columns[2])
	}
	hs.do(func() {
		r.Move(at)
		r.Press(widget.BTNLeft, at, 0)
		r.Move(widget.Point{X: at.X - 30, Y: at.Y + 9})
	})
	if hs.c.columns[2] != 120 {
		t.Errorf("kind column = %d after dragging its edge 30px left, want 120", hs.c.columns[2])
	}
	paint()
	if w := hs.c.ui.columns[2].(widget.Boundser).Bounds().W; w != 120 {
		t.Errorf("kind header = %dpx wide mid-drag, want the column's 120", w)
	}
	hs.do(func() { r.Move(widget.Point{X: at.X - 5000, Y: at.Y}) })
	if hs.c.columns[2] != maxColumnW {
		t.Errorf("kind column = %d, want clamped to %d", hs.c.columns[2], maxColumnW)
	}
	if !slices.Equal(paint(), before) {
		t.Error("the rows re-laid mid-drag")
	}
	hs.do(func() { r.Release(widget.BTNLeft, widget.Point{X: at.X - 5000, Y: at.Y}) })
	if slices.Equal(paint(), before) {
		t.Error("releasing the grip left the rows at the old widths")
	}
	hs.run(func() { r, at = treetest.Pointer(hs.cfg.Root, col, W, H) })
	hs.do(func() { drag(r, at, widget.Point{X: at.X + 5000, Y: at.Y}) })
	if hs.c.columns[2] != minColumnW {
		t.Errorf("kind column = %d, want clamped to %d", hs.c.columns[2], minColumnW)
	}
}

// A column grows only as far as the name column keeps its floor: the
// drag stops there instead of squeezing the name or hiding the column.
func TestColumnGripKeepsTheNameFloor(t *testing.T) {
	hs := newHarness(t, "")
	hs.open(t, OpenRequest{})
	const W, H = 1200, 800
	var grips []*columnGrip
	hs.run(func() { grips = treetest.All[*columnGrip](hs.cfg.Root) })
	var r *widget.Router
	var at widget.Point
	hs.do(func() { lay(hs.cfg.Root, W, H) })
	hs.run(func() { r, at = treetest.Pointer(hs.cfg.Root, grips[2], W, H) })
	hs.do(func() { drag(r, at, widget.Point{X: at.X - 300, Y: at.Y}) })
	if hs.c.columns[2] >= maxColumnW {
		t.Fatalf("kind column = %d, want held under %d by the name's floor", hs.c.columns[2], maxColumnW)
	}
	var name, kind render.Rect
	hs.do(func() {
		lay(hs.cfg.Root, W, H)
		name = hs.c.ui.sortButtons[SortName].Bounds()
		kind = hs.c.ui.sortButtons[SortKind].Bounds()
	})
	if !hs.c.ui.sortButtons[SortKind].Visible() || kind.W != hs.c.columns[2] {
		t.Errorf("kind header shown=%v %dpx wide, want shown at the column's %d", hs.c.ui.sortButtons[SortKind].Visible(), kind.W, hs.c.columns[2])
	}
	if name.W != nameMinW {
		t.Errorf("name header = %dpx wide, want held at its floor %d", name.W, nameMinW)
	}
}

// The overlay spans the target output (the focused one) and takes input
// only over the panel, which centres there at its size, its content
// filling it. The header drags it while the header's buttons keep
// their presses, and its place sticks across requests on that output;
// another output centres it again.
func TestPanelFloatsOnTheOutput(t *testing.T) {
	hs := newHarness(t, "")
	dp1, dp2 := &app.Output{Name: "DP-1"}, &app.Output{Name: "DP-2"}
	target := dp1
	hs.c.d.Target = func() *app.Output { return target }
	got := hs.open(t, OpenRequest{})
	c := hs.cfg
	if c.Output != dp1 {
		t.Errorf("mapped on %v, want the target output", c.Output)
	}
	if all := app.AnchorTop | app.AnchorBottom | app.AnchorLeft | app.AnchorRight; c.Anchor != all || c.Margin != (app.Margins{}) || c.Width != 0 || c.Height != 0 {
		t.Errorf("layer anchor %v margin %+v size %dx%d, want it spanning the output", c.Anchor, c.Margin, c.Width, c.Height)
	}
	panel := hs.panel(t)
	centred := render.Rect{X: (1920 - panelW) / 2, Y: (1080 - panelH) / 2, W: panelW, H: panelH}
	var r *widget.Router
	var grab, up widget.Point
	hs.run(func() {
		r, grab = treetest.Pointer(hs.cfg.Root, treetest.WithClass(panel, "file-chooser-title")[0].(widget.Boundser), 1920, 1080)
		up = center(treetest.Button(t, panel, "Up"))
		if p := panel.PanelRect(); p != centred {
			t.Errorf("panel at %v, want centred at %v", p, centred)
		}
		if reg, ok := widget.InputRegionOf(hs.cfg.Root); !ok || len(reg) != 1 || reg[0] != centred {
			t.Errorf("the overlay's input region %v (found %v), want the panel alone through its revealer", reg, ok)
		}
		if hit := panel.HitTest(widget.Point{X: 5, Y: 5}); hit != nil {
			t.Errorf("a press beside the panel hit %T, want it passed through", hit)
		}
		header := treetest.WithClass(panel, "file-chooser-header")[0].(widget.Boundser).Bounds()
		footer := treetest.WithClass(panel, "file-chooser-footer")[0].(widget.Boundser).Bounds()
		content := within(centred)
		if header.Y != content.Y || footer.Y+footer.H != content.Y+content.H || header.W != content.W || footer.W != content.W {
			t.Errorf("header %v and footer %v, want them spanning the panel %v inside its border", header, footer, centred)
		}
	})
	dir := hs.dir()
	hs.do(func() {
		drag(r, up, widget.Point{X: up.X + 3, Y: up.Y})
		lay(panel, 1920, 1080)
	})
	if p := panel.PanelRect(); p != centred || hs.dir() == dir {
		t.Errorf("a press on Up left the panel at %v in %s, want it clicked and the panel unmoved", p, hs.dir())
	}
	moved := centred
	moved.X, moved.Y = moved.X+40, moved.Y-30
	hs.do(func() {
		drag(r, grab, widget.Point{X: grab.X + 40, Y: grab.Y - 30})
		lay(panel, 1920, 1080)
	})
	if p := panel.PanelRect(); p != moved {
		t.Errorf("dragged the header to %v, want %v", p, moved)
	}
	hs.do(func() { hs.c.finish(nil) })
	answer(t, got)
	got = hs.open(t, OpenRequest{})
	hs.run(func() { lay(hs.cfg.Root, 1920, 1080) })
	if p := hs.panel(t).PanelRect(); p != moved {
		t.Errorf("the next request opened at %v, want the dragged %v", p, moved)
	}
	hs.do(func() { hs.c.finish(nil) })
	answer(t, got)
	target = dp2
	hs.open(t, OpenRequest{})
	hs.run(func() { lay(hs.cfg.Root, 2560, 1440) })
	if want := (render.Rect{X: (2560 - panelW) / 2, Y: (1440 - panelH) / 2, W: panelW, H: panelH}); hs.cfg.Output != dp2 || hs.panel(t).PanelRect() != want {
		t.Errorf("on another output: %v at %v, want DP-2 at %v", hs.cfg.Output, hs.panel(t).PanelRect(), want)
	}
}

// The corner grip resizes the panel within its floor and ceiling, its
// top left fixed, and the next request opens at the size it was left.
func TestPanelSizeIsLimitedAndKept(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.open(t, OpenRequest{})
	panel := hs.panel(t)
	r := &widget.Router{Root: panel}
	resize := func(dx, dy int) render.Rect {
		var p render.Rect
		hs.do(func() {
			lay(panel, 3840, 2160)
			p = panel.PanelRect()
			corner := widget.Point{X: p.X + p.W - 4, Y: p.Y + p.H - 4}
			drag(r, corner, widget.Point{X: corner.X + dx, Y: corner.Y + dy})
			lay(panel, 3840, 2160)
		})
		return p
	}
	from := resize(5000, 5000)
	if p := panel.PanelRect(); p != (render.Rect{X: from.X, Y: from.Y, W: panelMaxW, H: panelMaxH}) {
		t.Errorf("grown to %v from %v, want the %dx%d ceiling at the same corner", p, from, panelMaxW, panelMaxH)
	}
	resize(-5000, -5000)
	if p := panel.PanelRect(); p != (render.Rect{X: from.X, Y: from.Y, W: panelMinW, H: panelMinH}) {
		t.Errorf("shrunk to %v from %v, want the %dx%d floor at the same corner", p, from, panelMinW, panelMinH)
	}
	resize(100, 60)
	kept := panel.PanelRect()
	if kept.W != panelMinW+100 || kept.H != panelMinH+60 {
		t.Fatalf("resized to %v, want %dx%d", kept, panelMinW+100, panelMinH+60)
	}
	hs.do(func() { hs.c.finish(nil) })
	answer(t, got)
	hs.open(t, OpenRequest{})
	hs.run(func() { lay(hs.cfg.Root, 3840, 2160) })
	if p := hs.panel(t).PanelRect(); p != kept {
		t.Errorf("the next request opened at %v, want the %v it was left at", p, kept)
	}
}

// A new chooser opens at its default size, centred.
func TestPanelOpensAtItsDefaultSize(t *testing.T) {
	hs := newHarness(t, "")
	hs.open(t, OpenRequest{})
	hs.run(func() { lay(hs.cfg.Root, 1920, 1080) })
	if p := hs.panel(t).PanelRect(); p != (render.Rect{X: (1920 - panelW) / 2, Y: (1080 - panelH) / 2, W: panelW, H: panelH}) {
		t.Errorf("a new chooser opened at %v, want %dx%d centred", p, panelW, panelH)
	}
}

// A modal request holds the keyboard; a request with modal off takes it
// only on demand.
func TestModalDecidesTheKeyboard(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.ask(t, func() []string {
		return hs.c.Open(portalreq.Caller{Handle: "/r/m", Modal: true}, OpenRequest{CurrentFolder: hs.root}).URIs
	})
	if hs.cfg.Keyboard != app.KeyboardExclusive {
		t.Errorf("modal keyboard = %v, want exclusive", hs.cfg.Keyboard)
	}
	hs.do(func() { hs.c.finish(nil) })
	answer(t, got)
	hs.ask(t, func() []string {
		return hs.c.Open(portalreq.Caller{Handle: "/r/n", Modal: false}, OpenRequest{CurrentFolder: hs.root}).URIs
	})
	if hs.cfg.Keyboard != app.KeyboardOnDemand {
		t.Errorf("non-modal keyboard = %v, want on demand", hs.cfg.Keyboard)
	}
}

// Two requests at once both get answered: the second waits its turn.
func TestConcurrentRequestsBothGetAnswers(t *testing.T) {
	hs := newHarness(t, "")
	first := hs.open(t, OpenRequest{})
	second := make(chan []string, 1)
	go func() {
		second <- hs.c.Open(portalreq.Caller{Handle: "/r/2"}, OpenRequest{CurrentFolder: hs.root}).URIs
	}()
	select {
	case uris := <-first:
		t.Fatalf("a second request answered the first with %v", uris)
	case <-hs.opened:
		t.Fatal("the second request mapped over the first")
	case <-time.After(50 * time.Millisecond):
	}
	want := fileuri.FromPath(filepath.Join(hs.root, "a"))
	hs.do(func() { hs.c.finish([]string{want}) })
	if uris := answer(t, first); len(uris) != 1 || uris[0] != want {
		t.Errorf("first = %v", uris)
	}
	select {
	case hs.cfg = <-hs.opened:
	case <-time.After(treetest.Liveness):
		t.Fatal("the second request never showed")
	}
	hs.do(func() { hs.c.finish([]string{want + "2"}) })
	if uris := answer(t, second); len(uris) != 1 || uris[0] != want+"2" {
		t.Errorf("second = %v", uris)
	}
}

func TestDroppedPath(t *testing.T) {
	for in, want := range map[string]string{
		"file:///a/b%20c\r\nfile:///d": "/a/b c",
		"#c\nfile:///x":                "/x",
		"https://e.com/x":              "",
		"":                             "",
	} {
		got, ok := droppedPath([]byte(in))
		if got != want || ok != (want != "") {
			t.Errorf("droppedPath(%q) = %q %v", in, got, ok)
		}
	}
}

// Enter on several selected folders activates each; the first one
// entered replaces the listing, so the rest must not descend again
// from indices into the new one.
func TestEnterOnSeveralFoldersEntersOnlyTheFirst(t *testing.T) {
	hs := newHarness(t, "")
	touch(t, hs.root, "docs/d1/", "docs/d2/")
	hs.open(t, OpenRequest{Multiple: true})
	hs.do(func() {
		l := hs.c.activeView()
		l.SelectAll()
		l.KeyAction(widget.KeyEnter, 0)
	})
	var dir string
	hs.do(func() { dir = hs.c.s.dir })
	if dir != filepath.Join(hs.root, "docs") {
		t.Errorf("Enter on docs and pics went to %s, want docs", dir)
	}
	// The guard lifts once the gesture is over.
	var open bool
	hs.flush()
	hs.do(func() {
		hs.c.activate(0)
		open = hs.c.s.dir == filepath.Join(hs.root, "docs", "d1")
	})
	if !open {
		t.Error("a later activation was still ignored")
	}
}

// The sidebar's sections pack at the top of its scroll, not mid-view.
func TestSidebarPacksAtTheTop(t *testing.T) {
	hs := newHarness(t, "")
	hs.open(t, OpenRequest{})
	var labelY, scrollY int
	hs.do(func() {
		hs.cfg.Root.Measure(widget.Constraints{Max: widget.Size{W: 1280, H: 720}})
		hs.cfg.Root.Arrange(render.Rect{W: 1280, H: 720})
		for _, l := range treetest.All[*widget.Label](hs.cfg.Root) {
			if l.Text() == "Favorites" {
				labelY = l.Bounds().Y
			}
		}
		scrollY = treetest.First[*widget.Scroll](t, hs.cfg.Root).Bounds().Y
	})
	if labelY-scrollY > 16 {
		t.Errorf("Favorites sits %dpx into the sidebar, want it at the top", labelY-scrollY)
	}
}

// A long name ellipsizes instead of pushing its row's cells out of
// line with the column headers.
func TestLongNamesKeepTheColumnsAligned(t *testing.T) {
	hs := newHarness(t, "")
	touch(t, hs.root, "a-file-name-long-enough-to-overflow-any-sane-name-column-width.txt")
	hs.open(t, OpenRequest{})
	xs := map[int]bool{}
	hs.do(func() {
		for i := range hs.c.s.entries {
			row := rows{hs.c, false}.Row(i)
			row.Measure(widget.Constraints{Max: widget.Size{W: 600, H: listRowH}})
			row.Arrange(render.Rect{W: 600, H: listRowH})
			cells := treetest.WithClass(row, "file-chooser-cell")
			xs[cells[0].(interface{ Bounds() render.Rect }).Bounds().X] = true
		}
	})
	if len(xs) != 1 {
		t.Errorf("the size cells start at %v, want one column", xs)
	}
}

// The chooser's containers report a focused control to the focus ring,
// in the header and in the body alike.
func TestLayoutWidgetsReportTheirChildToTheFocusRing(t *testing.T) {
	hs := newHarness(t, "")
	hs.open(t, OpenRequest{})
	hs.run(func() {
		root := hs.cfg.Root
		for _, name := range []string{"Up", "Cancel"} {
			b := treetest.Button(t, root, name)
			lay(root, 800, 600)
			cv := render.New(make([]byte, render.Stride(800)*600), render.Stride(800), 800, 600)
			drawn := false
			cv.MarkFocus(b, func(*render.Canvas) { drawn = true })
			root.Paint(cv)
			if !drawn {
				t.Errorf("%s was not reported", name)
			}
			cv.FinishFocus()
		}
	})
}

// testCaller is the request the tests' dialogs serve.
var testCaller = portalreq.Caller{Handle: "/r/test", AppID: "org.app", Modal: true}

// Dismissing the request the chooser shows answers it with nothing and
// takes it down; another request's dismissal leaves it up.
func TestDismissTakesDownItsRequestOnly(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.open(t, OpenRequest{})
	hs.c.Dismiss("/r/other")
	select {
	case uris := <-got:
		t.Fatalf("another request's dismissal answered %v", uris)
	case <-time.After(50 * time.Millisecond):
	}
	hs.c.Dismiss(testCaller.Handle)
	if uris := answer(t, got); len(uris) != 0 {
		t.Errorf("a dismissed chooser answered %v", uris)
	}
}

// askResult starts a request and waits for its surface, answering the
// whole Result.
func (hs *harness) askResult(t *testing.T, fn func() Result) chan Result {
	t.Helper()
	got := make(chan Result, 1)
	go func() { got <- fn() }()
	select {
	case hs.cfg = <-hs.opened:
	case <-time.After(treetest.Liveness):
		t.Fatal("no chooser opened")
	}
	hs.settle()
	return got
}

func result(t *testing.T, got chan Result) Result {
	t.Helper()
	select {
	case r := <-got:
		return r
	case <-time.After(treetest.Liveness):
		t.Fatal("the request was never answered")
		return Result{}
	}
}

var (
	txtFilter = Filter{Name: "Text", Rules: []Rule{{RuleGlob, "*.txt"}}}
	pngFilter = Filter{Name: "Images", Rules: []Rule{{RuleGlob, "*.[pP][nN][gG]"}}}
)

// The confirm button takes the accept label, the dialog starts on the
// current filter, and the answer carries the filter active and the
// choices made; a cancel carries neither.
func TestOpenTakesTheAppsOptions(t *testing.T) {
	hs := newHarness(t, "")
	choices := []portalreq.Choice{
		{ID: "encoding", Label: "Encoding", Options: []portalreq.Option{{ID: "utf8", Label: "UTF-8"}, {ID: "latin1", Label: "Latin-1"}}, Initial: "latin1"},
		{ID: "readonly", Label: "Open read-only", Initial: "true"},
	}
	got := hs.askResult(t, func() Result {
		return hs.c.Open(testCaller, OpenRequest{
			CurrentFolder: hs.root, AcceptLabel: "Import", Filters: []Filter{txtFilter, pngFilter}, CurrentFilter: 1, Choices: choices,
		})
	})
	if shown := hs.shown(); !slices.Equal(shown, []string{"docs", "pics", "a.png"}) {
		t.Errorf("listing = %v, want the image filter applied from the start", shown)
	}
	checks := treetest.All[*widget.CheckButton](hs.cfg.Root)
	if len(checks) != 1 || !checks[0].Checked() {
		t.Fatalf("%d check buttons, want the read-only choice checked", len(checks))
	}
	hs.do(func() { checks[0].SetChecked(false) })
	hs.pick(t, "a.png")
	hs.click(t, "Import")
	r := result(t, got)
	if !slices.Equal(r.URIs, []string{hs.uri("a.png")}) || r.Filter.Name != "Images" {
		t.Errorf("result %+v", r)
	}
	if want := []portalreq.Selection{{ID: "encoding", Value: "latin1"}, {ID: "readonly", Value: "false"}}; !slices.Equal(r.Choices, want) {
		t.Errorf("choices = %v, want %v", r.Choices, want)
	}
	got = hs.askResult(t, func() Result { return hs.c.Open(testCaller, OpenRequest{CurrentFolder: hs.root, Choices: choices}) })
	hs.click(t, "Cancel")
	if r := result(t, got); r.URIs != nil || r.Choices != nil || len(r.Filter.Rules) != 0 {
		t.Errorf("a cancel answered %+v", r)
	}
}

// A pick of several folders answers each selected one.
func TestOpenSeveralFolders(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.open(t, OpenRequest{Directory: true, Multiple: true})
	hs.do(func() {
		hs.c.activeView().Select(slices.Index(names(hs.c.s.entries), "docs"))
		hs.c.activeView().SelectAll()
	})
	hs.click(t, "Select")
	if uris := answer(t, got); !slices.Equal(uris, []string{hs.uri("docs"), hs.uri("pics")}) {
		t.Errorf("answered %v, want both folders", uris)
	}
}

// The save name may be a path: absolute, under ~ (home), or into a
// subfolder; a folder it names opens instead.
func TestSaveResolvesTheNamedPath(t *testing.T) {
	hs := newHarness(t, "")
	elsewhere := t.TempDir()
	for name, want := range map[string]string{
		filepath.Join(elsewhere, "abs.txt"): "file://" + filepath.Join(elsewhere, "abs.txt"),
		"~/docs/home.txt":                   hs.uri("docs/home.txt"),
		"docs/sub.txt":                      hs.uri("docs/sub.txt"),
	} {
		got := hs.ask(t, func() []string { return hs.c.Save(testCaller, SaveRequest{CurrentFolder: hs.root}).URIs })
		hs.do(func() { hs.c.ui.name.SetText(name) })
		hs.click(t, "Save")
		if uris := answer(t, got); !slices.Equal(uris, []string{want}) {
			t.Errorf("%q answered %v, want %s", name, uris, want)
		}
	}
	got := hs.ask(t, func() []string { return hs.c.Save(testCaller, SaveRequest{CurrentFolder: hs.root}).URIs })
	hs.do(func() { hs.c.ui.name.SetText("docs") })
	hs.click(t, "Save")
	pending(t, got)
	var dir, text string
	hs.do(func() { dir, text = hs.c.s.dir, hs.c.ui.name.Text() })
	if dir != filepath.Join(hs.root, "docs") || text != "" {
		t.Errorf("naming a folder left %s with %q, want it opened and the name cleared", dir, text)
	}
	hs.click(t, "Cancel")
	answer(t, got)
}

// An existing file is replaced only on the notice's confirm; changing
// the name drops the prompt. A folder missing or not writable warns.
func TestSaveAsksBeforeReplacing(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.ask(t, func() []string {
		return hs.c.Save(testCaller, SaveRequest{CurrentFolder: hs.root, CurrentName: "b.txt"}).URIs
	})
	hs.click(t, "Save")
	pending(t, got)
	var notice string
	var visible bool
	hs.do(func() { notice, visible = hs.c.ui.noticeLabel.Text(), hs.c.ui.notice.Visible() })
	if !visible || notice != "“b.txt” already exists in “"+filepath.Base(hs.root)+"”. Replace it?" {
		t.Errorf("notice %v %q", visible, notice)
	}
	hs.do(func() { hs.c.ui.name.SetText("b.txt ") })
	hs.do(func() { visible = hs.c.ui.notice.Visible() })
	if visible {
		t.Error("editing the name kept the replace prompt")
	}
	hs.click(t, "Save")
	pending(t, got)
	hs.click(t, "Replace")
	if uris := answer(t, got); !slices.Equal(uris, []string{hs.uri("b.txt")}) {
		t.Errorf("replace answered %v", uris)
	}

	got = hs.ask(t, func() []string { return hs.c.Save(testCaller, SaveRequest{CurrentFolder: hs.root}).URIs })
	hs.do(func() { hs.c.ui.name.SetText("missing/x.txt") })
	hs.click(t, "Save")
	pending(t, got)
	hs.do(func() { notice = hs.c.ui.noticeLabel.Text() })
	if notice != "The folder "+filepath.Join(hs.root, "missing")+" does not exist." {
		t.Errorf("missing folder notice %q", notice)
	}
	locked := filepath.Join(hs.root, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	hs.do(func() { hs.c.ui.name.SetText("locked/x.txt") })
	hs.click(t, "Save")
	pending(t, got)
	hs.do(func() { notice = hs.c.ui.noticeLabel.Text() })
	if os.Geteuid() != 0 && notice != "You cannot save in "+locked+"." {
		t.Errorf("read-only folder notice %q", notice)
	}
	hs.click(t, "Cancel")
	answer(t, got)
}

// Picking a filter gives the save name its extension.
func TestSaveFilterSetsTheExtension(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.ask(t, func() []string {
		return hs.c.Save(testCaller, SaveRequest{CurrentFolder: hs.root, CurrentName: "report.txt", Filters: []Filter{txtFilter, pngFilter}}).URIs
	})
	hs.do(func() { hs.c.ui.filter.SetSelected(1) })
	var text string
	hs.do(func() { text = hs.c.ui.name.Text() })
	if text != "report.png" {
		t.Errorf("name = %q after the image filter", text)
	}
	hs.do(func() { hs.c.ui.filter.SetSelected(2) }) // All Files: no extension
	hs.do(func() { text = hs.c.ui.name.Text() })
	if text != "report.png" {
		t.Errorf("name = %q after All Files", text)
	}
	hs.click(t, "Cancel")
	answer(t, got)
}

// key presses a key on the dialog with focus on focused (none for nil).
func (hs *harness) key(keycode uint32, mods app.Mods, focused widget.Widget) {
	hs.do(func() {
		r := &widget.Router{Root: hs.cfg.Root}
		if focused != nil {
			r.SetFocus(focused)
		}
		hs.cfg.OnKey(r, keycode, mods)
	})
}

// dir is the folder shown.
func (hs *harness) dir() string {
	var d string
	hs.do(func() { d = hs.c.s.dir })
	return d
}

// GTK's chooser keys: Backspace and Alt+Up go up (not while typing),
// Alt+Left and Alt+Right walk the history, Ctrl+H shows hidden files,
// Ctrl+L the location entry, Ctrl+F the search; Space opens quick look
// only without modifiers.
func TestChooserKeys(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.open(t, OpenRequest{CurrentFolder: filepath.Join(hs.root, "docs")})
	hs.key(keyBackspace, 0, hs.c.ui.search)
	if hs.dir() != filepath.Join(hs.root, "docs") {
		t.Error("Backspace in the search box went up")
	}
	hs.key(keyBackspace, 0, nil)
	if hs.dir() != hs.root {
		t.Errorf("Backspace left %s, want the parent", hs.dir())
	}
	hs.key(keyLeft, app.ModAlt, nil)
	if hs.dir() != filepath.Join(hs.root, "docs") {
		t.Errorf("Alt+Left left %s, want back in docs", hs.dir())
	}
	hs.key(keyRight, app.ModAlt, nil)
	hs.key(keyUp, app.ModAlt, nil)
	if hs.dir() != filepath.Dir(hs.root) {
		t.Errorf("Alt+Right then Alt+Up left %s", hs.dir())
	}
	hs.do(func() { hs.c.goTo(hs.root) })
	hs.key(keyH, app.ModCtrl, nil)
	if shown := hs.shown(); !slices.Contains(shown, ".dot.txt") {
		t.Errorf("Ctrl+H left %v", shown)
	}
	hs.key(keyL, app.ModCtrl, nil)
	var locating bool
	hs.do(func() { locating = hs.c.ui.location.Visible() && !hs.c.ui.crumbs.Visible() })
	if !locating || hs.windows[0].focus != hs.c.ui.location {
		t.Error("Ctrl+L did not open the focused location entry")
	}
	hs.key(keyF, app.ModCtrl, nil)
	if hs.windows[0].focus != hs.c.ui.search {
		t.Error("Ctrl+F did not focus the search")
	}
	hs.pick(t, "a.png")
	hs.key(keySpace, app.ModCtrl, nil)
	var ql bool
	hs.do(func() { ql = hs.c.ui.quicklook.Visible() })
	if ql {
		t.Error("Ctrl+Space opened quick look")
	}
	hs.key(keySpace, 0, nil)
	hs.do(func() { ql = hs.c.ui.quicklook.Visible() })
	if !ql {
		t.Error("Space did not open quick look")
	}
	hs.click(t, "Cancel")
	answer(t, got)
}

// Typing into a file view searches; / or ~ starts a path in the
// location entry, whose Enter opens a folder, answers a file, or warns.
func TestTypeAheadAndLocation(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.open(t, OpenRequest{})
	// A click on the list lands on the wrapper, which takes the typing.
	var hit widget.Widget
	hs.do(func() {
		hs.cfg.Root.Measure(widget.Constraints{Max: widget.Size{W: 1280, H: 800}})
		hs.cfg.Root.Arrange(render.Rect{W: 1280, H: 800})
		b := hs.c.ui.list.Bounds()
		hit = hs.c.ui.listView.HitTest(widget.Point{X: b.X + b.W/2, Y: b.Y + b.H/2})
	})
	if hit != widget.Widget(hs.c.ui.listView) {
		t.Errorf("a click on the list hits %T, want the type-ahead view", hit)
	}
	hs.do(func() { hs.c.ui.listView.InsertRune('b') })
	if shown := hs.shown(); !slices.Equal(shown, []string{"b.txt"}) || hs.windows[0].focus != hs.c.ui.search {
		t.Errorf("typing b showed %v, focus %v", shown, hs.windows[0].focus)
	}
	hs.do(func() { hs.c.ui.listView.InsertRune('~') })
	var text string
	hs.do(func() { text = hs.c.ui.location.Text() })
	if text != "~" {
		t.Errorf("typing ~ seeded the location with %q", text)
	}
	hs.do(func() { hs.c.goToLocation("~/pics") })
	if hs.dir() != filepath.Join(hs.root, "pics") {
		t.Errorf("~/pics opened %s", hs.dir())
	}
	hs.do(func() { hs.c.goToLocation("../nope") })
	var notice string
	hs.do(func() { notice = hs.c.ui.noticeLabel.Text() })
	if notice != "No folder or file at "+filepath.Join(hs.root, "nope")+"." {
		t.Errorf("a missing path noticed %q", notice)
	}
	hs.do(func() { hs.c.goToLocation("a.png") })
	if uris := answer(t, got); !slices.Equal(uris, []string{hs.uri("pics/a.png")}) {
		t.Errorf("a typed file answered %v", uris)
	}
	// Saving, a typed path is the folder and the name.
	got = hs.ask(t, func() []string { return hs.c.Save(testCaller, SaveRequest{CurrentFolder: hs.root}).URIs })
	hs.do(func() { hs.c.goToLocation("docs/report.odt") })
	hs.do(func() { text = hs.c.ui.name.Text() })
	if hs.dir() != filepath.Join(hs.root, "docs") || text != "report.odt" {
		t.Errorf("a typed save path left %s with %q", hs.dir(), text)
	}
	hs.click(t, "Cancel")
	answer(t, got)
}

// Create Folder makes the folder in the one shown and opens it; a name
// taken or not a name warns. Opening files offers no Create Folder.
func TestCreateFolder(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.ask(t, func() []string { return hs.c.Save(testCaller, SaveRequest{CurrentFolder: hs.root}).URIs })
	if !hs.hasButton("Create Folder") {
		t.Error("a save offers no Create Folder")
	}
	hs.do(func() { hs.c.showNewFolder(); hs.c.ui.newFolderName.SetText("Invoices") })
	hs.do(func() { hs.c.createFolder() })
	if hs.dir() != filepath.Join(hs.root, "Invoices") {
		t.Errorf("creating left %s", hs.dir())
	}
	if info, err := os.Stat(filepath.Join(hs.root, "Invoices")); err != nil || !info.IsDir() {
		t.Errorf("no folder created: %v", err)
	}
	var notice string
	for name, want := range map[string]string{"..": "“..” is not a folder name.", "a/b": "“a/b” is not a folder name."} {
		hs.do(func() { hs.c.showNewFolder(); hs.c.ui.newFolderName.SetText(name); hs.c.createFolder() })
		hs.do(func() { notice = hs.c.ui.noticeLabel.Text() })
		if notice != want {
			t.Errorf("%q noticed %q", name, notice)
		}
	}
	hs.do(func() {
		hs.c.goTo(hs.root)
		hs.c.showNewFolder()
		hs.c.ui.newFolderName.SetText("docs")
		hs.c.createFolder()
	})
	hs.do(func() { notice = hs.c.ui.noticeLabel.Text() })
	if notice != "Cannot create “docs”: file exists" {
		t.Errorf("a taken name noticed %q", notice)
	}
	hs.click(t, "Cancel")
	answer(t, got)
	got = hs.open(t, OpenRequest{})
	if hs.hasButton("Create Folder") {
		t.Error("an open dialog offers Create Folder")
	}
	hs.click(t, "Cancel")
	answer(t, got)
}

// hasButton reports a button named name (its label or tooltip) in
// the chooser's tree, walked as the loop.
func (hs *harness) hasButton(name string) bool {
	found := false
	hs.run(func() {
		treetest.Walk(hs.cfg.Root, func(w widget.Widget) {
			if _, ok := w.(*widget.Button); ok && widget.Describe(w).Name == name {
				found = true
			}
		})
	})
	return found
}

// Recent lists the recently used files, offered when opening files;
// a folder that cannot be read says so instead of "No items".
func TestRecentAndListingErrors(t *testing.T) {
	hs := newHarness(t, "")
	recent := filepath.Join(t.TempDir(), "recently-used.xbel")
	body := `<xbel><bookmark href="` + hs.uri("b.txt") + `" modified="2026-10-01T00:00:00Z"/></xbel>`
	if err := os.WriteFile(recent, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	hs.c.d.RecentFile = recent
	got := hs.open(t, OpenRequest{})
	if !hs.hasButton("Recent") {
		t.Fatal("no Recent place")
	}
	hs.do(func() { hs.c.goTo(recentLocation) })
	if shown := hs.shown(); !slices.Equal(shown, []string{"b.txt"}) {
		t.Errorf("Recent shows %v", shown)
	}
	var up bool
	hs.do(func() { up = hs.c.s.up() })
	if up {
		t.Error("Recent has a parent")
	}
	locked := filepath.Join(hs.root, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	hs.do(func() { hs.c.goTo(locked) })
	var empty string
	hs.do(func() { empty = hs.c.ui.empty.Text() })
	if os.Geteuid() != 0 && empty != "Cannot read this folder: permission denied" {
		t.Errorf("an unreadable folder shows %q", empty)
	}
	hs.click(t, "Cancel")
	answer(t, got)
	got = hs.ask(t, func() []string { return hs.c.Save(testCaller, SaveRequest{CurrentFolder: hs.root}).URIs })
	if hs.hasButton("Recent") {
		t.Error("a save offers Recent")
	}
	hs.click(t, "Cancel")
	answer(t, got)
}

// Trash lists the trashed files by their original names when opening
// files, has no parent, and answers the trashed file itself; a save
// or a folder pick never offers it.
func TestTrashPlace(t *testing.T) {
	hs := newHarness(t, "")
	trash := filepath.Join(t.TempDir(), "Trash")
	trashed := writeTrashed(t, trash, "notes.txt.2", trashInfo("/home/u/notes.txt"))
	hs.c.d.TrashDirs = func() []string { return []string{trash} }
	got := hs.open(t, OpenRequest{})
	if !hs.hasButton("Trash") {
		t.Fatal("no Trash place")
	}
	hs.do(func() { hs.c.goTo(trashLocation) })
	if shown := hs.shown(); !slices.Equal(shown, []string{"notes.txt"}) {
		t.Errorf("Trash shows %v", shown)
	}
	var up bool
	hs.do(func() { up = hs.c.s.up() })
	if up {
		t.Error("Trash has a parent")
	}
	hs.pick(t, "notes.txt")
	hs.click(t, "Open")
	if uris := answer(t, got); !slices.Equal(uris, []string{fileuri.FromPath(trashed)}) {
		t.Errorf("opened %v, want the trashed file", uris)
	}

	got = hs.ask(t, func() []string { return hs.c.Save(testCaller, SaveRequest{CurrentFolder: hs.root}).URIs })
	if hs.hasButton("Trash") {
		t.Error("a save offers Trash")
	}
	hs.click(t, "Cancel")
	answer(t, got)
	got = hs.open(t, OpenRequest{Directory: true})
	if hs.hasButton("Trash") {
		t.Error("a folder pick offers Trash")
	}
	hs.click(t, "Cancel")
	answer(t, got)
}

// A dropped file opens its folder with it selected.
func TestDropSelectsTheFile(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.open(t, OpenRequest{CurrentFolder: filepath.Join(hs.root, "docs")})
	hs.do(func() { hs.c.dropped(filepath.Join(hs.root, "pics", "a.png")) })
	var sel Entry
	var ok bool
	hs.do(func() { sel, ok = hs.c.selected() })
	if hs.dir() != filepath.Join(hs.root, "pics") || !ok || sel.Name() != "a.png" {
		t.Errorf("drop left %s selecting %v %v", hs.dir(), sel.Name(), ok)
	}
	hs.click(t, "Open")
	if uris := answer(t, got); !slices.Equal(uris, []string{hs.uri("pics/a.png")}) {
		t.Errorf("answered %v", uris)
	}
}

// A file appearing in the folder shown is listed without navigating,
// the selection kept; a change after the dialog moved on lists nothing.
func TestListingFollowsTheFolder(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.open(t, OpenRequest{})
	hs.pick(t, "b.txt")
	if err := os.WriteFile(filepath.Join(hs.root, "c.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(treetest.Liveness); !slices.Contains(hs.shown(), "c.txt"); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the new file never listed: %v", hs.shown())
		}
	}
	var sel Entry
	var ok bool
	hs.do(func() { sel, ok = hs.c.selected() })
	if !ok || sel.Name() != "b.txt" {
		t.Errorf("after the reload %v %v is selected, want b.txt", sel.Name(), ok)
	}
	hs.click(t, "Cancel")
	answer(t, got)
}

// A mount coming or going rebuilds Locations; the watch stops with the
// dialog.
func TestLocationsFollowTheMounts(t *testing.T) {
	hs := newHarness(t, "")
	var changed func()
	stopped := 0
	locations := []Place{{"Computer", "/", "drive-harddisk-symbolic"}}
	hs.c.d.Locations = func() []Place { return locations }
	hs.c.d.WatchLocations = func(c func()) func() {
		changed = c
		return func() { stopped++ }
	}
	got := hs.open(t, OpenRequest{})
	if hs.hasButton("USB stick") {
		t.Fatal("a mount listed before it came")
	}
	locations = append(locations, Place{"USB stick", "/run/media/u/stick", "drive-removable-media-symbolic"})
	changed()
	hs.settle()
	if !hs.hasButton("USB stick") {
		t.Error("the new mount was not listed")
	}
	hs.click(t, "Cancel")
	answer(t, got)
	if stopped != 1 {
		t.Errorf("the mount watch stopped %d times", stopped)
	}
}

// The real mount watch starts and stops without leaking its reader.
func TestWatchMountsStops(t *testing.T) {
	stop := WatchMounts(func() {})
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stopping the mount watch hung")
	}
}

// A folder still being read says so, not "No items".
func TestListingShowsLoading(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.open(t, OpenRequest{})
	var empty string
	var visible bool
	hs.run(func() {
		hs.c.s.request = func(string) {}
		hs.c.goTo(filepath.Join(hs.root, "docs"))
		empty, visible = hs.c.ui.empty.Text(), hs.c.ui.empty.Visible()
		hs.c.s.loading = false
	})
	if empty != "Loading…" || !visible {
		t.Errorf("a folder being read shows %q (%v)", empty, visible)
	}
	hs.click(t, "Cancel")
	answer(t, got)
}

// dragRead is what a drag's content writes for mime.
func dragRead(t *testing.T, d *widget.DragContent, mime string) string {
	t.Helper()
	var b strings.Builder
	if err := d.Write(mime, &b); err != nil {
		t.Fatalf("write %s: %v", mime, err)
	}
	return b.String()
}

// Rows dragged out offer their files to copy, as a uri list and in
// the GNOME file managers' form, from the list and the grid alike; a
// folder pick drags folders and a trashed file its real path; nothing
// is offered while a listing loads or for rows that are gone.
func TestDragFilesOut(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.open(t, OpenRequest{Multiple: true})
	var drag *widget.DragContent
	hs.do(func() {
		rows := []int{slices.Index(names(hs.c.s.entries), "a.png"), slices.Index(names(hs.c.s.entries), "b.txt")}
		drag = hs.c.ui.list.DragContent(rows)
		if g := hs.c.ui.grid.DragContent(rows); g == nil {
			t.Error("the grid offers no drag")
		}
	})
	if drag == nil {
		t.Fatal("dragging two files offered nothing")
	}
	if drag.Actions != transfer.ActionCopy {
		t.Errorf("actions = %v, want copy only", drag.Actions)
	}
	if list := dragRead(t, drag, transfer.MimeURIList); list != hs.uri("a.png")+"\r\n"+hs.uri("b.txt")+"\r\n" {
		t.Errorf("uri list = %q", list)
	}
	if gnome := dragRead(t, drag, gnomeCopiedFiles); gnome != "copy\n"+hs.uri("a.png")+"\n"+hs.uri("b.txt") {
		t.Errorf("gnome copied files = %q", gnome)
	}
	hs.do(func() {
		if d := hs.c.ui.list.DragContent([]int{99}); d != nil {
			t.Error("a row that is gone offered a drag")
		}
		hs.c.s.loading = true
		if d := hs.c.ui.list.DragContent([]int{0}); d != nil {
			t.Error("a loading listing offered a drag")
		}
		hs.c.s.loading = false
	})
	hs.click(t, "Cancel")
	answer(t, got)

	got = hs.open(t, OpenRequest{Directory: true})
	hs.do(func() {
		d := hs.c.ui.list.DragContent([]int{slices.Index(names(hs.c.s.entries), "docs")})
		if d == nil || dragRead(t, d, transfer.MimeURIList) != hs.uri("docs")+"\r\n" {
			t.Errorf("a folder pick drags %v", d)
		}
	})
	hs.click(t, "Cancel")
	answer(t, got)

	trash := filepath.Join(t.TempDir(), "Trash")
	trashed := writeTrashed(t, trash, "notes.txt.2", trashInfo("/home/u/notes.txt"))
	hs.c.d.TrashDirs = func() []string { return []string{trash} }
	got = hs.open(t, OpenRequest{})
	hs.do(func() { hs.c.goTo(trashLocation) })
	hs.do(func() {
		d := hs.c.ui.list.DragContent([]int{0})
		if d == nil || dragRead(t, d, transfer.MimeURIList) != transfer.FileURI(trashed)+"\r\n" {
			t.Errorf("a trashed file drags %v", d)
		}
	})
	hs.click(t, "Cancel")
	answer(t, got)
}

// A place's icon resolves through GTK's dash fallbacks, as the GTK
// sidebar's GThemedIcon does: folder-documents finds folder.
func TestPlaceIconFallsBackAlongTheName(t *testing.T) {
	treetest.IconTheme(t, "folder")
	if treetest.Ink(placeIcon(Place{"Documents", "/d", "folder-documents"})) == 0 {
		t.Error("a place icon with a present dash-shortened form drew nothing")
	}
	if treetest.Ink(placeIcon(Place{"Odd", "/o", "unrelated"})) != 0 {
		t.Error("an absent place icon drew something")
	}
}

// The chooser stands on lys-ui's parts: a floating panel cut into bands
// by separators, ghost icon buttons in the header (never the old flat
// ones), a heading title a step over the text size, ghost crumbs and
// places, caption headings over the sidebar sections, dim cells, an
// outline Cancel and the suggested confirm.
func TestChooserTakesLysUiParts(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.open(t, OpenRequest{})
	hs.run(func() {
		root := hs.cfg.Root
		all := func(class string, want ...string) {
			t.Helper()
			ws := treetest.WithClass(root, class)
			if len(ws) == 0 {
				t.Fatalf("no .%s", class)
			}
			for _, w := range ws {
				for _, cl := range want {
					if !widget.HasClass(w, cl) {
						t.Errorf(".%s lacks .%s", class, cl)
					}
				}
				if widget.HasClass(w, "flat") {
					t.Errorf(".%s is still a flat button", class)
				}
			}
		}
		hs.panel(t)
		all("file-chooser-nav", css.Ghost, css.Icon)
		all("file-chooser-title", css.Heading)
		all("file-chooser-crumb", css.Ghost)
		all("file-chooser-place", css.Ghost)
		all("file-chooser-sidebar-header", css.CaptionHeading, css.DimLabel)
		for _, cell := range treetest.WithClass(hs.c.fileRow(hs.c.s.entries[0]), "file-chooser-cell") {
			if !widget.HasClass(cell, css.DimLabel) {
				t.Error("a row's cell is not dim")
			}
		}
		all("file-chooser-cancel", css.Outline)
		all("file-chooser-confirm", css.SuggestedAction)
		if n := len(treetest.All[*widget.Separator](root)); n != 3 {
			t.Errorf("%d separators, want the 3 between the bands", n)
		}
		title := treetest.WithClass(root, "file-chooser-title")[0].(*widget.Label)
		if want := widget.Current().TitleSize(); title.SizePx() != want {
			t.Errorf("title %vpx, want %vpx", title.SizePx(), want)
		}
		if widget.HasClass(treetest.WithClass(root, "file-chooser-cancel")[0], css.SuggestedAction) {
			t.Error("Cancel is the suggested action")
		}
	})
	hs.click(t, "Cancel")
	answer(t, got)
}

// The chooser reveals its panel with the file-chooser animation and
// keeps the overlay until the panel has hidden; with animations off
// the overlay closes as the dialog answers.
func TestPanelRevealsAndHidesWithItsAnimation(t *testing.T) {
	hs := newHarness(t, "")
	hs.conf.Animations.Enabled = true
	got := hs.open(t, OpenRequest{})
	rev, ok := hs.cfg.Root.(*widget.Revealer)
	if !ok {
		t.Fatalf("root %T, want the revealer around the panel", hs.cfg.Root)
	}
	want := reveal.Transition(hs.conf.Animations.TransitionFor(config.AnimFileChooser, false))
	if !rev.Revealed() || rev.Transition() != want {
		t.Errorf("revealed %v with %v, want revealed with the file chooser's %v", rev.Revealed(), rev.Transition(), want)
	}
	win := hs.windows[len(hs.windows)-1]
	hs.do(func() { hs.cfg.OnKey(&widget.Router{}, keyEscape, 0) })
	answer(t, got)
	if rev.Revealed() {
		t.Error("cancelling left the panel revealed")
	}
	if win.closed {
		t.Error("the overlay closed before its panel hid")
	}

	hs.conf.Animations.Enabled = false
	got = hs.open(t, OpenRequest{})
	win = hs.windows[len(hs.windows)-1]
	hs.do(func() { hs.cfg.OnKey(&widget.Router{}, keyEscape, 0) })
	answer(t, got)
	if !win.closed {
		t.Error("with animations off the overlay outlived the answer")
	}
}

// frame paints the whole surface at w x h as its window does and reads
// back its pixels.
func (hs *harness) frame(w, h int) func(x, y int) render.Color {
	px := make([]uint8, w*h*4)
	hs.do(func() {
		lay(hs.cfg.Root, w, h)
		hs.cfg.Root.Paint(render.New(px, w*4, w, h))
	})
	return func(x, y int) render.Color {
		i := (y*w + x) * 4
		return render.RGBA(px[i+2], px[i+1], px[i], px[i+3])
	}
}

// token is the palette color the stylesheet resolves var(--tok) to.
func (hs *harness) token(t *testing.T, tok config.CssToken) render.Color {
	t.Helper()
	c, ok := hs.theme.RenderPalette().Token(tok)
	if !ok {
		t.Fatalf("no palette token %s", tok)
	}
	return c
}

// The list's rows paint the stylesheet's fills behind their cells: the
// accent behind the selected row, the hover tint under the pointer,
// and the zebra tint on every other row by model index, never on the
// rows between, however far the list scrolled.
func TestListRowsPaintTheirFills(t *testing.T) {
	hs := newHarness(t, "")
	touch(t, hs.root, "c.txt", "d.txt")
	hs.open(t, OpenRequest{})
	hs.pick(t, "b.txt")
	const W, H = 1200, 800
	at := hs.frame(W, H)
	var lb render.Rect
	var sel int
	hs.run(func() {
		lb = hs.c.ui.list.Bounds()
		sel = slices.Index(names(hs.c.s.entries), "b.txt")
	})
	x := lb.X + lb.W - 3
	rowY := func(i int) int { return lb.Y + i*listRowH + listRowH/2 }
	n := len(hs.shown())
	blank := at(x, lb.Y+lb.H-2)
	if got, want := at(x, rowY(sel)), hs.token(t, config.TokenAccent); got != want {
		t.Errorf("selected row paints %v, want the accent %v", got, want)
	}
	for i := range n {
		if i == sel {
			continue
		}
		got := at(x, rowY(i))
		if i%2 == 0 && got != blank {
			t.Errorf("row %d paints %v, want the view's own %v", i, got, blank)
		}
		if i%2 == 1 && (got == blank || got != at(x, rowY(1))) {
			t.Errorf("alt row %d paints %v, want the zebra tint row 1 has, over the view's %v", i, got, blank)
		}
	}
	var r *widget.Router
	hs.run(func() {
		r = &widget.Router{Root: hs.cfg.Root}
		r.Move(widget.Point{X: lb.X + lb.W/2, Y: rowY(0)})
	})
	at = hs.frame(W, H)
	if got, want := at(x, rowY(0)), hs.token(t, config.TokenBgHover); got != want {
		t.Errorf("hovered row paints %v, want the hover tint %v", got, want)
	}
	if got := at(x, rowY(2)); got != blank {
		t.Errorf("row 2 paints %v with the pointer elsewhere, want the view's own %v", got, blank)
	}

	for i := range 40 {
		touch(t, hs.root, "docs/f"+strconv.Itoa(100+i)+".txt")
	}
	hs.run(func() { r.Move(widget.Point{}) })
	hs.do(func() { hs.c.goTo(filepath.Join(hs.root, "docs")) })
	at = hs.frame(W, H)
	plain, tint := at(x, rowY(0)), at(x, rowY(1))
	if plain != blank || tint == blank {
		t.Fatalf("rows 0 and 1 of the long listing paint %v and %v, want the view's %v and the zebra tint", plain, tint, blank)
	}
	for top := 1; top <= 3; top++ {
		hs.run(func() { hs.c.ui.list.ScrollPixels(0, listRowH) })
		at = hs.frame(W, H)
		for k := range lb.H / listRowH {
			want := plain
			if (top+k)%2 == 1 {
				want = tint
			}
			if got := at(x, rowY(k)); got != want {
				t.Errorf("scrolled %d rows down, row %d (slot %d) paints %v, want %v", top, top+k, k, got, want)
			}
		}
	}
}

// listRowCells lays the surface out and row i in its band of the list,
// under the list as the list places it, and returns the row's icon and
// its size, modified and kind cells.
func (hs *harness) listRowCells(t *testing.T, i, w, h int) (widget.Widget, []widget.Widget) {
	t.Helper()
	hs.frame(w, h)
	var icon widget.Widget
	var cells []widget.Widget
	hs.run(func() {
		lb := hs.c.ui.list.Bounds()
		row := rows{hs.c, false}.Row(i)
		widget.SetParents(hs.c.ui.list, row)
		band := render.Rect{X: lb.X, Y: lb.Y + i*listRowH, W: lb.W, H: listRowH}
		row.Measure(widget.Constraints{Min: widget.Size{W: band.W, H: band.H}, Max: widget.Size{W: band.W, H: band.H}})
		row.Arrange(band)
		cells = treetest.WithClass(row, "file-chooser-cell")
		icon = treetest.All[*columnRow](row)[0].lead.(*widget.Box).Children()[0]
	})
	return icon, cells
}

// A column that gave way paints nothing: a row laid out wide and then
// at the name's floor paints its cells at the wide layout and nothing
// past the floor once they hid, though they keep their wide bounds.
// A column hidden at the name's floor takes no hits: a point where it
// sat while shown lands on none of its widgets.
func TestHiddenColumnsTakeNoHits(t *testing.T) {
	hs := newHarness(t, "")
	hs.open(t, OpenRequest{})
	const wide, narrow, h = 1200, nameMinW, listRowH
	hs.run(func() {
		row := rows{hs.c, false}.Row(0)
		widget.SetParents(hs.c.ui.list, row)
		cells := treetest.WithClass(row, "file-chooser-cell")
		layAt := func(w int) {
			row.Measure(widget.Constraints{Min: widget.Size{W: w, H: h}, Max: widget.Size{W: w, H: h}})
			row.Arrange(render.Rect{W: w, H: h})
		}
		layAt(wide)
		centers := make([]widget.Point, len(cells))
		for i, c := range cells {
			b := c.(widget.Boundser).Bounds()
			centers[i] = widget.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}
			if hit := row.HitTest(centers[i]); !treetest.Contains(c, hit) {
				t.Fatalf("shown, cell %d at %v is not hit", i, centers[i])
			}
		}
		layAt(narrow)
		for i, c := range cells {
			if hit := row.HitTest(centers[i]); hit != nil && treetest.Contains(c, hit) {
				t.Errorf("hidden, cell %d still takes the hit at %v", i, centers[i])
			}
		}
	})
}

func TestHiddenColumnsPaintNothing(t *testing.T) {
	hs := newHarness(t, "")
	hs.open(t, OpenRequest{})
	const wide, narrow, h = 1200, nameMinW, listRowH
	paintsPast := func(px []uint8) bool {
		for y := range h {
			for x := narrow; x < wide; x++ {
				if px[(y*wide+x)*4+3] != 0 {
					return true
				}
			}
		}
		return false
	}
	var atWide, atNarrow []uint8
	var cells []widget.Widget
	hs.run(func() {
		row := rows{hs.c, false}.Row(0)
		widget.SetParents(hs.c.ui.list, row)
		cells = treetest.WithClass(row, "file-chooser-cell")
		paintAt := func(w int) []uint8 {
			row.Measure(widget.Constraints{Min: widget.Size{W: w, H: h}, Max: widget.Size{W: w, H: h}})
			row.Arrange(render.Rect{W: w, H: h})
			px := make([]uint8, wide*h*4)
			widget.PaintChild(render.New(px, wide*4, wide, h), row)
			return px
		}
		atWide = paintAt(wide)
		atNarrow = paintAt(narrow)
	})
	if !paintsPast(atWide) {
		t.Fatalf("laid out %dpx wide, the row paints nothing past %dpx", wide, narrow)
	}
	for i, c := range cells {
		if widget.IsVisible(c) {
			t.Errorf("cell %d still shows at the name's floor", i)
		}
	}
	if paintsPast(atNarrow) {
		t.Errorf("laid out at the name's floor, the row paints past it: a hidden column painted")
	}
}

// Every column header sits over its values: the same span, the same
// text alignment, the numbers flush right and the text flush left, and
// the name header starts where the row's icon does.
func TestColumnHeadersSitOverTheirValues(t *testing.T) {
	hs := newHarness(t, "")
	hs.open(t, OpenRequest{})
	icon, cells := hs.listRowCells(t, 0, 1200, 800)
	if len(cells) != 3 {
		t.Fatalf("%d cells in a row, want size, modified and kind", len(cells))
	}
	hs.run(func() {
		if hx, ix := hs.c.ui.sortLabels[SortName].Bounds().X, icon.(widget.Boundser).Bounds().X; hx != ix {
			t.Errorf("name header text at x=%d, want the icon's x=%d", hx, ix)
		}
		for i, cell := range cells {
			col := SortColumn(i + 1)
			head := hs.c.ui.sortLabels[col]
			hb, cb := head.Bounds(), cell.(widget.Boundser).Bounds()
			if hb.X != cb.X || hb.W != cb.W {
				t.Errorf("column %d header text spans x=%d+%d, want its cells' x=%d+%d", col, hb.X, hb.W, cb.X, cb.W)
			}
			if ha, ca := head.Alignment(), cell.(*widget.Label).Alignment(); ha != ca {
				t.Errorf("column %d header aligns %v, want its cells' %v", col, ha, ca)
			}
		}
		if a := hs.c.ui.sortLabels[SortSize].Alignment(); a != render.AlignEnd {
			t.Errorf("size header aligns %v, want flush right over the numbers", a)
		}
		if a := hs.c.ui.sortLabels[SortKind].Alignment(); a != render.AlignStart {
			t.Errorf("kind header aligns %v, want flush left over the text", a)
		}
	})
}

// With the preview open at the default size the name keeps its floor:
// the columns give way from the kind on, header and rows alike, and
// without the preview they all show again.
func TestColumnsGiveWayToTheName(t *testing.T) {
	hs := newHarness(t, "")
	touch(t, hs.root, "a-file-name-long-enough-to-overflow-any-sane-name-column-width.txt")
	hs.open(t, OpenRequest{})
	shown := func() (head, cells [3]bool) {
		_, cs := hs.listRowCells(t, 0, 1200, 800)
		hs.run(func() {
			for i := range head {
				head[i] = hs.c.ui.sortButtons[i+1].Visible()
				cells[i] = cs[i].(*widget.Label).Visible()
			}
		})
		return head, cells
	}
	if head, cells := shown(); head != [3]bool{true, true, true} || cells != head {
		t.Errorf("without the preview: header %v, cells %v; want every column", head, cells)
	}
	hs.do(func() { hs.c.togglePreview(true) })
	head, cells := shown()
	if want := [3]bool{false, true, false}; head != want || cells != want {
		t.Errorf("size, modified, kind shown: header %v, cells %v; want %v", head, cells, want)
	}
	var name render.Rect
	hs.run(func() { name = hs.c.ui.sortButtons[SortName].Bounds() })
	if name.W < nameMinW {
		t.Errorf("name column = %dpx, want at least %d", name.W, nameMinW)
	}
}
