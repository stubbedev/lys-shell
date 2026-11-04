package widgets

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"golang.org/x/sys/unix"
)

func paintOnce(t *testing.T, icon *widget.Icon) {
	t.Helper()
	const px = 32
	icon.Arrange(render.Rect{W: px, H: px})
	data := make([]byte, render.Stride(px)*px)
	icon.Paint(render.New(data, render.Stride(px), px, px))
}

func TestAFIFOKeepsTheFallbackAndThePaintFinishes(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "icon.png")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	posted := make(chan func(), 1)
	icon := NewAsyncFileIcon(fifo, "ld-file-symbolic", 32, func(fn func()) { posted <- fn }, nil)
	paintOnce(t, icon)
	if got := icon.Name(); got != "ld-file-symbolic" {
		t.Errorf("icon shows %q, want the fallback", got)
	}
	select {
	case fn := <-posted:
		fn()
		t.Errorf("a FIFO reached the loop as a ready icon: now %q", icon.Name())
	case <-time.After(100 * time.Millisecond):
	}
}

const greenSquareSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16"><rect width="16" height="16" fill="#00ff00"/></svg>`

func TestAFileIconsFirstPaintAtAFractionalScaleIsExactAndDecodesNothing(t *testing.T) {
	for _, sc := range []struct {
		name       string
		num, denom int
	}{{"1.25", 5, 4}, {"1.5", 3, 2}} {
		t.Run(sc.name, func(t *testing.T) {
			NoteDeviceScale(sc.num, sc.denom)
			path := filepath.Join(t.TempDir(), "green.svg")
			if err := os.WriteFile(path, []byte(greenSquareSVG), 0o600); err != nil {
				t.Fatal(err)
			}
			posted := make(chan func(), 1)
			icon := NewAsyncFileIcon(path, "", 16, func(fn func()) { posted <- fn }, nil)
			select {
			case show := <-posted:
				show()
			case <-time.After(10 * time.Second):
				t.Fatal("the icon never decoded")
			}
			if !widget.ThemeIconPreloaded(path, 16, sc.num, sc.denom) {
				t.Fatal("the first paint would decode: the icon is not cached at the surface's scale")
			}
			const px = 16
			dev := (px*sc.num + sc.denom - 1) / sc.denom
			icon.Arrange(render.Rect{W: px, H: px})
			data := make([]byte, render.Stride(dev)*dev)
			icon.Paint(render.NewScaled(data, render.Stride(dev), dev, dev, sc.num, sc.denom))
			at := (dev/2)*render.Stride(dev) + (dev/2)*4
			if got := data[at : at+4]; got[1] != 0xff || got[0] != 0 || got[2] != 0 {
				t.Errorf("first paint at %s: center %v, want the exact green", sc.name, got)
			}
		})
	}
}
