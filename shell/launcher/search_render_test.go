package launcher

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/desktopentry"
	"github.com/stubbedev/lys-shell/internal/icons"
	"github.com/stubbedev/lys-shell/service/launcher/search"
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

const keystrokeBudget = 50 * time.Millisecond

func percentile(sorted []time.Duration, p int) time.Duration { return sorted[len(sorted)*p/100] }

// typingWorkload types queries into a panel over 5,000 searchable rows on
// the real engine goroutine, laying out and painting each answer on a
// 1600x1000 canvas in software, and hands every keystroke's durations to
// observe.
func typingWorkload(t *testing.T, observe func(answer, paint time.Duration)) {
	t.Helper()
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
			observe(answered.Sub(start), done.Sub(answered))
		}
	}
}

func TestKeystrokeToPaintBudget(t *testing.T) {
	var answer, paint, total []time.Duration
	typingWorkload(t, func(a, p time.Duration) {
		answer, paint, total = append(answer, a), append(paint, p), append(total, a+p)
	})
	for _, s := range [][]time.Duration{answer, paint, total} {
		slices.Sort(s)
	}
	t.Logf("answer p50 %v p99 %v; layout+paint p50 %v p99 %v; total p50 %v p99 %v",
		percentile(answer, 50), percentile(answer, 99), percentile(paint, 50), percentile(paint, 99), percentile(total, 50), percentile(total, 99))
	if got := percentile(total, 50); !raceEnabled && got > keystrokeBudget {
		t.Errorf("median keystroke to paint %v, over %v", got, keystrokeBudget)
	}
}

// fileIO is the process's read and write syscalls so far, from the kernel's
// own count, which is all files, sockets and pipes.
func fileIO(t *testing.T) (reads, writes int) {
	t.Helper()
	data, err := os.ReadFile("/proc/self/io")
	if err != nil {
		t.Skipf("this kernel does not count a process's I/O: %v", err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		key, value, _ := strings.Cut(line, ": ")
		n, _ := strconv.Atoi(value)
		switch key {
		case "syscr":
			reads = n
		case "syscw":
			writes = n
		}
	}
	return reads, writes
}

// Typing reads and writes nothing: no file, on the engine goroutine or the
// UI loop. A provider or row that takes to touching the disk per keystroke
// fails here, however fast the disk.
func TestTypingDoesNoFileIO(t *testing.T) {
	const allowed = 10 // reading /proc/self/io itself, and stray runtime reads
	keys := 0
	started := false
	var reads0, writes0 int
	typingWorkload(t, func(time.Duration, time.Duration) {
		if !started {
			started = true
			reads0, writes0 = fileIO(t)
			return
		}
		keys++
	})
	reads, writes := fileIO(t)
	t.Logf("%d keystrokes: %d reads, %d writes", keys, reads-reads0, writes-writes0)
	if reads-reads0 > allowed || writes-writes0 > allowed {
		t.Errorf("%d keystrokes made %d reads and %d writes, want none of their own", keys, reads-reads0, writes-writes0)
	}
}

// However long the list, layout binds a viewport's worth of rows.
func TestLayoutBindsOnlyTheRowsInView(t *testing.T) {
	h := newPanelHarness(t)
	h.cfg.Launcher.Lines = 6
	links := make([]config.LauncherQuicklink, 200)
	for i := range links {
		links[i] = config.LauncherQuicklink{Name: "project " + strconv.Itoa(i), URL: "https://x/"}
	}
	h.cfg.Launcher.Search.Quicklinks = links
	h.cfg.Launcher.Search.Previews = false
	p, _ := h.openPanel()
	h.typed(p, "project")
	h.until("the rows", func() bool { return len(p.frame.Rows) > 6 })
	out := render.Rect{W: renderW, H: renderH}
	p.v.root.Arrange(out)
	p.v.root.Arrange(out)
	// The six lines, a part-row at each edge, and a heading.
	if got := len(p.v.list.pool); got > 6+3 {
		t.Errorf("%d row widgets for six visible lines", got)
	}
}

// TestRealIconsCostOnTheLoop measures, with this machine's real
// application icons, what the UI loop spends painting rows whose icons
// are not decoded yet: decoding at first paint, as rows did, against
// the loader decoding first. Set LYS_SHELL_LAUNCHER_ICONS to run it; it
// needs the host's icon themes.
func TestRealIconsCostOnTheLoop(t *testing.T) {
	if os.Getenv("LYS_SHELL_LAUNCHER_ICONS") == "" {
		t.Skip("set LYS_SHELL_LAUNCHER_ICONS to measure with this host's icons")
	}
	var names []string
	for _, app := range desktopentry.AllApps(desktopentry.ApplicationDirs()) {
		if name := app.Icon(); name != "" && !strings.HasPrefix(name, "/") && !slices.Contains(names, name) && widget.ThemeIconExists(name) {
			names = append(names, name)
		}
	}
	if len(names) < 8 {
		t.Skipf("only %d real icons here", len(names))
	}
	names = names[:min(len(names), 8)]
	const size = 26
	data := make([]byte, render.Stride(size)*size)
	cv := render.New(data, render.Stride(size), size, size)
	paintAll := func() time.Duration {
		start := time.Now()
		for _, name := range names {
			icon := widget.NewThemeIconFallback(name, size)
			icon.Arrange(render.Rect{W: size, H: size})
			icon.Paint(cv)
		}
		return time.Since(start)
	}
	var syncFirst, preloadOffLoop, paintAfter []time.Duration
	for range 15 {
		widget.RefreshIcons()
		syncFirst = append(syncFirst, paintAll())
		widget.RefreshIcons()
		start := time.Now()
		for _, name := range names {
			widget.PreloadThemeIcon(name, size, 1, 1)
		}
		preloadOffLoop = append(preloadOffLoop, time.Since(start))
		paintAfter = append(paintAfter, paintAll())
	}
	widget.RefreshIcons()
	for _, s := range [][]time.Duration{syncFirst, preloadOffLoop, paintAfter} {
		slices.Sort(s)
	}
	t.Logf("%d real icons, first paint on the UI loop: decoded there p50 %v max %v; preloaded, p50 %v max %v on the loop; decode moved off it p50 %v",
		len(names), percentile(syncFirst, 50), syncFirst[len(syncFirst)-1], percentile(paintAfter, 50), paintAfter[len(paintAfter)-1], percentile(preloadOffLoop, 50))
}

// TestWholeKeystrokeAllocations logs what one keystroke costs on the UI
// loop, as frames with different rows swap, and bounds it.
func TestWholeKeystrokeAllocations(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector allocates")
	}
	h := newPanelHarness(t)
	h.s.d.Sheet = apptheme.New(h.cfg).Sheet
	p, _ := h.openPanel()
	data := make([]byte, render.Stride(renderW)*renderH)
	h.typed(p, "alpha")
	h.waitTitles(p, "Alpha Docs", "Alpha Issues", webRow("alpha"))
	paintPanel(p, data)
	first := slices.Clone(p.frame.Rows)
	frame := search.Frame{Query: "alpha", Rows: first}
	other := search.Frame{Query: "alph", Rows: first}
	out := render.Rect{W: renderW, H: renderH}
	cv := render.NewScaled(data, render.Stride(renderW), renderW, renderH, 1, 1)
	eng := p.eng
	p.eng = nil
	defer func() { p.eng = eng }()
	swap := false
	run := func() {
		if swap {
			p.results(frame)
		} else {
			p.results(other)
		}
		swap = !swap
		p.v.root.Arrange(out)
		p.v.root.Paint(cv)
	}
	for range 4 {
		run()
	}
	got := testing.AllocsPerRun(50, run)
	t.Logf("same rows, query changes the highlights: %v allocations per frame on the UI loop", got)
	if got > 40 {
		t.Errorf("%v allocations per keystroke frame", got)
	}
}
