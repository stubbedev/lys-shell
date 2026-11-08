package bar

import (
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/service/notifications"
	"github.com/stubbedev/lys-shell/service/notifications/notifypolicy"
)

func TestAFullscreenWindowTurnsDoNotDisturbOnWhileItIs(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dbustest.Session(t)
	a := apptest.Run(t)
	svc := notifications.NewService()
	svc.SetFocusRules(notifypolicy.FocusRules{WhileFullscreen: true})
	var (
		stop   func()
		window *app.Window
	)
	apptest.On(a, func() error {
		stop = followFocusInputs(a, svc, nil, nil)
		var err error
		window, err = a.NewWindow(app.WindowConfig{Title: "video", AppID: "org.example.Player", Root: widget.NewDrawingArea(64, 64)})
		return err
	})
	t.Cleanup(func() { apptest.On(a, func() bool { stop(); return true }) })
	apptest.Until(t, a, "the window listed", func() bool { return len(a.Toplevels()) == 1 })
	if svc.DND() {
		t.Fatal("do-not-disturb on before anything went fullscreen")
	}
	a.Invoke(window.Fullscreen)
	apptest.Until(t, a, "do-not-disturb for the fullscreen window", func() bool {
		f := svc.Focus()
		return f.On && f.Reason == notifications.FocusFullscreen
	})
	a.Invoke(window.Unfullscreen)
	apptest.Until(t, a, "do-not-disturb off again", func() bool { return !svc.DND() })
}

func TestAPortalShareTurnsDoNotDisturbOnAndItsEndRestoresIt(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dbustest.Session(t)
	a := apptest.Run(t)
	f, shares := servePortalShares(t)
	svc := notifications.NewService()
	svc.SetFocusRules(notifypolicy.FocusRules{WhileSharing: true})
	var stop func()
	apptest.On(a, func() bool { stop = followFocusInputs(a, svc, shares, nil); return true })
	t.Cleanup(func() { apptest.On(a, func() bool { stop(); return true }) })
	if svc.DND() {
		t.Fatal("do-not-disturb on with nothing shared")
	}
	f.set(chromeShare)
	apptest.Until(t, a, "do-not-disturb for the share", func() bool {
		f := svc.Focus()
		return f.On && f.Reason == notifications.FocusSharing
	})
	f.set()
	apptest.Until(t, a, "do-not-disturb off after the share", func() bool { return !svc.DND() })
}
