package imagedecode

import (
	"bufio"
	"context"
	"fmt"
	"image"
	"io"
	"os"

	"github.com/stubbedev/memory-go"

	"github.com/stubbedev/lys-shell/offheap"
	"github.com/stubbedev/lys-shell/third_party/jpeg"
)

// Decoded is a decoded image whose pixels may live off the Go heap:
// Free releases them once nothing reads Image any more.
type Decoded struct {
	Image image.Image
	mem   *memory.Shared
	pix   memory.Slice[byte]
}

// Free frees the pixels a JPEG was decoded into and hands their pages
// back to the system; an image decoded onto the Go heap is left to the
// collector.
func (d Decoded) Free() error {
	if d.pix.Len() == 0 {
		return nil
	}
	return offheap.Free(d.mem, d.pix)
}

// OpenOffHeap decodes the image file at path as Open does, a JPEG
// straight into memory mem holds: its pixels until Free, and a
// progressive scan's coefficients only while it decodes.
func OpenOffHeap(mem *memory.Shared, path string) (Decoded, error) {
	f, err := os.Open(path) //nolint:gosec // the caller's image path
	if err != nil {
		return Decoded{}, err
	}
	defer f.Close()
	br := bufio.NewReader(f)
	if isJPEG(br) {
		img, pix, err := jpeg.Decode(br, mem)
		if err != nil {
			return Decoded{}, fmt.Errorf("decoding %s: %w", path, err)
		}
		return Decoded{Image: img, mem: mem, pix: pix}, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Decoded{}, fmt.Errorf("decoding %s: %w", path, err)
	}
	img, err := decodeFile(context.Background(), f, path)
	if err != nil {
		return Decoded{}, err
	}
	return Decoded{Image: img}, nil
}

// isJPEG reports whether br starts with the JPEG start-of-image marker.
func isJPEG(br *bufio.Reader) bool {
	magic, err := br.Peek(2)
	return err == nil && magic[0] == 0xff && magic[1] == 0xd8
}
