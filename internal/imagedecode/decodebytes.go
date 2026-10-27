package imagedecode

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
)

// DecodeBytes is how many bytes decoding the image file at path
// allocates for its pixels, read from its header before any pixel is:
// the decoded raster in its own color model, and for a JPEG the
// coefficient blocks a progressive scan keeps besides.
func DecodeBytes(path string) (int64, error) {
	f, err := os.Open(path) //nolint:gosec // the caller's image path
	if err != nil {
		return 0, err
	}
	defer f.Close()
	br := bufio.NewReader(f)
	if isJPEG(br) {
		n, err := jpegDecodeBytes(br)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", path, err)
		}
		return n, nil
	}
	cfg, _, err := image.DecodeConfig(br)
	if err == nil {
		return int64(cfg.Width) * int64(cfg.Height) * bytesPerPixel(cfg.ColorModel), nil
	}
	if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
		return 0, fmt.Errorf("%s: %w", path, errors.Join(err, seekErr))
	}
	t, tgaErr := readTGAHeader(bufio.NewReader(f))
	if tgaErr != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	return int64(t.w) * int64(t.h) * int64(t.px+4), nil
}

// bytesPerPixel is what a decoded pixel of model m takes: YCbCr at its
// unsubsampled worst.
func bytesPerPixel(m color.Model) int64 {
	switch m {
	case color.GrayModel, color.AlphaModel:
		return 1
	case color.Gray16Model, color.Alpha16Model:
		return 2
	case color.YCbCrModel:
		return 3
	case color.RGBA64Model, color.NRGBA64Model:
		return 8
	}
	if _, ok := m.(color.Palette); ok {
		return 1
	}
	return 4
}

// jpegDecodeBytes reads a JPEG's frame header and sizes what Go's
// decoder allocates for it: each component's plane, padded to whole
// MCUs; an int32 per sample for a progressive scan's coefficients; and
// the RGBA or CMYK raster a 3-component non-YCbCr or 4-component image
// is converted into.
func jpegDecodeBytes(r *bufio.Reader) (int64, error) {
	if _, err := r.Discard(2); err != nil {
		return 0, err
	}
	adobeRGB := false
	for {
		var m [4]byte
		if _, err := io.ReadFull(r, m[:2]); err != nil {
			return 0, fmt.Errorf("imagedecode: JPEG before its frame header: %w", err)
		}
		for m[1] == 0xff {
			b, err := r.ReadByte()
			if err != nil {
				return 0, fmt.Errorf("imagedecode: JPEG before its frame header: %w", err)
			}
			m[1] = b
		}
		if m[0] != 0xff {
			return 0, fmt.Errorf("imagedecode: JPEG marker expected, found %#x", m[0])
		}
		if m[1] >= 0xd0 && m[1] <= 0xd7 || m[1] == 0x01 {
			continue
		}
		if _, err := io.ReadFull(r, m[2:]); err != nil {
			return 0, fmt.Errorf("imagedecode: JPEG segment length: %w", err)
		}
		n := int(binary.BigEndian.Uint16(m[2:])) - 2
		if n < 0 {
			return 0, fmt.Errorf("imagedecode: JPEG segment %#x of length %d", m[1], n+2)
		}
		switch m[1] {
		case 0xc0, 0xc1, 0xc2:
			seg := make([]byte, n)
			if _, err := io.ReadFull(r, seg); err != nil {
				return 0, fmt.Errorf("imagedecode: JPEG frame header: %w", err)
			}
			return jpegFrameBytes(seg, m[1] == 0xc2, adobeRGB)
		case 0xee:
			seg := make([]byte, n)
			if _, err := io.ReadFull(r, seg); err != nil {
				return 0, fmt.Errorf("imagedecode: JPEG Adobe segment: %w", err)
			}
			adobeRGB = len(seg) >= 12 && string(seg[:5]) == "Adobe" && seg[11] == 0
			continue
		case 0xd9, 0xda:
			return 0, errors.New("imagedecode: JPEG without a frame header")
		}
		if _, err := r.Discard(n); err != nil {
			return 0, fmt.Errorf("imagedecode: JPEG segment %#x: %w", m[1], err)
		}
	}
}

// jpegFrameBytes sizes a decode from a frame header's segment; adobeRGB
// is an Adobe segment marking the components untransformed RGB.
func jpegFrameBytes(seg []byte, progressive, adobeRGB bool) (int64, error) {
	if len(seg) < 6 {
		return 0, errors.New("imagedecode: short JPEG frame header")
	}
	h, w, nc := int64(binary.BigEndian.Uint16(seg[1:3])), int64(binary.BigEndian.Uint16(seg[3:5])), int(seg[5])
	if w == 0 || h == 0 || nc == 0 || len(seg) < 6+3*nc {
		return 0, fmt.Errorf("imagedecode: JPEG frame %dx%d of %d components", w, h, nc)
	}
	var hmax, vmax int64 = 1, 1
	for i := range nc {
		hv := seg[6+3*i+1]
		hmax, vmax = max(hmax, int64(hv>>4)), max(vmax, int64(hv&0xf))
	}
	mcusX, mcusY := (w+8*hmax-1)/(8*hmax), (h+8*vmax-1)/(8*vmax)
	var samples int64
	for i := range nc {
		hv := seg[6+3*i+1]
		samples += mcusX * 8 * int64(hv>>4) * mcusY * 8 * int64(hv&0xf)
	}
	total := samples
	if progressive {
		total += 4 * samples
	}
	rgb := nc == 3 && (adobeRGB || seg[6] == 'R' && seg[9] == 'G' && seg[12] == 'B')
	if rgb || nc == 4 {
		total += 4 * w * h
	}
	return total, nil
}
