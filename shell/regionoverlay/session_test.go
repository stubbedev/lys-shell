package regionoverlay

import (
	"context"
	"errors"
	"testing"

	"github.com/stubbedev/lys-lib/render"

	"github.com/stubbedev/lys-shell/internal/apptest"
)

// onLoop stands in for the application's Invoke.
func onLoop(fn func()) { fn() }

// session is an OpenSession left open until its reply is answered or its
// context ends.
type session struct {
	reply  chan chan int
	answer chan sessionAnswer
}

type sessionAnswer struct {
	n   int
	err error
}

func openSession(ctx context.Context, g *Gate, frames Frames) *session {
	s := &session{reply: make(chan chan int, 1), answer: make(chan sessionAnswer, 1)}
	go func() {
		n, err := OpenSession(ctx, g, onLoop, frames,
			func(_ map[string]render.StillImage, reply chan int) { s.reply <- reply },
			func(reply chan int) { reply <- -1 })
		s.answer <- sessionAnswer{n, err}
	}()
	return s
}

func refuse(t *testing.T, g *Gate) {
	t.Helper()
	captured, shown := 0, 0
	_, err := OpenSession(context.Background(), g, onLoop,
		func() (map[string]render.StillImage, error) { captured++; return nil, nil },
		func(_ map[string]render.StillImage, reply chan int) { shown++; reply <- 0 },
		func(chan int) { t.Error("a refused request cancelled") })
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("the request answered %v, want ErrBusy", err)
	}
	if captured != 0 || shown != 0 {
		t.Fatalf("the refused request captured %d times and showed %d times", captured, shown)
	}
}

func TestSessionAdmitsOneAtATime(t *testing.T) {
	g := &Gate{}
	s := openSession(context.Background(), g, nil)
	reply := apptest.Answer(t, "the session opened", s.reply)
	refuse(t, g)
	if len(s.answer) != 0 {
		t.Fatal("a refused request answered the open session")
	}
	reply <- 7
	if a := apptest.Answer(t, "the session answered", s.answer); a.n != 7 || a.err != nil {
		t.Fatalf("the session answered %+v", a)
	}
	next := openSession(context.Background(), g, nil)
	apptest.Answer(t, "the next session opened", next.reply) <- 8
	if a := apptest.Answer(t, "the next session answered", next.answer); a.n != 8 || a.err != nil {
		t.Fatalf("the session after the first answered %+v", a)
	}
}

// Ending a session's context answers through cancel with that session's own
// reply, and frees the gate.
func TestSessionContextCancelsItsOwnReply(t *testing.T) {
	g := &Gate{}
	ctx, cancel := context.WithCancel(context.Background())
	s := openSession(ctx, g, nil)
	apptest.Answer(t, "the session opened", s.reply)
	cancel()
	if a := apptest.Answer(t, "the session answered", s.answer); a.n != -1 || a.err != nil {
		t.Fatalf("the cancelled session answered %+v", a)
	}
	end, err := g.Instant()
	if err != nil {
		t.Fatalf("the gate stayed closed: %v", err)
	}
	end()
}

// A context ended while the frames were captured shows nothing: the session
// answers the zero value, and the gate is free.
func TestSessionEndedDuringTheCaptureShowsNothing(t *testing.T) {
	g := &Gate{}
	ctx, cancel := context.WithCancel(context.Background())
	n, err := OpenSession(ctx, g, onLoop,
		func() (map[string]render.StillImage, error) { cancel(); return nil, nil },
		func(map[string]render.StillImage, chan int) { t.Error("a dismissed session showed") },
		func(chan int) { t.Error("a session never shown cancelled") })
	if n != 0 || err != nil {
		t.Fatalf("answered %d %v", n, err)
	}
	next := openSession(context.Background(), g, nil)
	apptest.Answer(t, "the next session opened", next.reply) <- 1
	apptest.Answer(t, "the next session answered", next.answer)
}

// A failed capture is that failure, not ErrBusy, and frees the gate.
func TestSessionCaptureFailure(t *testing.T) {
	g := &Gate{}
	failed := errors.New("no compositor")
	_, err := OpenSession(context.Background(), g, onLoop,
		func() (map[string]render.StillImage, error) { return nil, failed },
		func(map[string]render.StillImage, chan int) { t.Error("a failed capture showed") },
		func(chan int) {})
	if !errors.Is(err, failed) || errors.Is(err, ErrBusy) {
		t.Fatalf("answered %v", err)
	}
	end, err := g.Instant()
	if err != nil {
		t.Fatalf("the gate stayed closed: %v", err)
	}
	end()
}

// An instant capture is refused during a session, and refuses one while it
// runs; instant captures do not exclude each other.
func TestInstantAndSessionExcludeEachOther(t *testing.T) {
	g := &Gate{}
	s := openSession(context.Background(), g, nil)
	reply := apptest.Answer(t, "the session opened", s.reply)
	if _, err := g.Instant(); !errors.Is(err, ErrBusy) {
		t.Fatalf("an instant capture during a session answered %v", err)
	}
	reply <- 1
	apptest.Answer(t, "the session answered", s.answer)
	first, err := g.Instant()
	if err != nil {
		t.Fatal(err)
	}
	second, err := g.Instant()
	if err != nil {
		t.Fatalf("a second instant capture was refused: %v", err)
	}
	refuse(t, g)
	first()
	refuse(t, g)
	second()
	next := openSession(context.Background(), g, nil)
	apptest.Answer(t, "the next session opened", next.reply) <- 1
	apptest.Answer(t, "the next session answered", next.answer)
}

// A cancel for a request already over leaves the one open now alone.
func TestCancelIsForItsOwnRequest(t *testing.T) {
	o := &Overlay{}
	open, over := make(chan result, 1), make(chan result, 1)
	o.reply = open
	o.cancel(over)
	if o.reply != open || len(open) != 0 || len(over) != 0 {
		t.Fatal("a stale cancel answered the open request")
	}
	o.cancel(open)
	if r := <-open; r.ok {
		t.Fatalf("the cancel answered %+v", r)
	}
}

func TestBackgroundCaptureWaitsOutASession(t *testing.T) {
	g := &Gate{}
	s := openSession(context.Background(), g, nil)
	reply := apptest.Answer(t, "the session opened", s.reply)
	captured := make(chan int, 1)
	got := make(chan int, 1)
	go func() {
		n, err := BackgroundCapture(context.Background(), g, func() (int, error) { captured <- 1; return 1, nil })
		if err != nil {
			t.Errorf("the capture failed: %v", err)
		}
		got <- n
	}()
	apptest.Never(t, "a capture during the session", captured)
	reply <- 7
	apptest.Answer(t, "the session answered", s.answer)
	apptest.Answer(t, "the capture after the session", captured)
	if n := apptest.Answer(t, "the capture's answer", got); n != 1 {
		t.Fatalf("the capture answered %d", n)
	}
}

func TestBackgroundCaptureGivesWayToASession(t *testing.T) {
	g := &Gate{}
	running, release := make(chan int, 2), make(chan struct{})
	captures := 0
	got := make(chan int, 1)
	go func() {
		n, err := BackgroundCapture(context.Background(), g, func() (int, error) {
			captures++
			running <- captures
			if captures == 1 {
				<-release
			}
			return captures, nil
		})
		if err != nil {
			t.Errorf("the capture failed: %v", err)
		}
		got <- n
	}()
	apptest.Answer(t, "the first capture ran", running)
	s := openSession(context.Background(), g, nil)
	reply := apptest.Answer(t, "the session opened during the capture", s.reply)
	close(release)
	apptest.Never(t, "a second capture during the session", running)
	apptest.Never(t, "the capture's answer during the session", got)
	reply <- 1
	if a := apptest.Answer(t, "the session answered", s.answer); a.err != nil {
		t.Fatalf("the session answered %v", a.err)
	}
	apptest.Answer(t, "the second capture ran", running)
	if n := apptest.Answer(t, "the capture's answer", got); n != 2 {
		t.Fatalf("the capture answered capture %d, want the one after the session", n)
	}
}

func TestBackgroundCaptureEndsWithItsContext(t *testing.T) {
	g := &Gate{}
	s := openSession(context.Background(), g, nil)
	reply := apptest.Answer(t, "the session opened", s.reply)
	ctx, cancel := context.WithCancel(context.Background())
	failed := make(chan error, 1)
	go func() {
		_, err := BackgroundCapture(ctx, g, func() (int, error) {
			t.Error("a capture ran for an ended context")
			return 0, nil
		})
		failed <- err
	}()
	cancel()
	if err := apptest.Answer(t, "the ended capture", failed); !errors.Is(err, context.Canceled) {
		t.Fatalf("the capture answered %v, want its context's error", err)
	}
	reply <- 1
	apptest.Answer(t, "the session answered", s.answer)
}

func TestBackgroundCaptureFailure(t *testing.T) {
	failed := errors.New("no compositor")
	if _, err := BackgroundCapture(context.Background(), &Gate{}, func() (int, error) { return 0, failed }); !errors.Is(err, failed) {
		t.Fatalf("answered %v", err)
	}
}

func TestAskingWhetherASessionIsOpenRefusesNobody(t *testing.T) {
	g := &Gate{}
	if g.Open() {
		t.Fatal("a new gate reports an open session")
	}
	release := make(chan struct{})
	opened := make(chan error, 1)
	go func() {
		_, err := OpenSession(context.Background(), g, func(f func()) { f() }, nil, func(_ map[string]render.StillImage, reply chan struct{}) {
			opened <- nil
			go func() { <-release; reply <- struct{}{} }()
		}, func(chan struct{}) {})
		opened <- err
	}()
	if err := <-opened; err != nil {
		t.Fatal(err)
	}
	if !g.Open() {
		t.Error("an open session reads as closed")
	}
	close(release)
	if err := <-opened; err != nil {
		t.Fatal(err)
	}
	if g.Open() {
		t.Error("an ended session reads as open")
	}
}
