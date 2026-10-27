package lock

import (
	"errors"
	"log"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
)

// appLocker adapts lys-ui's session lock to the screen.
type appLocker struct{ a *app.Application }

func (l appLocker) Available() bool { return l.a.SessionLockAvailable() }
func (l appLocker) Active() bool    { return l.a.SessionLock() != nil }

func (l appLocker) Lock(cfg app.SessionLockConfig) error {
	_, err := l.a.LockSession(cfg)
	return err
}

func (l appLocker) Release() {
	sl := l.a.SessionLock()
	if sl == nil {
		return
	}
	if err := sl.Unlock(); errors.Is(err, app.ErrNotLocked) {
		// Still pending: the compositor never confirmed it, withdraw.
		_ = sl.Cancel()
	}
}

func (l appLocker) Focus(w widget.Widget) {
	sl := l.a.SessionLock()
	if sl == nil {
		return
	}
	if err := sl.SetFocus(w); err != nil {
		log.Printf("lock: cannot focus %T: %v", w, err)
	}
}
