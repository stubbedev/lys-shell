package portaldialogs

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"

	"github.com/stubbedev/lys-shell/internal/portalreq"
)

// fakeHost answers every dialog from fixed replies and records what it
// was asked, by whom, and what was dismissed.
type fakeHost struct {
	yes       bool
	choice    string
	asked     dbustest.Var[[]any]
	caller    dbustest.Var[portalreq.Caller]
	dismissed dbustest.Var[[]string]
}

func (f *fakeHost) record(c portalreq.Caller, args ...any) {
	f.caller.Store(c)
	f.asked.Store(args)
}

func (f *fakeHost) Access(c portalreq.Caller, r AccessRequest) (bool, []portalreq.Selection) {
	f.record(c, r)
	if !f.yes {
		return false, nil
	}
	return true, []portalreq.Selection{{ID: "picked", Value: "x"}}
}

func (f *fakeHost) Account(c portalreq.Caller, reason string, info AccountInfo) (bool, AccountInfo) {
	f.record(c, reason, info)
	return f.yes, AccountInfo{Name: info.Name + " (edited)", Image: info.Image}
}

func (f *fakeHost) ConfirmWallpaper(c portalreq.Caller, uri string) bool {
	f.record(c, uri)
	return f.yes
}

func (f *fakeHost) ChooseApplication(c portalreq.Caller, r ChooserRequest) string {
	f.record(c, r)
	return f.choice
}

func (f *fakeHost) UpdateChoices(handle string, choices []string) bool {
	f.asked.Store([]any{handle, choices})
	return handle == "/r/1"
}

func (f *fakeHost) ConfirmInstall(c portalreq.Caller, r LauncherRequest) (bool, string) {
	f.record(c, r)
	return f.yes, r.Name + " (edited)"
}

func (f *fakeHost) ChooseDevices(c portalreq.Caller, devices []Device) (bool, []string) {
	f.record(c, devices)
	if !f.yes {
		return false, nil
	}
	return true, []string{devices[0].ID}
}

func (f *fakeHost) Password(c portalreq.Caller, r PasswordRequest) (bool, string) {
	f.record(c, r)
	if !f.yes {
		return false, ""
	}
	return true, "typed"
}

func (f *fakeHost) Dismiss(handle string) {
	f.dismissed.Update(func(d []string) []string { return append(d, handle) })
}

func TestDialogsRoundTrip(t *testing.T) {
	bus := dbustest.Start(t)
	for _, yes := range []bool{true, false} {
		host := &fakeHost{yes: yes, choice: map[bool]string{true: "org.app.desktop"}[yes]}
		release, err := NewDaemon(host).Export(bus.Conn(t))
		if err != nil {
			t.Fatal(err)
		}
		c, ctx := NewClient(bus.Conn(t)), context.Background()
		caller := portalreq.Caller{Handle: "/r/1", AppID: "org.app", ParentWindow: "wayland:abc"}
		req := AccessRequest{"T", "S", "B", "Allow", "Deny", "camera", []portalreq.Choice{{ID: "c", Label: "C", Options: []portalreq.Option{}}}}
		got, picked, err := c.Access(ctx, caller, req)
		if err != nil || got != yes || !reflect.DeepEqual(host.asked.Load()[0], req) || host.caller.Load() != caller {
			t.Errorf("Access = %v, %v (asked %v by %+v)", got, err, host.asked.Load(), host.caller.Load())
		}
		if yes && !reflect.DeepEqual(picked, []portalreq.Selection{{ID: "picked", Value: "x"}}) || !yes && len(picked) != 0 {
			t.Errorf("Access picked %v", picked)
		}
		got, edited, err := c.Account(ctx, caller, "why", AccountInfo{Name: "Ada", Image: "/a.png"})
		if err != nil || got != yes || host.asked.Load()[0] != "why" || edited != (AccountInfo{Name: "Ada (edited)", Image: "/a.png"}) {
			t.Errorf("Account = %v, %+v, %v", got, edited, err)
		}
		if got, err := c.ConfirmWallpaper(ctx, caller, "file:///a.png"); err != nil || got != yes {
			t.Errorf("ConfirmWallpaper = %v, %v", got, err)
		}
		launcher := LauncherRequest{Name: "App", Icon: []byte{0x89, 'P'}, Target: "https://example.org", EditableName: true}
		if got, name, err := c.ConfirmInstall(ctx, caller, launcher); err != nil || got != yes || name != "App (edited)" || !reflect.DeepEqual(host.asked.Load()[0], launcher) {
			t.Errorf("ConfirmInstall = %v, %q, %v (asked %v)", got, name, err, host.asked.Load())
		}
		devices := []Device{{ID: "d0", Name: "Pad", Writable: true}, {ID: "d1", Name: "Key"}}
		allowed, ids, err := c.ChooseDevices(ctx, caller, devices)
		if err != nil || allowed != yes || !reflect.DeepEqual(host.asked.Load()[0], devices) {
			t.Errorf("ChooseDevices = %v, %v (asked %v)", allowed, err, host.asked.Load())
		}
		if yes && !slices.Equal(ids, []string{"d0"}) || !yes && len(ids) != 0 {
			t.Errorf("ChooseDevices picked %v", ids)
		}
		chosen, err := c.ChooseApplication(ctx, caller, ChooserRequest{ContentType: "text/plain", Target: "x.txt"})
		if asked := host.asked.Load(); err != nil || chosen != host.choice || !reflect.DeepEqual(asked[0], ChooserRequest{Choices: []string{}, ContentType: "text/plain", Target: "x.txt"}) {
			t.Errorf("ChooseApplication = %q, %v (asked %v)", chosen, err, asked)
		}
		r := ChooserRequest{Choices: []string{"a", "b"}, LastChoice: "b"}
		if _, _ = c.ChooseApplication(ctx, caller, r); !reflect.DeepEqual(host.asked.Load()[0], r) {
			t.Errorf("request = %v", host.asked.Load()[0])
		}
		if found, err := c.UpdateChoices(ctx, "/r/1", []string{"c"}); err != nil || !found || !slices.Equal(host.asked.Load()[1].([]string), []string{"c"}) {
			t.Errorf("UpdateChoices = %v, %v (asked %v)", found, err, host.asked.Load())
		}
		if found, err := c.UpdateChoices(ctx, "/r/none", nil); err != nil || found {
			t.Errorf("UpdateChoices without a chooser = %v, %v", found, err)
		}
		pw := PasswordRequest{Title: "Unlock", Body: "login", Confirm: true, Retry: true}
		if ok, typed, err := c.Password(ctx, caller, pw); err != nil || ok != yes || typed != map[bool]string{true: "typed"}[yes] || host.asked.Load()[0] != pw {
			t.Errorf("Password = %v, %q, %v (asked %v)", ok, typed, err, host.asked.Load())
		}
		if err := c.Dismiss(ctx, "/r/1"); err != nil || !slices.Equal(host.dismissed.Load(), []string{"/r/1"}) {
			t.Errorf("Dismiss = %v, dismissed %v", err, host.dismissed.Load())
		}
		release()
	}
	// Without a host every call is an error, never a silent answer.
	if _, _, err := NewClient(bus.Conn(t)).Access(context.Background(), portalreq.Caller{}, AccessRequest{}); err == nil {
		t.Error("Access answered without a host")
	}
}
