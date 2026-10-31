// Package lockcli is the lock command both `lys-shell lock` and the
// single-token lys-lock run: one declaration, so the two cannot drift.
package lockcli

import (
	"context"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/cli"
	"github.com/stubbedev/lys-shell/service/shellipc"
)

// lockTimeout bounds the wait for the running shell's lock screen.
const lockTimeout = 25 * time.Second

// Command is app.rs's Lock under name: lock the session through the
// running shell's lock screen (lys-shell/src/cli/lock.rs). The session
// bus is dialed only when it runs, so -h and --help never touch it.
func Command(name string) *cli.Command {
	return &cli.Command{
		Name:  name,
		About: "Lock the session via lys-shell's lock screen",
		Run: func(m *cli.Matches) error {
			conn, err := dbus.ConnectSessionBus()
			if conn != nil {
				defer func() { _ = conn.Close() }()
			}
			ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
			defer cancel()
			return shellipc.LockCommand(ctx, conn, err, m.Stdout())
		},
	}
}
