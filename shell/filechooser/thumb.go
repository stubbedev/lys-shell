package filechooser

import (
	"context"
	"errors"
	"image"
	"log"
	"os"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/memory-go"
	"golang.org/x/image/draw"

	"github.com/stubbedev/lys-shell/imagedecode"
	"github.com/stubbedev/lys-shell/internal/handoff"
	"github.com/stubbedev/lys-shell/internal/mime"
	"github.com/stubbedev/lys-shell/offheap"
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
// use) run for at most runTimeout each.
type thumbnailer struct {
	mem        memory.Shared
	work       handoff.Handoffs
	workers    chan struct{}
	budget     *byteBudget
	decode     func(ctx context.Context, path string) (image.Image, error)
	store      func(path string, data []byte) error
	runTimeout time.Duration
	installed  func() map[string]thumbnailerEntry
}

func newThumbnailer() *thumbnailer {
	return &thumbnailer{
		workers: make(chan struct{}, thumbWorkers), budget: newByteBudget(thumbDecodeBudget),
		decode: imagedecode.OpenContext, store: storeThumbnail, runTimeout: thumbnailerTimeout,
		installed: sync.OnceValue(func() map[string]thumbnailerEntry { return loadThumbnailers(mime.DataDirs()) }),
	}
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
	select {
	case th.workers <- struct{}{}:
	case <-ctx.Done():
		return thumbRaster{}, ctx.Err()
	}
	defer func() { <-th.workers }()
	info, err := os.Stat(path)
	if err != nil {
		return thumbRaster{}, err
	}
	tc, err := cacheFor(path, info)
	if err != nil {
		return thumbRaster{}, err
	}
	if src, err := tc.lookup(px); err == nil {
		return th.squareCrop(src, px)
	}
	if tc.failed() {
		return thumbRaster{}, errNoThumb
	}
	var src image.Image
	switch entry, ok := th.installed()[mimeType]; {
	case isRaster(path):
		src, err = th.decodeRaster(ctx, path, info)
	case ok:
		src, err = entry.run(ctx, path, tc.uri, flavorFor(px).px, th.runTimeout)
	default:
		return thumbRaster{}, errNoThumb
	}
	if err != nil {
		if ctx.Err() == nil && !errors.Is(err, errThumbTooLarge) {
			th.writeFail(tc)
		}
		return thumbRaster{}, err
	}
	return th.cacheAndCrop(tc, src, px)
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

// cacheAndCrop writes src, fitted to the cache size px falls in, to
// the cache, and crops the row's square from src itself. A cache that
// cannot be written still shows the thumbnail.
func (th *thumbnailer) cacheAndCrop(tc thumbCache, src image.Image, px int) (thumbRaster, error) {
	if tc.root != "" {
		if err := th.cache(tc, src, flavorFor(px)); err != nil {
			log.Printf("file chooser: caching the thumbnail of %s: %v", tc.uri, err)
		}
	}
	return th.squareCrop(src, px)
}

// cache writes src, fitted to flavor, as the file's thumbnail there.
func (th *thumbnailer) cache(tc thumbCache, src image.Image, flavor thumbSize) error {
	fitted, pix, err := th.fit(src, flavor.px)
	if err != nil {
		return err
	}
	data, err := tc.encode(fitted, src.Bounds())
	if ferr := offheap.Free(&th.mem, pix...); ferr != nil {
		err = errors.Join(err, ferr)
	}
	if err != nil {
		return err
	}
	return th.store(tc.path(flavor.dir), data)
}

// fit is src scaled to fit an edge square, kept as it is when it fits
// already (the spec never scales up); pix holds a scaled copy's pixels.
func (th *thumbnailer) fit(src image.Image, edge int) (image.Image, []memory.Slice[byte], error) {
	b := src.Bounds()
	if b.Dx() <= edge && b.Dy() <= edge {
		return src, nil, nil
	}
	w, h := edge, max(1, b.Dy()*edge/b.Dx())
	if b.Dy() > b.Dx() {
		w, h = max(1, b.Dx()*edge/b.Dy()), edge
	}
	dst, pix, err := offheap.NewRGBA(&th.mem, w, h)
	if err != nil {
		return nil, nil, err
	}
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst, []memory.Slice[byte]{pix}, nil
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
