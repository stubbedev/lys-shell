package lock

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/stubbedev/lys-ui/app"
	"golang.org/x/sys/unix"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/imagedecode"
	"github.com/stubbedev/lys-shell/logging"
	"github.com/stubbedev/lys-shell/outputsize"
	"github.com/stubbedev/lys-shell/service/auth"
	"github.com/stubbedev/lys-shell/service/logind"
	"github.com/stubbedev/lys-shell/shell/apptheme"
	"github.com/stubbedev/lys-shell/shell/lockclient"
)

// Errors Run ends with, which `lys-login lock` maps to lockclient's exit
// codes.
var (
	ErrHeld    = errors.New("lock: another lys-login holds the session locked")
	ErrRefused = errors.New("lock: the session was not locked")
)

// Run is `lys-login lock`: it locks the session with the login screen in
// its lock mode and returns once it unlocks, by password or by logind's
// Unlock. It writes lockclient.LockedLine to stdout when the compositor confirms
// the lock. One lys-login per session holds the lock: a second Run
// returns ErrHeld.
func Run(cfg *config.Config, stdout io.Writer) error {
	release, err := holdSession(os.Getenv("XDG_RUNTIME_DIR"))
	if err != nil {
		return err
	}
	defer release()
	app.SetLogger(logging.Library())
	sess, err := app.Connect()
	if err != nil {
		return fmt.Errorf("lock: connect: %w", err)
	}
	defer sess.Close()
	a := app.NewApplication(sess)
	defer apptheme.Setup(a)()
	base, err := app.Font(cfg.General.FontSans, 16)
	if err != nil {
		return fmt.Errorf("lock: font %q: %w", cfg.General.FontSans, err)
	}
	ld := logind.SystemClient("lock")
	deps := Deps{
		Locker: appLocker{a: a},
		Loop:   a,
		Now:    time.Now,
		After: func(d time.Duration, fn func()) func() {
			t := time.AfterFunc(d, func() { a.Invoke(fn) })
			return func() { t.Stop() }
		},
		Auth: func(service string, onEvent func(auth.Event)) authHandle {
			return auth.Spawn(auth.NewPAM(service), auth.CurrentUsername(), onEvent)
		},
		Decode:   imagedecode.OpenOffHeap,
		Outputs:  func() []image.Point { return outputsize.Devices(sess.Outputs()) },
		Forget:   a.ForgetImage,
		OnLocked: func() { _, _ = fmt.Fprintln(stdout, lockclient.LockedLine) },
		OnEnded:  a.Quit,
		CapsLock: func() bool { return sess.Mods()&app.ModCapsLock != 0 },
	}
	if ld != nil {
		deps.SetLockedHint = func(locked bool) {
			if err := ld.SetLockedHint(locked); err != nil {
				log.Printf("lock: SetLockedHint failed (non-fatal): %v", err)
			}
		}
	}
	s := New(cfg, app.FontFallback(base), apptheme.New(cfg).Sheet, deps)
	if ld != nil {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			if err := ld.Listen(ctx, func() {}, func() { a.Invoke(s.ForceUnlock) }); err != nil {
				log.Printf("lock: logind Unlock unavailable: %v", err)
			}
		}()
	}
	// The lock is requested before the loop runs: Run returns at once
	// while there is neither a window nor a lock to drive.
	if err := s.Lock(); err != nil {
		return fmt.Errorf("%w: %w", ErrRefused, err)
	}
	if err := a.Run(); err != nil && !errors.Is(err, app.ErrClosed) {
		return fmt.Errorf("lock: %w", err)
	}
	if !s.unlocked {
		return ErrRefused
	}
	return nil
}

// holdSession takes the session's lys-login lock file, held until the
// returned release; another holder is ErrHeld. Without a runtime dir
// there is no session to share it with, and nothing is held.
func holdSession(runtimeDir string) (release func(), err error) {
	if runtimeDir == "" {
		return func() {}, nil
	}
	dir := filepath.Join(runtimeDir, "lys-login")
	if err := os.MkdirAll(dir, 0o700); err != nil { //nolint:gosec // our own dir under XDG_RUNTIME_DIR
		return nil, fmt.Errorf("lock: create %s: %w", dir, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "lock.lock"), os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // our own runtime file
	if err != nil {
		return nil, fmt.Errorf("lock: open the session lock file: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrHeld
		}
		return nil, fmt.Errorf("lock: take the session lock file: %w", err)
	}
	return func() { _ = f.Close() }, nil
}
