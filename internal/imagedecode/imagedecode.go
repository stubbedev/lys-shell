// Package imagedecode opens raster images in the formats the Rust
// shell's `image` crate decodes by default for wallpapers: PNG, JPEG,
// GIF, BMP, TIFF, WebP (stdlib and golang.org/x/image), plus farbfeld,
// PNM (P1-P6), and TGA, which Go has no decoder for and this package
// registers itself. SVG, JPEG XL, and AVIF are listed as wallpaper
// extensions but the Rust decoder rejects them as well.
package imagedecode

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"  // GIF decoder
	_ "image/jpeg" // JPEG decoder
	_ "image/png"  // PNG decoder
	"io"
	"os"

	_ "golang.org/x/image/bmp"  // BMP decoder
	_ "golang.org/x/image/tiff" // TIFF decoder
	_ "golang.org/x/image/webp" // WebP decoder
)

func init() {
	image.RegisterFormat("farbfeld", "farbfeld", decodeFarbfeld, decodeFarbfeldConfig)
	for _, magic := range []string{"P1", "P2", "P3", "P4", "P5", "P6"} {
		image.RegisterFormat("pnm", magic, decodePNM, decodePNMConfig)
	}
}

// Decode decodes image bytes, sniffed as Open sniffs a file.
func Decode(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err == nil {
		return img, nil
	}
	if tga, tgaErr := decodeTGA(bytes.NewReader(data)); tgaErr == nil {
		return tga, nil
	}
	return nil, err
}

// Open decodes the image file at path. The format is sniffed from the
// content, except TGA, which has no magic and is tried last.
func Open(path string) (image.Image, error) {
	return OpenContext(context.Background(), path)
}

// OpenContext decodes the image file at path as Open does, and gives
// up with ctx's error at the decoder's next read once ctx is done.
func OpenContext(ctx context.Context, path string) (image.Image, error) {
	f, err := os.Open(path) //nolint:gosec // the caller's image path
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := ctxReader{ctx, f}
	img, _, err := image.Decode(bufio.NewReader(r))
	if err == nil {
		return img, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, ctxErr)
	}
	if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
		return nil, err
	}
	if tga, tgaErr := decodeTGA(bufio.NewReader(r)); tgaErr == nil {
		return tga, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, ctxErr)
	}
	return nil, fmt.Errorf("%s: %w", path, err)
}

// ctxReader reads r until ctx is done, then fails with ctx's error.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
