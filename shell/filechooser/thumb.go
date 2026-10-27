package filechooser

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // the thumbnail spec names files by the URI's MD5
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/memory-go"
	"golang.org/x/image/draw"

	"github.com/stubbedev/lys-shell/internal/fileuri"
	"github.com/stubbedev/lys-shell/internal/handoff"
	"github.com/stubbedev/lys-shell/internal/imagedecode"
	"github.com/stubbedev/lys-shell/internal/offheap"
	"github.com/stubbedev/lys-shell/internal/xdg"
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

// errNoThumb is a file with no cached thumbnail that is not a raster
// image to decode.
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
// the loop has not taken yet.
type thumbnailer struct {
	mem     memory.Shared
	work    handoff.Handoffs
	workers chan struct{}
	budget  *byteBudget
}

func newThumbnailer() *thumbnailer {
	return &thumbnailer{workers: make(chan struct{}, thumbWorkers), budget: newByteBudget(thumbDecodeBudget)}
}

// thumbnail is the file's thumbnail as an exact px square,
// centre-cropped to fill (every thumbnail is the same size, so rows
// align whatever the source's aspect ratio): the one the freedesktop
// cache holds for it, which file managers and viewers make for PDFs
// and videos too, else a raster image decoded. It waits its turn among
// the workers and for its decode's bytes in the budget, and gives up
// with ctx's error once ctx is done. The caller frees the result.
func (th *thumbnailer) thumbnail(ctx context.Context, path string, px int) (thumbRaster, error) {
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
	if src, err := cachedThumbnail(path, info, px); err == nil {
		return th.squareCrop(src, px)
	}
	if !isRaster(path) {
		return thumbRaster{}, errNoThumb
	}
	if info.Size() > thumbMaxBytes {
		return thumbRaster{}, errThumbTooLarge
	}
	need, err := imagedecode.DecodeBytes(path)
	if err != nil {
		return thumbRaster{}, err
	}
	release, err := th.budget.acquire(ctx, need)
	if err != nil {
		return thumbRaster{}, err
	}
	defer release()
	src, err := imagedecode.OpenContext(ctx, path)
	if err != nil {
		return thumbRaster{}, err
	}
	return th.squareCrop(src, px)
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

// thumbSizes are the cache's size directories and their edge, smallest
// first.
var thumbSizes = []struct {
	dir string
	px  int
}{{"normal", 128}, {"large", 256}, {"x-large", 512}, {"xx-large", 1024}}

// cachedThumbnail is the freedesktop thumbnail cache's image for the
// file: the smallest size at least px, else the largest below it, named
// by the MD5 of its URI, and only while its Thumb::MTime matches the
// file's modification time (a stale one is no thumbnail).
func cachedThumbnail(path string, info os.FileInfo, px int) (image.Image, error) {
	cache, ok := xdg.CacheHome()
	if !ok {
		return nil, errNoThumb
	}
	root := filepath.Join(cache, "thumbnails")
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	sum := md5.Sum([]byte(fileuri.FromPath(abs))) //nolint:gosec // the spec's naming, not a security use
	name := hex.EncodeToString(sum[:]) + ".png"
	start := 0
	for start < len(thumbSizes)-1 && thumbSizes[start].px < px {
		start++
	}
	order := append([]int{}, start)
	for i := start + 1; i < len(thumbSizes); i++ {
		order = append(order, i)
	}
	for i := start - 1; i >= 0; i-- {
		order = append(order, i)
	}
	mtime := strconv.FormatInt(info.ModTime().Unix(), 10)
	for _, i := range order {
		data, err := os.ReadFile(filepath.Join(root, thumbSizes[i].dir, name)) //nolint:gosec // the user's thumbnail cache
		if err != nil {
			continue
		}
		if pngText(data, "Thumb::MTime") != mtime {
			continue
		}
		if img, err := png.Decode(bytes.NewReader(data)); err == nil {
			return img, nil
		}
	}
	return nil, errNoThumb
}

// pngText is the value of a PNG's tEXt chunk keyed key, "" without one.
func pngText(data []byte, key string) string {
	const signature = "\x89PNG\r\n\x1a\n"
	if len(data) < len(signature) || string(data[:len(signature)]) != signature {
		return ""
	}
	for p := len(signature); p+8 <= len(data); {
		length := int(binary.BigEndian.Uint32(data[p:]))
		kind := string(data[p+4 : p+8])
		body := p + 8
		if length < 0 || body+length > len(data) {
			return ""
		}
		if kind == "tEXt" {
			k, v, ok := bytes.Cut(data[body:body+length], []byte{0})
			if ok && string(k) == key {
				return string(v)
			}
		}
		if kind == "IEND" {
			return ""
		}
		p = body + length + 4
	}
	return ""
}
