// Command lys-login is the login screen: one screen in two modes.
//
//	lys-login greet [--config PATH] [--sessions DIR]... [--xsessions DIR]...
//	                [--state PATH] [--env KEY=VAL]... [-- <session argv...>]
//	lys-login lock
//	lys-login daemon --vt N --service NAME --greeter-user NAME
//	                 --compositor PATH [--runtime-dir DIR] [-- <greet args...>]
//	lys-login apply-config [--config PATH] <STAGED.yaml>
//
// greet is the greetd greeter: the credential prompt over a session
// picker and the login users, run as the single client of a kiosk
// compositor; on success greetd starts the chosen session and replaces
// this process. lock locks the session with ext-session-lock-v1 and
// exits once it unlocks; the shell starts it for its lock triggers.
// daemon is the display manager in place of greetd and cage: run as root
// on a VT, it shows the greeter in a lys-wm kiosk and starts the session
// a user logs in to (its workers are `lys-login worker`, started by it).
// apply-config is the privileged config writer lys-settings runs through
// pkexec.
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

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/login/dm"
	"github.com/stubbedev/lys-shell/login/greeter"
	"github.com/stubbedev/lys-shell/login/lock"
	"github.com/stubbedev/lys-shell/login/lockclient"
)

const usage = "usage: lys-login greet [options] [-- <session argv...>] | lys-login lock | lys-login daemon [options] [-- <greet args...>] | lys-login apply-config [--config PATH] <STAGED.yaml>"

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
	case "daemon":
		os.Exit(daemon(args))
	case "worker":
		os.Exit(worker(args))
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

// workerFlags are the flags every worker takes.
func workerFlags(fs *flag.FlagSet) *dm.WorkerOptions {
	o := &dm.WorkerOptions{}
	fs.StringVar(&o.Service, "service", "lys-login", "PAM service")
	fs.IntVar(&o.VT, "vt", 1, "virtual terminal")
	return o
}

func daemon(args []string) int {
	fs := flag.NewFlagSet("lys-login daemon", flag.ContinueOnError)
	wo := workerFlags(fs)
	greeterUser := fs.String("greeter-user", "lys-greeter", "account the greeter runs as")
	compositor := fs.String("compositor", "lys-wm", "the greeter's compositor")
	runtimeDir := fs.String("runtime-dir", "/run/lys-login", "directory for the greeter's socket")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	self, err := self()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lys-login daemon:", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	err = dm.Run(ctx, dm.Options{
		WorkerOptions: *wo, Self: self, GreeterUser: *greeterUser,
		Compositor: []string{*compositor}, GreeterArgs: fs.Args(), RuntimeDir: *runtimeDir,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "lys-login daemon:", err)
		return 1
	}
	return 0
}

func worker(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "lys-login worker: greeter or session")
		return 2
	}
	kind := args[0]
	fs := flag.NewFlagSet("lys-login worker "+kind, flag.ContinueOnError)
	wo := workerFlags(fs)
	user := fs.String("user", "lys-greeter", "the greeter's account")
	var env multiFlag
	fs.Var(&env, "env", "KEY=value for the greeter's compositor (repeatable)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	var err error
	switch kind {
	case "greeter":
		if fs.NArg() == 0 {
			fmt.Fprintln(os.Stderr, "lys-login worker greeter: no compositor command after --")
			return 2
		}
		err = dm.RunGreeter(*wo, *user, fs.Args(), env)
	case "session":
		ch := os.NewFile(3, "dm")
		if ch == nil {
			fmt.Fprintln(os.Stderr, "lys-login worker session: no channel on fd 3")
			return 2
		}
		err = dm.RunSession(*wo, ch)
	default:
		fmt.Fprintf(os.Stderr, "lys-login worker: unknown kind %q\n", kind)
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "lys-login worker %s: %v\n", kind, err)
		return 1
	}
	return 0
}

// multiFlag collects a repeated flag.
type multiFlag []string

func (m *multiFlag) String() string     { return fmt.Sprint(*m) }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// self is the lys-login the workers and the greeter run: the program of
// that name beside the running one (a package's wrapper, which sets the
// library path), else the running one.
func self() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find lys-login: %w", err)
	}
	sibling := filepath.Join(filepath.Dir(exe), "lys-login")
	if _, err := os.Stat(sibling); err == nil {
		return sibling, nil
	}
	return exe, nil
}
