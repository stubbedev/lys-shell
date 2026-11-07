package portal

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/notifications"
)

// capsNotifyd is the fake daemon answering GetCapabilities with caps.
type capsNotifyd struct {
	*fakeNotifyd
	caps []string
}

func (c capsNotifyd) GetCapabilities() ([]string, *dbus.Error) { return c.caps, nil }

func serveCapsNotifyd(t *testing.T, r *rig, caps []string) *fakeNotifyd {
	t.Helper()
	f := &fakeNotifyd{conn: r.bus.Conn(t)}
	f.next.Store(40)
	if err := f.conn.Export(capsNotifyd{f, caps}, notifications.ObjPath, notifications.Interface); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.RequestName(notifications.Interface, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	return f
}

func supported(t *testing.T, r *rig) (categories, purposes []string) {
	t.Helper()
	v, err := r.obj.GetProperty(NotificationIface + ".SupportedOptions")
	if err != nil {
		t.Fatal(err)
	}
	opts, _ := v.Value().(map[string]dbus.Variant)
	categories, _ = opts["category"].Value().([]string)
	purposes, _ = opts["button-purpose"].Value().([]string)
	return categories, purposes
}

func TestSupportedOptionsFollowTheDaemonsCapabilities(t *testing.T) {
	r := newRig(t, nil)
	serveCapsNotifyd(t, r, notifications.Capabilities)
	categories, purposes := supported(t, r)
	if want := []string{"im.reply-with-text", "call.accept", "call.decline", "call.hang-up"}; !slices.Equal(purposes, want) {
		t.Errorf("button-purpose = %v, want %v", purposes, want)
	}
	if want := []string{"im.received", "call.incoming", "call.ongoing", "call.missed", "alarm.ringing", "alarm.pending"}; !slices.Equal(categories, want) {
		t.Errorf("category = %v, want %v", categories, want)
	}
}

func TestADaemonWithoutInlineReplyOffersNoReplyPurpose(t *testing.T) {
	r := newRig(t, nil)
	serveCapsNotifyd(t, r, []string{"body", "actions"})
	categories, purposes := supported(t, r)
	if slices.Contains(purposes, "im.reply-with-text") {
		t.Errorf("button-purpose = %v offers a reply the daemon cannot take", purposes)
	}
	if !slices.Contains(purposes, "call.accept") || slices.Contains(categories, "call.incoming") || !slices.Contains(categories, "im.received") {
		t.Errorf("categories %v, purposes %v", categories, purposes)
	}
}

func TestAReplyReachesTheAppAsItsActionsParameter(t *testing.T) {
	r := newRig(t, nil)
	d := serveCapsNotifyd(t, r, notifications.Capabilities)
	n := Vardict{
		"title":    dbus.MakeVariant("Ada"),
		"category": dbus.MakeVariant("im.received"),
		"buttons": dbus.MakeVariant([]map[string]dbus.Variant{
			{"label": dbus.MakeVariant("Reply to Ada"), "action": dbus.MakeVariant("app.reply"), "purpose": dbus.MakeVariant("im.reply-with-text")},
			{"label": dbus.MakeVariant("Mark read"), "action": dbus.MakeVariant("app.read")},
		}),
	}
	if err := r.obj.Call(notificationAdd, 0, "org.chat", "m1", n).Err; err != nil {
		t.Fatal(err)
	}
	got := d.last(t)
	if want := []string{notifications.InlineReplyActionID, "Reply to Ada", "button-1", "Mark read"}; !slices.Equal(got.actions, want) {
		t.Errorf("actions = %q, want %q", got.actions, want)
	}
	if got.hints["x-kde-reply-placeholder-text"].Value() != "Reply to Ada" {
		t.Errorf("hints = %v, want the label as the placeholder", got.hints)
	}
	if got.expire != -1 || got.hints["resident"].Value() != nil || got.hints["urgency"].Value() != nil {
		t.Errorf("im.received: expire %d, hints %v; want the normal mapping", got.expire, got.hints)
	}
	_ = d.conn.Emit(notifications.ObjPath, notifications.Interface+".NotificationReplied", uint32(99), "not ours")
	_ = d.conn.Emit(notifications.ObjPath, notifications.Interface+".NotificationReplied", uint32(41), "hi")
	body := nextActionInvoked(t, r)
	params, _ := body[3].([]dbus.Variant)
	if body[0] != "org.chat" || body[1] != "m1" || body[2] != "app.reply" || len(params) != 1 || params[0].Value() != "hi" {
		t.Fatalf("ActionInvoked = %v, want (org.chat, m1, app.reply, [<\"hi\">])", body)
	}
}

func TestRingingCategoriesStayUpAtCriticalUrgency(t *testing.T) {
	r := newRig(t, nil)
	d := serveCapsNotifyd(t, r, notifications.Capabilities)
	call := Vardict{
		"title":    dbus.MakeVariant("Ada calling"),
		"category": dbus.MakeVariant("call.incoming"),
		"priority": dbus.MakeVariant("normal"),
		"buttons": dbus.MakeVariant([]map[string]dbus.Variant{
			{"label": dbus.MakeVariant("Accept"), "action": dbus.MakeVariant("app.accept"), "purpose": dbus.MakeVariant("call.accept")},
			{"label": dbus.MakeVariant("Decline"), "action": dbus.MakeVariant("app.decline"), "purpose": dbus.MakeVariant("call.decline")},
		}),
	}
	if err := r.obj.Call(notificationAdd, 0, "org.chat", "c1", call).Err; err != nil {
		t.Fatal(err)
	}
	got := d.last(t)
	if got.expire != 0 || got.hints["resident"].Value() != true || got.hints["urgency"].Value() != byte(2) {
		t.Errorf("call.incoming: expire %d, hints %v; want resident, critical, no expiry", got.expire, got.hints)
	}
	purposes, _ := got.hints[notifications.ActionPurposesHint].Value().(map[string]string)
	if purposes["button-0"] != "call.accept" || purposes["button-1"] != "call.decline" {
		t.Errorf("purposes = %v", purposes)
	}
}

func TestTheSoundOptionMapsOntoTheDaemonsSoundHints(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	r := newRig(t, nil)
	d := serveCapsNotifyd(t, r, notifications.Capabilities)
	add := func(id string, sound dbus.Variant) notifyCall {
		t.Helper()
		if err := r.obj.Call(notificationAdd, 0, "org.app", id, Vardict{"sound": sound}).Err; err != nil {
			t.Fatal(err)
		}
		return d.last(t)
	}
	if got := add("d", dbus.MakeVariant("default")); got.hints["sound-name"].Value() != defaultSoundName {
		t.Errorf("default sound hints = %v", got.hints)
	}
	if got := add("s", dbus.MakeVariant("silent")); got.hints["suppress-sound"].Value() != true {
		t.Errorf("silent sound hints = %v", got.hints)
	}
	chirp := []byte("RIFF....WAVE sound bytes")
	fileSound := func() dbus.Variant {
		rd, wr, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := wr.Write(chirp); err != nil {
			t.Fatal(err)
		}
		_ = wr.Close()
		t.Cleanup(func() { _ = rd.Close() })
		return dbus.MakeVariant(gicon{"file", dbus.MakeVariant(dbus.UnixFD(rd.Fd()))})
	}
	first := add("f1", fileSound()).hints["sound-file"].Value()
	second := add("f2", fileSound()).hints["sound-file"].Value()
	path, _ := first.(string)
	if path == "" || second != first || filepath.Dir(path) != filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "lys-shell", "sounds") {
		t.Fatalf("sound-file = %v then %v", first, second)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != string(chirp) {
		t.Errorf("kept %q, %v", data, err)
	}
	r.backend.notifier.sounds.mu.Lock()
	writes := r.backend.notifier.sounds.writes
	r.backend.notifier.sounds.mu.Unlock()
	if writes != 1 {
		t.Errorf("two notifications with one sound wrote it %d times, want once", writes)
	}
}
