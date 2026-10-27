package popups

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/anim"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/fakeclock"
	"github.com/stubbedev/lys-shell/service/notifications"
	"github.com/stubbedev/lys-shell/shell/apptheme"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

func testFont(t *testing.T) render.Font { return treetest.Font(t) }

func newTestPopups(t *testing.T, ncfg config.NotificationConfig) (*Popups, *notifications.Service) {
	t.Helper()
	p, svc, _ := newClockedPopups(t, ncfg)
	return p, svc
}

// newClockedPopups is a popup host whose service and card ages keep
// time by the returned fake clock: countdowns run out only when it is
// advanced.
func newClockedPopups(t *testing.T, ncfg config.NotificationConfig) (*Popups, *notifications.Service, *fakeclock.Clock) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	svc := notifications.NewService()
	clock := fakeclock.New()
	svc.UseClock(clock)
	cfg := config.Defaults()
	cfg.Notification = ncfg
	p := New(nil, svc, cfg, testFont(t), nil, nil)
	p.now = clock.Now
	return p, svc, clock
}

// surface is a popup host mounted in the tree its window shows, the
// shell's stylesheet at its priority attached, under a router: the
// real input path, hit testing and the hover chain included. Motion
// lands at once (reduced motion), so every frame shows the laid-out
// state.
type surface struct {
	t     *testing.T
	p     *Popups
	svc   *notifications.Service
	clock *fakeclock.Clock
	host  *widget.Box
	r     *widget.Router
}

// surfaceW and surfaceH bound the test surface, which takes its
// content's size within them.
const (
	surfaceW = 400
	surfaceH = 3000
)

func newSurface(t *testing.T, ncfg config.NotificationConfig) *surface {
	t.Helper()
	p, svc, clock := newClockedPopups(t, ncfg)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p.sheet = apptheme.New(p.cfg).Sheet
	p.cfg.Animations.Enabled = false
	t.Cleanup(anim.SetInstant(true))
	s := &surface{t: t, p: p, svc: svc, clock: clock}
	s.mount()
	return s
}

// mount builds the window's tree for the host, as a newly mapped
// surface does.
func (s *surface) mount() {
	host, list := s.p.newHost()
	s.p.root = list
	s.p.layout()
	s.host = host
	s.r = &widget.Router{Root: host}
	r := s.r
	widget.SetRemovedHook(r.Forget)
	s.t.Cleanup(func() { widget.SetRemovedHook(nil) })
	s.layout()
}

// notify posts a notification a millisecond after the last, so ages
// order the burst.
func (s *surface) notify(appName, summary string, actions ...string) uint32 {
	s.clock.Advance(time.Millisecond)
	return s.svc.Notify(appName, 0, "", summary, "body of "+summary, actions, -1)
}

func (s *surface) sync() {
	s.p.sync()
	s.layout()
}

// layout sizes the surface to its content, as the layer surface
// follows its root's natural size.
func (s *surface) layout() {
	sz := s.host.Measure(widget.Constraints{Max: widget.Size{W: surfaceW, H: surfaceH}})
	s.host.Arrange(render.Rect{W: sz.W, H: sz.H})
}

// point is the centre of w.
func point(w widget.Boundser) widget.Point {
	b := w.Bounds()
	return widget.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}
}

// hover moves the pointer onto w and lays the surface out again, as
// the next frame does.
func (s *surface) hover(w widget.Boundser) {
	s.t.Helper()
	s.r.Move(point(w))
	s.layout()
}

// click presses and releases on w where the pointer is moved first.
func (s *surface) click(w widget.Boundser) {
	s.t.Helper()
	s.hover(w)
	at := point(w)
	s.r.Press(widget.BTNLeft, at, 0)
	s.r.Release(widget.BTNLeft, at)
	s.layout()
}

func (s *surface) leave() {
	s.r.Leave()
	s.layout()
}

// stackOf is the stack holding the card for id.
func (s *surface) stackOf(id uint32) *stack {
	s.t.Helper()
	for _, st := range s.p.stacks {
		for _, c := range st.cards {
			if c.id == id && !c.leaving {
				return st
			}
		}
	}
	s.t.Fatalf("no stack holds %d", id)
	return nil
}

func (s *surface) card(id uint32) *card {
	s.t.Helper()
	c, ok := s.p.cards[id]
	if !ok {
		s.t.Fatalf("no card for %d", id)
	}
	return c
}

// front is the stack's front card that is not leaving.
func (st *stack) front() *card {
	for _, c := range st.cards {
		if !c.leaving {
			return c
		}
	}
	return nil
}

// expanded is whether the stack's pile lays its cards out as a list.
func (st *stack) expanded() bool { return st.pile.Expanded() }

// within reports whether w is root or inside it.
func within(root, w widget.Widget) bool {
	found := false
	treetest.Walk(root, func(x widget.Widget) { found = found || x == w })
	return found
}

// fill is the color the card paints its plate in, read just under its
// top edge, clear of its urgency bar, its text, and the cards peeking
// out behind it: straight RGBA.
func (s *surface) fill(c *card) [4]byte {
	s.t.Helper()
	b := c.root.Bounds()
	return s.pixel(b.X+b.W/2, b.Y+4)
}

// pixel is the surface's color at (x, y) as it paints now: RGBA.
func (s *surface) pixel(x, y int) [4]byte {
	data := s.paint()
	i := y*render.Stride(surfaceW) + x*4
	return [4]byte{data[i+2], data[i+1], data[i], data[i+3]}
}

// paint is one full paint of the surface, premultiplied BGRA rows of
// render.Stride(surfaceW) bytes.
func (s *surface) paint() []byte {
	data := make([]byte, render.Stride(surfaceW)*surfaceH)
	s.host.Paint(render.NewScaled(data, render.Stride(surfaceW), surfaceW, surfaceH, 1, 1))
	return data
}

// burst posts six notifications from two apps, alternating, and
// returns their ids per app, oldest first.
func (s *surface) burst() (mail, chat []uint32) {
	for i := range 3 {
		mail = append(mail, s.notify("mail", "Mail "+strconv.Itoa(i), "default", "Open", "reply", "Reply"))
		chat = append(chat, s.notify("chat", "Chat "+strconv.Itoa(i)))
	}
	s.sync()
	return mail, chat
}

// A burst of six notifications from two apps renders as two compact
// stacks, the app with the newest notification on top, each its newest
// card in front with the count of the ones behind it; every card rests
// compact, its body clamped and its actions hidden.
func TestBurstCollapsesIntoAStackPerApp(t *testing.T) {
	cfg := config.DefaultsNotification()
	cfg.PopupMaxVisible = 6
	s := newSurface(t, cfg)
	mail, chat := s.burst()
	want := [][]uint32{{chat[2], chat[1], chat[0]}, {mail[2], mail[1], mail[0]}}
	if got := s.p.stackOrder(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("stacks = %v, want %v", got, want)
	}
	for _, id := range []uint32{mail[2], chat[2]} {
		st := s.stackOf(id)
		if st.expanded() || !st.front().count.Visible() || st.front().count.Text() != "+2" || st.clear.Visible() {
			t.Errorf("stack of %d: expanded %v, count %q shown %v, clear shown %v; want collapsed with +2", id, st.expanded(), st.front().count.Text(), st.front().count.Visible(), st.clear.Visible())
		}
	}
	c := s.card(mail[2])
	if c.actions.Visible() || c.body.MaxLines() != cardBodyLines || c.summary.MaxLines() != 1 {
		t.Errorf("a resting card: actions shown %v, body lines %d, summary lines %d", c.actions.Visible(), c.body.MaxLines(), c.summary.MaxLines())
	}

	// A new notification brings its app's stack to the top, in front.
	late := s.notify("mail", "Late")
	s.sync()
	if got := s.p.stackOrder(); len(got) != 2 || got[0][0] != late {
		t.Errorf("after a late mail: stacks %v, want the mail stack on top with %d in front", got, late)
	}
}

// Collapsed, a stack shows its front card whole with the next two
// peeking out past the edge away from the screen edge, each narrower
// and further out, the rest buried; only the front takes the pointer.
// Expanded, the cards lay out as a list from the front's end.
func TestCollapsedStackPeeks(t *testing.T) {
	for _, tc := range []struct {
		position config.PopupPosition
		down     bool
	}{
		{config.PopupPositionTopRight, true},
		{config.PopupPositionBottomRight, false},
	} {
		cfg := config.DefaultsNotification()
		cfg.PopupPosition = tc.position
		s := newSurface(t, cfg)
		var ids []uint32
		for i := range 4 {
			ids = append(ids, s.notify("mail", "Mail "+strconv.Itoa(i)))
		}
		s.sync()
		front, peek, deeper, buried := s.card(ids[3]), s.card(ids[2]), s.card(ids[1]), s.card(ids[0])
		f, pk, d := front.root.Bounds(), peek.root.Bounds(), deeper.root.Bounds()
		if !front.swipe.HasClass("front") || !peek.swipe.HasClass("peek") || !deeper.swipe.HasClass("peek") || !buried.swipe.HasClass("buried") {
			t.Errorf("%s: classes front %v peek %v %v buried %v", tc.position, front.swipe.Classes(), peek.swipe.Classes(), deeper.swipe.Classes(), buried.swipe.Classes())
		}
		if pk.W >= f.W || d.W >= pk.W {
			t.Errorf("%s: widths front %d peek %d deeper %d, want each deeper card narrower", tc.position, f.W, pk.W, d.W)
		}
		for _, b := range []render.Rect{pk, d} {
			if b.X <= f.X || b.X+b.W >= f.X+f.W {
				t.Errorf("%s: a peek spans x %d..%d, want it inside the front's %d..%d", tc.position, b.X, b.X+b.W, f.X, f.X+f.W)
			}
		}
		if out := pk.Y > f.Y && d.Y > pk.Y; out != tc.down {
			t.Errorf("%s: tops front %d peek %d deeper %d, want the peeks out past the far edge", tc.position, f.Y, pk.Y, d.Y)
		}
		edge := widget.Point{X: pk.X + pk.W/2, Y: pk.Y + pk.H - 2}
		if !tc.down {
			edge.Y = pk.Y + 2
		}
		if hit := s.host.HitTest(edge); hit != nil && within(peek.root, hit) {
			t.Errorf("%s: a peeking card's edge takes the pointer", tc.position)
		}

		s.hover(front.summary)
		f, pk = front.root.Bounds(), peek.root.Bounds()
		if pk.W != f.W || (pk.Y > f.Y) != tc.down || pk.Y == f.Y {
			t.Errorf("%s expanded: front %+v peek %+v, want a list of full-width cards from the front's end", tc.position, f, pk)
		}
	}
}

// A card mid-swipe darkens nothing outside itself: the frosted cards
// below it paint exactly as they do with the swiped card at rest, its
// fade and its backdrop blur staying on its own box.
func TestASwipedCardLeavesTheOthersAlone(t *testing.T) {
	s := newSurface(t, config.DefaultsNotification())
	for range 3 {
		s.notify("chat", "Chat")
	}
	s.sync()
	ids := s.p.stacks[0].liveIDs()
	swiped := s.card(ids[0])
	s.hover(swiped.summary)
	from := point(swiped.body)
	s.r.Move(from)
	s.r.Press(widget.BTNLeft, from, 0)
	at := from
	for i := 1; i <= 6; i++ {
		time.Sleep(20 * time.Millisecond)
		at = widget.Point{X: from.X + 7*i, Y: from.Y}
		s.r.Move(at)
		s.layout()
	}
	if swiped.swipe.Offset() == 0 {
		t.Fatal("the card did not follow the drag")
	}
	dragged := s.paint()
	s.r.Release(widget.BTNLeft, at)
	s.layout()
	if swiped.swipe.Offset() != 0 {
		t.Fatalf("a 42px swipe left the card at %v, want it back at rest", swiped.swipe.Offset())
	}
	rest := s.paint()
	stride := render.Stride(surfaceW)
	for _, id := range ids[1:] {
		b := s.card(id).root.Bounds()
		for y := b.Y; y < b.Y+b.H; y++ {
			row := y*stride + b.X*4
			if !bytes.Equal(dragged[row:row+b.W*4], rest[row:row+b.W*4]) {
				t.Errorf("card %d's row %d paints differently while the card above it is swiped", id, y-b.Y)
				break
			}
		}
	}
}

// Swiping a card sideways past the threshold dismisses it under
// popup-close-behavior and leaves the rest of its stack; a short swipe
// springs back and a drag up or down dismisses nothing. The drags take
// real time, as the swipe reads its speed off the animation clock: a
// short one made at once would be a fling.
func TestSwipeDismissesACard(t *testing.T) {
	s := newSurface(t, config.DefaultsNotification())
	older := s.notify("mail", "Older")
	id := s.notify("mail", "Swipe me")
	s.sync()
	c := s.card(id)
	drag := func(dx, dy int) {
		s.layout()
		from := point(c.body)
		s.r.Move(from)
		s.r.Press(widget.BTNLeft, from, 0)
		at := from
		for i := 1; i <= 5; i++ {
			time.Sleep(25 * time.Millisecond)
			at = widget.Point{X: from.X + dx*i/5, Y: from.Y + dy*i/5}
			s.r.Move(at)
		}
		s.r.Release(widget.BTNLeft, at)
		s.p.sync()
		s.layout()
	}
	drag(60, 0)
	if c.swipe.Offset() != 0 || len(s.svc.Popups()) != 2 {
		t.Fatalf("a 60px swipe: offset %v, popups %d; want it back and both up", c.swipe.Offset(), len(s.svc.Popups()))
	}
	drag(0, 200)
	if len(s.svc.Popups()) != 2 {
		t.Fatalf("a vertical drag dismissed: popups %d", len(s.svc.Popups()))
	}
	drag(250, 0)
	popups := s.svc.Popups()
	if len(popups) != 1 || popups[0].ID != older {
		t.Fatalf("a 250px swipe: popups %v, want only %d", popups, older)
	}
	if len(history(t, s.svc)) != 2 {
		t.Errorf("history = %d, want the swiped card dismissed into it", len(history(t, s.svc)))
	}
}

// popup-stack all puts every popup in one stack; off gives each its own
// card, ordered as popup-stacking-order says, without a count.
func TestStackModes(t *testing.T) {
	cfg := config.DefaultsNotification()
	cfg.PopupMaxVisible = 6
	cfg.PopupStack = config.PopupStackAll
	s := newSurface(t, cfg)
	mail, chat := s.burst()
	if got := s.p.stackOrder(); len(got) != 1 || len(got[0]) != 6 || got[0][0] != chat[2] {
		t.Errorf("popup-stack all: stacks %v, want one of six with the newest in front", got)
	}
	if st := s.stackOf(mail[0]); st.front().count.Text() != "+5" {
		t.Errorf("popup-stack all: count %q, want +5", st.front().count.Text())
	}

	cfg.PopupStack = config.PopupStackOff
	s = newSurface(t, cfg)
	mail, chat = s.burst()
	want := [][]uint32{{chat[2]}, {mail[2]}, {chat[1]}, {mail[1]}, {chat[0]}, {mail[0]}}
	if got := s.p.stackOrder(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("popup-stack off: stacks %v, want %v", got, want)
	}
	if st := s.stackOf(mail[0]); st.front().count.Visible() {
		t.Error("a stack of one shows a count")
	}

	cfg.PopupStackingOrder = config.StackingOrderOldestFirst
	s = newSurface(t, cfg)
	mail, chat = s.burst()
	want = [][]uint32{{mail[0]}, {chat[0]}, {mail[1]}, {chat[1]}, {mail[2]}, {chat[2]}}
	if got := s.p.stackOrder(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("oldest-first: stacks %v, want %v", got, want)
	}
}

// Hovering a stack expands it: its cards show whole with their actions,
// the clear-all button replaces the count, and the card under the
// pointer turns opaque while the rest stay frosted. The other stack
// stays as it was, and leaving restores the stack.
func TestHoverExpandsTheStackAndLeavingRestoresIt(t *testing.T) {
	cfg := config.DefaultsNotification()
	cfg.PopupMaxVisible = 6
	s := newSurface(t, cfg)
	mail, chat := s.burst()
	front := s.card(mail[2])
	behind := s.card(mail[1])
	rest := s.fill(front)
	if rest[3] == 0xff || rest[3] == 0 {
		t.Fatalf("a resting card's plate %v, want frosted", rest)
	}

	s.hover(front.summary)
	st, other := s.stackOf(mail[2]), s.stackOf(chat[2])
	if !st.expanded() || st.front().count.Visible() || !st.clear.Visible() {
		t.Errorf("hovered stack: expanded %v, count shown %v, clear shown %v", st.expanded(), st.front().count.Visible(), st.clear.Visible())
	}
	for _, id := range mail {
		if c := s.card(id); !c.actions.Visible() || c.body.MaxLines() != 0 || c.summary.MaxLines() != 0 {
			t.Errorf("card %d in a hovered stack: actions shown %v, body lines %d, summary lines %d", id, c.actions.Visible(), c.body.MaxLines(), c.summary.MaxLines())
		}
	}
	if other.expanded() || !other.front().count.Visible() {
		t.Error("hovering one stack expanded the other")
	}
	s.hover(front.summary)
	if got := s.fill(front); got[3] != 0xff {
		t.Errorf("the hovered card's plate alpha %#02x, want opaque", got[3])
	}
	if got := s.fill(behind); got[3] == 0xff {
		t.Error("a card the pointer is not over turned opaque")
	}

	s.leave()
	if st.expanded() || !st.front().count.Visible() || st.clear.Visible() || front.actions.Visible() || front.body.MaxLines() != cardBodyLines {
		t.Errorf("after leaving: expanded %v, count shown %v, clear shown %v, actions shown %v", st.expanded(), st.front().count.Visible(), st.clear.Visible(), front.actions.Visible())
	}
	if got := s.fill(front); got != rest {
		t.Errorf("after leaving the plate is %v, want the resting %v", got, rest)
	}
}

// The stacks keep popup-gap from every edge of the surface, as from
// each other, so no card or clear-all button touches the screen edge
// the surface is anchored to; the gap follows popup-gap.
func TestStacksInsetFromTheSurfaceEdges(t *testing.T) {
	for _, gap := range []config.Size{config.Scale(1), config.Px(20)} {
		cfg := config.DefaultsNotification()
		cfg.PopupGap = gap
		s := newSurface(t, cfg)
		mail, chat := s.burst()
		inset := int(gap.ResolvePx(popupGapBasePx, float64(s.p.cfg.Styling.Scale)))
		host := s.host.Bounds()
		top, bottom := s.stackOf(chat[2]).root.Bounds(), s.stackOf(mail[2]).root.Bounds()
		if top.Y != inset || host.H-(bottom.Y+bottom.H) != inset {
			t.Errorf("gap %v: stacks span %d..%d of a %dpx surface, want %dpx from the top and the bottom", gap, top.Y, bottom.Y+bottom.H, host.H, inset)
		}
		for _, id := range []uint32{mail[2], chat[2]} {
			b := s.card(id).root.Bounds()
			if b.X != inset || host.W-(b.X+b.W) != inset {
				t.Errorf("gap %v: card %d spans x %d..%d of a %dpx surface, want %dpx from either side", gap, id, b.X, b.X+b.W, host.W, inset)
			}
		}
		s.hover(s.card(mail[2]).summary)
		host = s.host.Bounds()
		clear := s.stackOf(mail[2]).clear.Bounds()
		card := s.card(mail[2]).root.Bounds()
		if right := host.W - (clear.X + clear.W); right != inset || clear.X+clear.W != card.X+card.W {
			t.Errorf("gap %v: clear all ends %dpx from the edge at %d, the card at %d; want both %dpx in", gap, right, clear.X+clear.W, card.X+card.W, inset)
		}
	}
}

// The count of the cards behind a collapsed stack's front sits inline
// in that card's top row, inside the card and the surface; no other
// card shows one.
func TestCountSitsInsideTheFrontCard(t *testing.T) {
	cfg := config.DefaultsNotification()
	cfg.PopupMaxVisible = 6
	s := newSurface(t, cfg)
	mail, _ := s.burst()
	front := s.card(mail[2])
	b, c, host := front.count.Bounds(), front.root.Bounds(), s.host.Bounds()
	if !front.count.Visible() || b.W == 0 {
		t.Fatal("the front card shows no count")
	}
	if b.X < c.X || b.Y < c.Y || b.X+b.W > c.X+c.W || b.Y+b.H > c.Y+c.H {
		t.Errorf("count %+v leaves its card %+v", b, c)
	}
	if b.X < 0 || b.Y < 0 || b.X+b.W > host.W || b.Y+b.H > host.H {
		t.Errorf("count %+v leaves the %dx%d surface", b, host.W, host.H)
	}
	if !within(front.head, front.count) {
		t.Error("the count is not in the front card's top row")
	}
	for _, id := range mail[:2] {
		if s.card(id).count.Visible() {
			t.Errorf("card %d behind the front shows a count", id)
		}
	}
}

// The urgency bar runs down the card's left edge, inside its border,
// and nothing of it shows along the top edge.
func TestUrgencyBarRunsDownTheLeftEdge(t *testing.T) {
	s := newSurface(t, config.DefaultsNotification())
	id := s.notify("mail", "Urgent")
	s.sync()
	c := s.card(id)
	if !c.root.HasClass("urgency-bar") {
		t.Fatal("a normal card under the default threshold has no urgency bar")
	}
	accent, _ := apptheme.New(s.p.cfg).RenderPalette().Token(config.TokenAccent)
	want := accent.Straight()
	near := func(px [4]byte) bool {
		for i := range 3 {
			if d := int(px[i]) - int(want[i]); d < -12 || d > 12 {
				return false
			}
		}
		return px[3] == 0xff
	}
	b := c.root.Bounds()
	for _, y := range []int{b.Y + b.H/3, b.Y + b.H/2, b.Y + 2*b.H/3} {
		if px := s.pixel(b.X+1, y); !near(px) {
			t.Errorf("left edge at y %d paints %v, want the accent %v", y, px, want)
		}
	}
	for _, x := range []int{b.X + b.W/3, b.X + b.W/2, b.X + 2*b.W/3} {
		if px := s.pixel(x, b.Y+1); near(px) {
			t.Errorf("top edge at x %d paints the accent %v", x, px)
		}
	}
}

// Without compositor blur the frosted cards rest opaque: what is behind
// them would show through unblurred.
func TestCardsRestOpaqueWithoutBlur(t *testing.T) {
	s := newSurface(t, config.DefaultsNotification())
	id := s.notify("mail", "One")
	s.sync()
	s.host.AddClass(css.BlurUnavailable)
	s.layout()
	if got := s.fill(s.card(id)); got[3] != 0xff {
		t.Errorf("without blur the plate alpha is %#02x, want opaque", got[3])
	}
}

// Hovering a stack pauses the countdowns of all its cards, a card
// arriving meanwhile included, while the other stack keeps counting;
// leaving resumes each with the time it had left.
func TestHoverPausesTheStacksCountdown(t *testing.T) {
	cfg := config.DefaultsNotification()
	cfg.PopupDuration = 40
	s := newSurface(t, cfg)
	a := s.notify("mail", "A")
	b := s.notify("mail", "B")
	other := s.notify("chat", "C")
	s.sync()
	s.clock.Advance(10 * time.Millisecond)
	s.hover(s.card(b).summary)
	late := s.notify("mail", "Late")
	s.sync()
	s.clock.Advance(time.Hour)
	live := map[uint32]bool{}
	for _, n := range s.svc.Popups() {
		live[n.ID] = true
	}
	if !live[a] || !live[b] || !live[late] {
		t.Fatalf("the hovered stack's cards timed out: live %v", live)
	}
	if live[other] {
		t.Error("the stack not hovered stopped counting")
	}
	s.leave()
	s.clock.Advance(27 * time.Millisecond)
	if got := len(s.svc.Popups()); got != 3 {
		t.Fatalf("the leave restarted short of the 28ms the oldest had left: popups = %d", got)
	}
	s.clock.Advance(13 * time.Millisecond)
	if got := len(s.svc.Popups()); got != 0 {
		t.Fatalf("the stack survived the leave: popups = %d", got)
	}
}

// A popup arriving for another stack leaves the hovered stack as it
// is: still under the pointer, expanded, its countdowns paused.
func TestSyncKeepsTheHoveredStackOpen(t *testing.T) {
	cfg := config.DefaultsNotification()
	cfg.PopupDuration = 40
	s := newSurface(t, cfg)
	a := s.notify("mail", "A")
	b := s.notify("mail", "B")
	s.sync()
	s.hover(s.card(b).summary)
	s.notify("chat", "C")
	s.sync()
	if st := s.stackOf(a); !st.expanded() {
		t.Error("a sync collapsed the hovered stack")
	}
	s.clock.Advance(time.Hour)
	live := map[uint32]bool{}
	for _, n := range s.svc.Popups() {
		live[n.ID] = true
	}
	if !live[a] || !live[b] {
		t.Errorf("a sync resumed the hovered stack's countdowns: live %v", live)
	}
}

// A stack whose last card goes while the pointer is on it leaves the
// surface, the hover leaving with it, and the surface takes the next
// popup as before.
func TestStackLeavingUnderThePointer(t *testing.T) {
	s := newSurface(t, config.DefaultsNotification())
	id := s.notify("mail", "Only")
	s.sync()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c := s.card(id)
		s.hover(c.summary)
		s.click(c.close)
		s.sync()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("closing the hovered stack's last card hung the surface")
	}
	if got := s.p.stackOrder(); len(got) != 0 {
		t.Fatalf("stacks after closing the last card: %v", got)
	}
	next := s.notify("chat", "Next")
	s.sync()
	if st := s.stackOf(next); st.expanded() {
		t.Error("a new stack opened expanded with the pointer gone")
	}
}

// With popup-hover-pause off a hovered stack still expands but keeps
// counting down.
func TestHoverPauseOffKeepsCounting(t *testing.T) {
	cfg := config.DefaultsNotification()
	cfg.PopupHoverPause = false
	cfg.PopupDuration = 30
	s := newSurface(t, cfg)
	id := s.notify("app", "Bare")
	s.sync()
	s.hover(s.card(id).summary)
	if !s.stackOf(id).expanded() {
		t.Error("a hovered stack did not expand with hover-pause off")
	}
	s.clock.Advance(30 * time.Millisecond)
	if got := len(s.svc.Popups()); got != 0 {
		t.Fatalf("a hovered card paused with hover-pause off: popups = %d", got)
	}
}

// A reload while a stack is hovered resumes its countdowns: the pointer
// leaves with the surface, and no stack is left to hear it.
func TestReloadReleasesAHoveredStack(t *testing.T) {
	cfg := config.DefaultsNotification()
	cfg.PopupDuration = 40
	s := newSurface(t, cfg)
	id := s.notify("mail", "A")
	s.sync()
	s.hover(s.card(id).summary)
	next := config.Defaults()
	next.Notification = cfg
	s.p.SetConfig(next)
	s.clock.Advance(40 * time.Millisecond)
	if got := len(s.svc.Popups()); got != 0 {
		t.Fatalf("a popup hovered across a reload never timed out: popups = %d", got)
	}
}

// Clear all on an expanded stack closes every popup of its group, the
// ones popup-max-visible keeps off screen included, and only those; a
// collapsed stack or a stack of one offers no clear all.
func TestClearAllDismissesTheStack(t *testing.T) {
	s := newSurface(t, config.DefaultsNotification())
	mail, chat := s.burst()
	single := s.notify("calendar", "Meeting")
	s.sync()
	s.hover(s.card(single).summary)
	if s.stackOf(single).clear.Visible() {
		t.Error("a stack of one offers clear all")
	}
	st := s.stackOf(mail[2])
	if st.clear.Visible() {
		t.Error("a collapsed stack offers clear all")
	}
	s.hover(s.card(mail[2]).summary)
	s.click(st.clear)
	s.p.sync()
	left := map[uint32]bool{}
	for _, n := range s.svc.Popups() {
		left[n.ID] = true
	}
	for _, id := range mail {
		if left[id] {
			t.Errorf("clear all left mail %d up", id)
		}
	}
	for _, id := range chat[1:] {
		if !left[id] {
			t.Errorf("clear all on the mail stack took chat %d", id)
		}
	}
	if len(history(t, s.svc)) != 7 {
		t.Errorf("history = %d, want clear all to dismiss into the history under the default close behavior", len(history(t, s.svc)))
	}
}

func TestSyncTracksPopupSet(t *testing.T) {
	p, svc := newTestPopups(t, config.DefaultsNotification())
	svc.Notify("app", 0, "", "One", "body", nil, 0)
	p.sync()
	if got := p.Visible(); got != 1 {
		t.Fatalf("visible = %d, want 1", got)
	}
	// DND keeps new cards off; the one already up stays.
	svc.SetDND(true)
	svc.Notify("app", 0, "", "Two", "", nil, 0)
	p.sync()
	if got := p.Visible(); got != 1 {
		t.Fatalf("under dnd = %d, want only the card shown before", got)
	}
	svc.SetDND(false)
	// Dismissal drops the card.
	svc.DismissAll()
	p.sync()
	if got := p.Visible(); got != 0 {
		t.Fatalf("after dismiss all = %d", got)
	}
}

// popup-max-visible caps the cards across all stacks, the oldest off
// first.
func TestSyncCapsAtMaxVisible(t *testing.T) {
	cfg := config.DefaultsNotification()
	cfg.PopupMaxVisible = 2
	p, svc := newTestPopups(t, cfg)
	svc.Notify("app", 0, "", "One", "", nil, 0)
	b := svc.Notify("other", 0, "", "Two", "", nil, 0)
	c := svc.Notify("app", 0, "", "Three", "", nil, 0)
	p.sync()
	if got := p.Visible(); got != 2 {
		t.Fatalf("visible = %d, want the 2 cap", got)
	}
	if got := p.stackOrder(); !slices.EqualFunc(got, [][]uint32{{c}, {b}}, slices.Equal) {
		t.Errorf("stacks = %v, want the oldest capped off", got)
	}
}

// TestCardsPlayTheirExit pins the exit: a dismissed card leaves the
// live set at once but stays in its stack, in its place, until its
// transition lands; the stack goes with its last card, and with
// animations off at once.
func TestCardsPlayTheirExit(t *testing.T) {
	p, svc := newTestPopups(t, config.DefaultsNotification())
	a := svc.Notify("app", 0, "", "First", "", nil, 0)
	b := svc.Notify("app", 0, "", "Second", "", nil, 0)
	p.sync()
	if rev := cardOf(t, p, b).rev; !rev.Revealed() || rev.Transition() != widget.RevealFade {
		t.Errorf("a new card: revealed %v, transition %v; want entering with the configured fade", rev.Revealed(), rev.Transition())
	}
	leaving := cardOf(t, p, b)
	svc.DismissPopup(b)
	p.sync()
	if got := p.Visible(); got != 1 {
		t.Errorf("live cards = %d, want the dismissed one gone from the live set", got)
	}
	if got := p.stackOrder(); !slices.EqualFunc(got, [][]uint32{{b, a}}, slices.Equal) {
		t.Fatalf("stack mid-exit = %v, want the leaving card still in its place", got)
	}
	if leaving.rev.Revealed() {
		t.Error("the dismissed card is not exiting")
	}
	leaving.rev.Finish()
	if got := p.stackOrder(); !slices.EqualFunc(got, [][]uint32{{a}}, slices.Equal) {
		t.Errorf("stack after the exit = %v, want [[%d]]", got, a)
	}

	// Disabled animations snap: the card and its stack go with the sync.
	p.cfg.Animations.Enabled = false
	svc.DismissPopup(a)
	p.sync()
	if got := p.stackOrder(); len(got) != 0 {
		t.Errorf("stacks with animations off = %v, want none at once", got)
	}
}

func TestCardTransitionFollowsConfig(t *testing.T) {
	ncfg := config.DefaultsNotification()
	ncfg.PopupPosition = config.PopupPositionTopCenter
	p, svc := newTestPopups(t, ncfg)
	enter, exit := config.AnimationGenie, config.AnimationSlideRight
	p.cfg.Animations.Notifications.Enter = &enter
	p.cfg.Animations.Notifications.Exit = &exit
	id := svc.Notify("app", 0, "", "One", "", nil, 0)
	p.sync()
	rev := cardOf(t, p, id).rev
	if rev.Transition() != widget.RevealGenie {
		t.Errorf("enter transition = %v, want the per-surface genie", rev.Transition())
	}
	svc.DismissPopup(id)
	p.sync()
	if rev.Transition() != widget.RevealSlideRight {
		t.Errorf("exit transition = %v, want the per-surface slide-right", rev.Transition())
	}
	if !popupAtTop(config.PopupPositionTopCenter) || popupAtTop(config.PopupPositionCenterLeft) {
		t.Error("genie edge: top positions collapse up, the rest down")
	}
}

// cardOf is the built card for id, laid out on its own and expanded,
// as its hovered stack shows it.
func cardOf(t *testing.T, p *Popups, id uint32) *card {
	t.Helper()
	c, ok := p.cards[id]
	if !ok {
		t.Fatalf("no card for %d", id)
	}
	c.setFull(true)
	c.root.Measure(widget.Constraints{Max: widget.Size{W: testCardWidth, H: 600}})
	c.root.Arrange(render.Rect{W: testCardWidth, H: 600})
	return c
}

// click presses and releases at the center of w through a router over
// the card.
func click(c *card, w widget.Boundser) {
	at := point(w)
	r := &widget.Router{Root: c.root}
	r.Move(at)
	r.Press(widget.BTNLeft, at, 0)
	r.Release(widget.BTNLeft, at)
}

// signals records the service's D-Bus emissions.
type signals struct {
	mu   sync.Mutex
	seen []string
}

func (s *signals) emit(member string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, strings.TrimPrefix(member, notifications.Interface+".")+fmt.Sprint(args...))
}

func (s *signals) count(prefix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.seen {
		if strings.HasPrefix(m, prefix) {
			n++
		}
	}
	return n
}

// A card's top row is the app icon, the app name and the age, the
// summary sits under it in heading weight, and the body under that in
// muted text.
func TestCardContent(t *testing.T) {
	cfg := config.DefaultsNotification()
	cfg.IconSource = config.IconSourceMapped
	cfg.PopupUrgencyBar = config.UrgencyBarThresholdCritical
	p, svc, clock := newClockedPopups(t, cfg)
	p.now = func() time.Time { return clock.Now().Add(5*time.Minute + time.Second) }
	full := svc.NotifyHints("firefox", 0, "", "Download done", "a &amp; <b>b</b>", nil,
		map[string]dbus.Variant{"urgency": dbus.MakeVariant(byte(2))}, 0)
	bare := svc.Notify("", 0, "", "No app", "", nil, 0)
	p.sync()
	c := cardOf(t, p, full)
	if c.appLabel().Text() != "firefox" || c.summary.Text() != "Download done" || c.body.Text() != "a & b" || !c.body.Visible() {
		t.Errorf("card = %q / %q / %q", c.appLabel().Text(), c.summary.Text(), c.body.Text())
	}
	if !c.summary.HasClass(css.Heading) || !c.body.HasClass(css.DimLabel) {
		t.Errorf("summary classes %v, body classes %v; want heading and muted", c.summary.Classes(), c.body.Classes())
	}
	if c.time.Text() != i18n.T("notification-popup-time-minutes-ago", i18n.Str("minutes", "5")) {
		t.Errorf("time = %q", c.time.Text())
	}
	if treetest.WithClass(c.head, "notification-popup-icon") == nil || treetest.WithClass(c.head, "notification-popup-time") == nil {
		t.Error("the top row lacks the icon or the age")
	}
	if !c.root.HasClass("critical") || !c.root.HasClass("urgency-bar") || !c.root.HasClass("notification-popup-card") || !c.root.HasClass(css.Glass) {
		t.Errorf("classes = %v", c.root.Classes())
	}
	if c.root.HasClass("shadow") != cfg.PopupShadow {
		t.Error("the shadow class does not follow popup-shadow")
	}
	if c.actions.Visible() {
		t.Error("an action row without actions")
	}
	b := cardOf(t, p, bare)
	if b.appLabel().Text() != i18n.T("notification-popup-unknown-app") || b.body.Visible() {
		t.Errorf("bare card: app %q body visible %v", b.appLabel().Text(), b.body.Visible())
	}
	if b.root.HasClass("urgency-bar") || !b.root.HasClass("normal") {
		t.Errorf("a normal card under the default threshold: %v", b.root.Classes())
	}

	cfg.PopupShadow = !cfg.PopupShadow
	cfg.PopupUrgencyBar = config.UrgencyBarThresholdNone
	next := config.Defaults()
	next.Notification = cfg
	p.SetConfig(next)
	if c := cardOf(t, p, full); c.root.HasClass("shadow") != cfg.PopupShadow || c.root.HasClass("urgency-bar") {
		t.Errorf("after a reload: %v", c.root.Classes())
	}
}

func TestCardCloseBehavior(t *testing.T) {
	for _, tc := range []struct {
		behavior    config.PopupCloseBehavior
		keepHistory bool
	}{
		{config.PopupCloseBehaviorDismiss, true},
		{config.PopupCloseBehaviorRemove, false},
	} {
		cfg := config.DefaultsNotification()
		cfg.PopupCloseBehavior = tc.behavior
		s := newSurface(t, cfg)
		id := s.svc.Notify("app", 0, "", "Close me", "", []string{"default", "Open"}, 0)
		var sig signals
		s.svc.SetEmitter(sig.emit)
		s.sync()
		c := s.card(id)
		s.hover(c.summary)
		s.click(c.close)
		if len(s.svc.Popups()) != 0 {
			t.Errorf("%s: the popup stayed", tc.behavior)
		}
		if kept := len(history(t, s.svc)) == 1; kept != tc.keepHistory {
			t.Errorf("%s: in history %v, want %v", tc.behavior, kept, tc.keepHistory)
		}
		if sig.count("ActionInvoked") != 0 {
			t.Errorf("%s: the close button invoked the default action", tc.behavior)
		}
		// The default action reaches the app under either behavior: it is
		// invoked before the close can take the notification away.
		open := s.svc.Notify("app", 0, "", "Open me", "", []string{"default", "Open"}, 0)
		s.sync()
		s.click(s.card(open).summary)
		if sig.count("ActionInvoked"+fmt.Sprint(open, notifications.DefaultActionID)) != 1 {
			t.Errorf("%s: default action not invoked: %v", tc.behavior, sig.seen)
		}
	}
}

func TestCardActionsAndDefault(t *testing.T) {
	p, svc := newTestPopups(t, config.DefaultsNotification())
	id := svc.Notify("chat", 0, "", "Ping", "", []string{
		"default", "Open", "a", "One", "b", "Two", "c", "Three", "d", "Four",
	}, 0)
	plain := svc.Notify("chat", 0, "", "No default", "", nil, 0)
	var sig signals
	svc.SetEmitter(sig.emit)
	p.sync()
	c := cardOf(t, p, id)
	rows := c.actions.Children()
	if !c.actions.Visible() || len(rows) != 2 || len(rows[0].(*widget.Box).Children()) != 3 || len(rows[1].(*widget.Box).Children()) != 1 {
		t.Fatalf("action rows = %d, want 3 + 1 without the default", len(rows))
	}
	// A click on plain text invokes the default action.
	click(c, c.summary)
	if sig.count("ActionInvoked"+fmt.Sprint(id, notifications.DefaultActionID)) != 1 || len(svc.Popups()) != 1 {
		t.Errorf("default: %v, popups %d", sig.seen, len(svc.Popups()))
	}
	// No default action: a click on the card does nothing.
	pc := cardOf(t, p, plain)
	click(pc, pc.summary)
	if sig.count("ActionInvoked"+strconv.FormatUint(uint64(plain), 10)) != 0 {
		t.Error("a card without a default action invoked one")
	}

	// An action button invokes its action and dismisses.
	other := svc.Notify("chat", 0, "", "Reply", "", []string{"reply", "Reply"}, 0)
	p.sync()
	oc := cardOf(t, p, other)
	btn := oc.actions.Children()[0].(*widget.Box).Children()[0].(*widget.Button)
	click(oc, btn)
	if sig.count("ActionInvoked"+fmt.Sprint(other, "reply")) != 1 {
		t.Errorf("action: %v", sig.seen)
	}
	for _, n := range history(t, svc) {
		if n.ID == other {
			t.Error("an invoked notification stayed in the history")
		}
	}
}

// tokenMinter answers every token request with token and err.
type tokenMinter struct {
	token string
	err   error
}

func (m tokenMinter) RequestActivationToken(done func(string, error)) { done(m.token, m.err) }

// A press on the card or an action button mints a token, which goes
// out as ActivationToken right before the ActionInvoked; on a
// compositor without xdg-activation the action goes alone.
func TestActionPressesCarryTheirToken(t *testing.T) {
	p, svc := newTestPopups(t, config.DefaultsNotification())
	p.minter = tokenMinter{token: "tok-9"}
	var sig signals
	svc.SetEmitter(sig.emit)
	id := svc.Notify("chat", 0, "", "Ping", "", []string{"default", "Open", "reply", "Reply"}, 0)
	p.sync()
	c := cardOf(t, p, id)
	click(c, c.summary)
	other := svc.Notify("chat", 0, "", "Again", "", []string{"reply", "Reply"}, 0)
	p.sync()
	oc := cardOf(t, p, other)
	click(oc, oc.actions.Children()[0].(*widget.Box).Children()[0].(*widget.Button))
	sig.mu.Lock()
	seen := slices.DeleteFunc(slices.Clone(sig.seen), func(m string) bool { return strings.HasPrefix(m, "NotificationClosed") })
	sig.mu.Unlock()
	want := []string{
		"ActivationToken" + fmt.Sprint(id, "tok-9"), "ActionInvoked" + fmt.Sprint(id, notifications.DefaultActionID),
		"ActivationToken" + fmt.Sprint(other, "tok-9"), "ActionInvoked" + fmt.Sprint(other, "reply"),
	}
	if !slices.Equal(seen, want) {
		t.Errorf("signals = %q, want %q", seen, want)
	}

	p.minter = tokenMinter{err: app.ErrActivationUnavailable}
	last := svc.Notify("chat", 0, "", "Last", "", []string{"default", "Open"}, 0)
	p.sync()
	lc := cardOf(t, p, last)
	click(lc, lc.summary)
	if sig.count("ActivationToken"+strconv.FormatUint(uint64(last), 10)) != 0 || sig.count("ActionInvoked"+fmt.Sprint(last, notifications.DefaultActionID)) != 1 {
		t.Errorf("without xdg-activation: %v", sig.seen)
	}
}

func TestPopupMargins(t *testing.T) {
	cfg := config.DefaultsNotification()
	cfg.PopupMarginX = config.Scale(1)
	cfg.PopupMarginY = config.Px(20)
	cfg.PopupPosition = config.PopupPositionTopRight
	if got := popupMargins(cfg, 2); got != (app.Margins{Top: 20, Right: 24}) {
		t.Errorf("top-right = %+v, want y 20px on top, x 1x12x2 on the right", got)
	}
	cfg.PopupPosition = config.PopupPositionBottomCenter
	if got := popupMargins(cfg, 1); got != (app.Margins{Bottom: 20}) {
		t.Errorf("bottom-center = %+v, want only the bottom", got)
	}
	cfg.PopupPosition = config.PopupPositionCenterLeft
	if got := popupMargins(cfg, 1); got != (app.Margins{Left: 12}) {
		t.Errorf("center-left = %+v, want only the left", got)
	}
}

func TestZeroPopupDurationSticks(t *testing.T) {
	cfg := config.DefaultsNotification()
	cfg.PopupDuration = 0
	p, svc := newTestPopups(t, cfg)
	svc.Notify("app", 0, "", "Sticky", "", nil, 0)
	p.sync()
	if got := p.Visible(); got != 1 {
		t.Fatalf("visible = %d", got)
	}
}

func TestPopupAnchors(t *testing.T) {
	// notification_popup/methods.rs apply_position: corners take both
	// edges, the centered positions one.
	for pos, want := range map[config.PopupPosition]app.Anchor{
		config.PopupPositionTopRight:     app.AnchorTop | app.AnchorRight,
		config.PopupPositionBottomLeft:   app.AnchorBottom | app.AnchorLeft,
		config.PopupPositionTopCenter:    app.AnchorTop,
		config.PopupPositionBottomCenter: app.AnchorBottom,
		config.PopupPositionCenterLeft:   app.AnchorLeft,
		config.PopupPositionCenterRight:  app.AnchorRight,
	} {
		if got := popupAnchors(pos); got != want {
			t.Errorf("%s anchors = %v, want %v", pos, got, want)
		}
	}
	if got := popupAnchors("sideways"); got != app.AnchorTop|app.AnchorRight {
		t.Errorf("an unknown position = %v, want the top-right default", got)
	}
}

func TestSetConfigRebuildsTheStacksWithTheNewLimits(t *testing.T) {
	cfg := config.DefaultsNotification()
	p, svc := newTestPopups(t, cfg)
	for range 3 {
		svc.Notify("app", 0, "", "hi", "", nil, 0)
	}
	p.sync()
	if got := p.Visible(); got != 3 {
		t.Fatalf("visible = %d, want 3 under the default limit", got)
	}
	cfg.PopupMaxVisible = 1
	next := config.Defaults()
	next.Notification = cfg
	p.SetConfig(next)
	if got := p.Visible(); got != 1 {
		t.Errorf("after a reload with popup-max-visible = 1: visible = %d", got)
	}
	cfg.PopupMaxVisible = 5
	cfg.PopupStack = config.PopupStackOff
	next = config.Defaults()
	next.Notification = cfg
	p.SetConfig(next)
	if got := p.stackOrder(); len(got) != 3 {
		t.Errorf("after a reload with popup-stack off: stacks %v, want one per card", got)
	}
}

// testCardWidth is the width the card tests lay a card out at.
const testCardWidth = 380

// The card is lys-ui's card, its top row an Item, its actions small
// outline buttons in one homogeneous row; the labels take their ink
// from the stylesheet, and only a default action brings the pointer.
func TestCardIsAnItemInACard(t *testing.T) {
	p, svc := newTestPopups(t, config.DefaultsNotification())
	plain := svc.Notify("app", 0, "", "Plain", "", nil, 0)
	acting := svc.Notify("app", 0, "", "Acting", "body", []string{"default", "Open", "a", "A", "b", "B"}, 0)
	p.sync()
	c := cardOf(t, p, acting)
	if !c.root.HasClass(css.Card) {
		t.Error("the card is not lys-ui's card")
	}
	for _, l := range treetest.All[*widget.Label](c.root) {
		if l.Color() != 0 {
			t.Errorf("label %q pins ink %#08x over the stylesheet", l.Text(), uint32(l.Color()))
		}
	}
	rows := treetest.WithClass(c.root, "notification-popup-action-row")
	if len(rows) != 1 || !rows[0].(*widget.Box).Homogeneous() {
		t.Errorf("action rows %v, want one homogeneous row", rows)
	}
	buttons := treetest.WithClass(c.root, "notification-popup-action-btn")
	if len(buttons) != 2 || !widget.HasClass(buttons[0], css.Outline) {
		t.Errorf("%d action buttons, want the two non-default actions as outline buttons", len(buttons))
	}
	if widget.CursorNameOf(c.root) != "pointer" {
		t.Error("a card with a default action shows the pointer")
	}
	if widget.CursorNameOf(cardOf(t, p, plain).root) != "" {
		t.Error("a card without a default action shows the pointer")
	}
}

// appLabel is the card's app name, its top row Item's title.
func (c *card) appLabel() *widget.Label {
	return treetest.WithClass(c.head, "title")[0].(*widget.Label)
}

// A stack is the configured card width whatever room the surface
// offers: the Items inside fill only their cards.
func TestStackKeepsItsWidth(t *testing.T) {
	p, svc := newTestPopups(t, config.DefaultsNotification())
	id := svc.Notify("app", 0, "", "Ping", "body", nil, 0)
	p.sync()
	st := p.stacks[0]
	sz := widget.MeasureAsChild(st.root, widget.Constraints{Max: widget.Size{W: 1920, H: 1080}})
	want := int(cardWidthPx * float64(p.cfg.Styling.Scale))
	if sz.W != want {
		t.Errorf("the stack of %d measures %dpx wide in a 1920px surface, want %d", id, sz.W, want)
	}
}

// history is the first page of svc's history, newest first.
func history(t *testing.T, svc *notifications.Service) []*notifications.Notification {
	t.Helper()
	got, err := svc.Page(nil, notifications.MaxPage)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
