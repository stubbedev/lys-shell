package settings

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/logging"
	"github.com/stubbedev/lys-shell/internal/portal"
	"github.com/stubbedev/lys-shell/shell/apptheme"
)

// appID is the window's application id (dev.stubbe.lys.settings, the
// desktop entry's name).
const appID = "dev.stubbe.lys.settings"

// Run loads the config and shows the settings window until it closes
// (main.rs). A config that cannot load is an error: the window edits
// it, so there is nothing to show without it.
func Run() error {
	// raise is set once the window exists; forwarded launches buffer
	// until Bind, after it.
	var raise func()
	ins, primary, err := claimInstance(app.OSInvocation(os.Args[1:]), func() { raise() })
	switch {
	case err != nil:
		// No socket for the guard: run anyway, as GApplication does
		// without a bus.
		log.Printf("settings: single instance: %v", err)
	case !primary:
		// The running window was raised in this launch's stead.
		return nil
	}
	svc, err := config.Open()
	if err != nil {
		return fmt.Errorf("cannot load config: %w", err)
	}
	defer svc.Close()
	app.SetLogger(logging.Library())
	sess, err := app.Connect()
	if err != nil {
		return fmt.Errorf("settings: connect: %w", err)
	}
	defer sess.Close()

	application := app.NewApplication(sess)
	defer apptheme.Setup(application)()
	cfg := svc.Config()
	theme := apptheme.New(cfg)
	theme.WatchUserStyles(application)
	face, err := app.Font(cfg.General.FontSans, 14)
	if err != nil {
		return fmt.Errorf("settings: font %q: %w", cfg.General.FontSans, err)
	}
	// The code views are GtkTextView's monospace (the family alias).
	mono, err := app.Font("monospace", 14)
	if err != nil {
		log.Printf("settings: monospace font: %v", err)
		mono = face
	}
	k := &kit{
		face: app.FontFallback(face), mono: app.FontFallback(mono), monoVariants: app.FontVariants(mono),
		store: store{svc}, invoke: application.Invoke,
		background: func(fn func()) { go fn() },
	}
	after := func(d time.Duration, fn func()) {
		time.AfterFunc(d, func() { application.Invoke(fn) })
	}
	w := newWindow(k, cfg, after)
	theme.Attach(w.root)

	win, err := application.NewWindow(app.WindowConfig{
		Title: i18n.Settings().Get("settings-title"), AppID: appID,
		Width: windowWidth, Height: windowHeight,
		Root: w.root, Background: theme.RenderPalette().Surface,
	})
	if err != nil {
		return fmt.Errorf("settings: window: %w", err)
	}
	w.onClose = win.Close
	if ins != nil {
		// A second launch raises this window (connect_activate's
		// present): its activation token focuses it when the launcher
		// gave one, else the window asks for attention.
		raise = win.RequestAttention
		ins.Bind(application)
	}
	k.pickers = windowPickers{app: application, win: win, theme: theme}
	w.onResetAll = func() { confirmResetAll(application, win, k) }

	// A change from any source - a row, the reset buttons, the config
	// file, another lys-shell - restyles the window and refreshes the page.
	cancel := svc.Subscribe(func(_, next *config.Config) {
		application.Invoke(func() {
			theme.SetConfig(next)
			w.refresh()
		})
	})
	defer cancel()
	return application.Run()
}

// confirmResetAll asks, then drops every runtime override
// (ConfirmResetAll, ExecuteResetAll).
func confirmResetAll(application *app.Application, parent *app.Window, k *kit) {
	if _, err := application.NewDialog(parent, resetAllDialog(k)); err != nil {
		log.Printf("settings: reset-all dialog: %v", err)
	}
}

// claimInstance is the dev.stubbe.lys.settings single-instance guard
// (set_application_id): the first launch is the primary, a later one
// forwards its invocation and exits.
// onRelaunch runs on the loop for each later launch.
func claimInstance(inv app.Invocation, onRelaunch func()) (*app.Instance, bool, error) {
	return app.ClaimInstance(app.InstanceConfig{
		AppID:         appID,
		OnCommandLine: func([]string, string) { onRelaunch() },
		OnOpen:        func([]string, string) { onRelaunch() },
	}, inv)
}

// windowPickers opens the editors' popovers and dialogs over the
// settings window, styled by its stylesheet.
type windowPickers struct {
	app   *app.Application
	win   *app.Window
	theme *apptheme.Theme
}

// popover opens content beside anchor, focus (its search entry)
// taking the keys as it opens, so typing filters at once.
func (p windowPickers) popover(anchor, content, focus widget.Widget) (closePopover func()) {
	bound, ok := anchor.(widget.Boundser)
	if !ok {
		return func() {}
	}
	if root, ok := content.(interface{ AttachStylesheet(*widget.Stylesheet) }); ok {
		p.theme.Attach(root)
	}
	pop, err := p.app.OpenPopover(p.win, pickerPopoverConfig(bound, content, focus))
	if err != nil {
		log.Printf("settings: popover: %v", err)
		return func() {}
	}
	return pop.Dismiss
}

// font opens lys-ui's font dialog for families alone (FontButton's
// family-only level), monospace ones only when monospace.
func (p windowPickers) font(family string, monospace bool, fn func(string)) {
	if _, err := p.app.FontChooserDialog(p.win, app.FontChooserConfig{
		Family: family, FamilyOnly: true, MonospaceOnly: monospace,
		OnFont: func(picked string, _ float64) { fn(picked) },
	}); err != nil {
		log.Printf("settings: font dialog: %v", err)
	}
}

// openFile asks the file chooser portal on the session bus, off the
// loop, and hands the pick back on it (FileDialog.open).
func (p windowPickers) openFile(fn func(path string)) {
	go func() {
		conn, err := dbus.ConnectSessionBus()
		if err != nil {
			log.Printf("settings: file chooser: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		path, err := portal.OpenFile(context.Background(), conn, "")
		if err != nil {
			if !errors.Is(err, portal.ErrCancelled) {
				log.Printf("settings: file chooser: %v", err)
			}
			return
		}
		p.app.Invoke(func() { fn(path) })
	}()
}

// pickerPopoverConfig is an editor's popover: it opens from the editor
// at lys-ui's popover gap, focus inside it taking the keys.
func pickerPopoverConfig(anchor widget.Boundser, content, focus widget.Widget) app.PopoverConfig {
	return app.PopoverConfig{Anchor: anchor, Content: content, Focus: focus, Gap: widget.PopoverGap}
}
