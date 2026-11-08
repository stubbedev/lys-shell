package launcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/stubbedev/lys-desktop/imagedecode"
)

const (
	// previewTextBytes is how much of a text file its pane reads.
	previewTextBytes = 16 << 10
	// previewSniffBytes is what deciding a file's kind reads.
	previewSniffBytes = 512
	// previewImageBytes refuses an image larger than this decoded: a
	// preview that takes a gigabyte is not one.
	previewImageBytes = 256 << 20
)

// ErrNoPreview is a row with nothing to show: a binary file nothing can
// thumbnail, a directory.
var ErrNoPreview = errors.New("launcher: nothing to preview")

// Preview is what a row shows in the side pane. The zero Preview is
// none.
type Preview struct {
	// Path is a file to picture or read.
	Path string
	// Text is text to show as it is.
	Text string
	// Load produces an encoded image, or text with LoadsText, off the UI loop
	// and only when the row is selected.
	Load      func() ([]byte, error)
	LoadsText bool
	// Image produces a decoded image off the UI loop, only when the row is
	// selected: a window's contents, captured from the compositor.
	Image func(context.Context) (image.Image, error)
	// Facts describe the row beside whatever loads: a window's title, app
	// and workspace, a clipboard entry's type and size.
	Facts []PreviewFact
}

// PreviewFact is one labelled line of a preview.
type PreviewFact struct{ Label, Value string }

// Any reports whether there is anything to preview.
func (p Preview) Any() bool {
	return p.Path != "" || p.Text != "" || p.Load != nil || p.Image != nil || len(p.Facts) > 0
}

// PreviewContent is a loaded preview: an image or text. Name is the file
// the text was read from, "" for text from no file.
type PreviewContent struct {
	Image image.Image
	Text  string
	Name  string
}

// Thumbnailing turns a file into an image file; Thumbnailer over the XDG
// thumbnailers.
type Thumbnailing interface {
	Generate(ctx context.Context, file string) (string, bool)
}

// LoadPreview loads a preview, off the UI loop. Facts alone have nothing
// to load: ErrNoPreview.
func LoadPreview(ctx context.Context, p Preview, thumbs Thumbnailing) (PreviewContent, error) {
	switch {
	case p.Image != nil:
		img, err := p.Image(ctx)
		if err != nil {
			return PreviewContent{}, err
		}
		return PreviewContent{Image: img}, nil
	case p.Load != nil:
		data, err := p.Load()
		if err != nil {
			return PreviewContent{}, err
		}
		if p.LoadsText {
			text, ok := textOf(data, false)
			if !ok {
				return PreviewContent{}, ErrNoPreview
			}
			return PreviewContent{Text: text}, nil
		}
		img, err := imagedecode.Decode(data)
		if err != nil {
			return PreviewContent{}, fmt.Errorf("preview: decode: %w", err)
		}
		return PreviewContent{Image: img}, nil
	case p.Text != "":
		return PreviewContent{Text: p.Text}, nil
	case p.Path != "":
		return loadFilePreview(ctx, p.Path, thumbs)
	}
	return PreviewContent{}, ErrNoPreview
}

func loadFilePreview(ctx context.Context, path string, thumbs Thumbnailing) (PreviewContent, error) {
	file, err := os.Open(path) //nolint:gosec // a file the user searched for
	if err != nil {
		return PreviewContent{}, fmt.Errorf("preview: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return PreviewContent{}, fmt.Errorf("preview: %w", err)
	}
	if !info.Mode().IsRegular() {
		return PreviewContent{}, ErrNoPreview
	}
	head := make([]byte, previewTextBytes)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return PreviewContent{}, fmt.Errorf("preview: %w", err)
	}
	head = head[:n]
	kind := http.DetectContentType(head[:min(n, previewSniffBytes)])
	if strings.HasPrefix(kind, "image/") {
		size, err := imagedecode.DecodeBytes(path)
		if err == nil && size <= previewImageBytes {
			if img, err := imagedecode.OpenContext(ctx, path); err == nil {
				return PreviewContent{Image: img}, nil
			}
		}
	}
	if text, ok := textOf(head, n == previewTextBytes); ok {
		return PreviewContent{Text: text, Name: path}, nil
	}
	if thumbs != nil {
		if thumb, ok := thumbs.Generate(ctx, path); ok {
			img, err := imagedecode.OpenContext(ctx, thumb)
			if err != nil {
				return PreviewContent{}, fmt.Errorf("preview: thumbnail: %w", err)
			}
			return PreviewContent{Image: img}, nil
		}
	}
	return PreviewContent{}, ErrNoPreview
}

// textOf is data as text when it reads as text: no NUL, valid UTF-8 but
// for a character cut at the end of what was read.
func textOf(data []byte, cut bool) (string, bool) {
	if len(data) == 0 || bytes.IndexByte(data, 0) >= 0 {
		return "", false
	}
	if cut {
		for range utf8.UTFMax {
			if utf8.Valid(data) {
				break
			}
			data = data[:len(data)-1]
		}
	}
	if !utf8.Valid(data) {
		return "", false
	}
	return string(data), true
}
