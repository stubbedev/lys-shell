package clipboard

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"testing"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/transfer"
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

// fakeOffer holds its read open until the test streams it.
type fakeOffer struct {
	mimes   []string
	own     bool
	readErr error
	asked   []string
	limit   int64
	w       io.Writer
	done    func(int64, error)
}

func (o *fakeOffer) Mimes() []string { return o.mimes }
func (o *fakeOffer) Own() bool       { return o.own }

func (o *fakeOffer) ReadTo(mime string, limit int64, w io.Writer, done func(int64, error)) error {
	if o.readErr != nil {
		return o.readErr
	}
	o.asked = append(o.asked, mime)
	o.limit, o.w, o.done = limit, w, done
	return nil
}

// transferChunk is the most a transfer writes at once (lys-ui's xfer).
const transferChunk = 32 << 10

// write streams data into the reader's writer in transfer-sized writes,
// as lys-ui's transfer goroutine does, stopping at the limit or the
// first refused write. It reports the bytes written and the error the
// transfer ends with, without calling done.
func (o *fakeOffer) write(data []byte) (int64, error) {
	var n int64
	for off := 0; off < len(data); off += transferChunk {
		chunk := data[off:min(off+transferChunk, len(data))]
		if room := o.limit - n; int64(len(chunk)) > room {
			if room > 0 {
				if _, err := o.w.Write(chunk[:room]); err != nil {
					return n, err
				}
				n += room
			}
			return n, fmt.Errorf("fake transfer past %d bytes: %w", o.limit, app.ErrTransferTooLarge)
		}
		if _, err := o.w.Write(chunk); err != nil {
			return n, err
		}
		n += int64(len(chunk))
	}
	return n, nil
}

// stream delivers data and ends the transfer.
func (o *fakeOffer) stream(data []byte) { o.done(o.write(data)) }

func (o *fakeOffer) finish(data string) { o.stream([]byte(data)) }

// fail ends the transfer with err after nothing more is written.
func (o *fakeOffer) fail(n int64, err error) { o.done(n, err) }

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
	broken.fail(0, app.ErrTransferTooLarge)

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

func TestPutClaimsEveryMimeServesEveryReaderAndRemembersTheBest(t *testing.T) {
	c, dev := newTestClipboard(t, nil)
	content := transfer.Payloads([]string{"image/png", "image/jpeg"}, map[string][]byte{
		"image/png": []byte("\x89PNG shot"), "image/jpeg": []byte("\xff\xd8 shot"),
	})

	if err := c.Put(content); err != nil {
		t.Fatal(err)
	}
	dev.announce(&fakeOffer{mimes: content.Mimes, own: true})

	if len(dev.claims) != 1 || !slices.Equal(dev.claims[0].src.Mimes, content.Mimes) {
		t.Fatalf("claims = %+v", dev.claims)
	}
	for range 3 {
		if got := dev.claims[0].paste(t, "image/png"); got != "\x89PNG shot" {
			t.Fatalf("a reader of image/png got %q", got)
		}
	}
	if got := dev.claims[0].paste(t, "image/jpeg"); got != "\xff\xd8 shot" {
		t.Fatalf("a reader of image/jpeg got %q", got)
	}
	if got := mimesOf(c.history); !slices.Equal(got, []string{"image/png=\x89PNG shot"}) {
		t.Fatalf("entries = %v", got)
	}
	if !dev.claims[0].live {
		t.Fatal("serving readers ended the claim")
	}
}

func TestPutReportsAFailedClaimAndRemembersNothing(t *testing.T) {
	c, dev := newTestClipboard(t, nil)
	dev.claimErr = app.ErrDataControlFinished

	err := c.Put(transfer.Bytes("image/png", []byte("\x89PNG")))

	if !errors.Is(err, app.ErrDataControlFinished) {
		t.Fatalf("Put = %v, want the claim's error", err)
	}
	if c.serving != nil || c.history.Len() != 0 {
		t.Fatalf("a failed claim left serving %v and %d entries", c.serving, c.history.Len())
	}
	if err := c.Put(transfer.Content{}); err == nil {
		t.Fatal("Put of empty content succeeded")
	}
}

func TestPutOfSensitiveContentIsServedButNotRemembered(t *testing.T) {
	c, dev := newTestClipboard(t, nil)

	if err := c.Put(SecretContent("hunter2")); err != nil {
		t.Fatal(err)
	}

	if got := dev.claims[0].paste(t, textMime); got != "hunter2" {
		t.Fatalf("served %q", got)
	}
	if c.history.Len() != 0 {
		t.Fatalf("a secret was remembered: %v", mimesOf(c.history))
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

// selection is n bytes of a payload that differs at every offset.
func selection(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('!' + i%94 + i/7919%3)
	}
	return b
}

// heapAllocated is the Go-heap bytes fn allocates.
func heapAllocated(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// storedBytes is the one entry the history holds, byte for byte.
func storedBytes(t *testing.T, h *History) (string, []byte) {
	t.Helper()
	snap := h.Entries()
	defer snap.Release()
	if len(snap.Entries()) != 1 {
		t.Fatalf("%d entries, want 1", len(snap.Entries()))
	}
	e := snap.Entries()[0]
	return e.Mime, bytes.Clone(e.content().data)
}

// A received selection streams into the history's own memory: a 4 MB
// one costs the Go heap nothing near its size, and is stored byte for
// byte, text (judged for whitespace) and binary alike.
func TestAReceivedSelectionNeverLandsOnTheGoHeap(t *testing.T) {
	const size = 4 << 20
	for _, mime := range []string{textMime, "image/png"} {
		h := defaultHistory(t)
		_, dev := newTestClipboard(t, h)
		data := selection(size)
		offer := &fakeOffer{mimes: []string{mime}}
		heap := heapAllocated(func() {
			dev.announce(offer)
			offer.stream(data)
		})
		t.Logf("%s: %d Go-heap bytes for a %d-byte selection", mime, heap, size)
		if heap >= size/16 {
			t.Errorf("%s: %d Go-heap bytes for a %d-byte selection", mime, heap, size)
		}
		if got, b := storedBytes(t, h); got != mime || !bytes.Equal(b, data) {
			t.Fatalf("%s: stored %d bytes under %s, not the selection", mime, len(b), got)
		}
		if st := h.mem.Stats(); st.Live != 1 || st.Bytes < size || st.Bytes > size+64<<10 {
			t.Fatalf("%s: %d allocations of %d bytes, want the entry alone", mime, st.Live, st.Bytes)
		}
	}
}

// A transfer that breaks off mid-stream remembers nothing and frees
// the bytes it had received at once.
func TestABrokenTransferFreesItsPartialBytes(t *testing.T) {
	h := defaultHistory(t)
	c, dev := newTestClipboard(t, h)
	offer := &fakeOffer{mimes: []string{"image/png"}}
	dev.announce(offer)
	n, err := offer.write(selection(200 << 10))
	if err != nil || live(h) != 1 {
		t.Fatalf("mid-transfer: %v, %d allocations", err, live(h))
	}
	offer.fail(n, errors.New("peer gone"))
	if live(h) != 0 || h.Len() != 0 || len(c.reads) != 0 {
		t.Fatalf("after the break: %d allocations, %d entries, %d reads", live(h), h.Len(), len(c.reads))
	}
}

// A selection over the entry cap is refused whole (#101): the first
// cap's worth the transfer kept is freed, nothing is stored, and the
// next selection is read as usual.
func TestAnOverCapSelectionIsFreedAndRefusedWhole(t *testing.T) {
	const capBytes = 64 << 10
	h := mustHistory(t, 4, capBytes, capBytes)
	c, dev := newTestClipboard(t, h)
	big := &fakeOffer{mimes: []string{"image/png"}}
	dev.announce(big)
	n, err := big.write(selection(capBytes + 1))
	if !errors.Is(err, app.ErrTransferTooLarge) || n != capBytes || live(h) != 1 {
		t.Fatalf("over the cap: %d bytes, %v, %d allocations", n, err, live(h))
	}
	big.fail(n, err)
	if live(h) != 0 || h.Len() != 0 || len(c.reads) != 0 {
		t.Fatalf("after the refusal: %d allocations, %d entries, %d reads", live(h), h.Len(), len(c.reads))
	}

	exact := selection(capBytes)
	remember(dev, "image/png", string(exact))
	if _, b := storedBytes(t, h); !bytes.Equal(b, exact) || live(h) != 1 {
		t.Fatalf("a selection of exactly the cap: %d bytes stored, %d allocations", len(b), live(h))
	}
}

// A selection already held moves to the front, and its fresh copy is
// freed rather than kept twice.
func TestAReceivedDuplicateIsFreed(t *testing.T) {
	h := defaultHistory(t)
	c, dev := newTestClipboard(t, h)
	remember(dev, textMime, "same")
	remember(dev, textMime, "other")
	remember(dev, textMime, "same")
	if got := texts(c.history); !slices.Equal(got, []string{"same", "other"}) || live(h) != 2 {
		t.Fatalf("entries = %v, %d allocations", got, live(h))
	}
}

// Close frees a read still streaming; the transfer's next write is
// refused, not written into freed memory, and its late end records
// nothing.
func TestCloseFreesAReadStillStreaming(t *testing.T) {
	h := defaultHistory(t)
	c, dev := newTestClipboard(t, h)
	offer := &fakeOffer{mimes: []string{"image/png"}}
	dev.announce(offer)
	if _, err := offer.write(selection(100 << 10)); err != nil || live(h) != 1 {
		t.Fatalf("mid-transfer: %v, %d allocations", err, live(h))
	}

	c.Close()
	if live(h) != 0 {
		t.Fatalf("%d allocations after Close", live(h))
	}
	n, err := offer.write(selection(100 << 10))
	if err == nil || n != 0 || live(h) != 0 {
		t.Fatalf("a write after Close: %d bytes, %v, %d allocations", n, err, live(h))
	}
	offer.fail(n, err)
	if live(h) != 0 || h.Len() != 0 {
		t.Fatalf("the late end: %d allocations, %d entries", live(h), h.Len())
	}
}
