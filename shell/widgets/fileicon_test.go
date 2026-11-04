package widgets

import (
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
