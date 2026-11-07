package filechooser

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/internal/fileuri"
)

func goroutineID() int64 {
	var buf [64]byte
	stack := buf[:runtime.Stack(buf[:], false)]
	stack = bytes.TrimPrefix(stack, []byte("goroutine "))
	id, err := strconv.ParseInt(string(stack[:bytes.IndexByte(stack, ' ')]), 10, 64)
	if err != nil {
		panic("filechooser: unreadable goroutine header: " + err.Error())
	}
	return id
}

// storeWatch counts the thumbnailer's cache writes and the decodes it
// makes, and notes every write made on one of the loop's goroutines.
type storeWatch struct {
	decodes, stores atomic.Int32
	mu              sync.Mutex
	loop            map[int64]bool
	onLoop          int
}

func watchStores(t *testing.T, hs *harness) *storeWatch {
	t.Helper()
	w := &storeWatch{loop: map[int64]bool{goroutineID(): true}}
	queue := make(chan int64, 1)
	hs.invoke(func() { queue <- goroutineID() })
	w.loop[<-queue] = true
	th := hs.c.thumbnailer
	decode, store := th.decode, th.store
	th.decode = func(ctx context.Context, path string) (image.Image, error) {
		w.decodes.Add(1)
		return decode(ctx, path)
	}
	th.store = func(path string, data []byte) error {
		w.stores.Add(1)
		w.mu.Lock()
		if w.loop[goroutineID()] {
			w.onLoop++
		}
		w.mu.Unlock()
		return store(path, data)
	}
	return w
}

// openRows opens dir and builds every row, waiting for the lookups of
// paths to end, then cancels; it reports how many found a thumbnail.
func (hs *harness) openRows(t *testing.T, dir string, paths []string) int {
	t.Helper()
	got := hs.open(t, OpenRequest{CurrentFolder: dir})
	hs.do(func() {
		list := rows{hs.c, false}
		for i := range list.Len() {
			list.Row(i)
		}
	})
	found := 0
	for _, p := range paths {
		hs.waitThumbFor(t, p, rowIconPx, 15*time.Second)
		hs.run(func() {
			if hs.c.thumbs.found[thumbKey{p, rowIconPx}] != nil {
				found++
			}
		})
	}
	hs.click(t, "Cancel")
	answer(t, got)
	return found
}

// A folder of photos is decoded once: the thumbnails go to the
// freedesktop cache, off the loop, carrying the spec's URI and
// modification time, and the second opening reads them back.
func TestDecodedThumbnailsAreCached(t *testing.T) {
	hs := newHarness(t, "")
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	dir := filepath.Join(hs.root, "photos")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	src := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for y := range 300 {
		for x := range 400 {
			src.SetRGBA(x, y, color.RGBA{uint8(x), uint8(y), 90, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, nil); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 20)
	for i := range paths {
		paths[i] = filepath.Join(dir, "IMG_"+strconv.Itoa(4400+i)+".jpg")
		if err := os.WriteFile(paths[i], buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w := watchStores(t, hs)
	if found := hs.openRows(t, dir, paths); found != 20 {
		t.Errorf("%d photos got a thumbnail, want 20", found)
	}
	if got := w.decodes.Load(); got != 20 {
		t.Errorf("the first opening decoded %d images, want 20", got)
	}
	if found := hs.openRows(t, dir, paths); found != 20 {
		t.Errorf("%d photos got a cached thumbnail, want 20", found)
	}
	if got := w.decodes.Load(); got != 20 {
		t.Errorf("the second opening decoded %d more images, want none", got-20)
	}
	if w.onLoop != 0 || w.stores.Load() != 20 {
		t.Errorf("%d cache writes, %d of them on the loop; want 20, none on it", w.stores.Load(), w.onLoop)
	}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		tc, err := cacheFor(p, info)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(cache, "thumbnails", "normal", tc.name))
		if err != nil {
			t.Fatalf("no normal thumbnail for %s: %v", p, err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("the cached thumbnail of %s: %v", p, err)
		}
		if b := img.Bounds(); b.Dx() != 128 || b.Dy() != 96 {
			t.Errorf("the cached thumbnail of %s is %v, want 400x300 fitted to 128x96", p, b)
		}
		if got := pngText(data, "Thumb::URI"); got != fileuri.FromPath(p) {
			t.Errorf("Thumb::URI %q, want %q", got, fileuri.FromPath(p))
		}
		if got := pngText(data, "Thumb::MTime"); got != strconv.FormatInt(info.ModTime().Unix(), 10) {
			t.Errorf("Thumb::MTime %q for %v", got, info.ModTime())
		}
		if got := pngText(data, "Thumb::Size"); got != strconv.Itoa(buf.Len()) {
			t.Errorf("Thumb::Size %q, want %d", got, buf.Len())
		}
	}
	dirInfo, err := os.Stat(filepath.Join(cache, "thumbnails", "normal"))
	if err != nil || dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("the size directory is %v (%v), want 0700", dirInfo.Mode(), err)
	}
}

// thumbnailerArgv is the command line of the fake thumbnailer doing
// mode, with the field codes a .thumbnailer gives it.
func thumbnailerArgv(t *testing.T, mode string) []string {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return []string{bin, fakeThumbnailerArg, mode, "%s", "%u", "%o"}
}

// installThumbnailer installs the fake thumbnailer doing mode for PDFs
// and gives hs a thumbnailer that finds it.
func installThumbnailer(t *testing.T, hs *harness, mode string) {
	t.Helper()
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_DATA_DIRS", filepath.Join(data, "none"))
	argv := thumbnailerArgv(t, mode)
	entry := "[Thumbnailer Entry]\nTryExec=" + argv[0] + "\nExec=" + strings.Join(argv, " ") + "\nMimeType=application/pdf;\n"
	if err := os.MkdirAll(filepath.Join(data, "thumbnailers"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "thumbnailers", "fake.thumbnailer"), []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	hs.c.thumbnailer = newThumbnailer()
}

// switchThumbnailer makes the PDF thumbnailer hs runs the fake doing
// mode, in the same sandbox, between openings.
func switchThumbnailer(t *testing.T, hs *harness, mode string) {
	t.Helper()
	installed := hs.c.thumbnailer.installed()
	if installed.byType == nil {
		t.Fatal("no thumbnailer is installed to switch")
	}
	entry := thumbnailerEntry{argv: thumbnailerArgv(t, mode), file: mode + ".thumbnailer"}
	hs.c.thumbnailer.installed = func() installedThumbnailers {
		return installedThumbnailers{sandbox: installed.sandbox, byType: map[string]thumbnailerEntry{"application/pdf": entry}}
	}
}

func pdfFile(t *testing.T, hs *harness) string {
	t.Helper()
	doc := filepath.Join(hs.root, "docs", "paper.pdf")
	if err := os.WriteFile(doc, []byte("%PDF-1.4"), 0o600); err != nil {
		t.Fatal(err)
	}
	return doc
}

// A PDF gets the thumbnail its installed thumbnailer makes, in the
// sandbox, and the second opening reads it from the cache: a
// thumbnailer that now fails changes nothing.
func TestThumbnailersMakeThumbnailsOnce(t *testing.T) {
	hs := newHarness(t, "")
	doc := pdfFile(t, hs)
	installThumbnailer(t, hs, "png")
	if hs.openRows(t, filepath.Dir(doc), []string{doc}) != 1 {
		t.Fatal("the PDF row got no thumbnail")
	}
	switchThumbnailer(t, hs, "fail")
	if hs.openRows(t, filepath.Dir(doc), []string{doc}) != 1 {
		t.Error("the PDF row got no cached thumbnail")
	}
}

// A thumbnailer that hangs is killed at its timeout, and the fail
// entry it leaves stops the next opening from running one again; a
// file changed since gets another try.
func TestHangingThumbnailerFailsOnce(t *testing.T) {
	hs := newHarness(t, "")
	doc := pdfFile(t, hs)
	installThumbnailer(t, hs, "hang")
	hs.c.thumbnailer.runTimeout = 500 * time.Millisecond
	start := time.Now()
	if hs.openRows(t, filepath.Dir(doc), []string{doc}) != 0 {
		t.Error("a hung thumbnailer made a thumbnail")
	}
	if waited := time.Since(start); waited > 10*time.Second {
		t.Errorf("the hanging thumbnailer held the row for %v", waited)
	}
	switchThumbnailer(t, hs, "png")
	hs.c.thumbnailer.runTimeout = thumbnailerTimeout
	if hs.openRows(t, filepath.Dir(doc), []string{doc}) != 0 {
		t.Error("a file that failed ran a thumbnailer again")
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(doc, later, later); err != nil {
		t.Fatal(err)
	}
	if hs.openRows(t, filepath.Dir(doc), []string{doc}) != 1 {
		t.Error("a changed file got no second try")
	}
}

// Readers of a thumbnail being written see no file or a whole one,
// never part of it.
func TestThumbnailWritesAreWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thumbnails", "normal", "x.png")
	var big bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for i := range img.Pix {
		img.Pix[i] = uint8(i * 7)
	}
	if err := png.Encode(&big, img); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var partial atomic.Int32
	go func() {
		defer close(done)
		for range 200 {
			data, err := os.ReadFile(path)
			if err == nil && !bytes.Equal(data, big.Bytes()) {
				partial.Add(1)
			}
		}
	}()
	for range 50 {
		if err := storeThumbnail(path, big.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	<-done
	if partial.Load() != 0 {
		t.Errorf("%d reads saw a partial thumbnail", partial.Load())
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Errorf("the size directory holds %d entries (%v), want the thumbnail alone", len(entries), err)
	}
}
