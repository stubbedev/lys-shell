package main

import (
	"context"
	"fmt"

	"github.com/stubbedev/lys-desktop/service/lyswm"
	"github.com/stubbedev/lys-lib/logind"

	"github.com/stubbedev/lys-shell/internal/cli"
)

// logoutCommand ends the user's session: lys-wm's exit action while
// lys-wm runs, else logind's Terminate of the session "auto" resolves to.
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
			return logout(context.Background())
		},
	}
}

func logout(ctx context.Context) error {
	conn, err := logind.Dial(ctx)
	if err != nil {
		return fmt.Errorf("log out: %w", err)
	}
	defer func() { _ = conn.Close() }()
	session, err := logind.NewManager(conn).GetSession(ctx, logind.AutoSession)
	if err != nil {
		return fmt.Errorf("log out: %w", err)
	}
	if err := session.Terminate(ctx); err != nil {
		return fmt.Errorf("log out: %w", err)
	}
	return nil
}
