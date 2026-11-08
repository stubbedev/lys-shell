package filechooser

import (
	"context"
	"errors"
	"image"
	"log"
	"os"
	"sync"
	"time"

	"github.com/stubbedev/lys-desktop/imagedecode"
	"github.com/stubbedev/lys-desktop/offheap"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/memory-go"
	"golang.org/x/image/draw"

	"github.com/stubbedev/lys-shell/internal/handoff"
	"github.com/stubbedev/lys-shell/internal/mime"
)

// thumbMaxBytes skips images too large to decode for a row icon.
const thumbMaxBytes = 32 << 20

// thumbWorkers is how many thumbnails are made at once, and
// thumbDecodeBudget how many bytes of source pixels their decodes hold
// at once: two 24-megapixel JPEGs, or one progressive one.
const (
	thumbWorkers      = 4
	thumbDecodeBudget = 96 << 20
)

// errThumbTooLarge is a source past thumbMaxBytes.
var errThumbTooLarge = errors.New("filechooser: image too large to thumbnail")

// errNoThumb is a file with no cached thumbnail, nothing to make one
// with, or a fresh record that making one failed.
var errNoThumb = errors.New("filechooser: no thumbnail")

// thumbRaster is a thumbnail whose pixels mem holds, drawn by its
// identity; pix frees them.
type thumbRaster struct {
	image render.StillImage
	pix   memory.Slice[byte]
}

// thumbnailer makes thumbnails off the loop for every dialog: at most
// thumbWorkers at once, their decodes within budget, into rasters mem
// holds. work is the lookups running and the thumbnails they made that
// the loop has not taken yet. decode opens a raster image, store writes
// a cache file whole, and the installed thumbnailers (loaded on first
// use) run sandboxed for at most runTimeout each.
type thumbnailer struct {
	mem        memory.Shared
	work       handoff.Handoffs
	workers    chan struct{}
	budget     *byteBudget
	decode     func(ctx context.Context, path string) (image.Image, error)
	store      func(path string, data []byte) error
	runTimeout time.Duration
	installed  func() installedThumbnailers
}

// installedThumbnailers are the thumbnailers per MIME type and the
// sandbox they run in.
type installedThumbnailers struct {
	sandbox sandbox
	byType  map[string]thumbnailerEntry
}

func newThumbnailer() *thumbnailer {
	return &thumbnailer{
		workers: make(chan struct{}, thumbWorkers), budget: newByteBudget(thumbDecodeBudget),
		decode: imagedecode.OpenContext, store: storeThumbnail, runTimeout: thumbnailerTimeout,
		installed: sync.OnceValue(func() installedThumbnailers { return loadInstalled(mime.DataDirs()) }),
	}
}

// loadInstalled is the thumbnailers installed in dataDirs and their
// sandbox; without a sandbox none run, which is logged.
func loadInstalled(dataDirs []string) installedThumbnailers {
	sb, err := newSandbox(context.Background())
	if err != nil {
		log.Printf("file chooser: installed thumbnailers are not run without a sandbox: %v", err)
		return installedThumbnailers{}
	}
	return installedThumbnailers{sandbox: sb, byType: loadThumbnailers(dataDirs)}
}

// thumbnail is the file's thumbnail as an exact px square,
// centre-cropped to fill (every thumbnail is the same size, so rows
// align whatever the source's aspect ratio). It is the one the
// freedesktop cache holds for it; else, unless a fresh fail entry says
// making one failed before, a raster image decoded or the output of the
// thumbnailer installed for mimeType, written to the cache for every
// app and the next dialog. It waits its turn among the workers and for
// its decode's bytes in the budget, and gives up with ctx's error once
// ctx is done. The caller frees the result.
func (th *thumbnailer) thumbnail(ctx context.Context, path, mimeType string, px int) (thumbRaster, error) {
	var out thumbRaster
	err := th.withWorker(ctx, func() error {
		src, err := th.source(ctx, path, mimeType, px)
		if err == nil {
			out, err = th.squareCrop(src, px)
		}
		return err
	})
	return out, err
}

// preview is the file's image fitted into a w x h box, its aspect kept
// and never scaled up, and the source's pixel size: a raster image
// decoded whole within the budget, else the large thumbnail the cache
// holds or a thumbnailer makes (whose source size is not known). The
// caller frees the result.
func (th *thumbnailer) preview(ctx context.Context, path, mimeType string, w, h int) (thumbRaster, image.Point, error) {
	var out thumbRaster
	var size image.Point
	err := th.withWorker(ctx, func() error {
		var src image.Image
		var err error
		if isRaster(path) {
			var info os.FileInfo
			if info, err = os.Stat(path); err == nil {
				src, err = th.decodeRaster(ctx, path, info)
			}
			if err == nil {
				size = src.Bounds().Size()
			}
		} else {
			src, err = th.source(ctx, path, mimeType, flavorFor(max(w, h)).px)
		}
		if err == nil {
			out, err = th.fitRaster(src, w, h)
		}
		return err
	})
	return out, size, err
}

// withWorker runs fn once a worker is free, or gives up with ctx's
// error once ctx is done.
func (th *thumbnailer) withWorker(ctx context.Context, fn func() error) error {
	select {
	case th.workers <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-th.workers }()
	return fn()
}

// source is the image a thumbnail of px is made from: the one the
// cache holds, else, unless a fresh fail entry says making one failed
// before, a raster image decoded or the output of the thumbnailer
// installed for mimeType, written to the cache fitted to px's size.
func (th *thumbnailer) source(ctx context.Context, path, mimeType string, px int) (image.Image, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	tc, err := cacheFor(path, info)
	if err != nil {
		return nil, err
	}
	if src, err := tc.lookup(px); err == nil {
		return src, nil
	}
	if tc.failed() {
		return nil, errNoThumb
	}
	var src image.Image
	installed := th.installed()
	switch entry, ok := installed.byType[mimeType]; {
	case isRaster(path):
		src, err = th.decodeRaster(ctx, path, info)
	case ok:
		src, err = entry.run(ctx, installed.sandbox, path, flavorFor(px).px, th.runTimeout)
	default:
		return nil, errNoThumb
	}
	if err != nil {
		if ctx.Err() == nil && !errors.Is(err, errThumbTooLarge) {
			th.writeFail(tc)
		}
		return nil, err
	}
	if tc.root != "" {
		if err := th.cache(tc, src, flavorFor(px)); err != nil {
			log.Printf("file chooser: caching the thumbnail of %s: %v", tc.uri, err)
		}
	}
	return src, nil
}

// decodeRaster decodes a raster image within the byte budget.
func (th *thumbnailer) decodeRaster(ctx context.Context, path string, info os.FileInfo) (image.Image, error) {
	if info.Size() > thumbMaxBytes {
		return nil, errThumbTooLarge
	}
	need, err := imagedecode.DecodeBytes(path)
	if err != nil {
		return nil, err
	}
	release, err := th.budget.acquire(ctx, need)
	if err != nil {
		return nil, err
	}
	defer release()
	return th.decode(ctx, path)
}

// cache writes src, fitted to flavor, as the file's thumbnail there.
func (th *thumbnailer) cache(tc thumbCache, src image.Image, flavor thumbSize) error {
	if b := src.Bounds(); b.Dx() <= flavor.px && b.Dy() <= flavor.px {
		return th.encodeAndStore(tc, src, src.Bounds(), flavor)
	}
	fitted, err := th.fitRaster(src, flavor.px, flavor.px)
	if err != nil {
		return err
	}
	err = th.encodeAndStore(tc, fitted.image, src.Bounds(), flavor)
	return errors.Join(err, th.free([]thumbRaster{fitted}))
}

func (th *thumbnailer) encodeAndStore(tc thumbCache, img image.Image, source image.Rectangle, flavor thumbSize) error {
	data, err := tc.encode(img, source)
	if err != nil {
		return err
	}
	return th.store(tc.path(flavor.dir), data)
}

// fitSize is the size b scales to inside a w x h box, its aspect kept
// and never larger than b.
func fitSize(b image.Rectangle, w, h int) image.Point {
	if b.Dx() <= w && b.Dy() <= h {
		return b.Size()
	}
	if b.Dx()*h >= b.Dy()*w {
		return image.Pt(w, max(1, b.Dy()*w/b.Dx()))
	}
	return image.Pt(max(1, b.Dx()*h/b.Dy()), h)
}

// fitRaster is src scaled into a w x h box as fitSize says.
func (th *thumbnailer) fitRaster(src image.Image, w, h int) (thumbRaster, error) {
	size := fitSize(src.Bounds(), w, h)
	dst, pix, err := offheap.NewRGBA(&th.mem, size.X, size.Y)
	if err != nil {
		return thumbRaster{}, err
	}
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	return thumbRaster{image: render.NewStillImage(dst), pix: pix}, nil
}

// writeFail records that no thumbnail could be made for the file as
// it is now, so no dialog tries again until it changes.
func (th *thumbnailer) writeFail(tc thumbCache) {
	if tc.root == "" {
		return
	}
	data, err := tc.encode(image.NewRGBA(image.Rect(0, 0, 1, 1)), image.Rectangle{})
	if err == nil {
		err = th.store(tc.failPath(), data)
	}
	if err != nil {
		log.Printf("file chooser: recording the failed thumbnail of %s: %v", tc.uri, err)
	}
}

// squareCrop scales the largest centred square of src to px.
func (th *thumbnailer) squareCrop(src image.Image, px int) (thumbRaster, error) {
	dst, pix, err := offheap.NewRGBA(&th.mem, px, px)
	if err != nil {
		return thumbRaster{}, err
	}
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	crop := image.Rect(0, 0, side, side).Add(b.Min).Add(image.Pt((b.Dx()-side)/2, (b.Dy()-side)/2))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, crop, draw.Src, nil)
	return thumbRaster{image: render.NewStillImage(dst), pix: pix}, nil
}

// free frees the thumbnails' pixels; nothing may draw them any more.
func (th *thumbnailer) free(rasters []thumbRaster) error {
	pix := make([]memory.Slice[byte], len(rasters))
	for i, r := range rasters {
		pix[i] = r.pix
	}
	return offheap.Free(&th.mem, pix...)
}

// byteBudget hands out bytes of a fixed total, first come first
// served: a request waits until everything before it was granted and
// its bytes are free. A request past the total is granted the whole
// total, so it runs alone rather than never.
type byteBudget struct {
	mu      sync.Mutex
	free    int64
	total   int64
	waiters []*budgetWaiter
}

type budgetWaiter struct {
	n     int64
	ready chan struct{}
}

func newByteBudget(total int64) *byteBudget {
	return &byteBudget{free: total, total: total}
}

// acquire waits for n bytes, or until ctx is done, and returns what
// gives them back.
func (b *byteBudget) acquire(ctx context.Context, n int64) (func(), error) {
	n = min(max(n, 0), b.total)
	release := func() { b.give(n) }
	b.mu.Lock()
	if len(b.waiters) == 0 && b.free >= n {
		b.free -= n
		b.mu.Unlock()
		return release, nil
	}
	w := &budgetWaiter{n: n, ready: make(chan struct{})}
	b.waiters = append(b.waiters, w)
	b.mu.Unlock()
	select {
	case <-w.ready:
		return release, nil
	case <-ctx.Done():
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	select {
	case <-w.ready:
		b.free += n
	default:
		for i, other := range b.waiters {
			if other == w {
				b.waiters = append(b.waiters[:i], b.waiters[i+1:]...)
				break
			}
		}
	}
	b.grant()
	return nil, ctx.Err()
}

func (b *byteBudget) give(n int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.free += n
	b.grant()
}

// grant wakes the waiters at the front whose bytes are free.
func (b *byteBudget) grant() {
	for len(b.waiters) > 0 && b.free >= b.waiters[0].n {
		w := b.waiters[0]
		b.waiters = b.waiters[1:]
		b.free -= w.n
		close(w.ready)
	}
}
