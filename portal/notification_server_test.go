package portal

import (
	"testing"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/notifytest"
	"github.com/stubbedev/lys-shell/service/notifications"
)

// The portal takes the daemon's signals only from the owner of
// org.freedesktop.Notifications, at the daemon's object: the shell's own
// notification server, started after the portal as a restarted shell
// is, sends them that way, the activation token before the press.
func TestNotificationActionsReachAppsFromTheShellsOwnServer(t *testing.T) {
	r := newRig(t, nil)
	svc := notifytest.NewService(t)
	server, err := notifications.Serve(r.bus.Conn(t), svc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Release(); err != nil {
			t.Errorf("release the notification server: %v", err)
		}
	})
	n := Vardict{
		"title":                 dbus.MakeVariant("Hello"),
		"default-action":        dbus.MakeVariant("app.open"),
		"default-action-target": dbus.MakeVariant(dbus.MakeVariant("inbox")),
	}
	if err := r.obj.Call(notificationAdd, 0, "org.app", "n1", n).Err; err != nil {
		t.Fatal(err)
	}
	var id uint32
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if popups := svc.Popups(); len(popups) == 1 {
			id = popups[0].ID
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the server never showed the portal's notification")
		}
	}
	svc.InvokeAction(id, "default", "tok-1")
	body := nextActionInvoked(t, r)
	if body[0] != "org.app" || body[1] != "n1" || body[2] != "app.open" {
		t.Fatalf("ActionInvoked = %v, want the app's default action", body)
	}
	params, _ := body[3].([]dbus.Variant)
	if len(params) != 2 || params[0].Value() != "inbox" {
		t.Fatalf("parameters = %v, want the target and the platform data", params)
	}
	if platform, _ := params[1].Value().(map[string]dbus.Variant); platform["activation-token"].Value() != "tok-1" {
		t.Errorf("platform data = %v, want the token the server announced before the press", platform)
	}
}
