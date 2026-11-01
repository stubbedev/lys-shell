package portal

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/dmabufprobe"
	"github.com/stubbedev/lys-lib/fourcc"
	"github.com/stubbedev/lys-lib/pipewire"
)

// fakeSession is a window's capture session whose copies the test
// lands one at a time.
type fakeSession struct {
	fakeCopy
	formats  []capture.DmabufFormat
	results  chan error
	started  chan struct{}
	closeErr error
	openErr  error
	opened   int

	mu     sync.Mutex
	closed chan struct{}
	closes int
}

func newFakeSession() *fakeSession {
	return &fakeSession{
		formats: []capture.DmabufFormat{offer()},
		results: make(chan error), started: make(chan struct{}, 8), closed: make(chan struct{}),
	}
}

func (f *fakeSession) Open() error {
	if f.openErr != nil {
		return f.openErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closes > 0 {
		f.closed = make(chan struct{})
	}
	f.opened++
	return nil
}

func (f *fakeSession) closedChan() (chan struct{}, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed, f.opened
}

func (f *fakeSession) Formats() ([]capture.DmabufFormat, error) {
	if _, opened := f.closedChan(); opened == 0 {
		return nil, errors.New("formats of a session never opened")
	}
	return f.formats, nil
}

func (f *fakeSession) Capture(importedBuffer) (capture.FrameInfo, error) {
	closed, opened := f.closedChan()
	if opened == 0 {
		return capture.FrameInfo{}, errors.New("a capture on a session never opened")
	}
	f.started <- struct{}{}
	select {
	case err := <-f.results:
		if err != nil {
			return capture.FrameInfo{}, err
		}
		return capture.FrameInfo{Damage: []capture.Rect{{X: 1, Y: 2, Width: 3, Height: 4}}}, nil
	case <-closed:
		return capture.FrameInfo{}, capture.ErrClosed
	}
}

func (f *fakeSession) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	select {
	case <-f.closed:
	default:
		close(f.closed)
	}
	return f.closeErr
}

// land answers the running copy with err and waits until the source
// filed it.
func (f *fakeSession) land(t *testing.T, s *windowGPUSource, err error) {
	t.Helper()
	select {
	case <-f.started:
	case <-time.After(5 * time.Second):
		t.Fatal("no copy started")
	}
	f.results <- err
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		s.mu.Lock()
		idle := s.inflight == nil
		s.mu.Unlock()
		if idle {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the copy was never filed")
		}
	}
}

func windowShm() *snapshotSource {
	return newSnapshotSource(func([]byte) (*capture.Frame, error) { return shmFrame, nil }, shmFrame, newVanishWatch(func() bool { return true }))
}

func newWindowHarness(t *testing.T) (*windowGPUSource, *fakeSession, *fakeDevice) {
	t.Helper()
	dev, sess := &fakeDevice{}, newFakeSession()
	src, err := newWindowGPUSource(windowShm(), dev, sess, offer(), []dmabufprobe.Layout{{Modifier: fourcc.Linear}})
	if err != nil {
		t.Fatal(err)
	}
	var _ pipewire.DmabufSource = src
	var _ pipewire.Resizable = src
	return src, sess, dev
}

// A buffer handed over starts a copy and goes out, with the session's
// damage, the next time it is handed over after the copy landed; a
// buffer handed over while a copy runs neither goes out nor starts one.
func TestWindowSourceSendsABufferOnceItsCopyLanded(t *testing.T) {
	src, sess, _ := newWindowHarness(t)
	t.Cleanup(src.Close)
	a, err := src.AllocDmabuf()
	if err != nil {
		t.Fatal(err)
	}
	b, err := src.AllocDmabuf()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := src.FillDmabuf(a); ok {
		t.Fatal("a buffer went out before a copy into it ran")
	}
	if _, ok := src.FillDmabuf(b); ok {
		t.Fatal("a second buffer went out while the first one's copy ran")
	}
	sess.land(t, src, nil)
	select {
	case <-sess.started:
		t.Fatal("a copy started into the second buffer while the first ran")
	default:
	}
	damage, ok := src.FillDmabuf(a)
	if !ok || len(damage) != 1 || damage[0] != (pipewire.Rect{X: 1, Y: 2, Width: 3, Height: 4}) {
		t.Fatalf("fill = %v %v, want the copied frame with its damage", damage, ok)
	}
	if _, ok := src.FillDmabuf(a); ok {
		t.Error("a frame went out twice")
	}
	if _, ok := src.FillDmabuf(otherDmabuf{}); ok {
		t.Error("a buffer the source did not allocate went out")
	}
	sess.land(t, src, nil)
	a.Release()
	b.Release()
}

// A copy that fails sends nothing and does not end the stream; the
// window closing ends it.
func TestWindowSourceEndsOnlyWhenTheWindowCloses(t *testing.T) {
	src, sess, _ := newWindowHarness(t)
	t.Cleanup(src.Close)
	a, err := src.AllocDmabuf()
	if err != nil {
		t.Fatal(err)
	}
	src.FillDmabuf(a)
	sess.land(t, src, capture.ErrFailed)
	if _, ok := src.FillDmabuf(a); ok {
		t.Fatal("a failed copy went out")
	}
	select {
	case <-src.watch.ended:
		t.Fatal("a failed copy ended the stream")
	default:
	}
	sess.land(t, src, capture.ErrSessionStopped)
	select {
	case <-src.watch.ended:
	default:
		t.Fatal("the window closing did not end the stream")
	}
	a.Release()
}

// A resize makes the source report the session's new size, refuses the
// old buffers, and allocates the new ones at that size.
func TestWindowSourceFollowsAResize(t *testing.T) {
	src, sess, dev := newWindowHarness(t)
	t.Cleanup(src.Close)
	if w, h, _ := src.Size(); w != 64 || h != 32 {
		t.Fatalf("size before any GPU buffer = %dx%d, want the shm frames' 64x32", w, h)
	}
	old, err := src.AllocDmabuf()
	if err != nil {
		t.Fatal(err)
	}
	src.FillDmabuf(old)
	sess.formats = []capture.DmabufFormat{{Fourcc: fourcc.XRGB8888, Width: 100, Height: 50}}
	sess.land(t, src, capture.ErrReconfigured)
	if w, h, stride := src.Size(); w != 100 || h != 50 || stride != 400 {
		t.Fatalf("size after the resize = %dx%d stride %d, want 100x50 stride 400", w, h, stride)
	}
	if _, ok := src.FillDmabuf(old); ok {
		t.Fatal("a buffer of the old size went out")
	}
	select {
	case <-sess.started:
		t.Fatal("a copy started into a buffer of the old size")
	default:
	}
	old.Release()
	fresh, err := src.AllocDmabuf()
	if err != nil {
		t.Fatal(err)
	}
	if got := dev.lastSize(); got != [2]int{100, 50} {
		t.Errorf("allocated %v after the resize, want 100x50", got)
	}
	fresh.Release()
}

// A resize to constraints that take no buffer of the stream's format
// ends the stream.
func TestWindowSourceEndsWhenTheResizedWindowTakesNoBuffer(t *testing.T) {
	src, sess, _ := newWindowHarness(t)
	t.Cleanup(src.Close)
	a, err := src.AllocDmabuf()
	if err != nil {
		t.Fatal(err)
	}
	src.FillDmabuf(a)
	sess.formats = []capture.DmabufFormat{{Fourcc: fourcc.Format(0x34325241), Width: 100, Height: 50}}
	sess.land(t, src, capture.ErrReconfigured)
	select {
	case <-src.watch.ended:
	default:
		t.Fatal("the stream outlived constraints it cannot fill")
	}
	a.Release()
}

// A buffer the producer releases while a copy runs into it stays
// allocated until the copy ends; Close wakes that copy, so it ends.
func TestWindowSourceFreesABufferReleasedMidCopyWhenTheCopyEnds(t *testing.T) {
	src, sess, _ := newWindowHarness(t)
	a, err := src.AllocDmabuf()
	if err != nil {
		t.Fatal(err)
	}
	fd, probed := a.Fd(), sess.destroyed
	src.FillDmabuf(a)
	<-sess.started
	a.Release()
	if !open(fd) || sess.destroyed != probed {
		t.Fatal("a buffer was freed while a copy ran into it")
	}
	src.Close()
	if open(fd) || sess.destroyed != sess.imported {
		t.Errorf("after Close: fd open %v, %d of %d imports destroyed", open(fd), sess.destroyed, sess.imported)
	}
	if _, ok := src.FillDmabuf(a); ok {
		t.Error("a closed source sent a frame")
	}
}

// The probe takes the first layout that allocates and imports, and
// declines a session format of another size or not packed 32-bit.
func TestWindowSourceProbe(t *testing.T) {
	dev, sess := &fakeDevice{echo: true}, newFakeSession()
	src, err := newWindowGPUSource(windowShm(), dev, sess, offer(), []dmabufprobe.Layout{{Modifier: fourcc.Linear}})
	if err != nil {
		t.Fatal(err)
	}
	if src.Modifier() != fourcc.Linear || sess.destroyed != sess.imported {
		t.Errorf("probe: modifier %s, %d of %d imports released", src.Modifier(), sess.destroyed, sess.imported)
	}
	for _, offered := range []capture.DmabufFormat{
		{Fourcc: fourcc.XRGB8888, Width: 65, Height: 32},
		{Fourcc: capture.FormatBGR888.Fourcc(), Width: 64, Height: 32},
	} {
		if _, err := newWindowGPUSource(windowShm(), dev, newFakeSession(), offered, []dmabufprobe.Layout{{Modifier: fourcc.Linear}}); !errors.Is(err, errNoDmabuf) {
			t.Errorf("%+v: err = %v, want errNoDmabuf", offered, err)
		}
	}
	rejecting := newFakeSession()
	rejecting.rejectImport = true
	if _, err := newWindowGPUSource(windowShm(), dev, rejecting, offer(), []dmabufprobe.Layout{{Modifier: fourcc.Linear}}); !errors.Is(err, errNoDmabuf) {
		t.Errorf("every import rejected: err = %v, want errNoDmabuf", err)
	}
}

// The session opens with the first GPU buffer and only then, so a
// consumer of shared memory never has one held open over its captures;
// a session that fails to open fails the allocation.
func TestWindowSourceOpensItsSessionWithTheFirstGPUBuffer(t *testing.T) {
	src, sess, _ := newWindowHarness(t)
	t.Cleanup(src.Close)
	if sess.opened != 0 {
		t.Fatal("the session opened before a consumer took the GPU buffers")
	}
	for range 2 {
		b, err := src.AllocDmabuf()
		if err != nil {
			t.Fatal(err)
		}
		b.Release()
	}
	if sess.opened != 1 {
		t.Errorf("the session opened %d times for two buffers, want once", sess.opened)
	}

	refusing, failing, _ := newWindowHarness(t)
	t.Cleanup(refusing.Close)
	failing.openErr = capture.ErrUnsupported
	if _, err := refusing.AllocDmabuf(); !errors.Is(err, capture.ErrUnsupported) {
		t.Fatalf("AllocDmabuf with a session that does not open = %v", err)
	}
	if w, h, _ := refusing.Size(); w != 64 || h != 32 {
		t.Errorf("size after a failed open = %dx%d, want the shm frames' 64x32", w, h)
	}
}

// Buffers that replace dropped ones get a new session, whose first frame
// is copied at once, even from a window that does not change: a copy
// waiting on the old session is woken, and a buffer already dropped is
// freed with it.
func TestWindowSourceRestartsItsSessionForNewBuffers(t *testing.T) {
	src, sess, _ := newWindowHarness(t)
	t.Cleanup(src.Close)
	a, err := src.AllocDmabuf()
	if err != nil {
		t.Fatal(err)
	}
	src.Restart()
	if sess.opened != 1 || sess.closes != 0 {
		t.Fatalf("a session that took no copy restarted: opened %d, closed %d", sess.opened, sess.closes)
	}
	src.FillDmabuf(a)
	sess.land(t, src, nil)
	if _, ok := src.FillDmabuf(a); !ok {
		t.Fatal("the landed copy did not go out")
	}
	src.FillDmabuf(a)
	<-sess.started
	fd := a.Fd()
	a.Release()
	src.Restart()
	sess.mu.Lock()
	opened, closes := sess.opened, sess.closes
	sess.mu.Unlock()
	if opened != 2 || closes != 1 {
		t.Fatalf("after the restart: opened %d, closed %d", opened, closes)
	}
	for deadline := time.Now().Add(5 * time.Second); open(fd); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the copy on the old session was not woken to free its buffer")
		}
	}
	b, err := src.AllocDmabuf()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Release()
	src.FillDmabuf(b)
	sess.land(t, src, nil)
	if _, ok := src.FillDmabuf(b); !ok {
		t.Fatal("the new session's frame did not go out")
	}
}

// A window whose session does not reopen can send nothing more.
func TestWindowSourceEndsWhenItsSessionDoesNotRestart(t *testing.T) {
	src, sess, _ := newWindowHarness(t)
	t.Cleanup(src.Close)
	a, err := src.AllocDmabuf()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	src.FillDmabuf(a)
	sess.land(t, src, nil)
	sess.openErr = capture.ErrSessionStopped
	src.Restart()
	select {
	case <-src.watch.ended:
	default:
		t.Fatal("the stream outlived its session")
	}
}

// Preparing starts the copy the next handover collects, and does nothing
// while one runs.
func TestWindowSourcePreparesTheNextCopy(t *testing.T) {
	src, sess, _ := newWindowHarness(t)
	t.Cleanup(src.Close)
	a, err := src.AllocDmabuf()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	b, err := src.AllocDmabuf()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Release()
	src.PrepareDmabuf(a)
	src.PrepareDmabuf(b)
	sess.land(t, src, nil)
	select {
	case <-sess.started:
		t.Fatal("a second copy started while one ran")
	default:
	}
	if _, ok := src.FillDmabuf(a); !ok {
		t.Fatal("the prepared frame did not go out")
	}
}

// A copy waiting on a still window takes its frame from a new session
// after the refresh, so the stream keeps sending what the window shows.
func TestWindowSourceRefreshesAStillWindow(t *testing.T) {
	src, sess, _ := newWindowHarness(t)
	t.Cleanup(src.Close)
	src.refresh = 10 * time.Millisecond
	a, err := src.AllocDmabuf()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	src.FillDmabuf(a)
	<-sess.started
	select {
	case <-sess.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the copy was not restarted for the refresh")
	}
	src.mu.Lock()
	src.refresh = time.Hour
	src.mu.Unlock()
	sess.mu.Lock()
	opened, closes := sess.opened, sess.closes
	sess.mu.Unlock()
	if opened < 2 || closes < 1 {
		t.Fatalf("opened %d, closed %d, want a new session", opened, closes)
	}
	drained := false
	for !drained {
		select {
		case <-sess.started:
		default:
			drained = true
		}
	}
	sess.results <- nil
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		if _, ok := src.FillDmabuf(a); ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the refreshed frame did not go out")
		}
	}
}
