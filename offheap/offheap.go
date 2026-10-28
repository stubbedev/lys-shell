// Package offheap holds large buffers (rasters, clipboard payloads) in
// memory-go memory instead of on the Go heap, so their pages go back
// to the system the moment their owner frees them rather than
// lingering as garbage that raises the heap goal.
package offheap

import (
	"errors"
	"fmt"
	"image"
	"math"

	"github.com/stubbedev/memory-go"
)

// NewRGBA is a zeroed w x h image whose pixels mem holds, and the
// slice to free them by. Both sides are positive.
func NewRGBA(mem *memory.Shared, w, h int) (*image.RGBA, memory.Slice[byte], error) {
	if w <= 0 || h <= 0 || w > math.MaxInt/4/h {
		return nil, memory.Slice[byte]{}, fmt.Errorf("cannot allocate a %dx%d raster: no such size", w, h)
	}
	pix, err := memory.MakeSlice[byte](mem, 4*w*h)
	if err != nil {
		return nil, pix, fmt.Errorf("cannot allocate a %dx%d raster: %w", w, h, err)
	}
	return &image.RGBA{Pix: pix.Elems(), Stride: 4 * w, Rect: image.Rect(0, 0, w, h)}, pix, nil
}

// Clone copies src into memory mem holds, and returns the slice to
// free it by. src is not empty.
func Clone(mem *memory.Shared, src []byte) (memory.Slice[byte], error) {
	if len(src) == 0 {
		return memory.Slice[byte]{}, errors.New("cannot hold an empty buffer off the heap")
	}
	buf, err := memory.MakeSlice[byte](mem, len(src))
	if err != nil {
		return buf, fmt.Errorf("cannot hold %d bytes off the heap: %w", len(src), err)
	}
	copy(buf.Elems(), src)
	return buf, nil
}

// Free frees buffers mem holds and hands the pages back to the system
// at once: the allocator otherwise keeps the newest region of each
// size committed for reuse, a whole buffer's worth of resident memory.
// Nothing may read the buffers any more.
func Free(mem *memory.Shared, bufs ...memory.Slice[byte]) error {
	var errs []error
	for _, b := range bufs {
		if err := memory.FreeSlice(mem, b); err != nil {
			errs = append(errs, fmt.Errorf("freeing %d bytes: %w", b.Len(), err))
		}
	}
	if err := mem.Trim(); err != nil {
		errs = append(errs, fmt.Errorf("returning freed pages: %w", err))
	}
	return errors.Join(errs...)
}
