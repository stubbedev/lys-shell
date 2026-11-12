package sharepicker

import (
	"context"
	"errors"
	"fmt"
	"image"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/shell/treetest"
	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/keytest"
	"github.com/stubbedev/lys-shell/service/sharepreview"
	"github.com/stubbedev/lys-shell/shell/regionoverlay"
)

// Physical keyboard codes this test's keymap leaves without a symbol.
const (
	physicalTab   = 15
	physicalRight = 106
	buttonLeft    = 0x110
)

// keyRig is a picker on a private sway with a toplevel of the same
// client focused, as the app asking to share is, and the remapped
// keyboard.
type keyRig struct {
	t         *testing.T
	a         *app.Application
	p         *Picker
	kb        *keytest.Keyboard
	answers   chan string
	callbacks *atomic.Uint64
}

// keyPicker shows the picker for r over five windows and two outputs,
// on the page named, and waits until it holds the keyboard: the
// compositor sends keys to its surface, not to the requester's, and its
// focus is on one of its widgets.
func keyPicker(t *testing.T, page config.SharePickerPage, r Request) *keyRig {
	t.Helper()
	a := apptest.Run(t)
	apptest.On(a, func() error {
		_, err := a.NewWindow(app.WindowConfig{Title: "Requester", AppID: "requester", Root: widget.NewBox(widget.Column, 0, 0)})
		return err
	})
	p, _ := headlessPicker(t, a, page)
	callbacks := new(atomic.Uint64)
	p.invoke = func(fn func()) {
		a.Invoke(func() {
			callbacks.Add(1)
			fn()
		})
	}
	t.Cleanup(func() { p.Dismiss("/r/keys") })
	kb := keytest.Dial(t, a)
	answers := make(chan string, 1)
	go func() { answers <- p.Pick(portaldialogs.Caller{Handle: "/r/keys"}, r) }()
	apptest.Until(t, a, "the picker holds the keyboard, laid out", func() bool {
		if p.layer == nil || a.Session().KeyboardFocus() != p.layer.HostSurface() || !treetest.Contains(p.root, a.Focused()) {
			return false
		}
		choices := p.choices[p.nb.SelectedTab()]
		return len(choices) > 0 && !slices.ContainsFunc(choices, func(c widget.Widget) bool { return c.(widget.Boundser).Bounds().W == 0 })
	})
	return &keyRig{t: t, a: a, p: p, kb: kb, answers: answers, callbacks: callbacks}
}

// headlessPicker is a picker on a's compositor over five windows and
// two outputs, opening on page, with animations off and, as the bar
// runs it, lys-ui's own surface motion off too; presented closes
// once the compositor presented its surface's first buffer, drawn at
// the size it was configured to.
func headlessPicker(t *testing.T, a *app.Application, page config.SharePickerPage) (p *Picker, presented <-chan struct{}) {
	t.Helper()
	apptest.On(a, func() bool { a.SetSurfaceMotion(false); return true })
	t.Cleanup(func() { apptest.On(a, func() bool { a.SetSurfaceMotion(true); return true }) })
	cfg := config.Defaults()
	cfg.Animations.Enabled = false
	cfg.SharePicker.DefaultPage = page
	p = New(a, Deps{Config: func() *config.Config { return cfg }, Font: treetest.Font(t)}, &regionoverlay.Gate{}, nil, nil)
	shown := make(chan struct{})
	p.presented = sync.OnceFunc(func() { close(shown) })
	p.windows = func() []sharepreview.Toplevel { return fiveWindows }
	p.outputs = func() []outputInfo {
		return []outputInfo{{name: "DP-1", width: 1920, height: 1080, scale: 1}, {name: "DP-2", x: 1920, width: 1920, height: 1080, scale: 1}}
	}
	return p, shown
}

var fiveWindows = func() []sharepreview.Toplevel {
	out := make([]sharepreview.Toplevel, 5)
	for i := range out {
		out[i] = sharepreview.Toplevel{ID: uint64(i), Class: "app", Title: fmt.Sprint("window ", i), Identifier: fmt.Sprint("id-", i)}
	}
	return out
}()

// windowAt is the payload of the window card at index i in display
// order, which reverses the list.
func windowAt(i int) string { return windowPayload(fiveWindows[len(fiveWindows)-1-i]) }

// press taps code and fails with what unless the focus is then on the
// open page's choice at index want (in paint order).
func (k *keyRig) press(code uint32, want int, what string) {
	k.t.Helper()
	k.kb.Tap(code)
	if got := apptest.On(k.a, func() int { return slices.Index(k.p.choices[k.p.nb.SelectedTab()], k.a.Focused()) }); got != want {
		k.t.Fatalf("%s: the focus is on choice %d, want %d", what, got, want)
	}
}

func (k *keyRig) answer(what string) string {
	k.t.Helper()
	return apptest.Answer(k.t, what, k.answers)
}

// Enter answers the opened page's first card at once, though another
// window of the client was focused when the picker came up; the
// physical keyboard's codes for Tab and Right, which carry no key in
// this keymap, move nothing.
func TestHeadlessEnterChoosesTheFirstCard(t *testing.T) {
	k := keyPicker(t, config.SharePickerWindows, Request{Sources: AllSources})
	k.kb.Tap(physicalTab)
	k.kb.Tap(physicalRight)
	apptest.Never(t, "a key with no symbol", k.answers)
	k.kb.Tap(keytest.Return)
	if got, want := k.answer("Enter on the first card"), "/"+windowAt(0); got != want {
		t.Fatalf("Enter answered %q, want %q", got, want)
	}
}

// In the window grid (five cards: a full row and one below) Down moves
// to the card below; Up, Left, and then Right and Down from the last
// card have no card that way and stay put.
func TestHeadlessArrowsMoveBetweenWindowCards(t *testing.T) {
	k := keyPicker(t, config.SharePickerWindows, Request{Sources: AllSources})
	defaults := config.DefaultsSharePicker()
	cols := gridColumns(len(fiveWindows), int(defaults.WindowsMinPerRow), int(defaults.WindowsMaxPerRow))
	if cols >= len(fiveWindows) {
		t.Fatalf("test premise: %d columns leave no second row", cols)
	}
	k.press(keytest.Up, 0, "Up from the first card stays")
	k.press(keytest.Left, 0, "Left from the first card stays")
	k.press(keytest.Down, cols, "Down reaches the first card of the second row")
	k.press(keytest.Right, cols, "Right from the last card stays")
	k.press(keytest.Down, cols, "Down from the last row stays")
	apptest.Never(t, "an arrow", k.answers)
	k.kb.Tap(keytest.Return)
	if got, want := k.answer("Enter after the arrows"), "/"+windowAt(cols); got != want {
		t.Fatalf("Enter answered %q, want %q (the first card of the second row)", got, want)
	}
}

// On the output map Right reaches the output to the right; past the
// last output it stays, and Space chooses as Enter does.
func TestHeadlessArrowsMoveAcrossOutputs(t *testing.T) {
	k := keyPicker(t, config.SharePickerOutputs, Request{Sources: SourceOutputs | SourceRegions})
	k.press(keytest.Right, 1, "Right reaches DP-2")
	k.press(keytest.Right, 1, "Right past the last output stays")
	k.kb.Tap(keytest.Space)
	if got := k.answer("Space on the right output"); got != "/screen:DP-2" {
		t.Fatalf("Right, Right then Space answered %q, want /screen:DP-2", got)
	}
}

// Ctrl+PageDown and Ctrl+PageUp switch pages, wrapping, and the page
// switched to has its first card focused; PageDown alone switches
// nothing.
func TestHeadlessCtrlPageKeysSwitchPages(t *testing.T) {
	k := keyPicker(t, config.SharePickerWindows, Request{Sources: AllSources})
	selected := func() string { return apptest.On(k.a, func() string { return k.p.nb.SelectedTab() }) }
	k.kb.Tap(keytest.PageDown)
	apptest.Never(t, "PageDown", k.answers)
	if got := selected(); got != pageWindows {
		t.Fatalf("PageDown without Ctrl opened %s", got)
	}
	k.kb.CtrlTap(keytest.PageUp)
	apptest.Until(t, k.a, "Ctrl+PageUp from Windows wraps to Dynamic", func() bool { return k.p.nb.SelectedTab() == pageDynamic })
	k.kb.CtrlTap(keytest.PageDown)
	k.kb.CtrlTap(keytest.PageDown)
	apptest.Until(t, k.a, "Ctrl+PageDown twice from Dynamic wraps to Outputs", func() bool { return k.p.nb.SelectedTab() == pageOutputs })
	k.kb.Tap(keytest.Return)
	if got := k.answer("Enter on the Outputs page"); got != "/screen:DP-1" {
		t.Fatalf("Enter answered %q, want the first output", got)
	}
}

// In multi-select mode Space toggles the focused card and answers
// nothing; Tab reaches the Share button, and Enter on it answers the
// set.
func TestHeadlessMultiSelectByKeyboard(t *testing.T) {
	k := keyPicker(t, config.SharePickerOutputs, Request{Sources: SourceOutputs, Multiple: true})
	k.kb.Tap(keytest.Space)
	k.press(keytest.Right, 1, "Right reaches DP-2")
	k.kb.Tap(keytest.Space)
	apptest.Never(t, "toggling cards", k.answers)
	k.kb.Tap(keytest.Tab)
	k.kb.Tap(keytest.Tab)
	apptest.Until(t, k.a, "two Tabs from the last card, past the restore box, reach Share", func() bool { return k.a.Focused() == widget.Widget(k.p.confirm) })
	k.kb.Tap(keytest.Return)
	if got := k.answer("Enter on Share"); got != "/screen:DP-1;screen:DP-2" {
		t.Fatalf("Share answered %q, want both outputs", got)
	}
}

// Escape cancels the picker on a real compositor by the symbol the
// keyboard's keymap gives it; the code a physical keyboard gives Escape,
// Return in that keymap, leaves the picker open.
func TestHeadlessEscapeCancelsByItsSymbol(t *testing.T) {
	a := apptest.Run(t)
	cfg := config.Defaults()
	cfg.Animations.Enabled = false
	p := New(a, Deps{Config: func() *config.Config { return cfg }, Font: treetest.Font(t)}, &regionoverlay.Gate{}, nil, nil)
	p.windows = func() []sharepreview.Toplevel { return nil }
	p.outputs = func() []outputInfo { return []outputInfo{{name: "HEADLESS-1", width: 1920, height: 1080, scale: 1}} }
	t.Cleanup(func() { p.Dismiss("/r/headless") })
	answers := make(chan string, 1)
	go func() { answers <- p.Pick(portaldialogs.Caller{Handle: "/r/headless"}, Request{Sources: AllSources}) }()
	kb := keytest.Dial(t, a)
	keytest.AwaitFocus(t, a)
	kb.Tap(keytest.Return)
	apptest.Never(t, "Return on a physical keyboard's Escape code", answers)
	kb.Tap(keytest.Escape)
	if got := apptest.Answer(t, "the picker after Escape", answers); got != "" {
		t.Fatalf("Escape answered %q, want a cancel", got)
	}
}

// A card spins while its preview loads; once every capture has landed,
// failed here as these windows do not exist, the open picker idles: no
// widget invalidates and no frame is drawn while nothing changes.
func TestHeadlessIdlePickerDrawsNothing(t *testing.T) {
	k := keyPicker(t, config.SharePickerWindows, Request{Sources: AllSources})
	spinners := apptest.On(k.a, func() []*widget.Spinner { return treetest.All[*widget.Spinner](k.p.root) })
	if len(spinners) != len(fiveWindows) {
		t.Fatalf("%d spinners, want one per window card (%d)", len(spinners), len(fiveWindows))
	}
	apptest.Until(t, k.a, "every capture of every page landed, each window's hiding its spinner", func() bool {
		return !slices.ContainsFunc(spinners, func(s *widget.Spinner) bool { return s.Visible() }) && k.p.loadingCards() == 0
	})
	widget.WaitAssetLoads()
	apptest.Drawn(t, k.a, k.p.layer.HostSurface(), k.p.root.(interface{ Invalidate() }).Invalidate)
	before := k.idleState()
	time.Sleep(idleWindow)
	if after := k.idleState(); after.damage != before.damage || after.walks != before.walks {
		t.Errorf("the idle picker invalidated %d times and walked damage %d times in %v\nbefore: %+v\nafter:  %+v",
			after.damage-before.damage, after.walks-before.walks, idleWindow, before, after)
	}
}

type idleState struct {
	damage, walks   uint64
	pickerCallbacks uint64
	assetsOwed      int
	loadingCards    int
	focus           string
	keyboardHeld    bool
}

func (k *keyRig) idleState() idleState {
	return apptest.On(k.a, func() idleState {
		focused := k.a.Focused()
		return idleState{
			damage:          widget.DamageGeneration(),
			walks:           widget.DamageWalks(),
			pickerCallbacks: k.callbacks.Load(),
			assetsOwed:      widget.TreeAssetsOwed(k.p.root),
			loadingCards:    k.p.loadingCards(),
			focus:           fmt.Sprintf("%T %q", focused, widget.Describe(focused).Name),
			keyboardHeld:    k.a.Session().KeyboardFocus() == k.p.layer.HostSurface(),
		}
	})
}

func (p *Picker) loadingCards() int {
	n := 0
	for _, choices := range p.choices {
		for _, c := range choices {
			n += len(treetest.WithClass(c, "share-picker-card-loading"))
		}
	}
	return n
}

// idleWindow is how long the idle picker is watched for frames.
const idleWindow = 500 * time.Millisecond

// A window's preview stays in its card whatever its shape: the card is
// as tall as the configured preview height allows, so a window taller
// than wide keeps its title and class on the page, and every card of a
// row lines up.
func TestHeadlessTallPreviewKeepsItsCardsTitle(t *testing.T) {
	a := apptest.Run(t)
	p, presented := headlessPicker(t, a, config.SharePickerWindows)
	t.Cleanup(func() { p.Dismiss("/r/tall") })
	shapes := []image.Point{{X: 500, Y: 4000}, {X: 4000, Y: 500}, {X: 800, Y: 800}, {X: 640, Y: 1280}, {X: 1280, Y: 640}}
	var landed atomic.Int32
	p.windowPreview = func(tl sharepreview.Toplevel, _ int) (*image.RGBA, error) {
		defer landed.Add(1)
		return image.NewRGBA(image.Rectangle{Max: shapes[tl.ID]}), nil
	}
	answers := make(chan string, 1)
	go func() { answers <- p.Pick(portaldialogs.Caller{Handle: "/r/tall"}, Request{Sources: AllSources}) }()
	apptest.Answer(t, "the picker presented", presented)
	apptest.Until(t, a, "every preview landed and laid out", func() bool {
		return int(landed.Load()) == len(shapes) && len(treetest.WithClass(p.root, "share-picker-card-loading")) == 0 && p.laidOutPreviews() == len(shapes)
	})
	cards := apptest.On(a, func() []cardLayout { return p.windowCardLayouts() })
	if len(cards) != len(shapes) {
		t.Fatalf("%d cards laid out, want %d", len(cards), len(shapes))
	}
	want := apptest.On(a, p.widgetSize)
	for i, c := range cards {
		if c.preview.H != want {
			t.Errorf("card %d's preview is %dpx tall, want the configured %dpx", i, c.preview.H, want)
		}
		if !c.preview.In(c.card) || !c.title.In(c.card) || !c.class.In(c.card) {
			t.Errorf("card %d %v does not hold its preview %v title %v and class %v", i, c.card, c.preview, c.title, c.class)
		}
		if c.title.Y < c.preview.Y+c.preview.H {
			t.Errorf("card %d's title %v overlaps its preview %v", i, c.title, c.preview)
		}
		if c.card.H != cards[0].card.H {
			t.Errorf("card %d is %dpx tall, card 0 %dpx", i, c.card.H, cards[0].card.H)
		}
		if i < 2 && !c.class.In(c.page) {
			t.Errorf("the first row's card %d has its class %v outside the page %v", i, c.class, c.page)
		}
	}
}

// cardLayout is where one window card's parts landed.
type cardLayout struct{ page, card, preview, title, class rect }

// rect is a laid out widget's box.
type rect render.Rect

// In reports whether the whole box lies inside o.
func (r rect) In(o rect) bool {
	return r.W > 0 && r.H > 0 && r.X >= o.X && r.Y >= o.Y && r.X+r.W <= o.X+o.W && r.Y+r.H <= o.Y+o.H
}

func boundsOf(w widget.Widget) rect { return rect(w.(widget.Boundser).Bounds()) }

// laidOutPreviews counts the window previews with a box and a picture.
func (p *Picker) laidOutPreviews() int {
	n := 0
	for _, im := range treetest.WithClass(p.root, "share-picker-image") {
		if b := boundsOf(im); b.W > 0 && b.H > 0 {
			n++
		}
	}
	return n
}

// windowCardLayouts reads the open page's window cards.
func (p *Picker) windowCardLayouts() []cardLayout {
	page := treetest.WithClass(p.root, "share-picker-page")
	var out []cardLayout
	for _, button := range treetest.WithClass(p.root, "share-picker-window-button") {
		out = append(out, cardLayout{
			page:    boundsOf(page[0]),
			card:    boundsOf(treetest.WithClass(button, "share-picker-card")[0]),
			preview: boundsOf(treetest.WithClass(button, "share-picker-image")[0]),
			title:   boundsOf(treetest.WithClass(button, "share-picker-image-label")[0]),
			class:   boundsOf(treetest.WithClass(button, "share-picker-image-class-label")[0]),
		})
	}
	return out
}

// regionRig is a share picker on its Region page with the real region overlay,
// and that overlay's gate as a screenshot press reaches it.
type regionRig struct {
	*keyRig
	gate   *regionoverlay.Gate
	region *regionoverlay.Overlay
}

func regionPicker(t *testing.T) *regionRig {
	t.Helper()
	k := keyPicker(t, config.SharePickerRegion, Request{Sources: SourceRegions})
	gate := &regionoverlay.Gate{}
	region := regionoverlay.New(k.a, gate, k.a.Session().Outputs, nil, treetest.Font(t), nil)
	apptest.On(k.a, func() bool { k.p.region, k.p.gate = region, gate; return true })
	return &regionRig{keyRig: k, gate: gate, region: region}
}

// selectRegion presses the Select region button.
func (r *regionRig) selectRegion(t *testing.T) {
	t.Helper()
	apptest.On(r.a, func() bool {
		r.p.choices[pageRegion][0].(*widget.Button).OnClick()
		return true
	})
}

// open reports whether a capture overlay session is open.
func (r *regionRig) open() bool { return r.gate.Open() }

// drawRegion drags out 100,100 to 300,200 on the overlay. The picker's surface
// can still hold the keyboard while the overlay maps, so a drag that reached
// nothing is repeated until one lands; one that lands must draw exactly this.
func (r *regionRig) drawRegion(t *testing.T) {
	t.Helper()
	w, h := r.kb.OutputSize()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		for _, step := range []func() error{
			func() error { return r.kb.MotionAbsolute(100, 100, uint32(w), uint32(h)) },
			func() error { return r.kb.Button(buttonLeft, true) },
			func() error { return r.kb.MotionAbsolute(300, 200, uint32(w), uint32(h)) },
			func() error { return r.kb.Button(buttonLeft, false) },
		} {
			if err := step(); err != nil {
				t.Fatal(err)
			}
			if err := r.kb.Roundtrip(); err != nil {
				t.Fatal(err)
			}
			apptest.Dispatched(t, r.a)
		}
		got := apptest.On(r.a, r.region.Selected)
		if got.Empty() {
			continue
		}
		if want := image.Rect(100, 100, 300, 200); got != want {
			t.Fatalf("the drag drew %v, want %v", got, want)
		}
		return
	}
	t.Fatal("no drag reached the overlay")
}

// A screenshot press during the share picker's region selection does not
// cancel it: it opens nothing and captures nothing, and the selection the
// user drew reaches the requester.
func TestHeadlessScreenshotPressLeavesTheRegionSelection(t *testing.T) {
	r := regionPicker(t)
	r.selectRegion(t)
	apptest.Until(t, r.a, "the overlay took the keyboard", func() bool {
		return r.open() && r.p.layer == nil && r.a.Session().KeyboardFocus() != nil
	})
	r.drawRegion(t)
	err := within(t, "the press's refusal", func() error {
		_, _, err := r.region.Request(context.Background(), func() (map[string]render.StillImage, error) {
			t.Error("the refused press took a capture")
			return nil, nil
		})
		return err
	})
	if !errors.Is(err, regionoverlay.ErrBusy) {
		t.Fatalf("the press answered %v, want ErrBusy", err)
	}
	apptest.Never(t, "the picker after a refused press", r.answers)
	r.kb.Tap(keytest.Return)
	got := apptest.Answer(t, "the picker after Return", r.answers)
	if want := "/region:" + apptest.On(r.a, func() string { return r.a.Session().Outputs()[0].Name }) + "@100,100,200,100"; got != want {
		t.Fatalf("the picker answered %q, want %q", got, want)
	}
}

// Select region while another capture holds the gate opens nothing: the picker
// comes back unanswered, and dismissing it leaves the other capture's session.
func TestHeadlessSelectRegionRefusedLeavesTheOtherSession(t *testing.T) {
	r := regionPicker(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	other := make(chan error, 1)
	go func() {
		_, _, err := r.region.Request(ctx, nil)
		other <- err
	}()
	apptest.Until(t, r.a, "the other capture holds the gate", r.open)
	r.selectRegion(t)
	apptest.Until(t, r.a, "the refused picker came back", func() bool { return r.p.layer != nil && r.p.cancelSelect == nil })
	apptest.Never(t, "the picker after a refused Select region", r.answers)
	r.p.Dismiss("/r/keys")
	if got := apptest.Answer(t, "the dismissed picker", r.answers); got != "" {
		t.Fatalf("the dismissed picker answered %q", got)
	}
	apptest.Never(t, "the other capture's session after the dismissal", other)
	cancel()
	if err := apptest.Answer(t, "the other capture's session", other); err != nil {
		t.Fatalf("the other session answered %v", err)
	}
}

// within runs fn, failing the test with what if it does not return in
// apptest.Answer's bound: a request wrongly admitted would block on its overlay.
func within[T any](t *testing.T, what string, fn func() T) T {
	t.Helper()
	answered := make(chan T, 1)
	go func() { answered <- fn() }()
	return apptest.Answer(t, what, answered)
}

func redFrames(a *app.Application) (map[string]render.StillImage, error) {
	frame := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := 0; i < len(frame.Pix); i += 4 {
		frame.Pix[i], frame.Pix[i+3] = 0xff, 0xff
	}
	return apptest.On(a, func() map[string]render.StillImage {
		frames := map[string]render.StillImage{}
		for _, out := range a.Session().Outputs() {
			frames[out.Name] = render.NewStillImage(frame)
		}
		return frames
	}), nil
}

func openOverlay(t *testing.T, a *app.Application, gate *regionoverlay.Gate) (end func()) {
	t.Helper()
	keytest.Dial(t, a)
	region := regionoverlay.New(a, gate, a.Session().Outputs, nil, treetest.Font(t), nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	answered := make(chan error, 1)
	go func() {
		_, _, err := region.Request(ctx, func() (map[string]render.StillImage, error) { return redFrames(a) })
		answered <- err
	}()
	apptest.Until(t, a, "the overlay took the keyboard", func() bool { return a.Session().KeyboardFocus() != nil })
	return func() {
		t.Helper()
		cancel()
		if err := apptest.Answer(t, "the session ended", answered); err != nil {
			t.Fatalf("the session answered %v", err)
		}
	}
}

func TestHeadlessOutputPreviewsWaitOutTheOverlay(t *testing.T) {
	a := apptest.Run(t)
	p, _ := headlessPicker(t, a, config.SharePickerOutputs)
	end := openOverlay(t, a, p.gate)
	windows := make(chan struct{}, len(fiveWindows))
	p.windowPreview = func(sharepreview.Toplevel, int) (*image.RGBA, error) {
		windows <- struct{}{}
		return image.NewRGBA(image.Rect(0, 0, 8, 8)), nil
	}
	outputs := make(chan string, 2)
	p.outputPreview = func(name string, _ int) (*image.RGBA, error) {
		outputs <- name
		return image.NewRGBA(image.Rect(0, 0, 8, 8)), nil
	}
	answers := make(chan string, 1)
	go func() { answers <- p.Pick(portaldialogs.Caller{Handle: "/r/over"}, Request{Sources: AllSources}) }()
	for range fiveWindows {
		apptest.Answer(t, "a window preview during the session", windows)
	}
	apptest.Never(t, "an output preview during the session", outputs)
	end()
	got := []string{apptest.Answer(t, "the first output preview", outputs), apptest.Answer(t, "the second output preview", outputs)}
	slices.Sort(got)
	if !slices.Equal(got, []string{"DP-1", "DP-2"}) {
		t.Fatalf("the previews after the session captured %v", got)
	}
	p.Dismiss("/r/over")
	apptest.Answer(t, "the dismissed request", answers)
}

func TestHeadlessPreviewsWaitingEndWithTheirRequest(t *testing.T) {
	a := apptest.Run(t)
	p, _ := headlessPicker(t, a, config.SharePickerOutputs)
	end := openOverlay(t, a, p.gate)
	captured := make(chan string, 2)
	p.outputPreview = func(name string, _ int) (*image.RGBA, error) {
		captured <- name
		return image.NewRGBA(image.Rect(0, 0, 8, 8)), nil
	}
	p.windowPreview = func(sharepreview.Toplevel, int) (*image.RGBA, error) {
		return image.NewRGBA(image.Rect(0, 0, 8, 8)), nil
	}
	answers := make(chan string, 1)
	go func() { answers <- p.Pick(portaldialogs.Caller{Handle: "/r/gone"}, Request{Sources: AllSources}) }()
	apptest.Until(t, a, "the picker mapped", func() bool { return p.layer != nil })
	p.Dismiss("/r/gone")
	apptest.Answer(t, "the dismissed request", answers)
	end()
	apptest.Never(t, "a capture for the request that ended", captured)
}

func showsRed(img *image.RGBA) bool {
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] > 0x20 && img.Pix[i+1] == 0 && img.Pix[i+2] == 0 {
			return true
		}
	}
	return false
}

func TestHeadlessOutputThumbShowsNoOverlay(t *testing.T) {
	a := apptest.Run(t)
	apptest.Until(t, a, "an output appeared", func() bool { return len(a.Session().Outputs()) > 0 })
	name := apptest.On(a, func() string { return a.Session().Outputs()[0].Name })
	gate := &regionoverlay.Gate{}
	end := openOverlay(t, a, gate)
	var during *image.RGBA
	for deadline := time.Now().Add(10 * time.Second); during == nil || !showsRed(during); {
		if time.Now().After(deadline) {
			t.Fatal("a capture during the session never showed the overlay's frame")
		}
		var err error
		if during, err = outputThumb(name, 64); err != nil {
			t.Fatalf("the capture during the session failed: %v", err)
		}
	}
	type thumb struct {
		img *image.RGBA
		err error
	}
	thumbs := make(chan thumb, 1)
	go func() {
		img, err := regionoverlay.BackgroundCapture(context.Background(), gate, func() (*image.RGBA, error) { return outputThumb(name, 64) })
		thumbs <- thumb{img, err}
	}()
	apptest.Never(t, "a thumbnail during the session", thumbs)
	end()
	got := apptest.Answer(t, "the thumbnail after the session", thumbs)
	if got.err != nil {
		t.Fatalf("the thumbnail failed: %v", got.err)
	}
	if showsRed(got.img) {
		t.Fatal("the thumbnail shows the overlay's frame")
	}
}

// The outputs and their thumbnails are read before the surface maps: the
// picker is in no thumbnail, and the loop waits on neither.
func TestHeadlessOutputsAreReadOffTheLoopBeforeThePickerMaps(t *testing.T) {
	a := apptest.Run(t)
	p, _ := headlessPicker(t, a, config.SharePickerOutputs)
	release := make(chan struct{})
	p.outputs = func() []outputInfo {
		<-release
		return []outputInfo{{name: "DP-1", width: 100, height: 100}}
	}
	mappedAtCapture := make(chan bool, 1)
	p.outputPreview = func(string, int) (*image.RGBA, error) {
		mappedAtCapture <- apptest.On(a, func() bool { return p.layer != nil })
		return image.NewRGBA(image.Rect(0, 0, 8, 8)), nil
	}
	answers := make(chan string, 1)
	go func() { answers <- p.Pick(portaldialogs.Caller{Handle: "/r/read"}, Request{Sources: AllSources}) }()
	apptest.Until(t, a, "the request reached the picker", func() bool { return p.reply != nil })
	if apptest.On(a, func() bool { return p.layer != nil }) {
		t.Error("the picker mapped before its outputs were read")
	}
	close(release)
	if apptest.Answer(t, "the capture", mappedAtCapture) {
		t.Error("the output was captured with the picker on it")
	}
	apptest.Until(t, a, "the picker mapped", func() bool { return p.layer != nil })
	p.Dismiss("/r/read")
	apptest.Answer(t, "the dismissed request", answers)
}

// A request dismissed while its outputs are read never maps.
func TestHeadlessDismissedWhileReadingMapsNothing(t *testing.T) {
	a := apptest.Run(t)
	p, _ := headlessPicker(t, a, config.SharePickerOutputs)
	release := make(chan struct{})
	p.outputs = func() []outputInfo {
		<-release
		return []outputInfo{{name: "DP-1", width: 100, height: 100}}
	}
	answers := make(chan string, 1)
	go func() { answers <- p.Pick(portaldialogs.Caller{Handle: "/r/gone"}, Request{Sources: AllSources}) }()
	apptest.Until(t, a, "the request reached the picker", func() bool { return p.reply != nil })
	p.Dismiss("/r/gone")
	if got := apptest.Answer(t, "the dismissed request", answers); got != "" {
		t.Errorf("answered %q", got)
	}
	close(release)
	apptest.Dispatched(t, a)
	if apptest.On(a, func() bool { return p.layer != nil }) {
		t.Error("the dismissed request mapped a surface")
	}
}

// A capture that does not finish does not hold the picker back for good.
func TestHeadlessASlowThumbnailDoesNotHoldThePickerBack(t *testing.T) {
	a := apptest.Run(t)
	p, _ := headlessPicker(t, a, config.SharePickerOutputs)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	p.outputPreview = func(string, int) (*image.RGBA, error) {
		<-release
		return nil, errors.New("released")
	}
	answers := make(chan string, 1)
	go func() { answers <- p.Pick(portaldialogs.Caller{Handle: "/r/slow"}, Request{Sources: AllSources}) }()
	apptest.Until(t, a, "the picker mapped", func() bool { return p.layer != nil })
	p.Dismiss("/r/slow")
	apptest.Answer(t, "the dismissed request", answers)
}
