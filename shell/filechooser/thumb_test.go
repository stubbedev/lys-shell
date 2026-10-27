package filechooser

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"golang.org/x/image/draw"

	"github.com/stubbedev/lys-shell/shell/treetest"
)

// photoFolder is a folder of n small PNG photos in the harness's tree,
// and their paths.
func photoFolder(t *testing.T, hs *harness, n int) (string, []string) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := filepath.Join(hs.root, "photos")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 40, 30))); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, n)
	for i := range paths {
		paths[i] = filepath.Join(dir, string(rune('a'+i))+".png")
		if err := os.WriteFile(paths[i], buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, paths
}

// scrollGrid builds every grid cell, as scrolling through them does.
func (hs *harness) scrollGrid() {
	hs.do(func() {
		grid := rows{hs.c, true}
		for i := range grid.Len() {
			grid.Row(i)
		}
	})
}

// thumbIDs are the identities of the request's thumbnails found.
func (hs *harness) thumbIDs() []render.ImageID {
	var ids []render.ImageID
	hs.run(func() {
		for _, r := range hs.c.thumbs.found {
			if r != nil {
				ids = append(ids, r.image.ID)
			}
		}
	})
	slices.Sort(ids)
	return ids
}

// Closing the dialog frees every thumbnail it found: the renderer
// forgets each one and its pixels go back; while it is open none is.
// Opening it again makes them anew.
func TestClosingFreesEveryThumbnailAndReopeningMakesThemAgain(t *testing.T) {
	hs := newHarness(t, "")
	dir, paths := photoFolder(t, hs, 3)
	open := func() []render.ImageID {
		hs.open(t, OpenRequest{CurrentFolder: dir})
		hs.scrollGrid()
		for _, p := range paths {
			hs.waitThumb(t, p, gridIconPx)
		}
		ids := hs.thumbIDs()
		if len(ids) != len(paths) {
			t.Fatalf("%d thumbnails found, want %d", len(ids), len(paths))
		}
		if live := hs.c.thumbnailer.mem.Stats().Live; live != len(paths) {
			t.Errorf("%d thumbnails allocated while the dialog is open, want %d", live, len(paths))
		}
		if len(hs.forgotten) != 0 {
			t.Errorf("the open dialog's thumbnails %v were forgotten", hs.forgotten)
		}
		return ids
	}
	closeDialog := func(ids []render.ImageID) {
		hs.do(func() { hs.c.finish(nil) })
		hs.run(func() {
			if live := hs.c.thumbnailer.mem.Stats().Live; live != 0 {
				t.Errorf("%d thumbnails still allocated after the dialog closed", live)
			}
			if len(hs.c.thumbs.found) != 0 || len(hs.c.thumbs.waiting) != 0 {
				t.Errorf("the closed dialog still holds %d thumbnails and %d lookups", len(hs.c.thumbs.found), len(hs.c.thumbs.waiting))
			}
			got := slices.Sorted(slices.Values(hs.forgotten))
			if !slices.Equal(got, ids) {
				t.Errorf("forgot %v, want every thumbnail %v", got, ids)
			}
			hs.forgotten = nil
		})
	}
	first := open()
	closeDialog(first)
	again := open()
	if slices.ContainsFunc(again, func(id render.ImageID) bool { return slices.Contains(first, id) }) {
		t.Errorf("the reopened dialog draws the freed thumbnails %v again", first)
	}
	cells := 0
	hs.run(func() {
		grid := rows{hs.c, true}
		for i := range grid.Len() {
			for _, img := range treetest.All[*widget.Image](grid.Row(i)) {
				if img.Loaded() {
					cells++
				}
			}
		}
	})
	if cells != len(paths) {
		t.Errorf("%d cells draw a thumbnail after reopening, want %d", cells, len(paths))
	}
	closeDialog(again)
}

// A lookup still waiting when the dialog closes gives up and lands
// nothing; one that finished as it closed is freed instead of shown.
func TestClosingCancelsThumbnailsInFlight(t *testing.T) {
	hs := newHarness(t, "")
	dir, paths := photoFolder(t, hs, 3)
	th := hs.c.thumbnailer
	release, err := th.budget.acquire(context.Background(), th.budget.total)
	if err != nil {
		t.Fatal(err)
	}
	hs.open(t, OpenRequest{CurrentFolder: dir})
	hs.scrollGrid()
	waitFor(t, "every lookup to wait for the budget", func() bool {
		th.budget.mu.Lock()
		defer th.budget.mu.Unlock()
		return len(th.budget.waiters) == len(paths)
	})
	hs.do(func() { hs.c.finish(nil) })
	waitFor(t, "the lookups to stop waiting", func() bool {
		th.budget.mu.Lock()
		defer th.budget.mu.Unlock()
		return len(th.budget.waiters) == 0
	})
	release()
	waitFor(t, "the lookups to end", func() bool {
		th.budget.mu.Lock()
		defer th.budget.mu.Unlock()
		return len(th.budget.waiters) == 0 && th.budget.free == th.budget.total && len(th.workers) == 0
	})
	hs.flush()
	hs.run(func() {
		if n := len(hs.c.thumbs.found); n != 0 {
			t.Errorf("%d cancelled lookups landed", n)
		}
		if live := th.mem.Stats().Live; live != 0 {
			t.Errorf("%d thumbnails allocated after the cancelled lookups", live)
		}
	})

	late, err := th.thumbnail(context.Background(), paths[0], 2*gridIconPx)
	if err != nil {
		t.Fatal(err)
	}
	hs.run(func() {
		hs.c.landThumb(hs.c.thumbs, thumbKey{paths[0], gridIconPx}, late, nil)
		if len(hs.c.thumbs.found) != 0 {
			t.Error("a thumbnail that finished after its dialog closed landed")
		}
		if live := th.mem.Stats().Live; live != 0 {
			t.Errorf("the late thumbnail is still allocated (%d)", live)
		}
	})
}

// Thumbnails made after the loop ended land nowhere: Close frees them
// with the ones the dialog found, leaving no allocation behind.
func TestCloseFreesThumbnailsMadeAfterTheLoopEnded(t *testing.T) {
	hs := newHarness(t, "")
	dir, paths := photoFolder(t, hs, 3)
	th := hs.c.thumbnailer
	release, err := th.budget.acquire(context.Background(), th.budget.total)
	if err != nil {
		t.Fatal(err)
	}
	hs.open(t, OpenRequest{CurrentFolder: dir})
	hs.scrollGrid()
	waitFor(t, "every lookup to wait for the budget", func() bool {
		th.budget.mu.Lock()
		defer th.budget.mu.Unlock()
		return len(th.budget.waiters) == len(paths)
	})
	hs.endLoop()
	release()
	waitFor(t, "the lookups to end", func() bool {
		th.budget.mu.Lock()
		defer th.budget.mu.Unlock()
		return len(th.budget.waiters) == 0 && th.budget.free == th.budget.total && len(th.workers) == 0
	})
	if live := th.mem.Stats().Live; live != len(paths) {
		t.Fatalf("%d thumbnails allocated after the loop ended, want %d", live, len(paths))
	}
	hs.c.Close()
	if live := th.mem.Stats().Live; live != 0 {
		t.Errorf("%d thumbnails still allocated after Close", live)
	}
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(treetest.Liveness); !done(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// The budget never hands out more than its total at once, grants a
// request past the total alone, and a request that gives up waiting
// lets the ones behind it through.
func TestByteBudgetBoundsWhatIsHeld(t *testing.T) {
	const total = 100
	b := newByteBudget(total)
	var held, peak atomic.Int64
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Go(func() {
			n := []int64{10, 30, 60, 100, 250}[i%5]
			release, err := b.acquire(context.Background(), n)
			if err != nil {
				t.Error(err)
				return
			}
			now := held.Add(min(n, total))
			for p := peak.Load(); now > p && !peak.CompareAndSwap(p, now); p = peak.Load() {
			}
			time.Sleep(100 * time.Microsecond)
			held.Add(-min(n, total))
			release()
		})
	}
	wg.Wait()
	if p := peak.Load(); p > total || p == 0 {
		t.Errorf("held at most %d bytes of a %d budget", p, total)
	}

	release, err := b.acquire(context.Background(), 70)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	gaveUp := make(chan error, 1)
	go func() {
		_, err := b.acquire(ctx, 50)
		gaveUp <- err
	}()
	waitFor(t, "the request to queue", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.waiters) == 1
	})
	behind := make(chan struct{})
	go func() {
		r, err := b.acquire(context.Background(), 20)
		if err != nil {
			t.Error(err)
		}
		r()
		close(behind)
	}()
	select {
	case <-behind:
		t.Fatal("a request overtook the one queued before it")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	if err := <-gaveUp; !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled wait = %v, want context.Canceled", err)
	}
	select {
	case <-behind:
	case <-time.After(treetest.Liveness):
		t.Fatal("the request behind a cancelled one never got its bytes")
	}
	release()
	if b.free != total {
		t.Errorf("%d of %d bytes free once everything was given back", b.free, total)
	}
}

// A photo's thumbnail is the centred square of the full decode scaled
// down, pixel for pixel as the chooser drew it before the thumbnails
// moved off the heap.
func TestThumbnailMatchesTheFullDecode(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	src := image.NewRGBA(image.Rect(0, 0, 600, 400))
	for y := range 400 {
		for x := range 600 {
			src.SetRGBA(x, y, color.RGBA{uint8(x * 255 / 600), uint8(y * 255 / 400), uint8((x + y) % 256), 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	want := image.NewRGBA(image.Rect(0, 0, 96, 96))
	draw.ApproxBiLinear.Scale(want, want.Bounds(), decoded, image.Rect(100, 0, 500, 400), draw.Src, nil)

	th := newThumbnailer()
	got, err := th.thumbnail(context.Background(), path, 96)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := th.free([]thumbRaster{got}); err != nil {
			t.Error(err)
		}
	}()
	if got.image.ID == 0 {
		t.Error("the thumbnail has no identity to forget it by")
	}
	if !bytes.Equal(got.image.Pix, want.Pix) || got.image.Rect != want.Rect {
		t.Error("the thumbnail differs from the full decode's centred square")
	}
}
