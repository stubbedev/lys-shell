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
