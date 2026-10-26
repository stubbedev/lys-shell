package bar

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
)

// separatorBar is a styled bar at loc with one separator between two
// clocks, settled within size; it returns the bar and the separator.
func separatorBar(t *testing.T, cfg *config.Config, loc config.Location, size widget.Size) (*widget.Bar, *widget.BarSeparator) {
	t.Helper()
	cfg.Bar.Location = loc
	layout := config.BarLayout{Monitor: "*", Left: []config.BarItem{{Module: "clock"}, {Module: "separator"}, {Module: "clock"}}}
	bar := buildRoot(styledContext(t, cfg), layout, "DP-1")
	settleBar(bar, size)
	item := bar.Start().Children()[1].(*widget.Box).Children()[0]
	sep, ok := item.(*widget.BarSeparator)
	if !ok {
		t.Fatalf("the separator's bar item holds %T, want the BarSeparator itself", item)
	}
	return bar, sep
}

// The separator is a rule, not a module button: it sits in its bar
// item bare, so no button chrome (the basic variant's pill) wraps it.
func TestSeparatorIsABarSeparatorNotAButton(t *testing.T) {
	cfg := config.Defaults()
	cfg.Bar.ButtonVariant = config.ButtonBasic
	bar, sep := separatorBar(t, cfg, config.LocationTop, widget.Size{W: 1000, H: 1000})
	walkTree(bar.Start().Children()[1], func(w widget.Widget) bool {
		switch w.(type) {
		case *widget.BarButton, *barButton:
			t.Errorf("a %T wraps the separator", w)
		}
		return true
	})
	if !widget.HasClass(sep, "module") {
		t.Error("the separator misses the module class")
	}
}

// size is the rule's thickness and length its extent across the bar:
// a vertical line in a top bar, a horizontal one in a side bar, in the
// configured color.
func TestSeparatorRunsAcrossTheBar(t *testing.T) {
	pink := render.RGB(0xf3, 0x8b, 0xa8)
	for _, loc := range []config.Location{config.LocationTop, config.LocationLeft} {
		cfg := config.Defaults()
		cfg.Separator.Size = 3
		cfg.Separator.Length = config.Size{Value: 12, Unit: config.SizePixels}
		cfg.Separator.Color, _ = config.ParseColorValue("#f38ba8")
		bar, sep := separatorBar(t, cfg, loc, widget.Size{W: 1000, H: 1000})
		size := settleBar(bar, widget.Size{W: 1000, H: 1000})
		w, h := 1000, size.H
		if loc.IsVertical() {
			w, h = size.W, 1000
		}
		bar.Measure(widgetConstraintsMax(w, h))
		bar.Arrange(render.Rect{W: w, H: h})
		data := make([]byte, render.Stride(w)*h)
		cv := render.New(data, render.Stride(w), w, h)
		cv.Clear(cv.Rect(), render.RGB(0, 0, 0))
		bar.Paint(cv)
		r := sep.Bounds()
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		run := func(x, y, dx, dy int) int {
			n := 0
			for x, y = x-dx*50, y-dy*50; x < w && y < h; x, y = x+dx, y+dy {
				if x >= 0 && y >= 0 && pixelAt(data, w, x, y) == pink {
					n++
				}
			}
			return n
		}
		across, along := run(cx, cy, 1, 0), run(cx, cy, 0, 1)
		if loc.IsVertical() {
			across, along = along, across
		}
		if across != 3 || along != 12 {
			t.Errorf("%s bar: the rule paints %dpx thick and %dpx long in %#08x, want the size 3 and the length 12 in the configured color",
				loc, across, along, uint32(pink))
		}
	}
}

func TestLoadFileAppliesSeparator(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "modules:\n  separator:\n    color: \"#f38ba8\"\n    length: 12px\n    size: 2\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	sep := c.Separator
	if sep.Color.Hex != "#f38ba8" {
		t.Errorf("color = %+v", sep.Color)
	}
	if sep.Length.Unit != config.SizePixels || sep.Length.Value != 12 {
		t.Errorf("length = %+v, want 12px", sep.Length)
	}
	if sep.Size != 2 {
		t.Errorf("size = %d, want 2", sep.Size)
	}
}

func TestLoadFileRejectsBadSeparator(t *testing.T) {
	for _, content := range []string{
		"modules:\n  separator:\n    color: not-a-token\n",
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.LoadFile(path); err == nil {
			t.Errorf("%q: want a load error, got nil", content)
		}
	}
}
