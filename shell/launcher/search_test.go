package launcher

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/anim"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/launcheripc"
	"github.com/stubbedev/lys-shell/service/launcher/modes"
	"github.com/stubbedev/lys-shell/service/launcher/search"
	"github.com/stubbedev/lys-shell/shell/apptheme"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// panelHarness is the surface with a search session's world faked: what
// it would spawn lands in a list, and the rows come from real providers
// over config, so a key press runs the path a person's does.
type panelHarness struct {
	*harness
	spawned []string
	windows *panelFakeWindows
}

type panelFakeWindows struct {
	list    []modes.WindowInfo
	focused []string
}

func (w *panelFakeWindows) List(context.Context) ([]modes.WindowInfo, error) { return w.list, nil }
func (w *panelFakeWindows) Focus(_ context.Context, id string) error {
	w.focused = append(w.focused, id)
	return nil
}
func (w *panelFakeWindows) Close(context.Context, string) error { return nil }

func newPanelHarness(t *testing.T) *panelHarness {
	t.Helper()
	h := newHarness(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	ph := &panelHarness{harness: h, windows: &panelFakeWindows{}}
	h.cfg.Launcher.Search.Providers = []config.LauncherSearchProvider{
		config.SearchCalculator, config.SearchWindows, config.SearchLinks, config.SearchWeb,
	}
	h.cfg.Launcher.Search.Quicklinks = []config.LauncherQuicklink{
		{Name: "Alpha Docs", URL: "https://alpha/"},
		{Name: "Alpha Issues", URL: "https://alpha/issues"},
		{Name: "Beta Search", URL: "https://beta/?q={query}", Keyword: "bs"},
	}
	h.cfg.Launcher.Search.Snippets = []config.LauncherSnippet{{Name: "Signature", Text: "Yours\ntruly"}}
	h.s.d.Search = func() search.Deps {
		return search.Deps{
			Env: search.Env{
				Spawn:      func(argv []string) { ph.record(strings.Join(argv, " ")) },
				SpawnShell: func(line string) { ph.record(line) },
			},
			Windows: ph.windows,
		}
	}
	h.s.d.OpenFrecency = func() (*search.Frecency, error) {
		return search.OpenFrecency(t.TempDir()+"/f.json", time.Now)
	}
	return ph
}

func (h *panelHarness) record(s string) {
	h.loop.invoke(func() { h.spawned = append(h.spawned, s) })
}

func searchOptions() launcheripc.SessionOptions {
	mode := SearchMode
	return launcheripc.SessionOptions{Mode: &mode}
}

// openPanel opens a session and waits for its window.
func (h *panelHarness) openPanel() (*panel, <-chan launcheripc.ServerFrame) {
	h.t.Helper()
	_, reply := h.open(searchOptions(), false)
	h.until("the panel", func() bool { return h.s.panel != nil && h.s.panel.win != nil })
	return h.s.panel, reply
}

func (p *panel) rowTitles() []string {
	var out []string
	for _, r := range p.frame.Rows {
		if r.Header == "" {
			out = append(out, r.Entry.Title)
		}
	}
	return out
}

func (h *panelHarness) typed(p *panel, text string) {
	h.t.Helper()
	p.v.entry.SetText(text)
	h.until("rows for "+text, func() bool { return p.frame.Query == text })
}

func (h *panelHarness) waitTitles(p *panel, want ...string) {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !slices.Equal(p.rowTitles(), want) {
		if time.Now().After(deadline) {
			h.t.Fatalf("rows = %q, want %q", p.rowTitles(), want)
		}
		h.loop.pump()
		time.Sleep(time.Millisecond)
	}
}

func (p *panel) selectedTitle() string {
	r, ok := p.selected()
	if !ok {
		return ""
	}
	return r.Entry.Title
}

func TestSearchPanelMapsOverTheOutputWithTheKeyboard(t *testing.T) {
	h := newPanelHarness(t)
	h.openPanel()
	cfg := h.harness.windows[0].cfg
	if cfg.Anchor != app.AnchorTop|app.AnchorBottom|app.AnchorLeft|app.AnchorRight || cfg.Keyboard != app.KeyboardExclusive || !cfg.BackgroundBlur || cfg.Output != nil {
		t.Errorf("layer = %+v, want full output, exclusive keys, blur, the compositor's choice of output", cfg)
	}
	if h.harness.windows[0].focused() == nil {
		t.Error("the field must take the keys")
	}
}

func TestBareInvocationIsSearchAndDmenuStaysDmenu(t *testing.T) {
	mode := "drun"
	if wantsSearch(launcheripc.SessionOptions{Mode: &mode}) || wantsSearch(dmenu()) {
		t.Error("rofi sessions must stay rofi sessions")
	}
	opts := searchOptions()
	opts.Dmenu = true
	if wantsSearch(opts) {
		t.Error("-dmenu wins")
	}
	if !wantsSearch(searchOptions()) {
		t.Error("-show search opens the panel")
	}
}

func TestKeyboardFlowSelectsOverHeadingsAndWraps(t *testing.T) {
	h := newPanelHarness(t)
	p, reply := h.openPanel()
	h.typed(p, "alpha")
	h.waitTitles(p, "Alpha Docs", "Alpha Issues", webRow("alpha"))
	if got := p.selectedTitle(); got != "Alpha Docs" {
		t.Fatalf("first selection %q", got)
	}
	h.key("Down")
	if got := p.selectedTitle(); got != "Alpha Issues" {
		t.Errorf("Down = %q", got)
	}
	h.key("Control+n")
	if got := p.selectedTitle(); got != webRow("alpha") {
		t.Errorf("Ctrl-N over the heading = %q", got)
	}
	h.key("Control+n")
	if got := p.selectedTitle(); got != "Alpha Docs" {
		t.Errorf("wrap = %q", got)
	}
	h.key("Control+p")
	if got := p.selectedTitle(); got != webRow("alpha") {
		t.Errorf("Ctrl-P wrap = %q", got)
	}
	h.key("Up")
	if got := p.selectedTitle(); got != "Alpha Issues" {
		t.Errorf("Up = %q", got)
	}
	h.key("Return")
	h.until("the session ended", func() bool { return h.s.panel == nil })
	h.until("the open", func() bool { return len(h.spawned) == 1 })
	if h.spawned[0] != "xdg-open https://alpha/issues" {
		t.Errorf("ran %v", h.spawned)
	}
	if f := h.frame(reply); f.Type != launcheripc.FrameResult {
		t.Errorf("reply %+v", f)
	}
}

func TestEscapeGoesBackALevelThenCloses(t *testing.T) {
	h := newPanelHarness(t)
	p, reply := h.openPanel()
	h.typed(p, "alpha")
	h.waitTitles(p, "Alpha Docs", "Alpha Issues", webRow("alpha"))
	h.key("Down")
	h.key("Control+k")
	h.until("the actions", func() bool { return p.depth == 1 })
	if p.v.entry.Text() != "" || !p.v.chip.Visible() || p.v.chip.Text() != "Alpha Issues" {
		t.Errorf("actions: field %q chip %q", p.v.entry.Text(), p.v.chip.Text())
	}
	if got := p.rowTitles(); !slices.Contains(got, "Copy URL") {
		t.Errorf("actions = %v", got)
	}
	h.key("Escape")
	h.until("back", func() bool { return p.depth == 0 })
	if p.v.entry.Text() != "alpha" {
		t.Errorf("field = %q, want the query restored", p.v.entry.Text())
	}
	if got := p.selectedTitle(); got != "Alpha Issues" {
		t.Errorf("selection = %q, want it where it was", got)
	}
	h.key("Escape")
	h.until("closed", func() bool { return h.s.panel == nil })
	if f := h.frame(reply); f.Type != launcheripc.FrameCancelled || f.Code != 1 {
		t.Errorf("reply %+v", f)
	}
}

func TestBackspaceOnAnEmptyFieldLeavesTheLevel(t *testing.T) {
	h := newPanelHarness(t)
	p, _ := h.openPanel()
	h.typed(p, "alpha")
	h.waitTitles(p, "Alpha Docs", "Alpha Issues", webRow("alpha"))
	if h.key("BackSpace") {
		t.Error("with text in the field Backspace edits it")
	}
	h.key("Control+k")
	h.until("the actions", func() bool { return p.depth == 1 })
	if !h.key("BackSpace") {
		t.Error("on an empty field in a level, Backspace goes back")
	}
	h.until("back", func() bool { return p.depth == 0 })
}

func TestTabCompletesOrOpensActions(t *testing.T) {
	h := newPanelHarness(t)
	p, _ := h.openPanel()
	h.typed(p, "beta")
	h.waitTitles(p, "Beta Search", webRow("beta"))
	h.key("Tab")
	h.until("the completion's rows", func() bool { return p.frame.Query == "bs " })
	if p.v.entry.Text() != "bs " {
		t.Errorf("field = %q", p.v.entry.Text())
	}
	p.v.entry.SetText("bs rust lang")
	h.until("the filled link", func() bool { return p.selectedTitle() == "Beta Search: rust lang" })
	h.key("Return")
	h.until("the open", func() bool { return len(h.spawned) == 1 })
	if h.spawned[0] != "xdg-open https://beta/?q=rust+lang" {
		t.Errorf("ran %v", h.spawned)
	}

	h = newPanelHarness(t)
	p, _ = h.openPanel()
	h.typed(p, "alpha docs")
	h.until("the row", func() bool { return p.selectedTitle() == "Alpha Docs" })
	h.key("Tab")
	h.until("the actions", func() bool { return p.depth == 1 })
}

func TestNumberShortcutsPickTheNthRowDigitsTypeInTheField(t *testing.T) {
	h := newPanelHarness(t)
	p, _ := h.openPanel()
	h.typed(p, "alpha")
	h.waitTitles(p, "Alpha Docs", "Alpha Issues", webRow("alpha"))
	h.harness.windows[0].cfg.Root.Arrange(render.Rect{W: 1200, H: 900})
	if h.key("2") {
		t.Error("a plain digit is what you search with")
	}
	h.key("Control+2")
	h.until("the open", func() bool { return len(h.spawned) == 1 })
	if h.spawned[0] != "xdg-open https://alpha/issues" {
		t.Errorf("ran %v", h.spawned)
	}
}

func TestEnterBeforeTheRowsArriveTakesWhatTheTextFinds(t *testing.T) {
	h := newPanelHarness(t)
	p, _ := h.openPanel()
	h.typed(p, "alpha docs")
	h.until("the row", func() bool { return p.selectedTitle() == "Alpha Docs" })
	// No pump between the keys: the engine has not answered.
	p.v.entry.SetText("alpha issues")
	if !h.key("Return") {
		t.Fatal("Enter must be taken")
	}
	h.until("the open", func() bool { return len(h.spawned) == 1 })
	if h.spawned[0] != "xdg-open https://alpha/issues" {
		t.Errorf("ran %v, want the row the last text finds", h.spawned)
	}
}

func TestSecondActionAndCalculatorCopy(t *testing.T) {
	h := newPanelHarness(t)
	p, reply := h.openPanel()
	h.typed(p, "6*7")
	h.until("the answer", func() bool { return p.selectedTitle() == "42" })
	h.key("Return")
	h.until("closed", func() bool { return h.s.panel == nil })
	if !slices.Equal(h.copied, []string{"42"}) {
		t.Errorf("copied %v", h.copied)
	}
	if f := h.frame(reply); len(f.Selected) != 1 || f.Selected[0].Text != "42" {
		t.Errorf("reply %+v", f)
	}
}

func TestWindowsJumpFromTheList(t *testing.T) {
	h := newPanelHarness(t)
	h.windows.list = []modes.WindowInfo{{ID: "9", Title: "Quarterly Report", Class: "Writer"}}
	p, _ := h.openPanel()
	h.until("the window row", func() bool {
		p.v.entry.SetText("quarterly")
		h.loop.pump()
		return p.selectedTitle() == "Quarterly Report"
	})
	h.key("Return")
	h.until("the focus", func() bool { return h.s.panel == nil && len(h.windows.focused) == 1 })
	if h.windows.focused[0] != "9" {
		t.Errorf("focused %v", h.windows.focused)
	}
}

func TestPressingTheBindAgainClosesAndOtherClientsAreBusy(t *testing.T) {
	h := newPanelHarness(t)
	_, first := h.openPanel()
	_, busy := h.open(dmenu(), false, "a")
	h.until("busy", func() bool { return len(busy) == 1 })
	if f := h.frame(busy); f.Type != launcheripc.FrameBusy {
		t.Errorf("a dmenu while the panel shows: %+v", f)
	}
	if h.s.panel == nil {
		t.Fatal("the panel must stay")
	}
	_, again := h.open(searchOptions(), false)
	h.until("the toggle", func() bool { return h.s.panel == nil })
	if f := h.frame(first); f.Type != launcheripc.FrameCancelled {
		t.Errorf("first reply %+v", f)
	}
	if f := h.frame(again); f.Type != launcheripc.FrameCancelled {
		t.Errorf("second reply %+v", f)
	}
}

func TestRofiSessionsStillWorkBesideThePanel(t *testing.T) {
	h := newPanelHarness(t)
	_, reply := h.open(dmenu(), false, "one", "two")
	h.until("rows", func() bool { return h.s.model.len() == 2 })
	h.key("Return")
	if f := h.frame(reply); len(f.Selected) != 1 || f.Selected[0].Text != "one" {
		t.Errorf("reply %+v", f)
	}
}

func TestPanelLeavesNothingRunningOnceClosed(t *testing.T) {
	h := newPanelHarness(t)
	p, _ := h.openPanel()
	eng := p.eng
	h.key("Escape")
	h.until("closed", func() bool { return h.s.panel == nil })
	select {
	case <-eng.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the engine is still running")
	}
	if !h.harness.windows[0].closed {
		t.Error("the surface must go")
	}
}

func TestHeightFollowsTheRowsAndTheFieldStays(t *testing.T) {
	h := newPanelHarness(t)
	p, _ := h.openPanel()
	v := p.v
	out := render.Rect{W: 1600, H: 1000}
	v.root.Arrange(out)
	if got := v.motion.targetH; got != v.fieldH() {
		t.Errorf("empty panel height %d, want the field alone %d", got, v.fieldH())
	}
	top := v.motion.shownRect(out).Y
	h.typed(p, "alpha")
	h.waitTitles(p, "Alpha Docs", "Alpha Issues", webRow("alpha"))
	grown := v.motion.targetH
	if grown <= v.fieldH() {
		t.Errorf("height %d did not grow with the rows", grown)
	}
	if got := v.motion.shownRect(out).Y; got != top {
		t.Errorf("the panel's top moved from %d to %d", top, got)
	}
	h.typed(p, "alpha docs")
	h.waitTitles(p, "Alpha Docs", webRow("alpha docs"))
	if v.motion.targetH >= grown {
		t.Errorf("height %d did not shrink from %d", v.motion.targetH, grown)
	}
}

func TestListHeightIsCappedByLinesAndScrollsToTheSelection(t *testing.T) {
	h := newPanelHarness(t)
	h.cfg.Launcher.Lines = 2
	h.cfg.Launcher.Search.Previews = false
	p, _ := h.openPanel()
	v := p.v
	out := render.Rect{W: 1600, H: 1000}
	h.typed(p, "a")
	h.until("rows", func() bool { return len(p.frame.Rows) >= 4 })
	v.root.Arrange(out)
	limit := v.fieldH() + 1 + 2*v.list.rowH + v.footerH()
	if v.motion.targetH > limit {
		t.Errorf("height %d over the cap %d", v.motion.targetH, limit)
	}
	v.root.Arrange(out)
	for range len(p.frame.Rows) {
		h.key("Down")
	}
	v.root.Arrange(out)
	last := p.sel
	if y, off := v.list.topOf(last), int(v.list.offset); y+v.list.rowH-off > v.list.Bounds().H {
		t.Errorf("selected row at %d, scroll %d, view %d: off screen", y, off, v.list.Bounds().H)
	}
}

func TestReducedMotionLandsAtOnce(t *testing.T) {
	restore := anim.SetInstant(true)
	defer restore()
	h := newPanelHarness(t)
	h.cfg.Animations.Enabled = true
	p, _ := h.openPanel()
	if p.v.motion.reveal != 1 {
		t.Errorf("reveal = %v, want it open at once", p.v.motion.reveal)
	}
	h.typed(p, "alpha")
	h.waitTitles(p, "Alpha Docs", "Alpha Issues", webRow("alpha"))
	if int(p.v.motion.h) != p.v.motion.targetH {
		t.Errorf("height %v, want %d at once", p.v.motion.h, p.v.motion.targetH)
	}
	h.key("Escape")
	h.until("closed", func() bool { return h.harness.windows[0].closed })
}

func TestDisabledAnimationsCloseAtOnceAndEnabledPlayTheExit(t *testing.T) {
	h := newPanelHarness(t)
	p, _ := h.openPanel()
	if p.v.motion.reveal != 1 {
		t.Errorf("animations off: reveal %v", p.v.motion.reveal)
	}
	h.key("Escape")
	h.until("closed", func() bool { return h.harness.windows[0].closed })

	h = newPanelHarness(t)
	h.cfg.Animations.Enabled = true
	p, _ = h.openPanel()
	if p.v.motion.reveal >= 1 {
		t.Errorf("animations on: reveal %v at the first frame", p.v.motion.reveal)
	}
	h.key("Escape")
	h.until("the session ended", func() bool { return h.s.panel == nil })
	if h.harness.windows[0].closed {
		t.Error("the surface closed before its exit played")
	}
}

func TestLauncherAnimationOverrideNoneIsInstant(t *testing.T) {
	h := newPanelHarness(t)
	h.cfg.Animations.Enabled = true
	none := config.AnimationNone
	h.cfg.Animations.Launcher.Enter = &none
	p, _ := h.openPanel()
	if p.v.motion.reveal != 1 {
		t.Errorf("reveal = %v", p.v.motion.reveal)
	}
}

func TestTransitionsMoveThePanelTheirWay(t *testing.T) {
	if s := panelStyleFor(config.AnimationFade); s.grow || s.shift != 0 {
		t.Errorf("fade: %+v", s)
	}
	if s := panelStyleFor(config.AnimationSlideUp); s.shift <= 0 {
		t.Errorf("slide up rises from below: %+v", s)
	}
	if s := panelStyleFor(config.AnimationSlideDown); s.shift >= 0 {
		t.Errorf("slide down falls from above: %+v", s)
	}
	if s := panelStyleFor(config.AnimationZoom); !s.grow {
		t.Errorf("zoom: %+v", s)
	}
}

func TestBlurRegionIsThePanelNotTheOutput(t *testing.T) {
	h := newPanelHarness(t)
	theme := apptheme.New(h.cfg)
	h.s.d.Sheet = theme.Sheet
	p, _ := h.openPanel()
	v := p.v
	out := render.Rect{W: 1600, H: 1000}
	v.root.Arrange(out)
	v.root.Arrange(out)
	region := widget.BackdropRegion(v.root)
	if len(region) == 0 {
		t.Fatal("no blur region")
	}
	bound := region[0]
	for _, r := range region[1:] {
		bound = bound.Union(r)
	}
	if bound.W >= out.W || bound.H >= out.H {
		t.Errorf("blur covers %+v of %+v", bound, out)
	}
	if bound != v.box.Bounds() {
		t.Errorf("blur %+v, panel %+v", bound, v.box.Bounds())
	}
}

func TestClickOutsideDismissesAndARowClickAccepts(t *testing.T) {
	h := newPanelHarness(t)
	p, _ := h.openPanel()
	v := p.v
	out := render.Rect{W: 1600, H: 1000}
	h.typed(p, "alpha")
	h.waitTitles(p, "Alpha Docs", "Alpha Issues", webRow("alpha"))
	v.root.Arrange(out)
	v.root.Arrange(out)
	if hit := v.root.HitTest(widget.Point{X: 2, Y: 2}); hit != widget.Widget(v.root) {
		t.Errorf("outside hit %T", hit)
	}
	row := v.list.pool[len(v.list.shown)-1]
	b := row.Bounds()
	if hit := v.root.HitTest(widget.Point{X: b.X + 4, Y: b.Y + 4}); hit != widget.Widget(row) {
		t.Errorf("row hit %T", hit)
	}
	h.harness.windows[0].cfg.OnPress(widget.BTNLeft, v.root)
	h.until("dismissed", func() bool { return h.s.panel == nil })
}

func TestFadeFallsBackToPaintWithoutTheProtocol(t *testing.T) {
	h := newPanelHarness(t)
	h.cfg.Animations.Enabled = true
	p, _ := h.openPanel()
	m := p.v.motion
	if m.paintAlpha() != 1 {
		t.Errorf("compositor fade paints at %v", m.paintAlpha())
	}
	if len(h.harness.windows[0].opacity) == 0 {
		t.Error("the fade must reach the compositor")
	}
	h.harness.windows[0].opacityErr = errors.New("no protocol")
	m.reveal = 0.5
	m.applyReveal()
	if m.compositorFade || m.paintAlpha() != 0.5 {
		t.Errorf("fallback: compositor %v, paint %v", m.compositorFade, m.paintAlpha())
	}
}

func TestPreviewLoadsWhenTheSelectionRests(t *testing.T) {
	h := newPanelHarness(t)
	p, _ := h.openPanel()
	h.typed(p, "signature")
	h.until("the snippet", func() bool { return p.selectedTitle() == "Signature" })
	if !p.v.withPreview {
		t.Fatal("a previewable row opens the pane")
	}
	if p.v.motion.targetW <= p.v.baseW {
		t.Errorf("width %d does not make room", p.v.motion.targetW)
	}
	timers := len(h.timers)
	if timers == 0 {
		t.Fatal("the preview must wait for the selection to rest")
	}
	h.timers[timers-1].fn()
	h.until("the text", func() bool { return p.v.text.Visible() && strings.Contains(p.v.text.Text(), "truly") })
}

func TestPreviewOffStaysOff(t *testing.T) {
	h := newPanelHarness(t)
	h.cfg.Launcher.Search.Previews = false
	p, _ := h.openPanel()
	h.typed(p, "signature")
	h.until("the snippet", func() bool { return p.selectedTitle() == "Signature" })
	if p.v.withPreview {
		t.Error("previews: false must not open the pane")
	}
}

func TestHighlightMarkup(t *testing.T) {
	open := `<span weight="700" foreground="#112233">`
	for _, tc := range []struct {
		title string
		at    []int
		want  string
	}{
		{"abc", nil, "abc"},
		{"abc", []int{0, 1}, open + "ab</span>c"},
		{"abc", []int{2}, "ab" + open + "c</span>"},
		{"a<b", []int{1}, "a" + open + "&lt;</span>b"},
		{"it's", nil, "it&apos;s"},
		{"é🙂x", []int{1}, "é" + open + "🙂</span>x"},
	} {
		if got := string(appendHighlightMarkup(nil, tc.title, tc.at, open)); got != tc.want {
			t.Errorf("%q %v = %q, want %q", tc.title, tc.at, got, tc.want)
		}
	}
}

func TestEveryPanelKeyParsesAndDigitsStayOut(t *testing.T) {
	for _, spec := range []string{"Down", "Up", "Control+n", "Control+p", "Return", "Tab", "Escape", "Control+k", "Control+1", "Control+9", "Page_Down"} {
		accel, err := app.ParseAccel(spec)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := lookupPanelKey(accel); !ok {
			t.Errorf("%s is not bound", spec)
		}
	}
	for _, spec := range []string{"1", "a", "Left", "Right", "Control+a"} {
		accel, err := app.ParseAccel(spec)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := lookupPanelKey(accel); ok {
			t.Errorf("%s must reach the field", spec)
		}
	}
}

// key is a key press through the panel's capture, as the layer hands it.
func (h *panelHarness) key(spec string) bool {
	h.t.Helper()
	if h.s.panel == nil {
		return h.harness.key(spec)
	}
	accel, err := app.ParseAccel(spec)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.s.panel.keyCaptured(accel)
}

// webRow is the fallback row's title, as the locale words it.
func webRow(query string) string {
	return i18n.T("launcher-web-search", i18n.Str("engine", "DuckDuckGo"), i18n.Str("query", query))
}

func TestLateRowsTakeTheSelectionUnlessThePersonMovedIt(t *testing.T) {
	h := newPanelHarness(t)
	p, _ := h.openPanel()
	row := func(id string) search.Row { return search.Row{Entry: &search.Entry{ID: id, Title: id}} }
	p.results(search.Frame{Query: "x", Rows: []search.Row{row("web")}})
	p.results(search.Frame{Query: "x", Late: true, Rows: []search.Row{row("app"), row("web")}})
	if got := p.selectedTitle(); got != "app" {
		t.Errorf("selection = %q, want the best row: nobody chose the fallback", got)
	}
	p.move(1)
	p.results(search.Frame{Query: "x", Late: true, Rows: []search.Row{row("new"), row("app"), row("web")}})
	if got := p.selectedTitle(); got != "web" {
		t.Errorf("selection = %q, want the row the person moved to", got)
	}
	p.results(search.Frame{Query: "xy", Rows: []search.Row{row("app"), row("web")}})
	if got := p.selectedTitle(); got != "app" {
		t.Errorf("a new query selects its best row, got %q", got)
	}
}

func TestTypingBeforeTheFirstFrameIsKept(t *testing.T) {
	h := newPanelHarness(t)
	_, _ = h.open(searchOptions(), false)
	h.s.d.Invoke(func() {})
	h.until("the panel", func() bool { return h.s.panel != nil && h.s.panel.win != nil })
	p := h.s.panel
	p.v.entry.SetText("6*7")
	h.until("the answer", func() bool { return p.selectedTitle() == "42" })
	if got := p.v.entry.Text(); got != "6*7" {
		t.Errorf("field = %q: a frame older than the keys overwrote them", got)
	}
	p.results(search.Frame{Query: "6*7", QuerySeq: 0, SetField: true, Field: "stale"})
	if got := p.v.entry.Text(); got != "6*7" {
		t.Errorf("field = %q after a stale frame", got)
	}
	p.results(search.Frame{Query: "new", QuerySeq: p.querySeq, SetField: true, Field: "new"})
	if got := p.v.entry.Text(); got != "new" {
		t.Errorf("field = %q, a current frame may set it", got)
	}
}

func TestDisabledAnimationsLandSizeAndScrollAtOnce(t *testing.T) {
	h := newPanelHarness(t)
	p, _ := h.openPanel()
	h.typed(p, "alpha")
	h.waitTitles(p, "Alpha Docs", "Alpha Issues", webRow("alpha"))
	if got := int(p.v.motion.h); got != p.v.motion.targetH {
		t.Errorf("height %d, want %d with animations off", got, p.v.motion.targetH)
	}
}

func TestPanelOwnsItsMotion(t *testing.T) {
	h := newPanelHarness(t)
	h.openPanel()
	if !h.harness.windows[0].cfg.OwnMotion {
		t.Error("the toolkit would scale the whole output's buffer every frame")
	}
}

func TestApostropheInATitleIsMarkupTheLabelReads(t *testing.T) {
	l := widget.NewRichLabel(treetest.Font(t), 14, string(appendEscaped(nil, "it's")), 0)
	if !strings.Contains(l.Text(), "it's") {
		t.Errorf("label reads %q", l.Text())
	}
}

func TestMonitorPicksTheOutputAndAnUnknownOneStillOpens(t *testing.T) {
	h := newPanelHarness(t)
	second := &app.Output{Name: "DP-2"}
	h.s.d.Outputs = func() []*app.Output { return []*app.Output{{Name: "DP-1"}, second} }
	h.cfg.Launcher.Monitor = "DP-2"
	h.openPanel()
	if got := h.harness.windows[0].cfg.Output; got != second {
		t.Errorf("output = %+v, want DP-2", got)
	}
	h.key("Escape")
	h.until("closed", func() bool { return h.s.panel == nil })

	h.cfg.Launcher.Monitor = "HDMI-9"
	h.openPanel()
	if got := h.harness.windows[1].cfg.Output; got != nil {
		t.Errorf("an unknown output must leave it to the compositor, got %+v", got)
	}
}

func TestMonitorAlsoReachesARofiSession(t *testing.T) {
	h := newPanelHarness(t)
	second := &app.Output{Name: "DP-2"}
	h.s.d.Outputs = func() []*app.Output { return []*app.Output{second} }
	h.cfg.Launcher.Monitor = "DP-2"
	_, _ = h.open(dmenu(), false, "a")
	h.until("the surface", func() bool { return len(h.harness.windows) == 1 })
	if h.harness.windows[0].cfg.Output != second {
		t.Error("launcher.monitor is every surface's")
	}
}

// loaderHarness lets a test decide when a decode finishes.
func TestIconsDecodeOffTheLoopAndLandWithoutMovingAnything(t *testing.T) {
	h := newPanelHarness(t)
	const name = "no-such-launcher-test-icon"
	h.cfg.Launcher.Search.Quicklinks[1].Icon = name
	p, _ := h.openPanel()
	v := p.v
	decoded := make(chan iconRequest, 8)
	release := make(chan struct{})
	v.list.loader.setDecode(func(r iconRequest) {
		decoded <- r
		<-release
	})
	defer close(release)
	out := render.Rect{W: 1600, H: 1000}
	h.typed(p, "alpha")
	h.waitTitles(p, "Alpha Docs", "Alpha Issues", webRow("alpha"))
	v.root.Arrange(out)
	var target *searchRow
	for _, r := range v.list.pool {
		if r.icon.name == name {
			target = r
		}
	}
	if target == nil {
		t.Fatal("no row bound the icon")
	}
	before := target.Bounds()
	if target.icon.shown == name {
		t.Fatal("shown before its decode")
	}
	select {
	case <-decoded:
	case <-time.After(10 * time.Second):
		t.Fatal("the decode must run on the loader, with the UI loop free")
	}
	v.root.Arrange(out)
	if target.Bounds() != before {
		t.Errorf("waiting moved the row: %+v to %+v", before, target.Bounds())
	}
	if got := target.icon.slot.Bounds().W; got != target.icon.size {
		t.Errorf("slot %d wide, want its icon's %d before the icon lands", got, target.icon.size)
	}
	v.list.iconReady(iconRequest{name: name, size: target.icon.size})
	if target.icon.shown != name {
		t.Error("the row did not take its icon when ready")
	}
	v.root.Arrange(out)
	if target.Bounds() != before {
		t.Errorf("landing moved the row: %+v to %+v", before, target.Bounds())
	}
}

func TestAStaleIconDoesNotLandOnARowThatMovedOn(t *testing.T) {
	h := newPanelHarness(t)
	p, _ := h.openPanel()
	l := p.v.list
	l.loader.setDecode(func(iconRequest) {})
	a := newAsyncIcon(24, "x")
	l.bindAsync(a, "first-launcher-test-icon")
	l.bindAsync(a, "second-launcher-test-icon")
	l.iconReady(iconRequest{name: "first-launcher-test-icon", size: 24})
	if a.shown == "first-launcher-test-icon" {
		t.Error("a decode for a row that moved on must not show")
	}
	l.iconReady(iconRequest{name: "second-launcher-test-icon", size: 24})
	if a.shown != "second-launcher-test-icon" {
		t.Errorf("shown %q", a.shown)
	}
}

func TestLoaderStopsWithThePanel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var ready []iconRequest
	loop := &fakeLoop{}
	l := newIconLoader(ctx, loop.invoke, func(r iconRequest) { ready = append(ready, r) })
	gate := make(chan struct{})
	l.setDecode(func(iconRequest) { <-gate })
	l.request(iconRequest{name: "a"})
	l.request(iconRequest{name: "b"})
	l.request(iconRequest{name: "b"})
	cancel()
	close(gate)
	deadline := time.Now().Add(10 * time.Second)
	for {
		l.mu.Lock()
		running := l.running
		l.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the loader kept running")
		}
		time.Sleep(time.Millisecond)
	}
	loop.pump()
	if len(ready) != 0 {
		t.Errorf("delivered after the panel closed: %v", ready)
	}
}

func TestReducedMotionFadesAndNothingMoves(t *testing.T) {
	anim.SetReduced(true)
	defer anim.SetReduced(false)
	h := newPanelHarness(t)
	h.cfg.Animations.Enabled = true
	p, _ := h.openPanel()
	m := p.v.motion
	if m.reveal >= 1 {
		t.Errorf("reveal %v: reduced motion keeps the fade", m.reveal)
	}
	if m.style.grow || m.style.shift != 0 {
		t.Errorf("style %+v: nothing may move", m.style)
	}
	out := render.Rect{W: 1600, H: 1000}
	v := p.v
	v.root.Arrange(out)
	if got, want := v.box.Bounds(), m.restRect(out); got != want {
		t.Errorf("panel at %+v mid-fade, want its resting %+v", got, want)
	}
	h.typed(p, "alpha")
	h.waitTitles(p, "Alpha Docs", "Alpha Issues", webRow("alpha"))
	if int(m.h) != m.targetH {
		t.Errorf("height %v, want %d at once", m.h, m.targetH)
	}
}

func TestFramesAndBindsAllocateLittleOnceWarm(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector allocates")
	}
	h := newPanelHarness(t)
	p, _ := h.openPanel()
	data := make([]byte, render.Stride(renderW)*renderH)
	h.typed(p, "alpha")
	h.waitTitles(p, "Alpha Docs", "Alpha Issues", webRow("alpha"))
	paintPanel(p, data)
	out := render.Rect{W: renderW, H: renderH}
	for range len(p.frame.Rows) {
		p.move(1)
		p.v.root.Arrange(out)
	}
	selection := testing.AllocsPerRun(50, func() {
		p.move(1)
		p.v.root.Arrange(out)
	})
	t.Logf("selection move and layout, rows seen before: %v allocations", selection)
	// What is left is lys-ui restyling the two rows whose state flipped.
	if selection > 4 {
		t.Errorf("moving over rows already seen allocates %v", selection)
	}
}

// animations.reduced-motion, the shell's own key, as the shell applies it:
// the toolkit's preference on, and every transition resolving to a fade.
func TestReducedMotionConfigKeyFadesThePanel(t *testing.T) {
	h := newPanelHarness(t)
	h.cfg.Animations.Enabled = true
	h.cfg.Animations.ReducedMotion = true
	zoom := config.AnimationZoom
	h.cfg.Animations.Launcher.Enter = &zoom
	if got := h.cfg.Animations.TransitionFor(config.AnimLauncher, false); got != config.AnimationFade {
		t.Fatalf("transition %v, want the fade the key promises", got)
	}
	widget.SetReducedMotion(h.cfg.Animations.Reduced())
	defer widget.SetReducedMotion(false)
	p, _ := h.openPanel()
	m := p.v.motion
	if m.style.grow || m.style.shift != 0 {
		t.Errorf("style %+v: nothing may move", m.style)
	}
	if m.reveal >= 1 || m.instant {
		t.Errorf("reveal %v instant %v: it must fade, not pop", m.reveal, m.instant)
	}
}

// setDecode replaces the loader's decoder, which may already be running.
func (l *iconLoader) setDecode(decode func(iconRequest)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.decode = decode
}

// A frame for the row already selected, as a provider publishing late
// sends, must not cancel the preview on its way: the row would keep its
// empty pane for good, since nothing asks for it again.
func TestAFrameForTheSameRowLeavesItsPreviewLoading(t *testing.T) {
	h := newPanelHarness(t)
	p, _ := h.openPanel()
	h.typed(p, "signature")
	h.until("the snippet", func() bool { return p.selectedTitle() == "Signature" })
	waiting := h.timers[len(h.timers)-1]
	count := len(h.timers)
	p.results(search.Frame{Query: "signature", Late: true, Rows: slices.Clone(p.frame.Rows)})
	if waiting.stopped {
		t.Fatal("the same row's frame cancelled its preview")
	}
	if len(h.timers) != count {
		t.Errorf("%d new timers: the same row need not load twice", len(h.timers)-count)
	}
	waiting.fn()
	h.until("the text", func() bool { return p.v.text.Visible() && strings.Contains(p.v.text.Text(), "truly") })
}
