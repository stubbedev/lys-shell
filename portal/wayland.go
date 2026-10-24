package portal

import (
	"errors"
	"slices"
	"sync"

	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/internal/logging"
)

// waylandLoop is the portal's one Wayland connection: a headless lys-ui
// loop, started on first use by the interfaces that need the
// compositor (Clipboard's data-control device, GlobalShortcuts). It
// maps no surface; Hold keeps it running without one. When it stops
// (the compositor went away) the interfaces built on it hear so, and
// the next use connects afresh.
type waylandLoop struct {
	mu sync.Mutex
	a  *app.Application
	// sess lists the outputs (the loop's session); read on the loop.
	sess interface{ Outputs() []*app.Output }
	// ended closes when the loop stops: nothing runs queued work after
	// that.
	ended chan struct{}
	// endFns run each time a loop stops.
	endFns []func()
}

// onEnd registers fn to run whenever the loop stops.
func (w *waylandLoop) onEnd(fn func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.endFns = append(w.endFns, fn)
}

// do runs fn on the loop goroutine, starting the loop first, and waits
// for it.
func (w *waylandLoop) do(fn func(*app.Application) error) error {
	a, ended, err := w.start()
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	a.Invoke(func() { done <- fn(a) })
	select {
	case err := <-done:
		return err
	case <-ended:
		return errors.New("the wayland connection is gone")
	}
}

func (w *waylandLoop) start() (*app.Application, chan struct{}, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.a != nil {
		return w.a, w.ended, nil
	}
	app.SetLogger(logging.Library())
	sess, err := app.Connect()
	if err != nil {
		return nil, nil, err
	}
	a := app.NewApplication(sess)
	a.Hold()
	ended := make(chan struct{})
	go func() {
		if err := a.Run(); err != nil && !errors.Is(err, app.ErrClosed) {
			warnf("wayland loop ended: %v", err)
		}
		w.mu.Lock()
		w.a, w.sess = nil, nil
		fns := slices.Clone(w.endFns)
		w.mu.Unlock()
		close(ended)
		for _, fn := range fns {
			fn()
		}
	}()
	w.a, w.sess, w.ended = a, sess, ended
	return a, ended, nil
}

// outputGeometry is an output's place in the compositor's logical
// space: xdg-output's position and size.
type outputGeometry struct {
	name                string
	x, y, width, height int32
}

// outputs is every output's logical geometry, read on the loop.
func (w *waylandLoop) outputs() ([]outputGeometry, error) {
	var out []outputGeometry
	err := w.do(func(*app.Application) error {
		w.mu.Lock()
		sess := w.sess
		w.mu.Unlock()
		for _, o := range sess.Outputs() {
			out = append(out, outputGeometry{name: o.Name, x: o.LogicalX, y: o.LogicalY, width: o.LogicalW, height: o.LogicalH})
		}
		return nil
	})
	return out, err
}
