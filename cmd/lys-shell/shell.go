package main

import (
	"fmt"
	"io"
	"os"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/cli"
	"github.com/stubbedev/lys-shell/internal/logging"
	"github.com/stubbedev/lys-shell/service/shellipc"
	"github.com/stubbedev/lys-shell/shell/bar"
)

// shellCommand is app.rs's Shell: the desktop shell in the foreground.
func shellCommand() *cli.Command {
	return &cli.Command{
		Name:  "shell",
		About: "Run the desktop shell in the foreground",
		Run: func(*cli.Matches) error {
			// lys-shell's tracing_init: its own daily file, echoed to
			// stdout.
			closer, err := logging.Setup(shellLogPrefix, os.Stdout)
			if err != nil {
				return err
			}
			defer func() { _ = closer.Close() }()
			return runShell(os.Stderr, bar.Run)
		},
	}
}

// runShell is wayle_shell::run's single-instance guard: a second shell
// says so and exits successfully instead of fighting over the bus names.
func runShell(stderr io.Writer, start func() error) error {
	if conn, err := dbus.ConnectSessionBus(); err == nil {
		running, _ := shellipc.IsRunning(conn)
		_ = conn.Close()
		if running {
			fmt.Fprintln(stderr, "lys-shell shell is already running")
			return nil
		}
	}
	return start()
}
