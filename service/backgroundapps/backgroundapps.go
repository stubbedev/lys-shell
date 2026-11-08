// Package backgroundapps is the sandboxed apps running without a window,
// as xdg-desktop-portal's background monitor lists them (built from the
// portal backend's GetAppState answers), and a way to stop one.
package backgroundapps

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stubbedev/lys-desktop/feed"
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// D-Bus identity of the frontend's background monitor.
const (
	BusName   = "org.freedesktop.background.Monitor"
	Path      = dbus.ObjectPath("/org/freedesktop/background/monitor")
	Interface = "org.freedesktop.background.Monitor"
	// Property is the monitor's aa{sv} list of background apps.
	Property = "BackgroundApps"
)

const propertiesChanged = "org.freedesktop.DBus.Properties.PropertiesChanged"

// callTimeout bounds a call to the monitor and a flatpak kill.
const callTimeout = 5 * time.Second

// App is one sandboxed app running in the background.
type App struct {
	// ID is the app's id, the name of its desktop entry.
	ID string
	// Instance is the Flatpak instance id flatpak kill takes.
	Instance string
	// Message is the status the app set with Background.SetStatus.
	Message string
	// Name and Icon are the desktop entry's display name and Icon=, or
	// the id and "" when no entry matches.
	Name, Icon string
}

// Describe names an app id from its desktop entry.
type Describe func(appID string) (name, icon string)

// Run runs argv and waits for it.
type Run func(ctx context.Context, argv []string) error

// RunCommand runs argv directly, never through a shell, and reports
// its output with a failure.
func RunCommand(ctx context.Context, argv []string) error {
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput() //nolint:gosec // a fixed program with an instance id argument, no shell
	if err != nil {
		return fmt.Errorf("%s: %w: %s", strings.Join(argv, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Apps follows the background monitor on a session bus: the list is
// read again on each PropertiesChanged and each time the monitor comes
// or goes.
type Apps struct {
	conn     *dbus.Conn
	describe Describe
	run      Run
	cancel   context.CancelFunc
	done     chan struct{}
	changes  feed.Feed[struct{}]

	mu     sync.Mutex
	list   []App
	absent bool
}

// Follow starts following the background monitor on conn, naming each
// app with describe and stopping one with run.
func Follow(conn *dbus.Conn, describe Describe, run Run) (*Apps, error) {
	ctx, cancel := context.WithCancel(context.Background())
	a := &Apps{conn: conn, describe: describe, run: run, cancel: cancel, done: make(chan struct{})}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	matches := [][]dbus.MatchOption{
		{dbus.WithMatchSender(BusName), dbus.WithMatchObjectPath(Path), dbus.WithMatchInterface("org.freedesktop.DBus.Properties"), dbus.WithMatchMember("PropertiesChanged"), dbus.WithMatchArg(0, Interface)},
		dbusx.OwnerMatch(BusName),
	}
	for _, m := range matches {
		if err := conn.AddMatchSignalContext(ctx, m...); err != nil {
			conn.RemoveSignal(signals)
			cancel()
			return nil, fmt.Errorf("background apps: watch %s: %w", BusName, err)
		}
	}
	a.refresh(ctx)
	go func() {
		defer close(a.done)
		defer conn.RemoveSignal(signals)
		for {
			select {
			case <-ctx.Done():
				return
			case sig, ok := <-signals:
				if !ok {
					return
				}
				if a.concerns(sig) {
					a.refresh(ctx)
				}
			}
		}
	}()
	return a, nil
}

func (a *Apps) concerns(sig *dbus.Signal) bool {
	if _, _, isOwner := dbusx.NameOwnerChange(sig, BusName); isOwner {
		return true
	}
	if sig.Name != propertiesChanged || sig.Path != Path || len(sig.Body) == 0 {
		return false
	}
	iface, _ := sig.Body[0].(string)
	return iface == Interface
}

// refresh reads the list. A monitor that is not on the bus lists
// nothing and is logged once until it comes back; any other failure is
// logged each time.
func (a *Apps) refresh(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var raw []map[string]dbus.Variant
	err := a.conn.Object(BusName, Path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, Interface, Property).Store(&raw)
	absent := isAbsent(err)
	if err != nil && !absent && ctx.Err() == nil {
		log.Printf("background apps: read %s: %v", Property, err)
	}
	list := a.parse(raw)
	a.mu.Lock()
	if absent && !a.absent {
		log.Printf("background apps: no %s on the session bus; nothing to list", BusName)
	}
	a.absent = absent
	same := slices.Equal(a.list, list)
	a.list = list
	a.mu.Unlock()
	if !same {
		a.changes.Publish(struct{}{})
	}
}

// isAbsent reports a call that failed because nobody owns the name.
func isAbsent(err error) bool {
	var de dbus.Error
	return errors.As(err, &de) && (de.Name == "org.freedesktop.DBus.Error.ServiceUnknown" || de.Name == "org.freedesktop.DBus.Error.NameHasNoOwner")
}

// parse reads the monitor's entries, skipping (and logging) one without
// an app id or a Flatpak instance id, which nothing could name or stop.
func (a *Apps) parse(raw []map[string]dbus.Variant) []App {
	out := make([]App, 0, len(raw))
	for _, e := range raw {
		app := App{ID: str(e, "app_id"), Instance: str(e, "instance"), Message: str(e, "message")}
		if app.ID == "" || !isInstanceID(app.Instance) {
			log.Printf("background apps: skipping an entry with app id %q and instance %q", app.ID, app.Instance)
			continue
		}
		app.Name, app.Icon = a.describe(app.ID)
		out = append(out, app)
	}
	return out
}

func str(e map[string]dbus.Variant, key string) string {
	s, _ := e[key].Value().(string)
	return s
}

// List is the apps running in the background now.
func (a *Apps) List() []App {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.list)
}

// Subscribe ticks whenever the list changed.
func (a *Apps) Subscribe() (<-chan struct{}, func()) { return a.changes.Subscribe() }

// Stop kills the Flatpak instance with flatpak kill, then reads the
// list again.
func (a *Apps) Stop(ctx context.Context, instance string) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if err := a.run(ctx, []string{"flatpak", "kill", instance}); err != nil {
		return fmt.Errorf("background apps: stop instance %s: %w", instance, err)
	}
	a.refresh(ctx)
	return nil
}

// Close stops following.
func (a *Apps) Close() {
	a.cancel()
	<-a.done
	a.changes.Close()
}

// isInstanceID reports a Flatpak instance id: a decimal number, so it
// can never read as one of flatpak kill's options.
func isInstanceID(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}
