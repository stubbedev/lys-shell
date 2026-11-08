package idle

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/stubbedev/lys-desktop/service/logind"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// sleepWho is the inhibitor's owner as `systemd-inhibit --list` shows it.
const sleepWho = "lys-shell"

// LockBeforeSleep holds a logind delay inhibitor on sleep, and when the
// system is about to sleep locks the session and lets go of the inhibitor
// once the lock screen has set LockedHint, so the machine wakes locked. On
// wake it takes the inhibitor again. lockWait bounds how long it waits for
// the lock screen; logind's own InhibitDelayMaxSec bounds the whole delay.
// It returns once the inhibitor is held and the signal watched; stop ends
// it and lets go of the inhibitor.
func LockBeforeSleep(conn *dbus.Conn, session *logind.Client, lockWait time.Duration) (stop func(), err error) {
	opts := logind.PrepareForSleepMatch()
	ctx, cancel := context.WithTimeout(context.Background(), logind.CallTimeout)
	defer cancel()
	if err := conn.AddMatchSignalContext(ctx, opts...); err != nil {
		return nil, fmt.Errorf("idle: watch PrepareForSleep: %w", err)
	}
	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	inhibitor, err := logind.InhibitSleep(ctx, conn, sleepWho, "Lock the screen before sleep")
	if err != nil {
		conn.RemoveSignal(signals)
		_ = conn.RemoveMatchSignal(opts...)
		return nil, fmt.Errorf("idle: lock before sleep: %w", err)
	}
	life, end := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		inhibitor = followSleep(life, conn, session, signals, inhibitor, lockWait)
		if inhibitor != nil {
			_ = inhibitor.Close()
		}
	}()
	return func() {
		end()
		<-done
		conn.RemoveSignal(signals)
		_ = conn.RemoveMatchSignal(opts...)
	}, nil
}

func followSleep(ctx context.Context, conn *dbus.Conn, session *logind.Client, signals <-chan *dbus.Signal, inhibitor *os.File, lockWait time.Duration) *os.File {
	for {
		select {
		case <-ctx.Done():
			return inhibitor
		case sig, ok := <-signals:
			if !ok {
				return inhibitor
			}
			s, err := logind.PrepareForSleep.Decode(sig)
			if err != nil {
				continue
			}
			if s.Start {
				lockForSleep(ctx, session, lockWait)
				if inhibitor != nil {
					_ = inhibitor.Close()
					inhibitor = nil
				}
				continue
			}
			if inhibitor == nil {
				inhibitor = retake(ctx, conn)
			}
		}
	}
}

func lockForSleep(ctx context.Context, session *logind.Client, lockWait time.Duration) {
	locking, cancel := context.WithTimeout(ctx, lockWait)
	defer cancel()
	if err := session.Lock(locking); err != nil {
		log.Printf("idle: lock before sleep: %v", err)
		return
	}
	if err := session.WaitLocked(locking); err != nil {
		log.Printf("idle: lock before sleep: the lock screen never confirmed: %v", err)
	}
}

func retake(ctx context.Context, conn *dbus.Conn) *os.File {
	taking, cancel := context.WithTimeout(ctx, logind.CallTimeout)
	defer cancel()
	inhibitor, err := logind.InhibitSleep(taking, conn, sleepWho, "Lock the screen before sleep")
	if err != nil {
		log.Printf("idle: lock before sleep: the next sleep will not wait for the lock: %v", err)
		return nil
	}
	return inhibitor
}
