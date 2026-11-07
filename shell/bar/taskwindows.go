package bar

import (
	"cmp"
	"slices"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
)

// taskWindow is one window of the compositor's foreign-toplevel list as
// the minimized-windows module and the dock use it; app.Toplevel is the
// real one. Equal values are the same window.
type taskWindow interface {
	Title() string
	AppID() string
	Minimized() bool
	Activated() bool
	OnOutput(name string) bool
	Activate()
	Close()
	SetRectangle(host app.Host, r render.Rect)
}

// toplevelSource is the window list taskWindows follows.
type toplevelSource interface {
	windows() []taskWindow
	onChanged(fn func()) (stop func())
}

// appToplevels is the application's own foreign-toplevel list: the
// connection the bars and the dock draw on, so a rectangle can name
// their surfaces.
type appToplevels struct{ app *app.Application }

func (s appToplevels) windows() []taskWindow {
	listed := s.app.Toplevels()
	out := make([]taskWindow, len(listed))
	for i, t := range listed {
		out[i] = t
	}
	return out
}

func (s appToplevels) onChanged(fn func()) func() { return s.app.OnToplevelsChanged(fn) }

// rectPriority orders the entries that may name a window's minimize
// rectangle: the dock's wins over the bar module's.
type rectPriority uint8

const (
	rectFromModule rectPriority = iota + 1
	rectFromDock
)

// rectClaim is one entry naming where a window minimizes to: its rect on
// host's surface, the output that surface is on, and its priority.
type rectClaim struct {
	priority rectPriority
	output   string
	host     app.Host
	rect     render.Rect
}

// sentRect is the rectangle a window was last given.
type sentRect struct {
	host app.Host
	rect render.Rect
}

// taskWindows is the shell's one source of window state for the
// minimized-windows module and the dock: the windows in the order they
// appeared, the order they were minimized in, and each window's
// minimize rectangle, settled from every entry that claims one. It runs
// on the loop.
type taskWindows struct {
	src      toplevelSource
	all      []taskWindow
	minSeq   map[taskWindow]uint64
	nextSeq  uint64
	claims   map[taskWindow]map[*rectOwner]rectClaim
	sent     map[taskWindow]sentRect
	owners   uint64
	watchers []*func()
	stop     func()
}

func newTaskWindows(src toplevelSource) *taskWindows {
	t := &taskWindows{
		src:    src,
		minSeq: map[taskWindow]uint64{},
		claims: map[taskWindow]map[*rectOwner]rectClaim{},
		sent:   map[taskWindow]sentRect{},
	}
	t.stop = src.onChanged(t.refresh)
	t.refresh()
	return t
}

// close stops following the window list.
func (t *taskWindows) close() { t.stop() }

func (t *taskWindows) refresh() {
	t.all = t.src.windows()
	for w := range t.minSeq {
		if !slices.Contains(t.all, w) || !w.Minimized() {
			delete(t.minSeq, w)
		}
	}
	for _, w := range t.all {
		if _, ok := t.minSeq[w]; w.Minimized() && !ok {
			t.nextSeq++
			t.minSeq[w] = t.nextSeq
		}
	}
	for w := range t.claims {
		if !slices.Contains(t.all, w) {
			delete(t.claims, w)
		}
	}
	for w := range t.sent {
		if !slices.Contains(t.all, w) {
			delete(t.sent, w)
		}
	}
	t.notify()
}

// notify tells every subscriber the windows or their apps changed.
func (t *taskWindows) notify() {
	for _, fn := range slices.Clone(t.watchers) {
		(*fn)()
	}
}

// subscribe calls fn after every change to the windows; stop ends it.
func (t *taskWindows) subscribe(fn func()) (stop func()) {
	w := &fn
	t.watchers = append(t.watchers, w)
	return func() {
		t.watchers = slices.DeleteFunc(t.watchers, func(o *func()) bool { return o == w })
	}
}

// windows is every window, in the order they appeared.
func (t *taskWindows) windows() []taskWindow { return t.all }

// inScope reports whether w is on output, or everywhere is set.
func inScope(w taskWindow, output string, everywhere bool) bool {
	return everywhere || w.OnOutput(output)
}

// minimized is the minimized windows on output, or on every output with
// everywhere, in the order they were minimized.
func (t *taskWindows) minimized(output string, everywhere bool) []taskWindow {
	var out []taskWindow
	for _, w := range t.all {
		if w.Minimized() && inScope(w, output, everywhere) {
			out = append(out, w)
		}
	}
	slices.SortStableFunc(out, func(a, b taskWindow) int { return cmp.Compare(t.minSeq[a], t.minSeq[b]) })
	return out
}

// owner starts a set of rectangle claims, one entry list's.
func (t *taskWindows) owner() *rectOwner {
	t.owners++
	return &rectOwner{t: t, id: t.owners}
}

// settle gives w the rectangle of its winning claim, or withdraws the
// one it was given when nothing claims it, sending only a change.
func (t *taskWindows) settle(w taskWindow) {
	var (
		best      rectClaim
		bestOwner *rectOwner
	)
	for o, c := range t.claims[w] {
		if bestOwner == nil || claimBeats(w, c, o, best, bestOwner) {
			best, bestOwner = c, o
		}
	}
	prev, had := t.sent[w]
	if bestOwner == nil {
		if had {
			delete(t.sent, w)
			w.SetRectangle(prev.host, render.Rect{})
		}
		return
	}
	next := sentRect{host: best.host, rect: best.rect}
	if had && prev == next {
		return
	}
	t.sent[w] = next
	w.SetRectangle(next.host, next.rect)
}

// claimBeats orders claims: the higher priority, then the entry on the
// window's own output, then the older owner.
func claimBeats(w taskWindow, c rectClaim, co *rectOwner, best rectClaim, bo *rectOwner) bool {
	if c.priority != best.priority {
		return c.priority > best.priority
	}
	if on, bestOn := w.OnOutput(c.output), w.OnOutput(best.output); on != bestOn {
		return on
	}
	return co.id < bo.id
}

// rectOwner is one entry list's claims: what it names now, replaced as
// a whole on each layout.
type rectOwner struct {
	t       *taskWindows
	id      uint64
	claimed map[taskWindow]rectClaim
}

// set makes claims the owner's whole set, settling every window whose
// claim changed.
func (o *rectOwner) set(claims map[taskWindow]rectClaim) {
	var changed []taskWindow
	for w, c := range o.claimed {
		if next, ok := claims[w]; !ok || next != c {
			changed = append(changed, w)
		}
	}
	for w, c := range claims {
		if prev, ok := o.claimed[w]; !ok || prev != c {
			changed = append(changed, w)
		}
	}
	o.claimed = claims
	slices.SortFunc(changed, func(a, b taskWindow) int { return cmp.Compare(slices.Index(o.t.all, a), slices.Index(o.t.all, b)) })
	changed = slices.Compact(changed)
	for _, w := range changed {
		if !slices.Contains(o.t.all, w) {
			continue
		}
		per := o.t.claims[w]
		if c, ok := claims[w]; ok {
			if per == nil {
				per = map[*rectOwner]rectClaim{}
				o.t.claims[w] = per
			}
			per[o] = c
		} else {
			delete(per, o)
		}
		o.t.settle(w)
	}
}

// release withdraws every claim of the owner.
func (o *rectOwner) release() { o.set(nil) }
