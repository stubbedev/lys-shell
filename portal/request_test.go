package portal

import (
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbustest"
	"github.com/stubbedev/lys-shell/internal/portalreq"
)

// gate is what every fake shell dialog embeds: it records who asked,
// holds the dialog open (when hold is set) until its request is
// dismissed, and records the dismissals.
type gate struct {
	hold      dbustest.Var[bool]
	callers   dbustest.Var[[]portalreq.Caller]
	dismissed dbustest.Var[[]string]
	mu        sync.Mutex
	waiting   map[string]chan struct{}
}

// enter records c and, when holding, waits for c's dismissal; it
// reports whether the dialog was dismissed.
func (g *gate) enter(c portalreq.Caller) bool {
	g.callers.Update(func(all []portalreq.Caller) []portalreq.Caller { return append(all, c) })
	if !g.hold.Load() {
		return false
	}
	ch := make(chan struct{})
	g.mu.Lock()
	if g.waiting == nil {
		g.waiting = map[string]chan struct{}{}
	}
	g.waiting[c.Handle] = ch
	g.mu.Unlock()
	<-ch
	return true
}

// Dismiss releases the dialog held for handle.
func (g *gate) Dismiss(handle string) {
	g.dismissed.Update(func(d []string) []string { return append(d, handle) })
	g.mu.Lock()
	defer g.mu.Unlock()
	if ch, ok := g.waiting[handle]; ok {
		close(ch)
		delete(g.waiting, handle)
	}
}

// open reports whether a dialog is held for handle.
func (g *gate) open(handle string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.waiting[handle]
	return ok
}

// waitOpen waits until a dialog is held for handle.
func (g *gate) waitOpen(t *testing.T, handle dbus.ObjectPath) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !g.open(string(handle)) {
		if time.Now().After(deadline) {
			t.Fatalf("no dialog held for %s", handle)
		}
		time.Sleep(time.Millisecond)
	}
}

// interactiveCase is one portal method that shows a shell dialog:
// serve stands up the fake dialog service and returns its gate, call
// invokes the method and returns its response code.
type interactiveCase struct {
	name  string
	serve func(t *testing.T, r *rig) *gate
	call  func(r *rig) (uint32, error)
}

func interactiveCases() []interactiveCase {
	code := func(r *rig, method string, args ...any) (uint32, error) {
		var c uint32
		var res map[string]dbus.Variant
		err := r.obj.Call(method, 0, args...).Store(&c, &res)
		return c, err
	}
	dialogs := func(t *testing.T, r *rig) *gate { return &serveDialogs(t, r, true).gate }
	chooser := func(t *testing.T, r *rig) *gate { return &serveChooser(t, r, "file:///x").gate }
	printer := func(t *testing.T, r *rig) *gate {
		f := &fakePrinter{granted: true}
		servePrinter(t, r, f)
		return &f.gate
	}
	shooter := func(t *testing.T, r *rig) *gate {
		f := &fakeShooter{path: "/tmp/x.png"}
		serveShooter(t, r, f)
		return &f.gate
	}
	return []interactiveCase{
		{"Access", dialogs, func(r *rig) (uint32, error) {
			return code(r, "org.freedesktop.impl.portal.Access.AccessDialog", handle, "org.app", "", "T", "S", "B", Vardict{})
		}},
		{"Account", dialogs, func(r *rig) (uint32, error) {
			return code(r, "org.freedesktop.impl.portal.Account.GetUserInformation", handle, "org.app", "", Vardict{})
		}},
		{"AppChooser", dialogs, func(r *rig) (uint32, error) {
			return code(r, "org.freedesktop.impl.portal.AppChooser.ChooseApplication", handle, "org.app", "", []string{}, Vardict{})
		}},
		{"DynamicLauncher", dialogs, func(r *rig) (uint32, error) {
			return code(r, "org.freedesktop.impl.portal.DynamicLauncher.PrepareInstall", handle, "org.app", "", "App", dbus.MakeVariant(gicon{"bytes", dbus.MakeVariant([]byte{1})}), Vardict{})
		}},
		{"Wallpaper", dialogs, func(r *rig) (uint32, error) {
			var c uint32
			err := r.obj.Call("org.freedesktop.impl.portal.Wallpaper.SetWallpaperURI", 0, handle, "org.app", "", "file:///w.png",
				Vardict{"show-preview": dbus.MakeVariant(true)}).Store(&c)
			return c, err
		}},
		{"OpenFile", chooser, func(r *rig) (uint32, error) {
			return code(r, fileChooserIfaceName+"OpenFile", handle, "org.app", "", "Open", Vardict{})
		}},
		{"SaveFile", chooser, func(r *rig) (uint32, error) {
			return code(r, fileChooserIfaceName+"SaveFile", handle, "org.app", "", "Save", Vardict{})
		}},
		{"SaveFiles", chooser, func(r *rig) (uint32, error) {
			return code(r, fileChooserIfaceName+"SaveFiles", handle, "org.app", "", "Save", Vardict{})
		}},
		{"PreparePrint", printer, func(r *rig) (uint32, error) {
			return code(r, "org.freedesktop.impl.portal.Print.PreparePrint", handle, "org.app", "", "Doc", Vardict{}, Vardict{}, Vardict{})
		}},
		{"Screenshot", shooter, func(r *rig) (uint32, error) {
			return code(r, "org.freedesktop.impl.portal.Screenshot.Screenshot", handle, "org.app", "", Vardict{"interactive": dbus.MakeVariant(true)})
		}},
		{"PickColor", shooter, func(r *rig) (uint32, error) {
			return code(r, "org.freedesktop.impl.portal.Screenshot.PickColor", handle, "org.app", "", Vardict{})
		}},
		{"RemoteDesktop.Start", func(t *testing.T, r *rig) *gate {
			g := dialogs(t, r)
			if c, err := code(r, RemoteDesktopIface+".CreateSession", handle, sessB, "org.app", Vardict{}); err != nil || c != ResponseSuccess {
				t.Fatalf("CreateSession = %d, %v", c, err)
			}
			return g
		}, func(r *rig) (uint32, error) {
			return code(r, RemoteDesktopIface+".Start", handle, sessB, "org.app", "", Vardict{})
		}},
		{"ScreenCast.Start", func(t *testing.T, r *rig) *gate {
			p := serveSharePicker(t, r, "/screen:DP-1")
			r.castSession(t, Vardict{})
			return &p.gate
		}, func(r *rig) (uint32, error) {
			return code(r, ScreenCastIface+".Start", handle, sessA, "org.app", "", Vardict{})
		}},
	}
}

// Every method that shows a shell dialog mounts a Request at its
// handle: the app's Close mid-dialog replies cancelled and dismisses
// the dialog in the shell, and the Request is gone after the reply.
// The shell is told which request and app the dialog serves.
func TestRequestCloseDismissesEveryDialog(t *testing.T) {
	for _, tc := range interactiveCases() {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := castRig(t)
			g := tc.serve(t, r)
			g.hold.Store(true)
			done := make(chan uint32, 1)
			go func() {
				c, err := tc.call(r)
				if err != nil {
					t.Error(err)
				}
				done <- c
			}()
			g.waitOpen(t, handle)
			if got := g.callers.Load(); len(got) != 1 || got[0] != (portalreq.Caller{Handle: string(handle), AppID: "org.app", Modal: true}) {
				t.Errorf("the shell was told %+v", got)
			}
			req := r.client.Object(BusName, handle)
			if err := req.Call(RequestIface+".Close", 0).Err; err != nil {
				t.Fatal(err)
			}
			select {
			case c := <-done:
				if c != ResponseCancelled {
					t.Errorf("reply after Close = %d, want cancelled", c)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Close did not end the call")
			}
			if got := g.dismissed.Load(); len(got) != 1 || got[0] != string(handle) {
				t.Errorf("dismissed %v, want the request's handle", got)
			}
			if err := req.Call(RequestIface+".Close", 0).Err; err == nil {
				t.Error("the Request outlived its call")
			}
		})
	}
}

// Without the app closing anything, the dialog's answer goes through
// and nothing is dismissed.
func TestAnsweredDialogsDismissNothing(t *testing.T) {
	for _, tc := range interactiveCases() {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := castRig(t)
			g := tc.serve(t, r)
			if _, err := tc.call(r); err != nil {
				t.Fatal(err)
			}
			if got := g.dismissed.Load(); len(got) != 0 {
				t.Errorf("an answered dialog was dismissed: %v", got)
			}
			if err := r.client.Object(BusName, handle).Call(RequestIface+".Close", 0).Err; err == nil {
				t.Error("the Request outlived its call")
			}
		})
	}
}
