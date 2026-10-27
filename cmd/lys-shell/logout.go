package main

import (
	"context"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/cli"
	"github.com/stubbedev/lys-shell/service/logind"
	"github.com/stubbedev/lys-shell/service/lyswm"
)

// logoutTimeout bounds the logind round trips of a logout.
const logoutTimeout = 10 * time.Second

// logoutCommand ends the user's session: lys-wm's exit action while
// lys-wm runs, else logind (logind.Logout).
func logoutCommand() *cli.Command {
	return &cli.Command{
		Name:  "logout",
		About: "Log out of the session through lys-wm or logind",
		Run: func(*cli.Matches) error {
			if lyswm.IsRunning() {
				if err := lyswm.Dispatch("exit"); err != nil {
					return fmt.Errorf("log out: %w", err)
				}
				return nil
			}
			conn, err := dbus.ConnectSystemBus()
			if err != nil {
				return fmt.Errorf("log out: connect to the system bus: %w", err)
			}
			defer func() { _ = conn.Close() }()
			ctx, cancel := context.WithTimeout(context.Background(), logoutTimeout)
			defer cancel()
			return logind.Logout(ctx, conn)
		},
	}
}
