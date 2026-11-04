package wallpaper

import (
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/stubbedev/lys-lib/atomicfile"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-lib/xdg"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/memory-go"

	"github.com/stubbedev/lys-shell/internal/version"
	"github.com/stubbedev/lys-shell/offheap"
)

const (
	rasterCacheMagic  = "LYSWALL1"
	rasterHeaderBytes = len(rasterCacheMagic) + 4 + 4 + 8 + 8
	rasterCacheBudget = 256 << 20
)

type sourceID struct {
	size    int64
	modTime int64
}

func statSource(path string) (sourceID, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return sourceID{}, false
	}
	return sourceID{size: info.Size(), modTime: info.ModTime().UnixNano()}, true
}

type rasterCache struct {
	dir string
}

func openRasterCache() *rasterCache {
	base, ok := xdg.OS.CacheDir(version.Name)
	if !ok {
		log.Print("wallpaper: no cache directory: neither XDG_CACHE_HOME nor HOME is an absolute path; every login decodes the wallpaper")
		return nil
	}
	dir := filepath.Join(base, "wallpaper")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("wallpaper: every login decodes the wallpaper: %v", err)
		return nil
	}
	return &rasterCache{dir: dir}
}

func (c *rasterCache) file(key rasterKey) string {
	h := sha256.New()
	for _, part := range []string{
		key.path, string(key.fit), strconv.Itoa(key.box.w), strconv.Itoa(key.box.h),
		strconv.FormatFloat(key.box.scale, 'g', -1, 64),
	} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return filepath.Join(c.dir, hex.EncodeToString(h.Sum(nil)))
}

func encodeRasterHeader(w, h int, source sourceID) []byte {
	header := make([]byte, 0, rasterHeaderBytes)
	header = append(header, rasterCacheMagic...)
	header = binary.LittleEndian.AppendUint32(header, uint32(w))            //nolint:gosec // a raster's width
	header = binary.LittleEndian.AppendUint32(header, uint32(h))            //nolint:gosec // a raster's height
	header = binary.LittleEndian.AppendUint64(header, uint64(source.size))  //nolint:gosec // bits round trip
	return binary.LittleEndian.AppendUint64(header, uint64(source.modTime)) //nolint:gosec // bits round trip
}

func (c *rasterCache) load(mem *memory.Shared, key rasterKey) (*raster, error) {
	source, ok := statSource(key.path)
	if !ok {
		return nil, nil
	}
	file := c.file(key)
	f, err := os.Open(file) //nolint:gosec // a file in the shell's own cache directory
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("opening its cached raster: %w", err)
	}
	defer f.Close()
	var header [rasterHeaderBytes]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return nil, fmt.Errorf("reading the header of %s: %w", file, err)
	}
	if string(header[:len(rasterCacheMagic)]) != rasterCacheMagic {
		return nil, fmt.Errorf("%s is no cached raster", file)
	}
	fields := header[len(rasterCacheMagic):]
	w, h := int(binary.LittleEndian.Uint32(fields)), int(binary.LittleEndian.Uint32(fields[4:]))
	if int64(binary.LittleEndian.Uint64(fields[8:])) != source.size || int64(binary.LittleEndian.Uint64(fields[16:])) != source.modTime { //nolint:gosec // bits round trip
		return nil, nil
	}
	if w <= 0 || h <= 0 || w > key.box.w || h > key.box.h {
		return nil, fmt.Errorf("%s holds a %dx%d raster for a %dx%d output", file, w, h, key.box.w, key.box.h)
	}
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("reading the size of %s: %w", file, err)
	}
	if want := int64(rasterHeaderBytes) + 4*int64(w)*int64(h); info.Size() != want {
		return nil, fmt.Errorf("%s is %d bytes, want %d", file, info.Size(), want)
	}
	dst, pix, err := offheap.NewRGBA(mem, w, h)
	if err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(f, dst.Pix); err != nil {
		return nil, errors.Join(fmt.Errorf("reading the pixels of %s: %w", file, err), offheap.Free(mem, pix))
	}
	return &raster{image: render.NewStillImage(dst), scale: widget.ImageScaleDown, pix: pix}, nil
}

func (c *rasterCache) store(r *raster, source sourceID) error {
	file := c.file(r.key)
	size := r.size()
	err := atomicfile.Write(file, 0o600, func(w io.Writer) error {
		if _, err := w.Write(encodeRasterHeader(size.X, size.Y, source)); err != nil {
			return err
		}
		_, err := w.Write(r.image.Pix)
		return err
	})
	if err != nil {
		return fmt.Errorf("writing %s: %w", file, err)
	}
	return c.prune(file)
}

func (c *rasterCache) prune(keep string) error {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return fmt.Errorf("listing %s: %w", c.dir, err)
	}
	type cached struct {
		path    string
		size    int64
		modTime int64
	}
	files := make([]cached, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, cached{filepath.Join(c.dir, entry.Name()), info.Size(), info.ModTime().UnixNano()})
	}
	slices.SortFunc(files, func(a, b cached) int {
		return cmp.Or(cmp.Compare(b.modTime, a.modTime), cmp.Compare(a.path, b.path))
	})
	var used int64
	var failures []error
	for _, f := range files {
		used += f.size
		if used <= rasterCacheBudget || f.path == keep {
			continue
		}
		if err := os.Remove(f.path); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
