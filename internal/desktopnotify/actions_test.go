package desktopnotify

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// askDaemon answers Notify with id 9 and then plays its script of
// signals, as a user clicking (or ignoring) the buttons would.
type askDaemon struct {
	conn   *dbus.Conn
	script [][]any // each: signal member, then its body
	// early is played before Notify is answered: what the daemon said
	// of a notification that had the id before this one.
	early [][]any
	// forged is played by impostor, another peer, ahead of the script.
	forged   [][]any
	impostor *dbus.Conn

	mu      sync.Mutex
	actions []string
	hints   map[string]dbus.Variant
	timeout int32
}

func (d *askDaemon) Notify(_ string, _ uint32, _, _, _ string, actions []string, hints map[string]dbus.Variant, timeout int32) (uint32, *dbus.Error) {
	d.mu.Lock()
	d.actions, d.hints, d.timeout = actions, hints, timeout
	d.mu.Unlock()
	for _, sig := range d.early {
		_ = d.conn.Emit(path, busName+"."+sig[0].(string), sig[1:]...)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		for _, sig := range d.forged {
			_ = d.impostor.Emit(path, busName+"."+sig[0].(string), sig[1:]...)
		}
		if d.impostor != nil {
			_ = d.impostor.BusObject().Call("org.freedesktop.DBus.GetId", 0).Err
		}
		for _, sig := range d.script {
			_ = d.conn.Emit(path, busName+"."+sig[0].(string), sig[1:]...)
		}
	}()
	return 9, nil
}

func startAskDaemon(t *testing.T, script ...[]any) (*dbustest.Bus, *askDaemon) {
	t.Helper()
	bus := dbustest.Start(t)
	d := &askDaemon{script: script}
	serveAskDaemon(t, bus, d)
	return bus, d
}

// serveAskDaemon puts d, set up as the test wants it, on bus as the
// notification daemon.
func serveAskDaemon(t *testing.T, bus *dbustest.Bus, d *askDaemon) {
	t.Helper()
	conn := bus.Conn(t)
	d.conn = conn
	if err := conn.Export(d, path, busName); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
}

var yesNoActions = []Action{{Key: "deny", Label: "Deny"}, {Key: "allow", Label: "Allow"}}

func TestAskReportsThePressedButton(t *testing.T) {
	bus, d := startAskDaemon(t,
		[]any{"ActionInvoked", uint32(3), "deny"}, // another notification's click
		[]any{"ActionInvoked", uint32(9), "allow"},
	)
	key, ok, err := NewSender(bus.Conn(t)).Ask(context.Background(), "lys-shell", "s", "b", "ld-bluetooth-symbolic", yesNoActions, time.Minute)
	if err != nil || !ok || key != "allow" {
		t.Fatalf("Ask = %q %v %v, want allow", key, ok, err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !slices.Equal(d.actions, []string{"deny", "Deny", "allow", "Allow"}) {
		t.Errorf("actions on the wire = %v", d.actions)
	}
	if d.hints["image-path"].Value() != "ld-bluetooth-symbolic" {
		t.Errorf("hints = %v, want the icon as image-path", d.hints)
	}
	if d.timeout != 60000 {
		t.Errorf("timeout = %d, want 60000", d.timeout)
	}
}

func TestAskClosedWithoutAnAnswer(t *testing.T) {
	bus, _ := startAskDaemon(t, []any{"NotificationClosed", uint32(9), uint32(1)})
	key, ok, err := NewSender(bus.Conn(t)).Ask(context.Background(), "lys-shell", "s", "b", "", yesNoActions, time.Minute)
	if err != nil || ok || key != "" {
		t.Fatalf("Ask = %q %v %v, want no answer", key, ok, err)
	}
}

func TestAskGivesUpWithTheContext(t *testing.T) {
	bus, _ := startAskDaemon(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, ok, err := NewSender(bus.Conn(t)).Ask(ctx, "lys-shell", "s", "b", "", yesNoActions, time.Minute); ok || err != nil {
		t.Fatalf("Ask = %v %v, want no answer on timeout", ok, err)
	}
}

func TestAskWithoutADaemonErrors(t *testing.T) {
	bus := dbustest.Start(t)
	if _, _, err := NewSender(bus.Conn(t)).Ask(context.Background(), "lys-shell", "s", "b", "", yesNoActions, time.Minute); err == nil {
		t.Error("no daemon: want an error")
	}
}

// What the daemon said before it answered Notify is about a
// notification that had the id earlier, not the one just posted.
func TestAskIgnoresWhatTheDaemonSaidBeforeItAnswered(t *testing.T) {
	bus := dbustest.Start(t)
	serveAskDaemon(t, bus, &askDaemon{
		early:  [][]any{{"ActionInvoked", uint32(9), "allow"}},
		script: [][]any{{"NotificationClosed", uint32(9), uint32(1)}},
	})
	key, ok, err := NewSender(bus.Conn(t)).Ask(context.Background(), "lys-shell", "s", "b", "", yesNoActions, time.Minute)
	if err != nil || ok || key != "" {
		t.Fatalf("Ask = %q %v %v, want no answer: the press was on the notification that had the id before", key, ok, err)
	}
}

// Only the daemon answers: a press another peer announces is not one,
// and the ask leaves no match rule on the sender's connection.
func TestAskTakesItsAnswerFromTheDaemonOnly(t *testing.T) {
	bus := dbustest.Start(t)
	serveAskDaemon(t, bus, &askDaemon{
		impostor: bus.Conn(t),
		forged:   [][]any{{"ActionInvoked", uint32(9), "allow"}},
		script:   [][]any{{"NotificationClosed", uint32(9), uint32(1)}},
	})
	conn := bus.Conn(t)
	key, ok, err := NewSender(conn).Ask(context.Background(), "lys-shell", "s", "b", "", yesNoActions, time.Minute)
	if err != nil || ok || key != "" {
		t.Fatalf("Ask = %q %v %v, want no answer: the press came from another peer", key, ok, err)
	}
	if rules := dbustest.MatchRules(t, conn); len(rules) != 0 {
		t.Fatalf("the ask left the match rules %q", rules)
	}
}

// A signal of the daemon's that does not read as the spec's ends the
// ask with an error instead of an answer nobody gave.
func TestAskReportsASignalItCannotRead(t *testing.T) {
	bus, _ := startAskDaemon(t, []any{"ActionInvoked", "nine", "allow"})
	if key, ok, err := NewSender(bus.Conn(t)).Ask(context.Background(), "lys-shell", "s", "b", "", yesNoActions, time.Minute); err == nil {
		t.Fatalf("Ask = %q %v, want an error for the malformed ActionInvoked", key, ok)
	}
}
