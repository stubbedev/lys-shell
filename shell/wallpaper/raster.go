package wallpaper

import (
	"image"
	"log"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/memory-go"

	"github.com/stubbedev/lys-shell/internal/offheap"
	"github.com/stubbedev/lys-shell/service/wallpaper"
)

// rasterKey is what a raster is made for: one file at one fit on one
// device box. Outputs asking for the same key share one raster.
type rasterKey struct {
	path string
	fit  wallpaper.FitMode
	box  target
	// known is false while the output's size is unreported: the whole
	// image, scaled by the widget.
	known bool
}

// raster is a wallpaper ready to draw, its pixels off the Go heap, and
// the number of surfaces that hold it.
type raster struct {
	key   rasterKey
	image render.StillImage
	scale widget.ImageScale
	pix   memory.Slice[byte]
	refs  int
}

// size is the raster's pixel size.
func (r *raster) size() image.Point { return r.image.Rect.Size() }

// rasterDeps are the effects the rasters run through.
type rasterDeps struct {
	// decode reads an image file.
	decode func(path string) (image.Image, error)
	// spawn runs work off the loop.
	spawn func(func())
	// invoke runs fn on the loop.
	invoke func(fn func())
	// forget drops the renderer's copy of a raster nothing draws any
	// more, before its pixels are freed.
	forget func(render.ImageID)
}

// rasters makes, shares and frees the wallpaper rasters. A file is
// decoded once for every key asked of it while the decode runs, each
// key is made once while anyone waits for it, and a raster lives while
// a surface holds it. Its methods run on the loop goroutine.
type rasters struct {
	d   rasterDeps
	mem memory.Shared
	// live are the rasters held now.
	live map[rasterKey]*raster
	// waiting are the keys being made and who waits for each.
	waiting map[rasterKey][]func(*raster)
	// decoding are the files being decoded and the keys to make from
	// each.
	decoding map[string][]rasterKey
}

func newRasters(d rasterDeps) *rasters {
	return &rasters{
		d:        d,
		live:     map[rasterKey]*raster{},
		waiting:  map[rasterKey][]func(*raster){},
		decoding: map[string][]rasterKey{},
	}
}

// acquire hands done the raster for key, on the loop, once it is made:
// at once when it is live. done holds one reference, which it gives
// back with release, whether it shows the raster or not. A file that
// cannot be decoded or rasterized is logged and done is never called.
func (rs *rasters) acquire(key rasterKey, done func(*raster)) {
	if r, ok := rs.live[key]; ok {
		r.refs++
		done(r)
		return
	}
	if waiters, ok := rs.waiting[key]; ok {
		rs.waiting[key] = append(waiters, done)
		return
	}
	rs.waiting[key] = []func(*raster){done}
	if keys, ok := rs.decoding[key.path]; ok {
		rs.decoding[key.path] = append(keys, key)
		return
	}
	rs.decoding[key.path] = []rasterKey{key}
	rs.d.spawn(func() {
		img, err := rs.d.decode(key.path)
		rs.d.invoke(func() { rs.decoded(key.path, img, err) })
	})
}

// decoded makes every key asked of path from its decoded image, each
// off the loop.
func (rs *rasters) decoded(path string, img image.Image, err error) {
	keys := rs.decoding[path]
	delete(rs.decoding, path)
	if err != nil {
		log.Printf("wallpaper: cannot decode wallpaper %s: %v", path, err)
		for _, key := range keys {
			delete(rs.waiting, key)
		}
		return
	}
	for _, key := range keys {
		rs.d.spawn(func() {
			r, err := rasterize(&rs.mem, img, key.fit, key.box, key.known)
			rs.d.invoke(func() { rs.made(key, r, err) })
		})
	}
}

// made hands a new raster to everyone waiting for its key.
func (rs *rasters) made(key rasterKey, r *raster, err error) {
	waiters := rs.waiting[key]
	delete(rs.waiting, key)
	if err != nil {
		log.Printf("wallpaper: cannot prepare wallpaper %s: %v", key.path, err)
		return
	}
	r.key, r.refs = key, len(waiters)
	rs.live[key] = r
	for _, done := range waiters {
		done(r)
	}
}

// release gives back a reference acquire handed out. The last one
// frees the raster: its holders no longer draw it, so the renderer
// forgets its texture, and its pixels are freed once every List
// recorded until now has ended.
func (rs *rasters) release(r *raster) {
	if r.refs <= 0 {
		panic("wallpaper: release of a raster nobody holds")
	}
	r.refs--
	if r.refs > 0 {
		return
	}
	delete(rs.live, r.key)
	rs.d.forget(r.image.ID)
	render.Retire(func() {
		if err := offheap.Free(&rs.mem, r.pix); err != nil {
			log.Printf("wallpaper: freeing the %v raster of %s: %v", r.size(), r.key.path, err)
		}
	})
}
