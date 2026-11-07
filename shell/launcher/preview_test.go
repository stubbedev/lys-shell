package launcher

import (
	"context"
	"image"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/launcheripc"
	"github.com/stubbedev/lys-shell/service/clipboard"
	engine "github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// picturedMode lists rows that preview as pictures, counting the captures
// asked of it, as the window mode's are.
type picturedMode struct {
	rows     int
	captures atomic.Int32
}

func (*picturedMode) Name() string { return VaultModeName }

func (m *picturedMode) Load(context.Context) engine.ModeState {
	items := make([]engine.Item, m.rows)
	for i := range items {
		items[i] = engine.NewItem("row " + strconv.Itoa(i))
	}
	return engine.ModeState{Items: items, Prompt: "pictures"}
}

func (*picturedMode) Activate(context.Context, engine.Target, engine.ActivateKind, string) engine.Action {
	return engine.ActionNothing{}
}

func (m *picturedMode) RowPreview(index uint32) (engine.Preview, bool) {
	return engine.Preview{
		Image: func(context.Context) (image.Image, error) {
			m.captures.Add(1)
			return image.NewRGBA(image.Rect(0, 0, int(index)+1, 1)), nil
		},
		Facts: []engine.PreviewFact{{Label: "Row", Value: strconv.Itoa(int(index))}},
	}, true
}

func openPictured(t *testing.T, rows int) (*harness, *picturedMode) {
	t.Helper()
	h := newHarness(t)
	mode := &picturedMode{rows: rows}
	h.s.d.Vault = func() engine.Mode { return mode }
	h.open(launcheripc.SessionOptions{Modes: []string{VaultModeName}}, false)
	h.until("the rows", func() bool { return h.s.cur != nil && h.s.model.len() == rows })
	return h, mode
}

// rest fires the newest wait, as the selection resting does, and waits
// for what it loads to show.
func (h *harness) rest(what string, shown func() bool) {
	h.t.Helper()
	h.timers[len(h.timers)-1].fn()
	h.until(what, shown)
}

func paneWidgets(p *previewPane) []widget.Widget {
	var out []widget.Widget
	treetest.Walk(p.box, func(w widget.Widget) { out = append(out, w) })
	return out
}

// Arrowing through a picture mode captures nothing; the row the
// selection rests on is captured once and shown with its facts, and the
// panel makes room for the pane.
func TestARofiSessionPreviewsTheRowTheSelectionRestsOn(t *testing.T) {
	h, mode := openPictured(t, 6)
	v := h.s.cur.views
	if !v.pane.box.Visible() {
		t.Fatal("a mode that previews its rows opens the pane")
	}
	if w, _ := v.targetIn(v.root.bounds()); w != v.width+v.pane.width {
		t.Errorf("panel %d wide, want the list's %d and the pane's %d", w, v.width, v.pane.width)
	}
	for range 4 {
		h.key("Down")
	}
	h.loop.pump()
	if n := mode.captures.Load(); n != 0 {
		t.Fatalf("%d captures while the selection moved", n)
	}
	waiting := 0
	for _, tm := range h.timers {
		if tm.d == previewSettle && !tm.stopped {
			waiting++
		}
	}
	if waiting != 1 {
		t.Fatalf("%d previews waiting, want only the row the selection rests on", waiting)
	}
	h.rest("the picture", func() bool { return v.pane.image.Visible() })
	if n := mode.captures.Load(); n != 1 {
		t.Errorf("%d captures, want 1", n)
	}
	if !v.pane.facts.Visible() || v.pane.rows[0].value.Text() != "4" {
		t.Errorf("facts shown %v, value %q", v.pane.facts.Visible(), v.pane.rows[0].value.Text())
	}
}

// The pane rebinds the widgets it has for another row: the tree is the
// same widgets, and a selection change allocates a bounded handful.
func TestThePaneRebindsItsWidgetsForAnotherRow(t *testing.T) {
	h, mode := openPictured(t, 3)
	v := h.s.cur.views
	h.rest("the first picture", func() bool { return v.pane.image.Visible() })
	before := paneWidgets(v.pane)
	h.key("Down")
	h.rest("the second picture", func() bool { return v.pane.rows[0].value.Text() == "1" })
	if after := paneWidgets(v.pane); !slices.Equal(before, after) {
		t.Errorf("the pane's tree changed: %d widgets before, %d after", len(before), len(after))
	}
	if n := mode.captures.Load(); n != 2 {
		t.Errorf("%d captures for two rested rows", n)
	}
	allocs := testing.AllocsPerRun(50, func() {
		h.key("Down")
		h.key("Up")
	})
	if allocs > 16 {
		t.Errorf("two selection changes allocate %.0f times, want at most 16", allocs)
	}
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	facts := []engine.PreviewFact{{Label: "Row", Value: "1"}}
	shown := testing.AllocsPerRun(50, func() {
		v.pane.show(facts, engine.PreviewContent{Image: img}, nil)
	})
	if shown > 0 {
		t.Errorf("rebinding the same row allocates %.0f times", shown)
	}
}

// A row whose preview is still loading when the selection moves on never
// lands in the pane.
func TestALoadForARowLeftBehindIsDropped(t *testing.T) {
	h, mode := openPictured(t, 3)
	v := h.s.cur.views
	first := h.timers[len(h.timers)-1]
	h.key("Down")
	if !first.stopped {
		t.Error("moving on left the first row's wait running")
	}
	first.fn()
	h.loop.pump()
	h.rest("the second picture", func() bool { return v.pane.image.Visible() })
	h.loop.pump()
	if got := v.pane.rows[0].value.Text(); got != "1" {
		t.Errorf("the pane shows row %s, want row 1", got)
	}
	if n := mode.captures.Load(); n != 1 {
		t.Errorf("%d captures: the row left behind was captured too", n)
	}
}

// A mode that does not preview, and previews turned off, leave the pane
// shut and the panel its own width.
func TestNoPreviewsLeaveTheRofiPaneShut(t *testing.T) {
	h := newHarness(t)
	h.open(dmenu(), false, "a", "b")
	h.until("the rows", func() bool { return h.s.cur != nil && h.s.model.len() == 2 })
	v := h.s.cur.views
	if v.pane.box.Visible() {
		t.Error("dmenu rows opened the pane")
	}
	if w, _ := v.targetIn(v.root.bounds()); w != v.width {
		t.Errorf("panel %d wide, want %d", w, v.width)
	}

	off := newHarness(t)
	off.cfg.Launcher.Search.Previews = false
	mode := &picturedMode{rows: 2}
	off.s.d.Vault = func() engine.Mode { return mode }
	off.open(launcheripc.SessionOptions{Modes: []string{VaultModeName}}, false)
	off.until("the rows", func() bool { return off.s.cur != nil && off.s.model.len() == 2 })
	if off.s.cur.views.pane.box.Visible() {
		t.Error("previews: false opened the pane")
	}
	for _, tm := range off.timers {
		if tm.d == previewSettle {
			t.Fatal("a preview waits with previews off")
		}
	}
}

// launcherClipboard is a ClipboardHistory over a real history; putting an
// entry back, which needs a seat, does nothing.
type launcherClipboard struct{ history *clipboard.History }

func newLauncherClipboard(t *testing.T, selections ...[2]string) *launcherClipboard {
	t.Helper()
	h, err := clipboard.NewHistory(clipboard.DefaultCapacity, clipboard.MaxEntryBytes, clipboard.MaxEntryBytes*4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	for _, s := range selections {
		if _, _, err := h.Push(s[0], []byte(s[1])); err != nil {
			t.Fatal(err)
		}
	}
	return &launcherClipboard{history: h}
}

func (c *launcherClipboard) Entries() *clipboard.Snapshot { return c.history.Entries() }
func (c *launcherClipboard) Forget(id uint64) bool        { return c.history.Remove(id) }
func (c *launcherClipboard) Clear()                       { c.history.Clear() }
func (*launcherClipboard) Copy(uint64) bool               { return true }

// The clipboard mode previews its entries in full: text whole, and an
// entry that is neither text nor picture by its type and size.
func TestTheClipboardModePreviewsItsEntries(t *testing.T) {
	h := newHarness(t)
	h.s.d.Clipboard = newLauncherClipboard(t,
		[2]string{"application/x-thing", "12345"},
		[2]string{"text/plain", "first line\nsecond line"},
	)
	clip := "clipboard"
	h.open(launcheripc.SessionOptions{Mode: &clip}, false)
	h.until("the rows", func() bool { return h.s.cur != nil && h.s.model.len() == 2 })
	v := h.s.cur.views
	h.rest("the text", func() bool { return v.pane.code.Visible() })
	if got := v.pane.code.Text(); got != "first line\nsecond line" {
		t.Errorf("text %q, want the entry with its lines", got)
	}
	h.key("Down")
	h.rest("the facts", func() bool { return v.pane.facts.Visible() && !v.pane.code.Visible() })
	want := [][2]string{{i18n.T("launcher-preview-type"), "application/x-thing"}, {i18n.T("launcher-preview-size"), "5 B"}}
	for i, w := range want {
		if r := v.pane.rows[i]; r.label.Text() != w[0] || r.value.Text() != w[1] {
			t.Errorf("fact %d is %q %q, want %q %q", i, r.label.Text(), r.value.Text(), w[0], w[1])
		}
	}
}
