package clipboard

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/stubbedev/lys-ui/app"
)

// fakeDevice plays the data-control device: the test announces
// offers through announce, and claims are recorded.
type fakeDevice struct {
	fn       func(selectionOffer)
	claims   []*fakeClaim
	claimErr error
}

func (d *fakeDevice) watch(fn func(selectionOffer)) { d.fn = fn }

// claim ends the previous claim first, as the device does.
func (d *fakeDevice) claim(src app.SelectionSource) (selectionClaim, error) {
	if d.claimErr != nil {
		return nil, d.claimErr
	}
	if n := len(d.claims); n > 0 {
		d.claims[n-1].cancel()
	}
	c := &fakeClaim{src: src, live: true}
	d.claims = append(d.claims, c)
	return c, nil
}

// fakeClaim is a claim the test ends by hand.
type fakeClaim struct {
	src       app.SelectionSource
	live      bool
	released  bool
	cancelled []func()
}

func (c *fakeClaim) OnCancelled(fn func()) {
	if !c.live {
		fn()
		return
	}
	c.cancelled = append(c.cancelled, fn)
}

func (c *fakeClaim) Release() { c.live, c.released, c.cancelled = false, true, nil }

// cancel ends the claim as another client taking the selection does.
func (c *fakeClaim) cancel() {
	if !c.live {
		return
	}
	c.live = false
	for _, fn := range c.cancelled {
		fn()
	}
	c.cancelled = nil
}

// pipe is a receiver's end of a paste.
type pipe struct {
	bytes.Buffer
	closed chan struct{}
}

func (p *pipe) Close() error {
	close(p.closed)
	return nil
}

// paste asks the claim for mime as a receiver does, and returns what
// it was sent once the write finished.
func (c *fakeClaim) paste(t *testing.T, mime string) string {
	t.Helper()
	w := &pipe{closed: make(chan struct{})}
	c.src.Send(mime, w)
	<-w.closed
	return w.String()
}

func (d *fakeDevice) announce(o selectionOffer) { d.fn(o) }

// fakeOffer holds its read open until the test finishes it.
type fakeOffer struct {
	mimes   []string
	own     bool
	readErr error
	asked   []string
	limit   int64
	done    func([]byte, error)
}

func (o *fakeOffer) Mimes() []string { return o.mimes }
func (o *fakeOffer) Own() bool       { return o.own }

func (o *fakeOffer) Read(mime string, limit int64, done func([]byte, error)) error {
	if o.readErr != nil {
		return o.readErr
	}
	o.asked = append(o.asked, mime)
	o.limit = limit
	o.done = done
	return nil
}

func (o *fakeOffer) finish(data string) { o.done([]byte(data), nil) }

// newTestClipboard runs invoke inline: the tests are the loop.
func newTestClipboard(t *testing.T, h *History) (*Clipboard, *fakeDevice) {
	t.Helper()
	if h == nil {
		h = defaultHistory(t)
	}
	dev := &fakeDevice{}
	c := start(dev, func(fn func()) { fn() }, h)
	t.Cleanup(c.Close)
	return c, dev
}

func mimesOf(h *History) []string {
	snap := h.Entries()
	defer snap.Release()
	out := make([]string, len(snap.Entries()))
	for i, x := range snap.Entries() {
		out[i] = fmt.Sprintf("%s=%s", x.Mime, x.content().data)
	}
	return out
}

// ids are the history's entry ids, most recent first.
func ids(c *Clipboard) []uint64 {
	snap := c.Entries()
	defer snap.Release()
	out := make([]uint64, len(snap.Entries()))
	for i, e := range snap.Entries() {
		out[i] = e.ID
	}
	return out
}

func TestAForeignCopyIsReadUnderItsBestMimeAndRemembered(t *testing.T) {
	c, dev := newTestClipboard(t, nil)
	offer := &fakeOffer{mimes: []string{"text/plain", URIListMime}}

	dev.announce(offer)
	offer.finish("file:///tmp/a\n")

	if !slices.Equal(offer.asked, []string{URIListMime}) {
		t.Fatalf("read under %v", offer.asked)
	}
	if offer.limit != MaxEntryBytes {
		t.Fatalf("read limit = %d, want the entry cap", offer.limit)
	}
	if got := mimesOf(c.history); !slices.Equal(got, []string{URIListMime + "=file:///tmp/a\n"}) {
		t.Fatalf("entries = %v", got)
	}
}

func TestSelectionsThatMustNotBeReadAreNot(t *testing.T) {
	c, dev := newTestClipboard(t, nil)
	cases := map[string]*fakeOffer{
		"own":       {mimes: []string{textMime}, own: true},
		"sensitive": {mimes: []string{textMime, "x-kde-passwordManagerHint"}},
		"empty":     {},
	}
	for name, offer := range cases {
		dev.announce(offer)
		if len(offer.asked) != 0 {
			t.Errorf("%s selection was read", name)
		}
	}
	dev.announce(nil)
	if c.history.Len() != 0 {
		t.Fatal("something was remembered")
	}
}

func TestReadsAreRecordedInSelectionOrder(t *testing.T) {
	c, dev := newTestClipboard(t, nil)
	slow := &fakeOffer{mimes: []string{textMime}}
	quick := &fakeOffer{mimes: []string{textMime}}

	dev.announce(slow)
	dev.announce(quick)
	quick.finish("second")
	if c.history.Len() != 0 {
		t.Fatal("a later read was recorded ahead of an earlier one in flight")
	}
	slow.finish("first")

	if got := texts(c.history); !slices.Equal(got, []string{"second", "first"}) {
		t.Fatalf("entries = %v, want newest first", got)
	}
}

func TestAFailedReadIsSkippedWithoutBlockingTheNext(t *testing.T) {
	c, dev := newTestClipboard(t, nil)
	broken := &fakeOffer{mimes: []string{textMime}}
	refused := &fakeOffer{mimes: []string{textMime}, readErr: errors.New("gone")}
	fine := &fakeOffer{mimes: []string{textMime}}

	dev.announce(broken)
	dev.announce(refused)
	dev.announce(fine)
	fine.finish("kept")
	broken.done(nil, app.ErrTransferTooLarge)

	if got := texts(c.history); !slices.Equal(got, []string{"kept"}) {
		t.Fatalf("entries = %v", got)
	}
	if len(c.reads) != 0 {
		t.Fatalf("%d reads left queued", len(c.reads))
	}
}

func TestCopyPutsAnEntryBackUnderItsOwnMime(t *testing.T) {
	c, dev := newTestClipboard(t, nil)
	png := &fakeOffer{mimes: []string{"image/png"}}
	dev.announce(png)
	png.finish("\x89PNG")
	text := &fakeOffer{mimes: []string{textMime}}
	dev.announce(text)
	text.finish("hello")
	before := mimesOf(c.history)

	if !c.Copy(ids(c)[1]) {
		t.Fatal("Copy of a remembered entry reported false")
	}

	if len(dev.claims) != 1 || !slices.Equal(dev.claims[0].src.Mimes, []string{"image/png"}) {
		t.Fatalf("claims = %+v", dev.claims)
	}
	if got := dev.claims[0].paste(t, "image/png"); got != "\x89PNG" {
		t.Fatalf("served %q", got)
	}
	// A restore is not a new copy: the order is unchanged.
	if got := mimesOf(c.history); !slices.Equal(got, before) {
		t.Fatalf("entries moved: %v", got)
	}
}

func TestCopyingTextOffersEveryTextAlias(t *testing.T) {
	c, dev := newTestClipboard(t, nil)
	offer := &fakeOffer{mimes: []string{"STRING", textMime}}
	dev.announce(offer)
	offer.finish("hi")

	c.Copy(ids(c)[0])

	if got := dev.claims[0].src.Mimes; got[0] != textMime || len(got) != len(TextMimes) {
		t.Fatalf("offered %v", got)
	}
	if got := dev.claims[0].paste(t, "STRING"); got != "hi" {
		t.Fatalf("an alias served %q", got)
	}
}

// The Rust service stops recording for the rest of the session after
// its first restore (its serving slot is never cleared); here only the
// restore's own offer is skipped, and the next foreign copy counts.
func TestACopyAfterARestoreIsRememberedAgain(t *testing.T) {
	c, dev := newTestClipboard(t, nil)
	first := &fakeOffer{mimes: []string{textMime}}
	dev.announce(first)
	first.finish("one")
	c.Copy(ids(c)[0])
	dev.announce(&fakeOffer{mimes: []string{textMime}, own: true})

	next := &fakeOffer{mimes: []string{textMime}}
	dev.announce(next)
	next.finish("two")

	if got := texts(c.history); !slices.Equal(got, []string{"two", "one"}) {
		t.Fatalf("entries = %v", got)
	}
}

func TestCopyOfAGoneEntryClaimsNothing(t *testing.T) {
	c, dev := newTestClipboard(t, nil)

	if c.Copy(42) {
		t.Fatal("Copy of an unknown id reported true")
	}
	if len(dev.claims) != 0 {
		t.Fatal("an unknown id still claimed the selection")
	}
}

func TestForgetAndClear(t *testing.T) {
	c, dev := newTestClipboard(t, nil)
	for _, s := range []string{"a", "b", "c"} {
		o := &fakeOffer{mimes: []string{textMime}}
		dev.announce(o)
		o.finish(s)
	}
	id := ids(c)[1]

	if !c.Forget(id) || c.Forget(id) {
		t.Fatal("Forget did not report presence exactly once")
	}
	if c.Copy(id) {
		t.Fatal("a forgotten entry still resolves")
	}
	if got := texts(c.history); !slices.Equal(got, []string{"c", "a"}) {
		t.Fatalf("entries = %v", got)
	}
	c.Clear()
	if c.history.Len() != 0 {
		t.Fatal("Clear left entries")
	}
}

func TestAClaimFailureIsNotFatal(t *testing.T) {
	c, dev := newTestClipboard(t, nil)
	offer := &fakeOffer{mimes: []string{textMime}}
	dev.announce(offer)
	offer.finish("x")
	dev.claimErr = errors.New("finished")

	if !c.Copy(ids(c)[0]) {
		t.Fatal("Copy reported the entry gone")
	}
	if c.history.Len() != 1 {
		t.Fatal("the history lost an entry to a claim failure")
	}
}

func TestAZeroSizeHistoryReadsNothing(t *testing.T) {
	h := mustHistory(t, 10, 0, 0)
	_, dev := newTestClipboard(t, h)
	offer := &fakeOffer{mimes: []string{textMime}}

	dev.announce(offer)

	if len(offer.asked) != 0 {
		t.Fatal("a history that keeps nothing still read the selection")
	}
}

// remember announces a foreign copy of data and finishes reading it.
func remember(dev *fakeDevice, mime, data string) {
	o := &fakeOffer{mimes: []string{mime}}
	dev.announce(o)
	o.finish(data)
}

// blockingPipe holds a paste's write until the test lets it through.
type blockingPipe struct {
	pipe
	started chan struct{}
	proceed chan struct{}
}

func (p *blockingPipe) Write(b []byte) (int, error) {
	close(p.started)
	<-p.proceed
	return p.pipe.Write(b)
}

func TestARestoredEntryStillPastesAfterTheHistoryDropsIt(t *testing.T) {
	h := mustHistory(t, 1, MaxEntryBytes, MaxEntryBytes)
	c, dev := newTestClipboard(t, h)
	remember(dev, "image/png", "\x89PNG one")
	c.Copy(ids(c)[0])

	remember(dev, textMime, "newer")
	c.Clear()

	if n := live(h); n != 1 {
		t.Fatalf("%d allocations while the claim serves the dropped entry, want 1", n)
	}
	if got := dev.claims[0].paste(t, "image/png"); got != "\x89PNG one" {
		t.Fatalf("pasted %q after the history dropped the entry", got)
	}
	if n := live(h); n != 1 {
		t.Fatalf("a finished paste left %d allocations, want the claim's 1", n)
	}

	dev.claims[0].cancel()
	if n := live(h); n != 0 {
		t.Fatalf("%d allocations once the claim ended", n)
	}
}

func TestAPasteInFlightOutlivesItsClaimAndEntry(t *testing.T) {
	h := mustHistory(t, 1, MaxEntryBytes, MaxEntryBytes)
	c, dev := newTestClipboard(t, h)
	remember(dev, textMime, "in flight")
	c.Copy(ids(c)[0])
	w := &blockingPipe{closed: make(chan struct{}), started: make(chan struct{}), proceed: make(chan struct{})}
	dev.claims[0].src.Send(textMime, w)
	<-w.started

	c.Clear()
	dev.claims[0].cancel()
	if n := live(h); n != 1 {
		t.Fatalf("%d allocations mid-paste, want the paste's 1", n)
	}

	close(w.proceed)
	<-w.closed
	if w.String() != "in flight" {
		t.Fatalf("pasted %q", w.String())
	}
	if n := live(h); n != 0 {
		t.Fatalf("%d allocations after the last paste finished", n)
	}
}

func TestARestoreReplacedByAnotherFreesTheFirst(t *testing.T) {
	h := mustHistory(t, 2, MaxEntryBytes, MaxEntryBytes)
	c, dev := newTestClipboard(t, h)
	remember(dev, textMime, "one")
	remember(dev, textMime, "two")
	first := ids(c)
	c.Copy(first[1])
	c.Forget(first[1])

	c.Copy(first[0])

	if dev.claims[0].live || c.serving == nil || c.serving.claim != dev.claims[1] {
		t.Fatal("the second restore did not replace the first")
	}
	if n := live(h); n != 1 {
		t.Fatalf("%d allocations, want only the entry still in the history", n)
	}
}

func TestAFailedClaimLetsGoOfTheEntry(t *testing.T) {
	h := mustHistory(t, 2, MaxEntryBytes, MaxEntryBytes)
	c, dev := newTestClipboard(t, h)
	remember(dev, textMime, "x")
	dev.claimErr = errors.New("finished")

	c.Copy(ids(c)[0])
	c.Clear()

	if n := live(h); n != 0 {
		t.Fatalf("%d allocations after a refused claim", n)
	}
}

func TestCloseFreesTheHistoryAndTheRestoredEntry(t *testing.T) {
	h := mustHistory(t, 4, MaxEntryBytes, MaxEntryBytes)
	c, dev := newTestClipboard(t, h)
	remember(dev, textMime, "one")
	remember(dev, "image/png", "\x89PNG")
	c.Copy(ids(c)[1])
	pending := &fakeOffer{mimes: []string{textMime}}
	dev.announce(pending)

	c.Close()

	if n := live(h); n != 0 {
		t.Fatalf("%d allocations after Close", n)
	}
	if !dev.claims[0].released || dev.claims[0].live {
		t.Fatal("Close did not give the selection up")
	}
	pending.finish("late")
	late := &fakeOffer{mimes: []string{textMime}}
	dev.announce(late)
	if live(h) != 0 || len(late.asked) != 0 {
		t.Fatal("the clipboard recorded after Close")
	}
}

func TestSetBudgetEvictsOldestFirst(t *testing.T) {
	h := mustHistory(t, 8, 4, 12)
	c, dev := newTestClipboard(t, h)
	for _, s := range []string{"aaaa", "bbbb", "cccc"} {
		remember(dev, textMime, s)
	}
	b, err := NewBudget(MinBudgetMiB)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetBudget(b); err != nil || h.Len() != 3 {
		t.Fatalf("a larger budget: %v, %d entries", err, h.Len())
	}
	if err := h.SetBudget(8); err != nil {
		t.Fatal(err)
	}
	if got := texts(h); !slices.Equal(got, []string{"cccc", "bbbb"}) || live(h) != 2 {
		t.Fatalf("entries = %v, %d allocations", got, live(h))
	}
}
