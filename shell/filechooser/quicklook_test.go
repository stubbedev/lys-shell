package filechooser

import (
	"context"
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

	"github.com/stubbedev/lys-lib/xkb"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/shell/treetest"
	"github.com/stubbedev/lys-shell/shell/widgets"
)

// loadWatch counts the preview loads started, the goroutines they ran
// on and what each read, and can hold every load until released.
type loadWatch struct {
	mu      sync.Mutex
	started int
	reads   []int
	onLoop  int
	loop    map[int64]bool
	hold    chan struct{}
	ctxs    []context.Context
}

func watchLoads(t *testing.T, hs *harness) *loadWatch {
	t.Helper()
	w := &loadWatch{loop: map[int64]bool{goroutineID(): true}}
	queue := make(chan int64, 1)
	hs.invoke(func() { queue <- goroutineID() })
	w.loop[<-queue] = true
	load := hs.c.previewLoader
	hs.c.previewLoader = func(ctx context.Context, req previewRequest) (preview, error) {
		w.mu.Lock()
		w.started++
		w.ctxs = append(w.ctxs, ctx)
		if w.loop[goroutineID()] {
			w.onLoop++
		}
		hold := w.hold
		w.mu.Unlock()
		if hold != nil {
			select {
			case <-hold:
			case <-ctx.Done():
			}
		}
		p, err := load(ctx, req)
		w.mu.Lock()
		w.reads = append(w.reads, p.read)
		w.mu.Unlock()
		return p, err
	}
	return w
}

func (w *loadWatch) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.started
}

// waitPreview waits until the preview of the entry named name landed.
func (hs *harness) waitPreview(t *testing.T, name string) {
	t.Helper()
	for deadline := time.Now().Add(treetest.Liveness); ; time.Sleep(2 * time.Millisecond) {
		var done bool
		hs.do(func() { done = hs.c.pv.shown != nil && hs.c.pv.entry.Name() == name })
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the preview of %s never landed", name)
		}
	}
}

// quicklookOn opens folder, selects name and presses Space.
func (hs *harness) quicklookOn(t *testing.T, folder, name string) chan []string {
	t.Helper()
	got := hs.open(t, OpenRequest{CurrentFolder: folder})
	hs.pick(t, name)
	hs.key(xkb.KeySpace, 0, nil)
	hs.waitPreview(t, name)
	return got
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Space on source shows its first lines highlighted under its type's
// description; on a binary file it shows the card alone.
func TestQuicklookShowsTextHighlighted(t *testing.T) {
	hs := newHarness(t, "")
	dir := filepath.Join(hs.root, "docs")
	writeFile(t, filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"))
	writeFile(t, filepath.Join(dir, "blob.txt"), []byte("ELF\x00\x01\x02binary"))
	got := hs.quicklookOn(t, dir, "main.go")
	var code []*widgets.CodeView
	var markup string
	hs.run(func() {
		lay(hs.cfg.Root, 1400, 1000)
		code = treetest.All[*widgets.CodeView](hs.c.ui.ql.visual)
		if lines := treetest.All[*widget.RichLabel](hs.c.ui.ql.visual); len(lines) > 2 {
			markup = lines[2].Markup()
		}
	})
	if len(code) != 1 || !strings.HasPrefix(code[0].Text(), "package main\n") || code[0].Name() != "main.go" {
		t.Fatalf("Quick Look on main.go shows %d code views", len(code))
	}
	if !strings.Contains(markup, "<span") || !strings.Contains(markup, "func") {
		t.Errorf("the third line is not highlighted: %q", markup)
	}
	hs.key(xkb.KeyEscape, 0, nil)
	hs.pick(t, "blob.txt")
	hs.key(xkb.KeySpace, 0, nil)
	hs.waitPreview(t, "blob.txt")
	var info string
	hs.run(func() {
		code = treetest.All[*widgets.CodeView](hs.c.ui.ql.visual)
		info = hs.c.ui.ql.info.Text()
	})
	if len(code) != 0 || !strings.Contains(info, "B ·") {
		t.Errorf("a binary file shows %d code views, detail %q", len(code), info)
	}
	hs.click(t, "Cancel")
	answer(t, got)
}

// A wide image previews whole at its aspect ratio with its pixel size
// in the detail line, never cropped square.
func TestQuicklookShowsTheWholeImage(t *testing.T) {
	hs := newHarness(t, "")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := filepath.Join(hs.root, "pics")
	f, err := os.Create(filepath.Join(dir, "pano.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewGray(image.Rect(0, 0, 4000, 1000))); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	got := hs.quicklookOn(t, dir, "pano.png")
	var imgs []*widget.Image
	var info string
	hs.run(func() {
		lay(hs.cfg.Root, 1400, 1000)
		imgs = treetest.All[*widget.Image](hs.c.ui.ql.visual)
		info = hs.c.ui.ql.info.Text()
	})
	if len(imgs) != 1 {
		t.Fatalf("Quick Look on an image shows %d images", len(imgs))
	}
	if b := imgs[0].Bounds(); b.W != 4*b.H || b.W == 0 {
		t.Errorf("the 4000x1000 image shows at %dx%d, want 4:1", b.W, b.H)
	}
	if !strings.Contains(info, "4000 × 1000") {
		t.Errorf("the detail line %q has no pixel size", info)
	}
	hs.click(t, "Cancel")
	answer(t, got)
	for deadline := time.Now().Add(treetest.Liveness); ; time.Sleep(2 * time.Millisecond) {
		var live int
		var shown *preview
		hs.do(func() { live, shown = hs.c.thumbnailer.mem.Stats().Live, hs.c.pv.shown })
		if shown != nil {
			t.Fatal("the closed dialog still holds its preview")
		}
		if live == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d rasters left after the dialog closed", live)
		}
	}
}

// A 2 GB text file previews from its first 64 KiB, read off the loop.
func TestQuicklookReadsAtMost64KiB(t *testing.T) {
	hs := newHarness(t, "")
	path := filepath.Join(hs.root, "docs", "huge.txt")
	writeFile(t, path, []byte(strings.Repeat("line of text\n", previewTextBytes/13+1)))
	if err := os.Truncate(path, 2<<30); err != nil {
		t.Fatal(err)
	}
	w := watchLoads(t, hs)
	got := hs.quicklookOn(t, filepath.Dir(path), "huge.txt")
	w.mu.Lock()
	reads, onLoop := slices.Clone(w.reads), w.onLoop
	w.mu.Unlock()
	if len(reads) != 1 || reads[0] > previewTextBytes || reads[0] == 0 || onLoop != 0 {
		t.Errorf("previewing 2 GB read %v bytes, %d loads on the loop; want one read of at most %d off it", reads, onLoop, previewTextBytes)
	}
	hs.click(t, "Cancel")
	answer(t, got)
}

// Moving the selection 50 times before the loop rests starts one load;
// a load superseded by a later move is cancelled.
func TestPreviewLoadsOncePerRest(t *testing.T) {
	hs := newHarness(t, "")
	dir := filepath.Join(hs.root, "docs")
	for i := range 10 {
		writeFile(t, filepath.Join(dir, "note"+strconv.Itoa(i)+".txt"), []byte("note "+strconv.Itoa(i)))
	}
	w := watchLoads(t, hs)
	got := hs.open(t, OpenRequest{CurrentFolder: dir})
	hs.pick(t, "note0.txt")
	hs.do(func() { hs.c.ui.previewToggle.SetActive(true) })
	hs.waitPreview(t, "note0.txt")
	before := w.count()
	hs.do(func() {
		for i := range 50 {
			hs.c.activeView().Select(i % 10)
		}
	})
	hs.waitPreview(t, "note9.txt")
	if loads := w.count() - before; loads != 1 {
		t.Errorf("50 moves in one rest started %d loads, want 1", loads)
	}
	var text string
	hs.run(func() { text = treetest.All[*widgets.CodeView](hs.c.ui.pv.visual)[0].Text() })
	if text != "note 9" {
		t.Errorf("the pane shows %q, want the last selection's text", text)
	}

	w.mu.Lock()
	w.hold = make(chan struct{})
	w.mu.Unlock()
	hs.do(func() { hs.c.activeView().Select(3) })
	for deadline := time.Now().Add(treetest.Liveness); w.count() < before+2; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the held load never started")
		}
	}
	hs.do(func() { hs.c.activeView().Select(4) })
	w.mu.Lock()
	superseded := w.ctxs[len(w.ctxs)-1]
	close(w.hold)
	w.hold = nil
	w.mu.Unlock()
	if superseded.Err() == nil {
		t.Error("the superseded load was not cancelled")
	}
	hs.waitPreview(t, "note4.txt")
	hs.click(t, "Cancel")
	answer(t, got)
}

// A folder previews its item count, capped.
func TestFolderPreviewCountsItems(t *testing.T) {
	hs := newHarness(t, "")
	got := hs.quicklookOn(t, hs.root, "docs")
	var info string
	hs.run(func() { info = hs.c.ui.ql.info.Text() })
	if !strings.HasPrefix(info, "Folder · 0 items") {
		t.Errorf("an empty folder's detail line is %q", info)
	}
	hs.click(t, "Cancel")
	answer(t, got)
	dir := t.TempDir()
	for i := range 12 {
		writeFile(t, filepath.Join(dir, strconv.Itoa(i)), nil)
	}
	if n, more, err := testReader.Count(dir, 10); n != 10 || !more || err != nil {
		t.Errorf("counting 12 to 10 = %d, %v, %v", n, more, err)
	}
	if n, more, err := testReader.Count(dir, 12); n != 12 || more || err != nil {
		t.Errorf("counting 12 to 12 = %d, %v, %v", n, more, err)
	}
	if got := previewDetail(Entry{IsDir: true}, preview{kind: previewFolder, items: folderCountCap, more: true}); !strings.HasPrefix(got, "Folder · 10,000+ items") {
		t.Errorf("a capped count reads %q", got)
	}
}

// A PDF previews its large thumbnail, made by its thumbnailer, under
// its type's description.
func TestQuicklookShowsAThumbnailerPage(t *testing.T) {
	hs := newHarness(t, "")
	doc := pdfFile(t, hs)
	installThumbnailer(t, hs, `while [ $# -gt 1 ]; do shift; done; cp PNG "$1"`)
	got := hs.quicklookOn(t, filepath.Dir(doc), "paper.pdf")
	var imgs []*widget.Image
	hs.run(func() {
		lay(hs.cfg.Root, 1400, 1000)
		imgs = treetest.All[*widget.Image](hs.c.ui.ql.visual)
	})
	if len(imgs) != 1 {
		t.Fatalf("Quick Look on a PDF shows %d images", len(imgs))
	}
	if b := imgs[0].Bounds(); 3*b.W != 2*b.H {
		t.Errorf("the 200x300 page shows at %dx%d, want 2:3", b.W, b.H)
	}
	hs.click(t, "Cancel")
	answer(t, got)
}
