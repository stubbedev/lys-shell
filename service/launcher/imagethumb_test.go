package launcher

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/fileuri"
	"github.com/stubbedev/lys-desktop/imagedecode"
)

func writeImage(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 0x80, 0xff})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestImageThumbnailsDecodeOnceThenReadTheCache(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "wide.png")
	writeImage(t, src, 600, 300)
	decodes := 0
	thumbs := ImageThumbnails{Dir: filepath.Join(dir, "large"), Decode: func(path string) (image.Image, error) {
		decodes++
		return imagedecode.Open(path)
	}}
	img, decoded, err := thumbs.Load(src)
	if err != nil || !decoded || decodes != 1 {
		t.Fatalf("first load: decoded %t, %d decodes, %v", decoded, decodes, err)
	}
	if got := img.Bounds().Size(); got != image.Pt(256, 128) {
		t.Errorf("thumbnail %v, want 256x128", got)
	}
	data, err := os.ReadFile(thumbnailPath(thumbs.Dir, src))
	if err != nil {
		t.Fatal(err)
	}
	text := pngText(data)
	info, _ := os.Stat(src)
	if text["Thumb::URI"] != fileuri.FromPath(src) || text["Thumb::MTime"] != strconv.FormatInt(info.ModTime().Unix(), 10) {
		t.Errorf("thumbnail attributes %v", text)
	}
	img, decoded, err = thumbs.Load(src)
	if err != nil || decoded || decodes != 1 || img.Bounds().Size() != image.Pt(256, 128) {
		t.Fatalf("second load: decoded %t, %d decodes, %v", decoded, decodes, err)
	}
	later := info.ModTime().Add(3 * time.Second)
	if err := os.Chtimes(src, later, later); err != nil {
		t.Fatal(err)
	}
	if _, decoded, _ := thumbs.Load(src); !decoded || decodes != 2 {
		t.Errorf("an edited image read its old thumbnail: decoded %t, %d decodes", decoded, decodes)
	}
}

func TestImageThumbnailsKeepASmallImageAndFailLoudly(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "small.png")
	writeImage(t, src, 40, 90)
	thumbs := ImageThumbnails{Dir: filepath.Join(dir, "large"), Decode: imagedecode.Open}
	img, _, err := thumbs.Load(src)
	if err != nil || img.Bounds().Size() != image.Pt(40, 90) {
		t.Errorf("small image thumbnail %v, %v", img, err)
	}
	if _, _, err := thumbs.Load(filepath.Join(dir, "missing.png")); err == nil {
		t.Error("a missing image has a thumbnail")
	}
	broken := filepath.Join(dir, "broken.png")
	if err := os.WriteFile(broken, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, decoded, err := thumbs.Load(broken); err == nil || !decoded {
		t.Errorf("a broken image = decoded %t, %v; want a decode error", decoded, err)
	}
}

func TestPNGTextIgnoresAMalformedFile(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("GIF89a"), []byte(pngSignature + "\x00\x00\xff\xffIHDR")} {
		if got := pngText(data); len(got) != 0 {
			t.Errorf("pngText(%q) = %v", data, got)
		}
	}
}
