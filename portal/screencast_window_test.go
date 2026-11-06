package portal

import (
	"errors"
	"image"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/dmabufprobe"
	"github.com/stubbedev/lys-lib/fourcc"
	"github.com/stubbedev/lys-lib/pipewire"
	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/service/lyswm/lyswmtest"
	"github.com/stubbedev/lys-shell/shell/regionoverlay"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
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

// An ended copy wakes the producer, whatever its outcome, for the next fill
// to send it or start another: the producer does not poll this source.
func TestWindowSourceWakesTheProducerWhenACopyEnds(t *testing.T) {
	src, sess, _ := newWindowHarness(t)
	t.Cleanup(src.Close)
	woken := make(chan struct{}, 4)
	src.NotifyFrames(func() { woken <- struct{}{} })
	a, err := src.AllocDmabuf()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := src.FillDmabuf(a); ok {
		t.Fatal("a buffer went out before a copy into it ran")
	}
	select {
	case <-woken:
		t.Fatal("woken before the copy ended")
	default:
	}
	for _, outcome := range []error{nil, errors.New("copy refused")} {
		sess.land(t, src, outcome)
		select {
		case <-woken:
		case <-time.After(5 * time.Second):
			t.Fatalf("no wake after a copy ended with %v", outcome)
		}
		src.FillDmabuf(a)
		src.FillDmabuf(a)
	}
	sess.land(t, src, nil)
	<-woken
	a.Release()
}

// windowClick is what the user does in the click-to-pick overlay: a
// click at a global point on an output, Enter, or a reply of its own.
type windowClick struct {
	output string
	at     image.Point
	enter  bool
	reply  string
}

// PickWindow answers as the shell's overlay does: a click picks the
// front window there, Enter the offered screen, no click is Escape.
func (f *fakeSharePicker) PickWindow(c portaldialogs.Caller, r sharepicker.WindowRequest) (string, *dbus.Error) {
	f.windowAsked.Update(func(a []sharepicker.WindowRequest) []sharepicker.WindowRequest { return append(a, r) })
	if shell := f.shell.Load(); shell != nil {
		return shell.PickWindow(c, r)
	}
	if f.enter(c) {
		return "", nil
	}
	click := f.click.Load()
	switch {
	case click == nil:
		return "", nil
	case click.reply != "":
		return click.reply, nil
	case click.enter:
		return "/screen:" + r.Screen, nil
	}
	w, ok := regionoverlay.WindowAt(r.Windows, click.output, click.at)
	if !ok {
		return "", nil
	}
	return "/window:" + w.ID, nil
}

// stackedWindows has lys-wm show a full-screen-sized window 3 behind a
// smaller 7 on DP-1, a window 9 no output shows over both, and 5 alone
// on DP-2.
const stackedWindows = `[{"id":9,"output":"DP-1","rect":{"x":40,"y":40,"width":100,"height":100}},` +
	`{"id":3,"output":"DP-1","rect":{"x":0,"y":0,"width":1280,"height":720},"stack":1},` +
	`{"id":7,"output":"DP-1","rect":{"x":100,"y":100,"width":400,"height":300},"stack":0},` +
	`{"id":5,"output":"DP-2","rect":{"x":1280,"y":0,"width":1920,"height":1080},"stack":0}]`

// windowRig is a cast rig on a lys-wm showing stackedWindows, with
// DP-2 focused, and the share picker replying grid.
func windowRig(t *testing.T, grid string) (*rig, *fakeStarts, *fakeSharePicker) {
	t.Helper()
	r, f := castRig(t)
	wm := lyswmtest.Start(t)
	wm.Result("outputs", twoOutputsDP2Focused)
	wm.Result("windows", stackedWindows)
	return r, f, serveSharePicker(t, r, grid)
}

func windowIDs(r sharepicker.WindowRequest) []string {
	ids := make([]string, len(r.Windows))
	for i, w := range r.Windows {
		ids[i] = w.ID
	}
	return ids
}

func TestAWindowRequestOnLysWMSharesTheFrontWindowAClickPicks(t *testing.T) {
	r, f, p := windowRig(t, "/window:lys-wm-3")
	p.click.Store(&windowClick{output: "DP-1", at: image.Pt(200, 200)})
	r.castSession(t, Vardict{"types": dbus.MakeVariant(sourceWindow), "persist_mode": dbus.MakeVariant(uint32(2))})
	code, res := r.castStart(t)
	if code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	if got := f.started.Load(); !slices.Equal(got, []string{"window:lys-wm-7"}) {
		t.Errorf("started %v, want the front window 7 under the click", got)
	}
	asked := p.windowAsked.Load()
	if len(asked) != 1 {
		t.Fatalf("the overlay was asked %d times, want once", len(asked))
	}
	if got, want := windowIDs(asked[0]), []string{"lys-wm-7", "lys-wm-5", "lys-wm-3"}; !slices.Equal(got, want) {
		t.Errorf("the overlay was offered %v, want the shown windows front to back %v", got, want)
	}
	if got, want := asked[0].Windows[0], (regionoverlay.Window{ID: "lys-wm-7", Output: "DP-1", X: 100, Y: 100, Width: 400, Height: 300}); got != want {
		t.Errorf("window 7 offered as %+v, want %+v", got, want)
	}
	if asked[0].Screen != "" {
		t.Errorf("a window request offered the screen %q", asked[0].Screen)
	}
	if len(p.asked.Load()) != 0 {
		t.Error("the picker asked after the click")
	}
	if _, ok := res["restore_data"]; ok {
		t.Error("a click granted a restore token")
	}
}

func TestAClickBesideTheFrontWindowSharesTheOneBehindNotAHiddenOne(t *testing.T) {
	r, f, p := windowRig(t, "")
	p.click.Store(&windowClick{output: "DP-1", at: image.Pt(50, 50)})
	r.castSession(t, Vardict{"types": dbus.MakeVariant(sourceWindow)})
	if code, _ := r.castStart(t); code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	if got := f.started.Load(); !slices.Equal(got, []string{"window:lys-wm-3"}) {
		t.Errorf("started %v, want window 3, not the unshown 9 over it", got)
	}
}

// Escape in the click-to-pick fails the request with the code a
// cancelled picker's does, and does not go on to the picker, which
// would share what it replies.
func TestEscapeInTheClickToPickCancelsTheRequestAsACancelledPickerDoes(t *testing.T) {
	cancelled, _ := castRig(t)
	serveSharePicker(t, cancelled, "")
	cancelled.castSession(t, Vardict{"types": dbus.MakeVariant(sourceWindow)})
	want, _ := cancelled.castStart(t)
	if want != ResponseCancelled {
		t.Fatalf("a cancelled picker's Start = %d, want cancelled", want)
	}

	r, f, p := windowRig(t, "/window:lys-wm-5")
	r.castSession(t, Vardict{"types": dbus.MakeVariant(sourceWindow)})
	if code, _ := r.castStart(t); code != want {
		t.Errorf("Start = %d after Escape, want %d as a cancelled picker's", code, want)
	}
	if len(p.windowAsked.Load()) != 1 {
		t.Errorf("the overlay was asked %d times, want once", len(p.windowAsked.Load()))
	}
	if len(p.asked.Load()) != 0 {
		t.Errorf("the picker was asked %d times after Escape, want never", len(p.asked.Load()))
	}
	if got := f.started.Load(); len(got) != 0 {
		t.Errorf("started %v after Escape, want nothing", got)
	}
}

// A click-to-pick that cannot open, because another capture holds the
// shell's gate, is not the user declining: the picker asks, and what
// it picks is shared.
func TestAClickToPickThatCannotOpenAsksThePicker(t *testing.T) {
	r, f, p := windowRig(t, "/window:lys-wm-5")
	gate := &regionoverlay.Gate{}
	end, err := gate.Instant()
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	a := &app.Application{}
	p.shell.Store(sharepicker.NewDaemon(sharepicker.New(a, sharepicker.Deps{}, gate, nil, regionoverlay.NewWindowPick(a, gate, nil, nil, nil))))
	r.castSession(t, Vardict{"types": dbus.MakeVariant(sourceWindow)})
	if code, _ := r.castStart(t); code != ResponseSuccess {
		t.Fatalf("Start = %d, want the picker's pick shared", code)
	}
	if len(p.windowAsked.Load()) != 1 || len(p.asked.Load()) != 1 {
		t.Errorf("the overlay was asked %d times and the picker %d, want once each", len(p.windowAsked.Load()), len(p.asked.Load()))
	}
	if got := f.started.Load(); !slices.Equal(got, []string{"window:lys-wm-5"}) {
		t.Errorf("started %v, want the window the picker picked", got)
	}
}

func TestAWindowOrMonitorRequestSharesTheFocusedOutputOnEnter(t *testing.T) {
	r, f, p := windowRig(t, "")
	p.click.Store(&windowClick{enter: true})
	r.castSession(t, Vardict{"types": dbus.MakeVariant(sourceMonitor | sourceWindow)})
	if code, _ := r.castStart(t); code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	if asked := p.windowAsked.Load(); len(asked) != 1 || asked[0].Screen != "DP-2" {
		t.Fatalf("the overlay was asked %+v, want the focused DP-2 offered", asked)
	}
	if got := f.started.Load(); !slices.Equal(got, []string{"screen:DP-2"}) {
		t.Errorf("started %v, want the focused output", got)
	}
}

func TestAClickToPickReplyNotOfferedFailsTheRequest(t *testing.T) {
	for _, reply := range []string{"/window:lys-wm-9", "/screen:DP-2", "/window:lys-wm-7;window:lys-wm-3", "garbage"} {
		t.Run(reply, func(t *testing.T) {
			r, f, p := windowRig(t, "/window:lys-wm-3")
			p.click.Store(&windowClick{reply: reply})
			r.castSession(t, Vardict{"types": dbus.MakeVariant(sourceWindow)})
			if code, _ := r.castStart(t); code != ResponseOther {
				t.Errorf("Start = %d, want other", code)
			}
			if got := f.started.Load(); len(got) != 0 || len(p.asked.Load()) != 0 {
				t.Errorf("started %v after %d picker calls, want nothing", got, len(p.asked.Load()))
			}
		})
	}
}

func TestWindowRequestsTheClickToPickCannotServeAskThePicker(t *testing.T) {
	for name, tc := range map[string]struct {
		lysWM   bool
		windows string
		options Vardict
	}{
		"without lys-wm":     {options: Vardict{"types": dbus.MakeVariant(sourceWindow)}},
		"with no window":     {lysWM: true, windows: `[{"id":9,"output":"DP-1","rect":{"x":0,"y":0,"width":9,"height":9}}]`, options: Vardict{"types": dbus.MakeVariant(sourceWindow)}},
		"for several":        {lysWM: true, windows: stackedWindows, options: Vardict{"types": dbus.MakeVariant(sourceWindow), "multiple": dbus.MakeVariant(true)}},
		"for a monitor only": {lysWM: true, windows: stackedWindows, options: Vardict{"multiple": dbus.MakeVariant(true)}},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := castRig(t)
			if tc.lysWM {
				wm := lyswmtest.Start(t)
				wm.Result("outputs", twoOutputsDP2Focused)
				wm.Result("windows", tc.windows)
			}
			p := serveSharePicker(t, r, "/window:lys-wm-3")
			p.click.Store(&windowClick{output: "DP-1", at: image.Pt(200, 200)})
			r.castSession(t, tc.options)
			if code, _ := r.castStart(t); code != ResponseSuccess && code != ResponseOther {
				t.Fatalf("Start = %d", code)
			}
			if asked := p.windowAsked.Load(); len(asked) != 0 {
				t.Errorf("the overlay was asked %+v", asked)
			}
			if len(p.asked.Load()) != 1 {
				t.Errorf("the picker was asked %d times, want once", len(p.asked.Load()))
			}
		})
	}
}

func TestClosingTheRequestDuringTheClickToPickCancelsIt(t *testing.T) {
	r, f, p := windowRig(t, "/window:lys-wm-3")
	p.hold.Store(true)
	r.castSession(t, Vardict{"types": dbus.MakeVariant(sourceWindow)})
	done := make(chan uint32, 1)
	go func() {
		code, _ := r.castStart(t)
		done <- code
	}()
	p.waitOpen(t, handle)
	if err := r.client.Object(BusName, handle).Call(RequestIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != ResponseCancelled {
			t.Errorf("Start = %d after Close, want cancelled", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not end the click-to-pick")
	}
	if got := p.dismissed.Load(); !slices.Equal(got, []string{string(handle)}) {
		t.Errorf("dismissed %v, want the request's handle", got)
	}
	if len(p.asked.Load()) != 0 || len(f.started.Load()) != 0 {
		t.Error("a closed request went on to the picker or a stream")
	}
}
