package widgets

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"golang.org/x/sys/unix"

	"github.com/stubbedev/lys-shell/shell/treetest"
)

func paintOnce(t *testing.T, icon *widget.Icon) {
	t.Helper()
	const px = 32
	icon.Arrange(render.Rect{W: px, H: px})
	data := make([]byte, render.Stride(px)*px)
	icon.Paint(render.New(data, render.Stride(px), px, px))
}

func runPosted(t *testing.T, path string) (icon *widget.Icon, shown bool) {
	t.Helper()
	posted := make(chan func(), 4)
	icon = NewAsyncFileIcon(path, "ld-file-symbolic", 32, func(fn func()) { posted <- fn }, func() { shown = true })
	paintOnce(t, icon)
	if got := icon.Name(); got != "ld-file-symbolic" {
		t.Errorf("icon shows %q before its load, want the fallback", got)
	}
	for {
		select {
		case fn := <-posted:
			fn()
		case <-time.After(200 * time.Millisecond):
			return icon, shown
		}
	}
}

func TestAFileThatPaintsNothingKeepsTheFallbackAndThePaintFinishes(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "icon.png")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	garbage := filepath.Join(dir, "garbage.png")
	if err := os.WriteFile(garbage, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"a FIFO": fifo, "undecodable": garbage, "missing": filepath.Join(dir, "gone.png")} {
		icon, shown := runPosted(t, path)
		if shown || icon.Name() != "ld-file-symbolic" {
			t.Errorf("%s: shown %v as %q, want the fallback kept", name, shown, icon.Name())
		}
	}
}

func TestADecodableFileReplacesTheFallback(t *testing.T) {
	path := treetest.GreenSVG(t)
	icon, shown := runPosted(t, path)
	if !shown || icon.Name() != path {
		t.Errorf("shown %v as %q, want the file", shown, icon.Name())
	}
}

func TestAFileIconsFirstPaintAtAFractionalScaleIsExactAndDecodesNothing(t *testing.T) {
	for _, sc := range treetest.FractionalScales {
		t.Run(sc.Name, func(t *testing.T) {
			path := treetest.GreenSVG(t)
			center := treetest.FractionalFirstPaint(t, sc.Num, func(post func(func())) *widget.Icon {
				return NewAsyncFileIcon(path, "", 16, post, nil)
			})
			treetest.ExpectGreen(t, sc.Name, center)
		})
	}
}
