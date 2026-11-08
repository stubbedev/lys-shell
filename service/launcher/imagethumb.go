package launcher

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"

	"github.com/stubbedev/lys-desktop/fileuri"
	"github.com/stubbedev/lys-lib/atomicfile"
	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/render"
)

// LargeThumbnailSize is the freedesktop large thumbnail size in pixels:
// what a picture grid cell shows.
const LargeThumbnailSize = 256

// ImageThumbnails makes and reads the freedesktop large thumbnails of
// image files in process: a cached thumbnail whose Thumb::MTime matches
// the file is read back, anything else is decoded once, scaled to fit
// LargeThumbnailSize and saved for the next reader, this one or a file
// manager.
type ImageThumbnails struct {
	// Dir is the large thumbnail directory (LargeThumbnailDir).
	Dir string
	// Decode reads an image file (imagedecode.Open).
	Decode func(path string) (image.Image, error)
}

// LargeThumbnailDir is $XDG_CACHE_HOME/thumbnails/large, the shared spec
// directory.
func LargeThumbnailDir() string { return thumbnailCacheDir("large") }

// Load is file's thumbnail; decoded reports that the source image was
// decoded to make it, rather than read from the cache. A thumbnail that
// cannot be saved is still returned, with the save's error.
func (t ImageThumbnails) Load(file string) (thumb image.Image, decoded bool, err error) {
	info, err := os.Stat(file)
	if err != nil {
		return nil, false, fmt.Errorf("thumbnail of %s: %w", file, err)
	}
	mtime := strconv.FormatInt(info.ModTime().Unix(), 10)
	cached := thumbnailPath(t.Dir, file)
	if img, ok := readThumbnail(cached, mtime); ok {
		return img, false, nil
	}
	src, err := t.Decode(file)
	if err != nil {
		return nil, true, fmt.Errorf("thumbnail of %s: %w", file, err)
	}
	img := fitThumbnail(src)
	data, err := encodeThumbnail(img, fileuri.FromPath(file), mtime)
	if err != nil {
		return img, true, fmt.Errorf("thumbnail of %s: %w", file, err)
	}
	if err := os.MkdirAll(filepath.Dir(cached), 0o700); err != nil {
		return img, true, fmt.Errorf("thumbnail directory for %s: %w", file, err)
	}
	if err := atomicfile.WriteFile(cached, data, 0o600); err != nil {
		return img, true, fmt.Errorf("saving the thumbnail of %s: %w", file, err)
	}
	return img, true, nil
}

// fitThumbnail scales src down to fit LargeThumbnailSize, keeping its
// aspect; a smaller image is kept at its size.
func fitThumbnail(src image.Image) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if longest := max(w, h); longest > LargeThumbnailSize {
		w = max(1, w*LargeThumbnailSize/longest)
		h = max(1, h*LargeThumbnailSize/longest)
	}
	return render.Resample(src, b, w, h)
}

// readThumbnail decodes a cached thumbnail made from the file as it is
// now: its Thumb::MTime is mtime.
func readThumbnail(path, mtime string) (image.Image, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // the spec's cache path for a listed file
	if err != nil {
		return nil, false
	}
	if text, ok := pngText(data)["Thumb::MTime"]; !ok || text != mtime {
		return nil, false
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	return img, true
}

const pngSignature = "\x89PNG\r\n\x1a\n"

// pngText is a PNG's tEXt keywords and values, the spec's Thumb::*
// attributes; a malformed file has none.
func pngText(data []byte) map[string]string {
	out := map[string]string{}
	if !bytes.HasPrefix(data, []byte(pngSignature)) {
		return out
	}
	rest := data[len(pngSignature):]
	for len(rest) >= 12 {
		n, err := intconv.Exact[int](binary.BigEndian.Uint32(rest))
		if err != nil || n > len(rest)-12 {
			return out
		}
		kind, body := string(rest[4:8]), rest[8:8+n]
		if kind == "IDAT" || kind == "IEND" {
			return out
		}
		if kind == "tEXt" {
			if key, value, ok := bytes.Cut(body, []byte{0}); ok {
				out[string(key)] = string(value)
			}
		}
		rest = rest[12+n:]
	}
	return out
}

// encodeThumbnail is img as PNG carrying the spec's required Thumb::URI
// and Thumb::MTime, right after the header.
func encodeThumbnail(img image.Image, uri, mtime string) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	data := buf.Bytes()
	const ihdrEnd = len(pngSignature) + 4 + 4 + 13 + 4
	if len(data) < ihdrEnd || string(data[len(pngSignature)+4:len(pngSignature)+8]) != "IHDR" {
		return nil, errors.New("png: the encoder wrote no header first")
	}
	var out bytes.Buffer
	out.Grow(len(data) + len(uri) + len(mtime) + 64)
	out.Write(data[:ihdrEnd])
	writeChunk(&out, "tEXt", []byte("Thumb::URI\x00"+uri))
	writeChunk(&out, "tEXt", []byte("Thumb::MTime\x00"+mtime))
	out.Write(data[ihdrEnd:])
	return out.Bytes(), nil
}

func writeChunk(w *bytes.Buffer, kind string, body []byte) {
	var head [8]byte
	binary.BigEndian.PutUint32(head[:4], uint32(len(body))) //nolint:gosec // a URI or a decimal time, far below 4 GiB
	copy(head[4:], kind)
	var tail [4]byte
	binary.BigEndian.PutUint32(tail[:], crc32.Update(crc32.ChecksumIEEE(head[4:]), crc32.IEEETable, body))
	w.Write(head[:])
	w.Write(body)
	w.Write(tail[:])
}
