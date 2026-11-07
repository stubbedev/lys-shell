package filechooser

import (
	"bytes"
	"crypto/md5" //nolint:gosec // the thumbnail spec names files by the URI's MD5
	"encoding/binary"
	"encoding/hex"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"

	"github.com/stubbedev/lys-lib/atomicfile"
	"github.com/stubbedev/lys-lib/xdg"

	"github.com/stubbedev/lys-shell/internal/fileuri"
)

// failApp is the directory under the cache's fail/ that records the
// files lys-shell could not thumbnail.
const failApp = "lys-shell"

// thumbSize is one of the cache's size directories and its edge.
type thumbSize struct {
	dir string
	px  int
}

// thumbSizes are the cache's sizes, smallest first.
var thumbSizes = []thumbSize{{"normal", 128}, {"large", 256}, {"x-large", 512}, {"xx-large", 1024}}

// flavorFor is the smallest cache size at least px, else the largest.
func flavorFor(px int) thumbSize {
	for _, s := range thumbSizes {
		if px <= s.px {
			return s
		}
	}
	return thumbSizes[len(thumbSizes)-1]
}

// thumbCache is one file's place in the freedesktop thumbnail cache:
// the cache root ("" without a cache directory, where nothing is read
// or written), the file name every size shares (the MD5 of the URI),
// and the URI, modification time and size a thumbnail must carry.
type thumbCache struct {
	root, name, uri, mtime, size string
}

// cacheFor is path's place in the cache as info describes it.
func cacheFor(path string, info os.FileInfo) (thumbCache, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return thumbCache{}, err
	}
	uri := fileuri.FromPath(abs)
	sum := md5.Sum([]byte(uri)) //nolint:gosec // the spec's naming, not a security use
	tc := thumbCache{
		name: hex.EncodeToString(sum[:]) + ".png", uri: uri,
		mtime: strconv.FormatInt(info.ModTime().Unix(), 10), size: strconv.FormatInt(info.Size(), 10),
	}
	if cache, ok := xdg.OS.CacheHome(); ok {
		tc.root = filepath.Join(cache, "thumbnails")
	}
	return tc, nil
}

// path is the thumbnail's path in the size directory dir.
func (tc thumbCache) path(dir string) string { return filepath.Join(tc.root, dir, tc.name) }

// failPath is the record that lys-shell could not thumbnail the file.
func (tc thumbCache) failPath() string { return filepath.Join(tc.root, "fail", failApp, tc.name) }

// lookup is the cache's image for the file: the smallest size at least
// px, else the largest below it, and only while its Thumb::MTime
// matches the file's (a stale one is no thumbnail).
func (tc thumbCache) lookup(px int) (image.Image, error) {
	if tc.root == "" {
		return nil, errNoThumb
	}
	start := 0
	for start < len(thumbSizes)-1 && thumbSizes[start].px < px {
		start++
	}
	order := make([]int, 0, len(thumbSizes))
	for i := start; i < len(thumbSizes); i++ {
		order = append(order, i)
	}
	for i := start - 1; i >= 0; i-- {
		order = append(order, i)
	}
	for _, i := range order {
		data, err := os.ReadFile(tc.path(thumbSizes[i].dir))
		if err != nil || !tc.fresh(data) {
			continue
		}
		if img, err := png.Decode(bytes.NewReader(data)); err == nil {
			return img, nil
		}
	}
	return nil, errNoThumb
}

// failed reports a fresh record that thumbnailing the file failed.
func (tc thumbCache) failed() bool {
	if tc.root == "" {
		return false
	}
	data, err := os.ReadFile(tc.failPath())
	return err == nil && tc.fresh(data)
}

// fresh reports a cache PNG made from the file as it is now.
func (tc thumbCache) fresh(data []byte) bool { return pngText(data, "Thumb::MTime") == tc.mtime }

// encode is img as a cache PNG carrying the spec's Thumb::URI,
// Thumb::MTime and Thumb::Size, and the source's pixel size when one
// is given.
func (tc thumbCache) encode(img image.Image, source image.Rectangle) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	text := [][2]string{{"Thumb::URI", tc.uri}, {"Thumb::MTime", tc.mtime}, {"Thumb::Size", tc.size}, {"Software", failApp}}
	if !source.Empty() {
		text = append(text, [2]string{"Thumb::Image::Width", strconv.Itoa(source.Dx())}, [2]string{"Thumb::Image::Height", strconv.Itoa(source.Dy())})
	}
	return withPNGText(buf.Bytes(), text), nil
}

// storeThumbnail writes a cache file whole, in directories only the
// user may enter: a reader sees the old thumbnail or the new one, never
// part of one.
func storeThumbnail(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicfile.WriteFile(path, data, 0o600)
}

// pngHeaderEnd is where a PNG's IHDR chunk ends: the signature, the
// chunk's length and type, its 13 bytes and its CRC.
const pngHeaderEnd = 8 + 8 + 13 + 4

// withPNGText is a PNG with tEXt chunks for text right after its IHDR.
func withPNGText(data []byte, text [][2]string) []byte {
	out := make([]byte, 0, len(data)+64*len(text))
	out = append(out, data[:pngHeaderEnd]...)
	for _, kv := range text {
		body := append(append([]byte(kv[0]), 0), kv[1]...)
		out = binary.BigEndian.AppendUint32(out, uint32(len(body))) //nolint:gosec // a key and a URI, far below 4 GiB
		start := len(out)
		out = append(out, "tEXt"...)
		out = append(out, body...)
		out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out[start:]))
	}
	return append(out, data[pngHeaderEnd:]...)
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
