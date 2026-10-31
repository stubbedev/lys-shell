package sharepicker

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/keytest"
	"github.com/stubbedev/lys-shell/service/sharepreview"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// Physical keyboard codes this test's keymap leaves without a symbol.
const (
	physicalTab   = 15
	physicalRight = 106
)

// keyRig is a picker on a private sway with a toplevel of the same
// client focused, as the app asking to share is, and the remapped
// keyboard.
type keyRig struct {
	t       *testing.T
	a       *app.Application
	p       *Picker
	kb      *keytest.Keyboard
	answers chan string
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
	p := headlessPicker(t, a, page)
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
	return &keyRig{t: t, a: a, p: p, kb: kb, answers: answers}
}

// headlessPicker is a picker on a's compositor over five windows and
// two outputs, opening on page, with animations off.
func headlessPicker(t *testing.T, a *app.Application, page config.SharePickerPage) *Picker {
	t.Helper()
	cfg := config.Defaults()
	cfg.Animations.Enabled = false
	cfg.SharePicker.DefaultPage = page
	p := New(a, Deps{Config: func() *config.Config { return cfg }, Font: treetest.Font(t)}, nil)
	p.windows = func() []sharepreview.Toplevel { return fiveWindows }
	p.outputs = func() []outputInfo {
		return []outputInfo{{name: "DP-1", width: 1920, height: 1080, scale: 1}, {name: "DP-2", x: 1920, width: 1920, height: 1080, scale: 1}}
	}
	return p
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
	apptest.Until(t, k.a, "Ctrl+PageUp from Windows wraps to Region", func() bool { return k.p.nb.SelectedTab() == pageRegion })
	k.kb.CtrlTap(keytest.PageDown)
	k.kb.CtrlTap(keytest.PageDown)
	apptest.Until(t, k.a, "Ctrl+PageDown twice from Region wraps to Outputs", func() bool { return k.p.nb.SelectedTab() == pageOutputs })
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
	p := New(a, Deps{Config: func() *config.Config { return cfg }, Font: treetest.Font(t)}, nil)
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

// A card spins while its preview loads; once the capture lands, failed
// here as these windows do not exist, its spinner is hidden, which
// lys-ui takes as off screen and schedules no frames for.
func TestHeadlessLandedPreviewsHideTheirSpinners(t *testing.T) {
	k := keyPicker(t, config.SharePickerWindows, Request{Sources: AllSources})
	spinners := apptest.On(k.a, func() []*widget.Spinner { return treetest.All[*widget.Spinner](k.p.root) })
	if len(spinners) != len(fiveWindows) {
		t.Fatalf("%d spinners, want one per window card (%d)", len(spinners), len(fiveWindows))
	}
	apptest.Until(t, k.a, "every preview landed and hid its spinner", func() bool {
		return !slices.ContainsFunc(spinners, func(s *widget.Spinner) bool { return s.Visible() })
	})
}
