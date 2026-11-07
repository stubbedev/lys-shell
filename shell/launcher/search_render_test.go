package launcher

import (
	"context"
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
	"github.com/stubbedev/lys-ui/anim"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
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
func paintPanel(p *panel, data []byte) { paintFrame(p.v.panelFrame, data) }

func paintFrame(f *panelFrame, data []byte) {
	clear(data)
	out := render.Rect{W: renderW, H: renderH}
	f.root.Arrange(out)
	f.root.Arrange(out)
	f.root.Paint(render.NewScaled(data, render.Stride(renderW), renderW, renderH, 1, 1))
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

func useBundledIcons(t *testing.T) {
	t.Helper()
	widget.SetIconSearchPaths([]string{"../../resources/icons"})
	widget.SetIconNamespace(icons.IconsSubdir, icons.AllPrefixes())
	t.Cleanup(func() { widget.SetIconSearchPaths(nil) })
}

// The panel paints where it says it is and leaves the rest of the output
// clear, in each state a person meets.
func TestPanelPaintsOnlyThePanel(t *testing.T) {
	useBundledIcons(t)
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
	h.until("the text", func() bool { return p.v.pane.code.Visible() })
	check("4-preview")
	h.typed(p, "6*7")
	h.until("the answer", func() bool { return p.selectedTitle() == "42" })
	check("5-answer")
}

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

func TestKeystrokeToPaintIsMeasured(t *testing.T) {
	var answer, paint, total []time.Duration
	typingWorkload(t, func(a, p time.Duration) {
		answer, paint, total = append(answer, a), append(paint, p), append(total, a+p)
	})
	for _, s := range [][]time.Duration{answer, paint, total} {
		slices.Sort(s)
	}
	t.Logf("answer p50 %v p99 %v; layout+paint p50 %v p99 %v; total p50 %v p99 %v",
		percentile(answer, 50), percentile(answer, 99), percentile(paint, 50), percentile(paint, 99), percentile(total, 50), percentile(total, 99))
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
	useBundledIcons(t)
	h := newPanelHarness(t)
	h.s.d.Sheet = apptheme.New(h.cfg).Sheet
	p, _ := h.openPanel()
	data := make([]byte, render.Stride(renderW)*renderH)
	h.typed(p, "alpha")
	h.waitTitles(p, "Alpha Docs", "Alpha Issues", webRow("alpha"))
	paintPanel(p, data)
	h.until("the rows' icons", func() bool { return len(p.v.list.waiting) == 0 })
	paintPanel(p, data)
	shown := 0
	for _, r := range p.v.list.pool {
		if r.icon.shown != "" {
			shown++
		}
	}
	if want := len(p.rowTitles()); shown != want {
		t.Fatalf("%d icons shown for %d rows", shown, want)
	}
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

// The panel's border comes from a token the theme defines: a misspelt one
// drops the declaration without a word.
func TestThePanelHasItsBorder(t *testing.T) {
	h := newPanelHarness(t)
	h.s.d.Sheet = apptheme.New(h.cfg).Sheet
	p, _ := h.openPanel()
	data := make([]byte, render.Stride(renderW)*renderH)
	paintPanel(p, data)
	b := p.v.box.Bounds()
	at := func(x, y int) render.Color { return render.ColorFromBytes(data[(y*renderW+x)*4:]) }
	if edge, inside := at(b.X+b.W/2, b.Y), at(b.X+b.W/2, b.Y+4); edge == inside {
		t.Errorf("the panel's top edge %#x is its fill: no border", uint32(edge))
	}
}

// A site's icon is a file on disk: it paints in its row like a theme's.
func TestAFileIconPaintsInItsRow(t *testing.T) {
	useBundledIcons(t)
	const side = 32
	icon := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			icon.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	path := filepath.Join(t.TempDir(), "site.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, icon); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	h := newPanelHarness(t)
	h.s.d.Sheet = apptheme.New(h.cfg).Sheet
	h.cfg.Launcher.Search.Providers = []config.LauncherSearchProvider{config.SearchLinks, config.SearchToggles}
	h.cfg.Launcher.Search.Quicklinks = []config.LauncherQuicklink{{Name: "Needle Site", URL: "https://site.example/", Icon: path}, {Name: "Needle Plain", URL: "https://plain.example/"}}
	base := h.s.d.Search
	h.s.d.Search = func() search.Deps {
		d := base()
		d.Toggles = func(context.Context) []search.Control {
			return []search.Control{{
				ID: "x", Title: i18n.T("launcher-control-audio", i18n.Str("device", "Needle Headset")), Subtitle: i18n.T("launcher-control-audio-subtitle"),
				Icon: "ld-volume-2-symbolic", Effect: search.RunNow(func(context.Context) error { return nil }),
			}}
		}
		return d
	}
	p, _ := h.openPanel()
	h.typed(p, "needle")
	h.until("the rows", func() bool { return len(p.rowTitles()) == 3 })
	data := make([]byte, render.Stride(renderW)*renderH)
	red := func() int {
		n := 0
		for i := 0; i+3 < len(data); i += 4 {
			if data[i+3] == 255 && data[i+2] > 240 && data[i+1] < 16 && data[i] < 16 {
				n++
			}
		}
		return n
	}
	h.until("the icon", func() bool {
		paintPanel(p, data)
		return red() > 64
	})
	savePanel(t, data, "file-icon")
}

func useEmojiFont(t *testing.T, h *panelHarness) {
	t.Helper()
	path := os.Getenv("LYS_SHELL_TEST_EMOJI_FONT")
	if path == "" {
		t.Fatal("LYS_SHELL_TEST_EMOJI_FONT is unset: the dev shell names a real colour emoji font")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	emoji, err := render.LoadFont(data)
	if err != nil {
		t.Fatal(err)
	}
	h.s.d.Font = render.NewChain(h.s.d.Font.(*render.Typeface), emoji)
	h.s.d.Sheet = apptheme.New(h.cfg).Sheet
}

func pixelAt(data []byte, x, y int) uint32 {
	i := (y*renderW + x) * 4
	return uint32(data[i]) | uint32(data[i+1])<<8 | uint32(data[i+2])<<16 | uint32(data[i+3])<<24
}

func inkRows(data []byte, x0, x1, ref, y0, y1 int) (top, bottom int, found bool) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			if pixelAt(data, x, y) != pixelAt(data, ref, y) {
				if !found {
					top, found = y, true
				}
				bottom = y
				break
			}
		}
	}
	return top, bottom, found
}

func TestAnEmojisInkIsCentredInItsRow(t *testing.T) {
	h := newPanelHarness(t)
	useEmojiFont(t, h)
	h.cfg.Launcher.Search.Providers = []config.LauncherSearchProvider{config.SearchEmoji}
	p, _ := h.openPanel()
	h.typed(p, "grinning")
	h.until("the faces", func() bool { return len(p.rowTitles()) >= 3 })
	data := make([]byte, render.Stride(renderW)*renderH)
	paintPanel(p, data)
	savePanel(t, data, "emoji")
	measured := 0
	for _, r := range p.v.list.pool {
		if !r.glyph.Visible() {
			continue
		}
		measured++
		rb, gb := r.Bounds(), r.glyph.Bounds()
		if gb.W != p.v.list.iconSize {
			t.Errorf("glyph %dpx wide, want the icon column's %d", gb.W, p.v.list.iconSize)
		}
		if r.glyph.Alignment() != render.AlignCenter {
			t.Error("the glyph does not centre across its column")
		}
		top, bottom, found := inkRows(data, gb.X, gb.X+gb.W, gb.X-2, rb.Y, rb.Y+rb.H)
		if !found {
			t.Fatalf("%q paints no ink in row %v", r.glyph.Text(), rb)
		}
		const tolerance = 1
		if off := (top + bottom + 1) - (2*rb.Y + rb.H); off < -2*tolerance || off > 2*tolerance {
			t.Errorf("%q ink [%d,%d] is %d half-pixels off the middle of row %v", r.glyph.Text(), top, bottom, off, rb)
		}
		if top <= rb.Y || bottom >= rb.Y+rb.H-1 {
			t.Errorf("%q ink [%d,%d] reaches the edge of row %v", r.glyph.Text(), top, bottom, rb)
		}
	}
	if measured < 3 {
		t.Fatalf("%d rows show a glyph", measured)
	}
}

func TestAnIconRowShowsNoGlyphInk(t *testing.T) {
	h := newPanelHarness(t)
	useEmojiFont(t, h)
	useBundledIcons(t)
	p, _ := h.openPanel()
	h.typed(p, "alpha")
	h.until("the rows", func() bool { return p.selectedTitle() == "Alpha Docs" })
	data := make([]byte, render.Stride(renderW)*renderH)
	paintPanel(p, data)
	for _, r := range p.v.list.pool {
		if r.glyph.Visible() {
			t.Errorf("the icon row %v shows a glyph", r.Bounds())
		}
	}
}

func TestAHeroEmojisInkStaysInsideItsLabel(t *testing.T) {
	h := newPanelHarness(t)
	useEmojiFont(t, h)
	h.cfg.Launcher.Search.Providers = []config.LauncherSearchProvider{config.SearchEmoji}
	p, _ := h.openPanel()
	h.typed(p, "grinning")
	h.until("the faces", func() bool { return p.v.pane.summary.glyph.Visible() && p.v.pane.summary.glyph.Text() != "" })
	data := make([]byte, render.Stride(renderW)*renderH)
	paintPanel(p, data)
	paintPanel(p, data)
	savePanel(t, data, "emoji-hero")
	gb := p.v.pane.summary.glyph.Bounds()
	titleTop := p.v.pane.summary.title.Bounds().Y
	top, bottom, found := inkRows(data, gb.X, gb.X+gb.W, gb.X-1, p.v.pane.box.Bounds().Y, titleTop)
	if !found {
		t.Fatalf("the hero %q paints no ink", p.v.pane.summary.glyph.Text())
	}
	if top < gb.Y || bottom >= gb.Y+gb.H {
		t.Errorf("hero ink [%d,%d] leaves its label %v", top, bottom, gb)
	}
	const tolerance = 2
	if off := (top + bottom + 1) - (2*gb.Y + gb.H); off < -2*tolerance || off > 2*tolerance {
		t.Errorf("hero ink [%d,%d] is %d half-pixels off the middle of its label %v", top, bottom, off, gb)
	}
}

func highlightRect(t *testing.T, data []byte, list render.Rect, rowY int) render.Rect {
	t.Helper()
	ref := list.X + 1
	y := rowY
	left := list.X + 2
	for left < list.X+list.W && pixelAt(data, left, y) == pixelAt(data, ref, y) {
		left++
	}
	if left == list.X+list.W {
		t.Fatalf("no highlight on line %d of list %v", y, list)
	}
	right := list.X + list.W - 2
	for pixelAt(data, right, y) == pixelAt(data, ref, y) {
		right--
	}
	x := left + 12
	top := y
	for top > list.Y && pixelAt(data, x, top-1) != pixelAt(data, ref, top-1) {
		top--
	}
	bottom := y
	for bottom+1 < list.Y+list.H && pixelAt(data, x, bottom+1) != pixelAt(data, ref, bottom+1) {
		bottom++
	}
	return render.Rect{X: left, Y: top, W: right - left + 1, H: bottom - top + 1}
}

func assertRoomBeside(t *testing.T, what string, hl, list render.Rect) {
	t.Helper()
	if got := hl.X - list.X; got != rowEdgeGap {
		t.Errorf("%s: highlight %v is %dpx from the left of list %v, want %d", what, hl, got, list, rowEdgeGap)
	}
	if got := list.X + list.W - (hl.X + hl.W); got != rowEdgeGap {
		t.Errorf("%s: highlight %v is %dpx from the right of list %v, want %d", what, hl, got, list, rowEdgeGap)
	}
}

func assertRoomAbove(t *testing.T, what string, hl, list render.Rect) {
	t.Helper()
	if got := hl.Y - list.Y; got != rowEdgeGap {
		t.Errorf("%s: highlight %v is %dpx from the top of list %v, want %d", what, hl, got, list, rowEdgeGap)
	}
}

func assertRoomBelow(t *testing.T, what string, hl, list render.Rect) {
	t.Helper()
	if got := list.Y + list.H - (hl.Y + hl.H); got != rowEdgeGap {
		t.Errorf("%s: highlight %v is %dpx from the bottom of list %v, want %d", what, hl, got, list, rowEdgeGap)
	}
}

func searchPanelOf(t *testing.T, rows int) (*panelHarness, *panel) {
	t.Helper()
	h := newPanelHarness(t)
	h.s.d.Sheet = apptheme.New(h.cfg).Sheet
	h.cfg.Launcher.Lines = 4
	h.cfg.Launcher.Search.Previews = false
	h.cfg.Launcher.Search.Providers = []config.LauncherSearchProvider{config.SearchLinks}
	links := make([]config.LauncherQuicklink, rows)
	for i := range links {
		links[i] = config.LauncherQuicklink{Name: "needle " + strconv.Itoa(i), URL: "https://x/" + strconv.Itoa(i)}
	}
	h.cfg.Launcher.Search.Quicklinks = links
	p, _ := h.openPanel()
	h.typed(p, "needle")
	h.until("the rows", func() bool { return len(p.rowTitles()) >= 1 && p.sel >= 0 })
	return h, p
}

func (p *panel) showFlat(h *panelHarness, n int) {
	frame := p.frame
	frame.Rows = make([]search.Row, n)
	for i := range frame.Rows {
		id := strconv.Itoa(i)
		frame.Rows[i] = search.Row{Entry: &search.Entry{ID: id, Title: "needle " + id}}
	}
	p.results(frame)
	h.until("a selection", func() bool { return p.sel >= 0 })
}

func (p *panel) selectedHighlight(t *testing.T, data []byte) (hl, list render.Rect) {
	t.Helper()
	paintPanel(p, data)
	for _, r := range p.v.list.pool {
		if r.index == p.sel && r.Parent() != nil && r.Visible() {
			rb := r.Bounds()
			return highlightRect(t, data, p.v.list.Bounds(), rb.Y+rb.H/2), p.v.list.Bounds()
		}
	}
	t.Fatalf("row %d is not in view", p.sel)
	return hl, list
}

func TestASearchHighlightHasRoomOnEverySideThroughAScroll(t *testing.T) {
	restore := anim.SetInstant(true)
	defer restore()
	const rows = 12
	h, p := searchPanelOf(t, rows)
	p.showFlat(h, rows)
	data := make([]byte, render.Stride(renderW)*renderH)
	hl, list := p.selectedHighlight(t, data)
	savePanel(t, data, "selection-first")
	assertRoomBeside(t, "the first row", hl, list)
	assertRoomAbove(t, "the first row", hl, list)
	for range rows - 1 {
		h.key("Down")
	}
	if p.sel != rows-1 {
		t.Fatalf("selection at %d, want the last row", p.sel)
	}
	hl, list = p.selectedHighlight(t, data)
	savePanel(t, data, "selection-last")
	assertRoomBeside(t, "the last row", hl, list)
	assertRoomBelow(t, "the last row", hl, list)
	for range 4 {
		h.key("Up")
	}
	hl, list = p.selectedHighlight(t, data)
	assertRoomAbove(t, "a row scrolled back to the top", hl, list)
	for range 4 {
		h.key("Down")
	}
	hl, list = p.selectedHighlight(t, data)
	assertRoomBelow(t, "a row scrolled down to the bottom", hl, list)
}

func TestSearchHighlightsLeaveAGapBetweenThem(t *testing.T) {
	h, p := searchPanelOf(t, 3)
	p.showFlat(h, 3)
	data := make([]byte, render.Stride(renderW)*renderH)
	first, _ := p.selectedHighlight(t, data)
	h.key("Down")
	second, list := p.selectedHighlight(t, data)
	if got := second.Y - (first.Y + first.H); got != 2*rowHalfGap {
		t.Errorf("highlights %v and %v are %dpx apart, want %d", first, second, got, 2*rowHalfGap)
	}
	x := first.X + 12
	if pixelAt(data, x, first.Y+first.H/2) != pixelAt(data, list.X+1, first.Y+first.H/2) {
		t.Error("the row left behind still paints a highlight")
	}
}

func TestAnEmptySearchListKeepsNoRoom(t *testing.T) {
	h, p := searchPanelOf(t, 3)
	h.typed(p, "nothing matches this")
	h.until("no rows", func() bool { return len(p.frame.Rows) == 0 })
	if got := p.v.list.contentH(); got != 0 {
		t.Errorf("an empty list wants %dpx", got)
	}
}

func rofiHighlight(t *testing.T, h *harness, data []byte) (hl, list render.Rect) {
	t.Helper()
	v := h.s.cur.views
	paintFrame(v.panelFrame, data)
	row, ok := v.list.rows[h.s.sel].(*launcherRow)
	if !ok {
		t.Fatalf("row %d is not laid out", h.s.sel)
	}
	rb := row.Bounds()
	return highlightRect(t, data, v.list.Bounds(), rb.Y+rb.H/2), v.list.Bounds()
}

func TestARofiHighlightHasRoomOnEverySideThroughAScroll(t *testing.T) {
	h := newHarness(t)
	h.s.d.Sheet = apptheme.New(h.cfg).Sheet
	rows := make([]string, 30)
	for i := range rows {
		rows[i] = "row " + strconv.Itoa(i)
	}
	h.open(dmenu(), false, rows...)
	h.until("the rows", func() bool { return h.s.cur != nil && h.s.model.len() == len(rows) })
	data := make([]byte, render.Stride(renderW)*renderH)
	hl, list := rofiHighlight(t, h, data)
	savePanel(t, data, "rofi-selection-first")
	assertRoomBeside(t, "the first row", hl, list)
	assertRoomAbove(t, "the first row", hl, list)
	for range len(rows) - 1 {
		h.key("Down")
	}
	h.until("the last row", func() bool { return h.s.sel == len(rows)-1 })
	hl, list = rofiHighlight(t, h, data)
	savePanel(t, data, "rofi-selection-last")
	assertRoomBeside(t, "the last row", hl, list)
	assertRoomBelow(t, "the last row", hl, list)
}

func TestARofiListWithNoRowsKeepsNoRoom(t *testing.T) {
	h := newHarness(t)
	h.open(dmenu(), false, "alpha")
	h.until("the rows", func() bool { return h.s.cur != nil && h.s.model.len() == 1 })
	v := h.s.cur.views
	v.entry.SetText("nothing matches this")
	h.until("no rows", func() bool { return h.s.model.len() == 0 })
	if got := v.list.Measure(widget.Column, 0).Nat; got != 0 {
		t.Errorf("an empty list wants %dpx", got)
	}
}
