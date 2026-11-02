package search

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 3))
	img.Set(1, 1, color.NRGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeThumbs stands in for the XDG thumbnailers.
type fakeThumbs struct {
	out   string
	ok    bool
	asked []string
}

func (f *fakeThumbs) Generate(_ context.Context, file string) (string, bool) {
	f.asked = append(f.asked, file)
	return f.out, f.ok
}

var ctx = context.Background()

func TestPreviewOfAnImageFileDecodesIt(t *testing.T) {
	thumbs := &fakeThumbs{}
	got, err := LoadPreview(ctx, Preview{Path: writeTemp(t, "a.png", pngBytes(t))}, thumbs)
	if err != nil || got.Image == nil || got.Image.Bounds().Dx() != 4 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if len(thumbs.asked) != 0 {
		t.Error("an image needs no thumbnailer")
	}
}

func TestPreviewOfATextFileIsItsText(t *testing.T) {
	got, err := LoadPreview(ctx, Preview{Path: writeTemp(t, "a.txt", []byte("hello\nworld\n"))}, nil)
	if err != nil || got.Text != "hello\nworld\n" || got.Image != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestPreviewOfALargeTextFileCutsOnACharacter(t *testing.T) {
	// Pad so the read's end lands inside a two-byte character.
	data := append(bytes.Repeat([]byte("a"), previewTextBytes-1), []byte(strings.Repeat("é", 100))...)
	got, err := LoadPreview(ctx, Preview{Path: writeTemp(t, "big.txt", data)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(got.Text) || len(got.Text) > previewTextBytes {
		t.Errorf("%d bytes, valid %v", len(got.Text), utf8.ValidString(got.Text))
	}
}

func TestPreviewOfABinaryAsksTheThumbnailer(t *testing.T) {
	binary := writeTemp(t, "a.pdf", []byte("%PDF\x00\x01\x02"))
	thumbs := &fakeThumbs{out: writeTemp(t, "thumb.png", pngBytes(t)), ok: true}
	got, err := LoadPreview(ctx, Preview{Path: binary}, thumbs)
	if err != nil || got.Image == nil {
		t.Fatalf("got %+v, %v", got, err)
	}
	if len(thumbs.asked) != 1 || thumbs.asked[0] != binary {
		t.Errorf("asked %v", thumbs.asked)
	}
}

func TestPreviewOfABinaryNothingCanThumbnailIsNoPreview(t *testing.T) {
	binary := writeTemp(t, "a.bin", []byte{0, 1, 2})
	if _, err := LoadPreview(ctx, Preview{Path: binary}, &fakeThumbs{}); !errors.Is(err, ErrNoPreview) {
		t.Errorf("err = %v", err)
	}
	if _, err := LoadPreview(ctx, Preview{Path: binary}, nil); !errors.Is(err, ErrNoPreview) {
		t.Errorf("no thumbnailer: err = %v", err)
	}
}

func TestPreviewOfACorruptThumbnailIsAnError(t *testing.T) {
	thumbs := &fakeThumbs{out: writeTemp(t, "thumb.png", []byte("not a png")), ok: true}
	_, err := LoadPreview(ctx, Preview{Path: writeTemp(t, "a.bin", []byte{0, 1})}, thumbs)
	if err == nil || errors.Is(err, ErrNoPreview) {
		t.Errorf("err = %v, want it said rather than looking like nothing to show", err)
	}
}

func TestPreviewOfAFileClaimingToBeAnImageFallsThrough(t *testing.T) {
	// The PNG signature with nothing after it: sniffed as an image, not one.
	data := []byte("\x89PNG\r\n\x1a\n")
	if _, err := LoadPreview(ctx, Preview{Path: writeTemp(t, "bad.png", data)}, nil); !errors.Is(err, ErrNoPreview) {
		t.Errorf("err = %v", err)
	}
}

func TestPreviewOfADirectoryOrNothing(t *testing.T) {
	if _, err := LoadPreview(ctx, Preview{Path: t.TempDir()}, nil); !errors.Is(err, ErrNoPreview) {
		t.Errorf("directory: %v", err)
	}
	if _, err := LoadPreview(ctx, Preview{}, nil); !errors.Is(err, ErrNoPreview) {
		t.Errorf("zero preview: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "gone")
	_, err := LoadPreview(ctx, Preview{Path: missing}, nil)
	if err == nil || errors.Is(err, ErrNoPreview) || !strings.Contains(err.Error(), missing) {
		t.Errorf("missing: %v, want the path named", err)
	}
}

func TestPreviewOfEncodedBytesDecodesThem(t *testing.T) {
	got, err := LoadPreview(ctx, Preview{Load: func() ([]byte, error) { return pngBytes(t), nil }}, nil)
	if err != nil || got.Image == nil {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := LoadPreview(ctx, Preview{Load: func() ([]byte, error) { return []byte("junk"), nil }}, nil); err == nil {
		t.Error("undecodable bytes are an error")
	}
	gone := errors.New("gone")
	if _, err := LoadPreview(ctx, Preview{Load: func() ([]byte, error) { return nil, gone }}, nil); !errors.Is(err, gone) {
		t.Errorf("err = %v", err)
	}
}

func TestPreviewOfTextIsThatText(t *testing.T) {
	got, err := LoadPreview(ctx, Preview{Text: "x"}, nil)
	if err != nil || got.Text != "x" {
		t.Errorf("got %+v, %v", got, err)
	}
}
