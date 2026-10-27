// Command lys-login is the login screen: one screen in two modes.
//
//	lys-login greet [--config PATH] [--sessions DIR]... [--xsessions DIR]...
//	                [--state PATH] [--env KEY=VAL]... [-- <session argv...>]
//	lys-login lock
//	lys-login apply-config [--config PATH] <STAGED.yaml>
//
// greet is the greetd greeter: the credential prompt over a session
// picker and the login users, run as the single client of a kiosk
// compositor; on success greetd starts the chosen session and replaces
// this process. lock locks the session with ext-session-lock-v1 and
// exits once it unlocks; the shell starts it for its lock triggers.
// apply-config is the privileged config writer lys-settings runs through
// pkexec.
package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/login/greeter"
	"github.com/stubbedev/lys-shell/login/lock"
	"github.com/stubbedev/lys-shell/login/lockclient"
)

const usage = "usage: lys-login greet [options] [-- <session argv...>] | lys-login lock | lys-login apply-config [--config PATH] <STAGED.yaml>"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	args := os.Args[2:]
	switch os.Args[1] {
	case "greet":
		os.Exit(greet(args))
	case "lock":
		os.Exit(lockSession(args))
	case "apply-config":
		os.Exit(greeter.RunApplyConfig(args, os.Stdout, os.Stderr))
	default:
		fmt.Fprintf(os.Stderr, "lys-login: unknown mode %q\n%s\n", os.Args[1], usage)
		os.Exit(2)
	}
}

func greet(args []string) int {
	opts, err := greeter.ParseOptions(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	cfg, err := config.LoadGreeter(opts.ConfigPath, config.StderrDiagnostics)
	if err != nil {
		log.Printf("greeter: config load failed; using defaults: %v", err)
	}

	// Wayland and X11 sessions, sorted by name, plus the explicit
	// `-- <argv>` as a Custom entry (or the only one).
	sessions := greeter.Discover(opts.SessionDirs, greeter.Wayland)
	sessions = append(sessions, greeter.Discover(opts.XSessionDirs, greeter.X11)...)
	greeter.SortSessions(sessions)
	if len(opts.Command) > 0 {
		sessions = append(sessions, greeter.Session{ID: "custom", Name: greeter.CustomSessionName(), Exec: opts.Command})
	}
	if len(sessions) == 0 {
		fmt.Fprintf(os.Stderr, "no sessions found in %q or %q and no `-- <argv>` fallback given; nothing to log into\n",
			opts.SessionDirs, opts.XSessionDirs)
		return 2
	}

	err = greeter.Run(greeter.Init{
		Config:      cfg,
		Sessions:    sessions,
		Users:       greeter.LoadUsers(),
		LastSession: greeter.LoadLast(opts.StatePath),
		LastUser:    greeter.LoadLast(greeter.LastUserPath(opts.StatePath)),
		StatePath:   opts.StatePath,
		SessionEnv:  opts.Env,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "lys-login greet:", err)
		return 1
	}
	return 0
}

func lockSession(args []string) int {
	if len(args) != 0 {
		fmt.Fprintf(os.Stderr, "lys-login lock: unexpected argument %q\n", args[0])
		return 2
	}
	svc, err := config.Open()
	if err != nil {
		log.Printf("lock: config: %v", err)
	}
	cfg := config.Defaults()
	if svc != nil {
		defer svc.Close()
		cfg = svc.Config()
	}
	switch err := lock.Run(cfg, os.Stdout); {
	case err == nil:
		return 0
	case errors.Is(err, lock.ErrHeld):
		log.Print(err)
		return lockclient.ExitHeld
	case errors.Is(err, lock.ErrRefused):
		log.Print(err)
		return lockclient.ExitRefused
	default:
		fmt.Fprintln(os.Stderr, "lys-login lock:", err)
		return 1
	}
}
