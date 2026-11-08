package main

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/logging"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/cli"
	"github.com/stubbedev/lys-shell/internal/version"
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
			return runShell(os.Stderr, refreshingSchema(bar.Run))
		},
	}
}

func refreshingSchema(start func(*dbus.Conn) error) func(*dbus.Conn) error {
	return func(session *dbus.Conn) error {
		go refreshSchema()
		return start(session)
	}
}

func refreshSchema() {
	dir, err := config.Dir()
	if err != nil {
		log.Printf("schema: cannot find the config dir: %v", err)
		return
	}
	if err := config.EnsureSchemaCurrent(dir, version.Version); err != nil {
		log.Printf("schema: editors keep the old schema: %v", err)
	}
}

// runShell is wayle_shell::run's single-instance guard on the session
// connection the shell then runs on: a second shell, refused the
// application id, says so and exits successfully instead of fighting over
// the bus names.
func runShell(stderr io.Writer, start func(*dbus.Conn) error) error {
	session, err := dbus.ConnectSessionBus()
	if err != nil {
		return start(nil)
	}
	defer func() { _ = session.Close() }()
	if claimed, err := shellipc.Claim(session); err == nil && !claimed {
		fmt.Fprintln(stderr, "lys-shell shell is already running")
		return nil
	}
	return start(session)
}
