package sharepicker

import (
	"context"
	"slices"
	"strconv"
	"testing"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/service/sharepreview"
	"github.com/stubbedev/lys-shell/shell/regionoverlay"
	"github.com/stubbedev/lys-shell/shell/treetest"
	"github.com/stubbedev/lys-shell/styling"
)

// testPicker is a picker without an application: the loop marshal is a
// no-op and the compositor queries are canned.
func testPicker(t *testing.T, cfg config.SharePickerConfig, allowToken, multiple bool) (*Picker, chan string) {
	t.Helper()
	full := config.Defaults()
	full.SharePicker = cfg
	full.Animations.Enabled = false
	p := &Picker{
		d:      Deps{Config: func() *config.Config { return full }, Font: treetest.Font(t)},
		invoke: func(func()) {},
		windows: func() []sharepreview.Toplevel {
			return []sharepreview.Toplevel{{ID: 0, Class: "foot", Title: "a", Identifier: "id-a"}, {ID: 1, Class: "kitty", Title: " "}}
		},
		outputs: func() []outputInfo {
			return []outputInfo{{name: "DP-1", width: 1920, height: 1080, scale: 1}, {name: "DP-2", x: 1920, width: 2560, height: 1440, scale: 2}}
		},
	}
	ch := make(chan string, 1)
	p.snapshot()
	p.queue.Enqueue("/r/test", func() {}, func() {})
	p.reply, p.allowToken, p.multiple = ch, allowToken, multiple
	p.root = p.build(nil, AllSources)
	return p, ch
}

// fakeRegion is the region overlay; it records cancels.
type fakeRegion struct{ cancels int }

func (*fakeRegion) Request(map[string]render.StillImage) (regionoverlay.Selection, bool) {
	return regionoverlay.Selection{}, false
}
func (f *fakeRegion) Cancel() { f.cancels++ }

// Dismissing the request the picker shows answers it cancelled, the
// region overlay it opened included; another request's dismissal does
// nothing.
func TestDismissCancelsItsRequestOnly(t *testing.T) {
	p, ch := testPicker(t, config.DefaultsSharePicker(), false, false)
	region := &fakeRegion{}
	p.region = region
	p.invoke = func(fn func()) { fn() }
	p.Dismiss("/r/other")
	if p.reply == nil {
		t.Fatal("another request's dismissal answered this one")
	}
	queued := make(chan string, 1)
	p.queue.Enqueue("/r/next", func() { t.Error("a dismissed waiting request showed") }, func() { queued <- "" })
	p.Dismiss("/r/next")
	if got := <-queued; got != "" {
		t.Errorf("a dismissed waiting request replied %q", got)
	}
	p.selecting = true
	p.Dismiss("/r/test")
	if got := <-ch; got != "" {
		t.Errorf("a dismissed picker replied %q, want the cancel", got)
	}
	if region.cancels != 1 {
		t.Errorf("region overlay cancels = %d, want the open overlay taken down", region.cancels)
	}
}

func TestSingleSelectConfirmsAtOnce(t *testing.T) {
	p, ch := testPicker(t, config.DefaultsSharePicker(), false, false)
	p.selectPayload("screen:DP-1")
	if got := <-ch; got != "/screen:DP-1" {
		t.Fatalf("reply %q", got)
	}
	p, ch = testPicker(t, config.DefaultsSharePicker(), true, false)
	p.selectPayload("window:7")
	if got := <-ch; got != "r/window:7" {
		t.Fatalf("token reply %q", got)
	}
	if p.reply != nil {
		t.Fatal("an answered request stays open")
	}
}

func TestMultiSelectTogglesAndConfirms(t *testing.T) {
	p, ch := testPicker(t, config.DefaultsSharePicker(), true, true)
	if p.confirm == nil || p.confirm.Enabled() {
		t.Fatal("multi-select starts without a disabled Share button")
	}
	p.confirmPending()
	select {
	case got := <-ch:
		t.Fatalf("an empty set confirmed %q", got)
	default:
	}
	p.selectPayload("screen:DP-1")
	p.selectPayload("window:3")
	p.selectPayload("region:DP-1@0,0,10,10")
	p.selectPayload("window:3") // toggles back out
	if !p.confirm.Enabled() || p.confirmLbl.Text() != "Share 2 sources" {
		t.Fatalf("button %v %q", p.confirm.Enabled(), p.confirmLbl.Text())
	}
	select {
	case got := <-ch:
		t.Fatalf("a toggle answered %q", got)
	default:
	}
	p.confirmPending()
	if got := <-ch; got != "r/screen:DP-1;region:DP-1@0,0,10,10" {
		t.Fatalf("reply %q", got)
	}
}

func TestCancelAnswersEmpty(t *testing.T) {
	p, ch := testPicker(t, config.DefaultsSharePicker(), true, false)
	p.answer("")
	if got := <-ch; got != "" {
		t.Fatalf("cancel %q", got)
	}
	p.answer("late") // no request open: nothing to answer, no panic
}

func TestPayloads(t *testing.T) {
	if got := windowPayload(sharepreview.Toplevel{ID: 5, Identifier: "abc"}); got != "window:abc" {
		t.Errorf("ext payload %q", got)
	}
	if got := windowPayload(sharepreview.Toplevel{ID: 5}); got != "window:5" {
		t.Errorf("XDPH payload %q", got)
	}
	if got := regionPayload(regionoverlay.Selection{Output: "DP-1", X: 1, Y: 2, Width: 3, Height: 4}); got != "region:DP-1@1,2,3,4" {
		t.Errorf("region payload %q", got)
	}
	if cardTitle(sharepreview.Toplevel{Title: "  ", Class: "kitty"}) != "kitty" || cardTitle(sharepreview.Toplevel{Title: "a", Class: "foot"}) != "a" {
		t.Error("a blank title does not fall back to the class")
	}
	if confirmLabel(0) != "Share" || confirmLabel(1) != "Share" || confirmLabel(3) != "Share 3 sources" {
		t.Error("confirm labels")
	}
}

func TestBuildPagesAndOptions(t *testing.T) {
	cfg := config.DefaultsSharePicker()
	cfg.DefaultPage = config.SharePickerOutputs
	p, _ := testPicker(t, cfg, false, false)
	nb := treetest.First[*widget.Notebook](t, p.root)
	if nb.SelectedTab() != pageOutputs {
		t.Fatalf("notebook %v", nb)
	}
	if r := treetest.WithClass(p.root, "share-picker-restore-button"); len(r) != 1 || !isLabeledCheck(r[0], "Allow a restore token") {
		t.Error("the restore-token checkbutton is missing")
	}
	if p.confirm != nil {
		t.Error("single-select built a Share button")
	}
	cfg.HideTokenRestore = true
	p, _ = testPicker(t, cfg, false, false)
	if len(treetest.WithClass(p.root, "share-picker-restore-button")) != 0 {
		t.Error("hide-token-restore kept the box")
	}
	if n := len(treetest.WithClass(p.windowsPage(nil), "share-picker-card")); n != 2 {
		t.Errorf("%d window cards, want 2", n)
	}
	if n := len(treetest.All[*widget.Button](p.outputsPage())); n != 2 {
		t.Errorf("%d output buttons, want 2", n)
	}
}

func TestWindowCardsRunInReverseWithTheirTitles(t *testing.T) {
	p, _ := testPicker(t, config.DefaultsSharePicker(), false, false)
	var titles []string
	treetest.Walk(p.windowsPage(nil), func(w widget.Widget) {
		if b, ok := w.(*widget.Box); ok && b.Element() == "flowboxchild" {
			titles = append(titles, b.TooltipText())
		}
	})
	// The tooltips carry title and class; the list order is reversed.
	if len(titles) != 2 || titles[0] != " \nkitty" || titles[1] != "a\nfoot" {
		t.Fatalf("titles %v", titles)
	}
}

// Only the pages of the sources offered are built; the configured
// default page opens when it is one of them, else the first.
func TestBuildOffersOnlyTheSourcesAsked(t *testing.T) {
	cfg := config.DefaultsSharePicker()
	cfg.DefaultPage = config.SharePickerWindows
	p, _ := testPicker(t, cfg, false, false)
	tabs := func(nb *widget.Notebook) []string {
		var out []string
		for _, name := range []string{pageWindows, pageOutputs, pageRegion} {
			if nb.TabLabel(name) != nil {
				out = append(out, name)
			}
		}
		return out
	}
	for _, tc := range []struct {
		sources  Sources
		tabs     []string
		selected string
	}{
		{AllSources, []string{pageWindows, pageOutputs, pageRegion}, pageWindows},
		{SourceOutputs | SourceRegions, []string{pageOutputs, pageRegion}, pageOutputs},
		{SourceWindows, []string{pageWindows}, pageWindows},
		{SourceRegions, []string{pageRegion}, pageRegion},
	} {
		p.root = p.build(nil, tc.sources)
		nb := treetest.First[*widget.Notebook](t, p.root)
		if got := tabs(nb); !slices.Equal(got, tc.tabs) || nb.SelectedTab() != tc.selected {
			t.Errorf("sources %#x: tabs %v on %q, want %v on %q", tc.sources, got, nb.SelectedTab(), tc.tabs, tc.selected)
		}
	}
}

func TestEmptyPagesShowPlaceholders(t *testing.T) {
	p, _ := testPicker(t, config.DefaultsSharePicker(), false, false)
	p.windows = func() []sharepreview.Toplevel { return nil }
	p.outputs = func() []outputInfo { return nil }
	p.root = p.build(nil, AllSources)
	if n := len(treetest.WithClass(p.windowsPage(nil), "share-picker-placeholder")) + len(treetest.WithClass(p.outputsPage(), "share-picker-placeholder")); n != 2 {
		t.Fatalf("%d placeholders, want 2", n)
	}
}

func TestOutputInfosAndScaling(t *testing.T) {
	infos := outputInfos([]capture.Output{
		{Name: "DP-1", Width: 1920, Height: 1080, Scale: 1},
		{Name: "DP-2", X: 1920, Width: 2560, Height: 1440, Scale: 2, Transform: capture.Transform90},
		{Name: "", Width: 800, Height: 600}, // unnamed: skipped
		{Name: "DP-3", Width: 0, Height: 0}, // no mode: skipped
	})
	if len(infos) != 2 {
		t.Fatalf("infos %+v", infos)
	}
	if infos[1].width != 1440 || infos[1].height != 2560 {
		t.Fatalf("rotation did not swap the axes: %+v", infos[1])
	}
	applyScaling(infos)
	if infos[0].width != 1920 || infos[1].width != 720 || infos[1].height != 1280 {
		t.Fatalf("scaled %+v", infos)
	}
}

func TestMonitorAreaPlacement(t *testing.T) {
	slots := []outputSlot{{x: 0, y: 0, width: 1920, height: 1080}, {x: 1920, y: 0, width: 1920, height: 1080}}
	a := newMonitorArea(slots)
	if a.width != 3840 || a.height != 1080 {
		t.Fatalf("area %+v", a)
	}
	// A wide map in a squarer box fits by width and centers vertically.
	r := a.place(slots[1], 1000, 500)
	if r.X != 500 || r.W != 500 || r.H != 281 || r.Y != 109 {
		t.Fatalf("place %+v", r)
	}
	left, top, right, bottom := a.margins(slots[0], 6)
	if left != 0 || right != 6 || top != 0 || bottom != 0 {
		t.Fatalf("margins %d %d %d %d", left, top, right, bottom)
	}
	if gridColumns(2, 3, 4) != 3 || gridColumns(10, 3, 4) != 4 || gridColumns(0, 0, 0) != 1 {
		t.Error("gridColumns")
	}
}

type fakePicker struct{ reply string }

func (f fakePicker) Pick(c portalreq.Caller, r Request) string {
	return f.reply + "|" + c.Handle + "|" + r.WindowList + "|" + boolStr(r.AllowToken) + boolStr(r.Multiple) + "|" + strconv.Itoa(int(r.Sources))
}

func (fakePicker) Dismiss(string) {}

func boolStr(b bool) string {
	if b {
		return "t"
	}
	return "f"
}

func TestDaemonOverTheBus(t *testing.T) {
	bus := dbustest.Start(t)
	server := bus.Conn(t)
	client := bus.Conn(t)
	release, err := (&Daemon{picker: fakePicker{reply: "/screen:DP-1"}}).Export(server)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	got, err := NewClient(client).Pick(context.Background(), portalreq.Caller{Handle: "/r/1"}, Request{WindowList: "1[HC>]a[HT>]b[HE>]", Sources: SourceWindows, AllowToken: true})
	if err != nil || got != "/screen:DP-1|/r/1|1[HC>]a[HT>]b[HE>]|tf|2" {
		t.Fatalf("Pick = %q %v", got, err)
	}
	// A request offering nothing to pick is refused, not shown empty.
	if got, err := NewClient(client).Pick(context.Background(), portalreq.Caller{Handle: "/r/2"}, Request{}); err == nil {
		t.Fatalf("Pick with no sources = %q", got)
	}
	if _, err := (&Daemon{picker: fakePicker{}}).Export(client); err == nil {
		t.Fatal("a second daemon claimed the owned name")
	}
}

// The surface is the view!: window.share-picker-window over the reveal
// over the .share-picker-surface at the configured size (scaled by
// [styling] scale), spinners on the window cards until the captures
// land, and every label's ink left to the stylesheet.
func TestSurfaceIsTheRustTree(t *testing.T) {
	p, _ := testPicker(t, config.DefaultsSharePicker(), false, false)
	root, ok := p.root.(*widget.Box)
	if !ok || root.Element() != "window" || !widget.HasClass(root, "share-picker-window") {
		t.Fatalf("root %T, want the .share-picker-window node", p.root)
	}
	surfaces := treetest.WithClass(p.root, "share-picker-surface")
	if len(surfaces) != 1 {
		t.Fatal("no .share-picker-surface")
	}
	if got := surfaces[0].(*widget.Box).SizeRequest(); got != (widget.Size{W: p.width(), H: p.height()}) {
		t.Errorf("surface request %v, want %dx%d", got, p.width(), p.height())
	}
	nbs := treetest.All[*widget.Notebook](p.root)
	if len(nbs) != 1 {
		t.Fatalf("%d notebooks, want the one", len(nbs))
	}
	for _, name := range []string{pageWindows, pageOutputs, pageRegion} {
		l, ok := nbs[0].TabLabel(name).(*widget.Label)
		if !ok || l.Text() != name || !widget.HasClass(l, "share-picker-tab-label") || widget.CursorNameOf(l) != "pointer" || l.Color() != 0 {
			t.Errorf("tab %q label %v, want page_label's pointer .share-picker-tab-label", name, nbs[0].TabLabel(name))
		}
	}
	if len(treetest.All[*widget.Revealer](p.root)) != 1 {
		t.Error("the surface is not behind the reveal")
	}
	if len(treetest.All[*widget.Spinner](p.windowsPage(nil))) != 2 {
		t.Error("the window cards carry no loading spinners")
	}
	for _, l := range treetest.All[*widget.Label](p.root) {
		if l.Color() != 0 {
			t.Errorf("label %q pins ink %#08x", l.Text(), uint32(l.Color()))
		}
	}
	full := config.Defaults()
	full.Styling.Scale = 2
	p.d.Config = func() *config.Config { return full }
	p.snapshot()
	if want := int(full.SharePicker.Width.ResolvePx(config.SharePickerWidthBaseRem*styling.RemBase, 2)); p.width() != want {
		t.Errorf("width %d at scale 2, want %d", p.width(), want)
	}
}

// isLabeledCheck reports a checkbutton carrying text as its label.
func isLabeledCheck(w widget.Widget, text string) bool {
	c, ok := w.(*widget.CheckButton)
	if !ok {
		return false
	}
	l, ok := c.Child().(*widget.Label)
	return ok && l.Text() == text
}

// The picker names the app asking to share.
func TestPickerNamesTheRequester(t *testing.T) {
	p, _ := testPicker(t, config.DefaultsSharePicker(), false, false)
	p.d.Describe = func(appID string) (string, string) { return "Meet " + appID, "" }
	p.requester = p.describe(portalreq.Caller{Handle: "/r/9", AppID: "org.meet"})
	p.root = p.build(nil, AllSources)
	if labels := treetest.Labels(p.root); !slices.Contains(labels, "Meet org.meet wants to share your screen") {
		t.Errorf("labels = %q", labels)
	}
}

// The picker stands on lys-ui's parts: the surface and the source
// cards are its cards, a class line its dim caption, Share its
// suggested action, and an empty page its status page laid out inside
// the page rather than across an unbounded width.
func TestPickerTakesLysUiParts(t *testing.T) {
	p, _ := testPicker(t, config.DefaultsSharePicker(), false, true)
	if s := treetest.WithClass(p.root, "share-picker-surface"); len(s) != 1 || !widget.HasClass(s[0], css.Card) {
		t.Fatal("the surface is not lys-ui's card")
	}
	cards := treetest.WithClass(p.windowsPage(nil), "share-picker-card")
	if len(cards) != 2 {
		t.Fatalf("%d window cards, want 2", len(cards))
	}
	for _, c := range cards {
		if !widget.HasClass(c, css.Card) {
			t.Error("a window card is not lys-ui's card")
		}
	}
	for _, l := range treetest.WithClass(p.windowsPage(nil), "share-picker-image-class-label") {
		if !widget.HasClass(l, css.Caption) || !widget.HasClass(l, css.DimLabel) {
			t.Error("a class line is not a dim caption")
		}
	}
	for _, l := range treetest.WithClass(p.windowsPage(nil), "share-picker-image-label") {
		if widget.HasClass(l, css.Caption) {
			t.Error("a title line is a caption")
		}
	}
	if p.confirm == nil || !widget.HasClass(p.confirm, css.SuggestedAction) {
		t.Error("Share is not the suggested action")
	}

	p.windows = func() []sharepreview.Toplevel { return nil }
	empty := p.windowsPage(nil)
	const w, h = 600, 400
	empty.Arrange(render.Rect{W: w, H: h})
	pages := treetest.All[*widget.StatusPage](empty)
	if len(pages) != 1 {
		t.Fatalf("%d status pages on the empty page, want 1", len(pages))
	}
	if b := pages[0].Bounds(); b.X < 0 || b.X+b.W > w || b.W == 0 {
		t.Errorf("empty state laid out at %v, outside the %dpx page", b, w)
	}
}
