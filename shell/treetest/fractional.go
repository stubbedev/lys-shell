package treetest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
)

// FractionalScales are the device scales, in 120ths, the fractional icon gates
// paint at.
var FractionalScales = []struct {
	Name string
	Num  int
}{{"1.25", 150}, {"1.5", 180}}

const greenSquareSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16"><rect width="16" height="16" fill="#00ff00"/></svg>`

// GreenSVG writes a 16px green square to a file of its own.
func GreenSVG(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "green.svg")
	if err := os.WriteFile(path, []byte(greenSquareSVG), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// FractionalFirstPaint puts the 16px icon makeIcon builds into a tree that
// presents at num/120, as a window does before its first frame, runs what it
// posts to the loop until it shows an .svg file, and returns the center pixel
// of its first paint. It fails when that paint would have to decode.
func FractionalFirstPaint(t *testing.T, num int, makeIcon func(post func(func())) *widget.Icon) []byte {
	t.Helper()
	posted := make(chan func(), 8)
	root := widget.NewBox(widget.Row, 0, 0)
	widget.SetDeviceScale(root, num, 120)
	icon := makeIcon(func(fn func()) { posted <- fn })
	root.Append(icon, false)
	root.Arrange(render.Rect{W: 16, H: 16})
	path := ""
	for deadline := time.After(Liveness); path == ""; {
		select {
		case fn := <-posted:
			fn()
			if strings.HasSuffix(icon.Name(), ".svg") {
				path = icon.Name()
			}
		case <-deadline:
			t.Fatal("the icon never showed its file")
		}
	}
	if !widget.ThemeIconPreloaded(path, 16, num, 120) {
		t.Fatal("the icon shows before it is decoded at its tree's scale: the first paint would decode on the loop")
	}
	dev := (16*num + 119) / 120
	data := make([]byte, render.Stride(dev)*dev)
	icon.Paint(render.NewScaled(data, render.Stride(dev), dev, dev, num, 120))
	at := (dev/2)*render.Stride(dev) + (dev/2)*4
	return data[at : at+4]
}

// ExpectGreen fails unless the pixel is the exact green GreenSVG draws.
func ExpectGreen(t *testing.T, scale string, px []byte) {
	t.Helper()
	if px[0] != 0 || px[1] != 0xff || px[2] != 0 {
		t.Errorf("first paint at %s: center %v, want the exact green", scale, px)
	}
}
