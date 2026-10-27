// Command lys-secrets serves the freedesktop Secret Service
// (org.freedesktop.secrets) for a lys desktop: libsecret, Chromium and
// Electron apps, git's credential helper and the portal's Secret backend
// keep their secrets in collections encrypted at rest under
// $XDG_DATA_HOME/lys-secrets. It is a small process of its own, apart
// from the shell, so the code holding unlocked keys is only this.
//
//	lys-secrets [--unlock-fd N]
//
// The login collection unlocks with the password the user logs in or
// unlocks the session with: the login screen writes it to an inherited
// descriptor (--unlock-fd), the lock screen sends it over the control
// socket at $XDG_RUNTIME_DIR/lys-secrets/control. When another provider
// already owns the bus name, lys-secrets logs who and exits.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"

	"github.com/stubbedev/lys-shell/internal/xdg"
	"github.com/stubbedev/lys-shell/secrets/control"
	"github.com/stubbedev/lys-shell/secrets/keyring"
	"github.com/stubbedev/lys-shell/secrets/prompt"
	"github.com/stubbedev/lys-shell/secrets/service"
)

func main() {
	unlockFD := flag.Int("unlock-fd", -1, "read the login password from this inherited descriptor and unlock the login collection with it")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "lys-secrets: unexpected argument %q\n", flag.Arg(0))
		os.Exit(2)
	}
	if err := run(*unlockFD); err != nil {
		fmt.Fprintln(os.Stderr, "lys-secrets:", err)
		os.Exit(1)
	}
}

func run(unlockFD int) error {
	// Neither a core dump nor a same-user ptrace may read unlocked keys.
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("make the process undumpable: %w", err)
	}
	var password []byte
	if unlockFD >= 0 {
		f := os.NewFile(uintptr(unlockFD), "unlock-fd")
		if f == nil {
			return fmt.Errorf("--unlock-fd %d is not a descriptor", unlockFD)
		}
		var err error
		password, err = control.ReadPassword(f)
		_ = f.Close()
		if err != nil {
			return err
		}
		defer clear(password)
	}
	data, ok := xdg.DataHome()
	if !ok {
		return errors.New("neither XDG_DATA_HOME nor HOME is set")
	}
	sock, err := control.SocketPath()
	if err != nil {
		return err
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("connect to the session bus: %w", err)
	}
	defer func() { _ = conn.Close() }()

	svc, err := service.Start(conn, service.Config{
		Dir: filepath.Join(data, "lys-secrets"), KDF: keyring.DefaultKDF, Prompter: prompt.New(conn),
	})
	if errors.Is(err, service.ErrNameTaken) {
		var owner string
		_ = conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, service.BusName).Store(&owner)
		log.Printf("lys-secrets: %s is already served by %s; staying off", service.BusName, owner)
		return nil
	}
	if err != nil {
		return err
	}
	defer svc.Close()

	if password != nil {
		if err := svc.UnlockLogin(password); err != nil {
			log.Printf("lys-secrets: the login password does not unlock the login collection: %v", err)
		}
		clear(password)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	ln, err := control.Listen(sock)
	if err != nil {
		return err
	}
	log.Printf("lys-secrets: serving %s, unlock socket %s", service.BusName, sock)
	control.Serve(ctx, ln, svc)
	return nil
}
