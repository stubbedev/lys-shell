package bar

import (
	"context"
	"log"
	"os"
	"os/user"
	"path/filepath"

	"github.com/godbus/dbus/v5"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/desktopentry"
	"github.com/stubbedev/lys-shell/internal/fileuri"
	"github.com/stubbedev/lys-shell/internal/ipp"
	"github.com/stubbedev/lys-shell/internal/mime"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/internal/xdg"
	"github.com/stubbedev/lys-shell/shell/filechooser"
	"github.com/stubbedev/lys-shell/shell/portaldialogs"
	"github.com/stubbedev/lys-shell/shell/printdialog"
)

// servePortalHosts hosts the shell's halves of the portal backend on
// one session bus connection: dev.stubbe.lys.PortalDialogs1 (access,
// account, app chooser, dynamic launcher and the wallpaper preview;
// services/portal_dialogs), dev.stubbe.lys.FileChooser1 (services/
// file_chooser) and dev.stubbe.lys.Print1 (services/print, spooling over
// CUPS' IPP). A bus failure leaves the portal answering those requests
// "other". The returned stop ends the hosts once the loop has ended,
// freeing what the file chooser still holds.
func servePortalHosts(application *app.Application, outputs func() []*app.Output, current func() *config.Config, focused func() (string, bool), font render.Font, sheet *widget.Stylesheet) (stop func()) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		log.Printf("portal hosts: session bus: %v", err)
		return func() {}
	}
	exported := 0
	home, _ := os.UserHomeDir()
	viewFile := ""
	if dir, ok := xdg.StateDir(); ok {
		viewFile = filepath.Join(dir, "file-chooser-view")
	}
	chooser := filechooser.New(filechooser.Deps{
		Config:         current,
		Open:           layerOpener[filechooser.Window](application),
		Invoke:         application.Invoke,
		Font:           font,
		Sheet:          sheet,
		MIME:           mime.System(),
		Home:           home,
		Places:         func() []filechooser.Place { return filechooser.UserPlaces(home) },
		Locations:      filechooser.SystemLocations(home),
		WatchLocations: filechooser.WatchSystemLocations,
		ViewFile:       viewFile,
		RecentFile:     filechooser.RecentFile(),
		TrashDirs:      filechooser.SystemTrashDirs(home),
		Target:         func() *app.Output { return dialogOutput(outputs(), focused) },
		ForgetImage:    application.ForgetImage,
	})
	if _, err := filechooser.NewDaemon(chooser).Export(conn); err != nil {
		log.Printf("file chooser: daemon: %v", err)
	} else {
		exported++
	}
	dialogs := portaldialogs.New(portaldialogs.Deps{
		Config:     current,
		Open:       layerOpener[portaldialogs.Window](application),
		Invoke:     application.Invoke,
		Font:       font,
		Sheet:      sheet,
		Candidates: portaldialogs.CandidateApps,
		SetDefault: desktopentry.SetDefault,
		PickImage:  func(chosen func(string)) { pickImage(application, chooser, chosen) },
		Describe:   describeApp,
	})
	if _, err := portaldialogs.NewDaemon(dialogs).Export(conn); err != nil {
		log.Printf("portal dialogs: daemon: %v", err)
	} else {
		exported++
	}
	cups := ipp.NewClient(ipp.DefaultServer())
	printer := printdialog.New(printdialog.Deps{
		Config:     current,
		Open:       layerOpener[printdialog.Window](application),
		Invoke:     application.Invoke,
		Font:       font,
		Sheet:      sheet,
		Printers:   cups.Printers,
		Spool:      func(ctx context.Context, j ipp.Job) error { _, err := cups.Print(ctx, j); return err },
		PickOutput: func(suggested string, chosen func(string)) { pickOutput(application, chooser, suggested, chosen) },
		Home:       home,
		User:       currentUser(),
	})
	if _, err := printdialog.NewDaemon(printer).Export(conn); err != nil {
		log.Printf("print: daemon: %v", err)
	} else {
		exported++
	}
	if exported == 0 {
		_ = conn.Close()
	}
	return chooser.Close
}

// dialogOutput is the output a portal dialog maps on: the focused one,
// as the app asking is normally the one focused, else the first; nil
// with no output.
func dialogOutput(outs []*app.Output, focused func() (string, bool)) *app.Output {
	if name, ok := focused(); ok {
		for _, o := range outs {
			if o.Name == name {
				return o
			}
		}
	}
	if len(outs) == 0 {
		return nil
	}
	return outs[0]
}

// pickImage lets the user choose a picture with the file chooser, for a
// dialog that offers to change one, and hands the path to chosen on the
// loop. The chooser waits on the user, so it runs off the loop.
func pickImage(application *app.Application, chooser *filechooser.Chooser, chosen func(string)) {
	images := filechooser.Filter{Name: "Images", Rules: []filechooser.Rule{{Kind: filechooser.RuleMIME, Value: "image/*"}}}
	go func() {
		uris := chooser.Open(portalreq.Caller{Modal: true}, filechooser.OpenRequest{Title: "Choose a picture", Filters: []filechooser.Filter{images}}).URIs
		if len(uris) == 0 {
			return
		}
		if path, ok := fileuri.Path(uris[0]); ok {
			application.Invoke(func() { chosen(path) })
		}
	}()
}

// pickOutput lets the user choose the file Print to File writes with
// the file chooser, starting from suggested, and hands the path to
// chosen on the loop. The chooser waits on the user, so it runs off the
// loop.
func pickOutput(application *app.Application, chooser *filechooser.Chooser, suggested string, chosen func(string)) {
	go func() {
		uris := chooser.Save(portalreq.Caller{Modal: true}, filechooser.SaveRequest{
			Title: "Print to File", CurrentName: filepath.Base(suggested), CurrentFolder: filepath.Dir(suggested),
		}).URIs
		if len(uris) == 0 {
			return
		}
		if path, ok := fileuri.Path(uris[0]); ok {
			application.Invoke(func() { chosen(path) })
		}
	}()
}

// describeApp names the app a portal request comes from, from its
// desktop entry.
func describeApp(appID string) (name, icon string) {
	return desktopentry.Describe(desktopentry.ApplicationDirs(), appID)
}

// currentUser names print jobs' owner: $USER, else the account.
func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

// layerOpener maps a host's overlay as its own Window type; a failure
// is a nil interface, never a typed nil.
func layerOpener[W interface{ Close() }](application *app.Application) func(app.LayerConfig) (W, error) {
	return func(cfg app.LayerConfig) (W, error) {
		w, err := application.NewLayer(cfg)
		if err != nil {
			var none W
			return none, err
		}
		// Always holds: the assertions below pin every host's Window.
		win, _ := any(w).(W)
		return win, nil
	}
}

// Every host's Window is a layer window.
var (
	_ portaldialogs.Window = (*app.LayerWindow)(nil)
	_ filechooser.Window   = (*app.LayerWindow)(nil)
	_ printdialog.Window   = (*app.LayerWindow)(nil)
)
