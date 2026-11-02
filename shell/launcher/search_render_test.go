package launcher

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/icons"
	"github.com/stubbedev/lys-shell/shell/apptheme"
)

const (
	renderW = 1600
	renderH = 1000
)

// paintPanel lays the panel out on an output and paints it.
func paintPanel(p *panel, data []byte) {
	clear(data)
	out := render.Rect{W: renderW, H: renderH}
	p.v.root.Arrange(out)
	p.v.root.Arrange(out)
	p.v.root.Paint(render.NewScaled(data, render.Stride(renderW), renderW, renderH, 1, 1))
}

// savePanel writes the frame over a test backdrop when
// LYS_SHELL_LAUNCHER_RENDER names a directory, for a person to look at.
func savePanel(t *testing.T, data []byte, name string) {
	t.Helper()
	dir := os.Getenv("LYS_SHELL_LAUNCHER_RENDER")
	if dir == "" {
		return
	}
	img := image.NewNRGBA(image.Rect(0, 0, renderW, renderH))
	for y := range renderH {
		for x := range renderW {
			i := (y*renderW + x) * 4
			a := uint32(data[i+3])
			bg := [3]uint32{uint32(40 + x*80/renderW), uint32(70 + y*60/renderH), 130}
			// The canvas is premultiplied BGRA.
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(uint32(data[i+2]) + bg[0]*(255-a)/255),
				G: uint8(uint32(data[i+1]) + bg[1]*(255-a)/255),
				B: uint8(uint32(data[i]) + bg[2]*(255-a)/255),
				A: 255,
			})
		}
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func alphaAt(data []byte, x, y int) byte { return data[(y*renderW+x)*4+3] }

// The panel paints where it says it is and leaves the rest of the output
// clear, in each state a person meets.
func TestPanelPaintsOnlyThePanel(t *testing.T) {
	widget.SetIconSearchPaths([]string{"../../resources/icons"})
	widget.SetIconNamespace(icons.IconsSubdir, icons.AllPrefixes())
	defer widget.SetIconSearchPaths(nil)
	for _, name := range []string{"ld-search-symbolic", "ld-link-symbolic", "ld-external-link-symbolic", "ld-calculator-symbolic", "ld-globe-symbolic"} {
		if !widget.ThemeIconExists(name) {
			t.Errorf("the bundle has no %s", name)
		}
	}
	h := newPanelHarness(t)
	h.s.d.Sheet = apptheme.New(h.cfg).Sheet
	p, _ := h.openPanel()
	data := make([]byte, render.Stride(renderW)*renderH)
	check := func(name string) {
		t.Helper()
		paintPanel(p, data)
		savePanel(t, data, name)
		b := p.v.box.Bounds()
		if b.Empty() {
			t.Fatalf("%s: no panel", name)
		}
		if alphaAt(data, b.X+b.W/2, b.Y+b.H/2) == 0 {
			t.Errorf("%s: the panel's middle is clear", name)
		}
		// Past where any shadow reaches.
		if alphaAt(data, 2, 2) != 0 || alphaAt(data, renderW-3, renderH-3) != 0 {
			t.Errorf("%s: the output's corners are painted", name)
		}
	}
	check("1-empty")
	h.typed(p, "alpha")
	h.waitTitles(p, "Alpha Docs", "Alpha Issues", webRow("alpha"))
	check("2-results")
	h.key("Down")
	h.key("Control+k")
	h.until("the actions", func() bool { return p.depth == 1 })
	check("3-actions")
	h.key("Escape")
	h.until("back", func() bool { return p.depth == 0 })
	h.typed(p, "signature")
	h.until("the snippet", func() bool { return p.selectedTitle() == "Signature" })
	h.timers[len(h.timers)-1].fn()
	h.until("the text", func() bool { return p.v.text.Visible() })
	check("4-preview")
	h.typed(p, "6*7")
	h.until("the answer", func() bool { return p.selectedTitle() == "42" })
	check("5-answer")
}

func percentile(sorted []time.Duration, p int) time.Duration { return sorted[len(sorted)*p/100] }

// TestKeystrokeToPaintBudget measures, over 5,000 searchable rows on the
// real engine goroutine, from a key's edit of the field to its rows being
// laid out and painted on a 1600x1000 canvas in software. It bounds a
// regression loosely; the numbers are in the verbose output.
func TestKeystrokeToPaintBudget(t *testing.T) {
	h := newPanelHarness(t)
	h.s.d.Sheet = apptheme.New(h.cfg).Sheet
	links := make([]config.LauncherQuicklink, 5000)
	for i := range links {
		name := fmt.Sprintf("project-%04d report %d", i, i%37)
		links[i] = config.LauncherQuicklink{Name: name, URL: "https://x/" + name}
	}
	h.cfg.Launcher.Search.Quicklinks = links
	p, _ := h.openPanel()
	data := make([]byte, render.Stride(renderW)*renderH)
	h.typed(p, "project-0001")
	h.until("the rows", func() bool { return len(p.frame.Rows) > 2 })
	paintPanel(p, data)
	queries := []string{"p", "pr", "pro", "proj", "proje", "project", "project-", "project-1", "project-12", "project-123"}
	var answer, paint, total []time.Duration
	for range 12 {
		for _, q := range queries {
			start := time.Now()
			p.v.entry.SetText(q)
			for p.frame.Query != q {
				h.loop.pump()
				if time.Since(start) > 10*time.Second {
					t.Fatalf("no answer for %q", q)
				}
			}
			answered := time.Now()
			paintPanel(p, data)
			done := time.Now()
			answer = append(answer, answered.Sub(start))
			paint = append(paint, done.Sub(answered))
			total = append(total, done.Sub(start))
		}
	}
	for _, s := range [][]time.Duration{answer, paint, total} {
		slices.Sort(s)
	}
	t.Logf("answer p50 %v p99 %v; layout+paint p50 %v p99 %v; total p50 %v p99 %v",
		percentile(answer, 50), percentile(answer, 99), percentile(paint, 50), percentile(paint, 99), percentile(total, 50), percentile(total, 99))
	if got := percentile(total, 50); got > 100*time.Millisecond {
		t.Errorf("median keystroke to paint %v", got)
	}
}
