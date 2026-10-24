package filechooser

import (
	"bytes"
	"crypto/md5" //nolint:gosec // the thumbnail spec names files by the URI's MD5
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/image/draw"

	"github.com/stubbedev/lys-shell/internal/fileuri"
	"github.com/stubbedev/lys-shell/internal/imagedecode"
	"github.com/stubbedev/lys-shell/internal/xdg"
)

// thumbMaxBytes skips images too large to decode for a row icon.
const thumbMaxBytes = 32 << 20

// errThumbTooLarge is a source past thumbMaxBytes.
var errThumbTooLarge = errors.New("filechooser: image too large to thumbnail")

// errNoThumb is a file with no cached thumbnail that is not a raster
// image to decode.
var errNoThumb = errors.New("filechooser: no thumbnail")

// thumbnail is the file's thumbnail as an exact px square,
// centre-cropped to fill (every thumbnail is the same size, so rows
// align whatever the source's aspect ratio): the one the freedesktop
// cache holds for it, which file managers and viewers make for PDFs
// and videos too, else a raster image decoded. Run it off the loop.
func thumbnail(path string, px int) (*image.RGBA, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if src, err := cachedThumbnail(path, info, px); err == nil {
		return squareCrop(src, px), nil
	}
	if !isRaster(path) {
		return nil, errNoThumb
	}
	if info.Size() > thumbMaxBytes {
		return nil, errThumbTooLarge
	}
	src, err := imagedecode.Open(path)
	if err != nil {
		return nil, err
	}
	return squareCrop(src, px), nil
}

// squareCrop scales the largest centred square of src to px.
func squareCrop(src image.Image, px int) *image.RGBA {
	px = max(1, px)
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	crop := image.Rect(0, 0, side, side).Add(b.Min).Add(image.Pt((b.Dx()-side)/2, (b.Dy()-side)/2))
	dst := image.NewRGBA(image.Rect(0, 0, px, px))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, crop, draw.Src, nil)
	return dst
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
