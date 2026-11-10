package idle

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/logind"
)

// sleepWho is the inhibitor's owner as `systemd-inhibit --list` shows it.
const sleepWho = "lys-shell"

const sleepWhy = "Lock the screen before sleep"

// LockBeforeSleep holds a logind delay inhibitor on sleep, and when the
// system is about to sleep locks the session and lets go of the inhibitor
// once the lock screen has set LockedHint, so the machine wakes locked. On
// wake it takes the inhibitor again. lockWait bounds how long it waits for
// the lock screen; logind's own InhibitDelayMaxSec bounds the whole delay.
// It returns once the signal is watched and the inhibitor held; stop ends
// it and lets go of the inhibitor.
func LockBeforeSleep(login Login, lockWait time.Duration) (stop func(), err error) {
	l := &sleepLock{login: login, lockWait: lockWait}
	life, end := context.WithCancel(context.Background())
	w, err := login.Session.Watch(life, func(e logind.Event, err error) {
		if err != nil {
			log.Printf("idle: lock before sleep: %v", err)
			return
		}
		if s, ok := e.(logind.Sleep); ok {
			l.follow(life, s)
		}
	})
	if err != nil {
		end()
		return nil, fmt.Errorf("idle: watch PrepareForSleep: %w", err)
	}
	inhibitor, err := inhibitSleep(life, login.Manager)
	if err != nil {
		end()
		return nil, fmt.Errorf("idle: lock before sleep: %w", errors.Join(err, w.Stop()))
	}
	l.hold(inhibitor)
	return func() {
		end()
		if err := w.Stop(); err != nil {
			log.Printf("idle: lock before sleep: %v", err)
		}
		l.hold(nil)
	}, nil
}

type sleepLock struct {
	login    Login
	lockWait time.Duration

	mu        sync.Mutex
	inhibitor *os.File
}

func (l *sleepLock) hold(inhibitor *os.File) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inhibitor != nil {
		_ = l.inhibitor.Close()
	}
	l.inhibitor = inhibitor
}

func (l *sleepLock) holding() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inhibitor != nil
}

func (l *sleepLock) follow(ctx context.Context, s logind.Sleep) {
	if s.Start {
		l.lockForSleep(ctx)
		l.hold(nil)
		return
	}
	if l.holding() {
		return
	}
	inhibitor, err := inhibitSleep(ctx, l.login.Manager)
	if err != nil {
		log.Printf("idle: lock before sleep: the next sleep will not wait for the lock: %v", err)
		return
	}
	l.hold(inhibitor)
}

func (l *sleepLock) lockForSleep(ctx context.Context) {
	locking, cancel := context.WithTimeout(ctx, l.lockWait)
	defer cancel()
	if err := l.login.Session.Lock(locking); err != nil {
		log.Printf("idle: lock before sleep: %v", err)
		return
	}
	if err := l.login.Session.WaitLocked(locking); err != nil {
		log.Printf("idle: lock before sleep: the lock screen never confirmed: %v", err)
	}
}

func inhibitSleep(ctx context.Context, m logind.Manager) (*os.File, error) {
	return m.Inhibit(ctx, logind.InhibitSleep, sleepWho, sleepWhy, logind.InhibitDelay)
}
