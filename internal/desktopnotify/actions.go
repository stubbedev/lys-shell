package desktopnotify

import (
	"context"
	"fmt"
	"time"

	"github.com/stubbedev/lys-lib/dbusx"
	"github.com/stubbedev/lys-lib/intconv"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/notifications"
)

// Action is one notification button: Key is what Ask reports, Label
// what the daemon shows.
type Action struct {
	Key   string
	Label string
}

// outcome is what the daemon said of one notification: the key of the
// button pressed, or that it closed.
type outcome struct {
	id      uint32
	key     string
	pressed bool
	said    dbus.Sequence
}

// Ask posts a notification with buttons and waits for the user
// (notify.rs's notify_with_actions + NotifyReceipt::action): the key of
// the button pressed, or ok=false when the notification closed or
// expired without one, or ctx ended first. timeout is the daemon's
// expire timeout. The icon also goes in the image-path hint, as
// libnotify does, which lys-shell's own popup reads. Only what the
// daemon says after it answered Notify is about this notification: an
// id it used before may be the one it hands out again.
func (s *Sender) Ask(ctx context.Context, appName, summary, body, appIcon string, actions []Action, timeout time.Duration) (key string, ok bool, err error) {
	watching, stopWatching := context.WithCancel(ctx)
	defer stopWatching()
	outcomes, unreadable := make(chan outcome, 16), make(chan error, 1)
	tell := func(o outcome) {
		select {
		case outcomes <- o:
		case <-watching.Done():
		}
	}
	// Watching before sending, so the answer cannot race the id.
	watch, err := dbusx.WatchSignals(watching, s.conn, dbusx.FromOwner(busName),
		func(err error) {
			select {
			case unreadable <- err:
			default:
			}
		},
		dbusx.On(notifications.ActionInvoked, path, func(invoked notifications.ActionInvocation, said dbus.Sequence) error {
			tell(outcome{id: invoked.ID, key: invoked.Key, pressed: true, said: said})
			return nil
		}),
		dbusx.On(notifications.NotificationClosed, path, func(closure notifications.Closure, said dbus.Sequence) error {
			tell(outcome{id: closure.ID, said: said})
			return nil
		}))
	if err != nil {
		return "", false, fmt.Errorf("desktopnotify: %w", err)
	}
	defer func() {
		stopWatching()
		_ = watch.Stop()
	}()

	flat := make([]string, 0, 2*len(actions))
	for _, a := range actions {
		flat = append(flat, a.Key, a.Label)
	}
	hints := iconHints(appIcon)
	var id uint32
	call := s.conn.Object(busName, path).CallWithContext(ctx, method, 0,
		appName, uint32(0), appIcon, summary, body, flat, hints, intconv.Clamp[int32](timeout.Milliseconds()))
	if call.Err != nil {
		return "", false, fmt.Errorf("desktopnotify: Notify: %w", call.Err)
	}
	if err := call.Store(&id); err != nil {
		return "", false, fmt.Errorf("desktopnotify: Notify reply: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return "", false, nil
		case <-watch.Done():
			return "", false, nil
		case err := <-unreadable:
			return "", false, fmt.Errorf("desktopnotify: %w", err)
		case o := <-outcomes:
			if o.id != id || o.said < call.ResponseSequence {
				continue
			}
			return o.key, o.pressed, nil
		}
	}
}
