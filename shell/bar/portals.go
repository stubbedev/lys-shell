package bar

import (
	"log"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/desktopentry"
	"github.com/stubbedev/lys-desktop/fileuri"
	"github.com/stubbedev/lys-desktop/identity"
	"github.com/stubbedev/lys-desktop/mime"
	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-lib/render"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/xdg"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/ipp"
	"github.com/stubbedev/lys-shell/service/dnssd"
	"github.com/stubbedev/lys-shell/service/udisks"
	"github.com/stubbedev/lys-shell/shell/dialogsurface"
	"github.com/stubbedev/lys-shell/shell/filechooser"
	"github.com/stubbedev/lys-shell/shell/portaldialogs/dialoghost"
	"github.com/stubbedev/lys-shell/shell/printdialog"
)

// userPlaces are the file chooser's favorite places in home, logging a
// user directory or bookmarks file that could not be read.
func userPlaces(home string) []filechooser.Place {
	places, err := filechooser.UserPlaces(xdg.OS, home)
	if err != nil {
		log.Printf("file chooser: places: %v", err)
	}
	return places
}

// authSignal sends what the user does in an authentication dialog no
// Authenticate call waits on, logging a signal the bus refused.
func authSignal(e *portaldialogs.Emitter) func(string, portaldialogs.AuthAction) {
	return func(handle string, a portaldialogs.AuthAction) {
		if err := e.AuthAction(handle, a); err != nil {
			log.Printf("portal dialogs: %v", err)
		}
	}
}

// printerRefresh is how often the print dialog reads the selected
// printer's state again.
const printerRefresh = 3 * time.Second

// servePortalHosts hosts the shell's halves of the portal backend on
// the shell's session bus connection: dev.stubbe.lys.PortalDialogs1 (access,
// account, app chooser, dynamic launcher and the wallpaper preview;
// services/portal_dialogs), dev.stubbe.lys.FileChooser1 (services/
// file_chooser) and dev.stubbe.lys.Print1 (services/print, spooling over
// CUPS' IPP). Without a bus the portal answers those requests
// "other". The returned stop ends the hosts once the loop has ended,
// freeing what the file chooser still holds.
func servePortalHosts(conn *dbus.Conn, application *app.Application, outputs func() []*app.Output, current func() *config.Config, focused func() (string, bool), font render.Font, sheet *widget.Stylesheet, system func() *dbus.Conn) (stop func()) {
	if conn == nil {
		log.Print("portal hosts: no session bus")
		return func() {}
	}
	home, _ := os.UserHomeDir()
	viewFile, presetsFile := "", ""
	if dir, ok := xdg.OS.StateDir(identity.Name); ok {
		viewFile, presetsFile = filepath.Join(dir, "file-chooser-view"), filepath.Join(dir, "print-presets")
	}
	chooser := filechooser.New(filechooser.Deps{
		Config:         current,
		Open:           dialogOpener[filechooser.Window](application),
		Invoke:         application.Invoke,
		KeySym:         application.KeySym,
		Font:           font,
		Sheet:          sheet,
		MIME:           mime.System(),
		Home:           home,
		Places:         func() []filechooser.Place { return userPlaces(home) },
		Locations:      filechooser.SystemLocations(home),
		WatchLocations: filechooser.WatchSystemLocations,
		Drives: func() *udisks.Client {
			if conn := system(); conn != nil {
				return udisks.New(conn)
			}
			return nil
		},
		ViewFile:      viewFile,
		BookmarksFile: filechooser.BookmarksFile(xdg.OS),
		RecentFile:    filechooser.RecentFile(),
		TrashDirs:     filechooser.SystemTrashDirs(home),
		Target:        func() *app.Output { return dialogOutput(outputs(), focused) },
		ForgetImage:   application.ForgetImage,
	})
	if _, err := filechooser.NewDaemon(chooser).Export(conn); err != nil {
		log.Printf("file chooser: daemon: %v", err)
	}
	dialogs := dialoghost.New(dialoghost.Deps{
		Config:     current,
		Open:       dialogOpener[dialoghost.Window](application),
		Invoke:     application.Invoke,
		KeySym:     application.KeySym,
		Font:       font,
		Sheet:      sheet,
		Candidates: dialoghost.CandidateApps,
		SetDefault: desktopentry.SetDefault,
		PickImage:  func(chosen func(string)) { pickImage(application, chooser, chosen) },
		Describe:   describeApp,
		Signal:     authSignal(portaldialogs.NewEmitter(conn)),
	})
	if _, err := portaldialogs.NewDaemon(dialogs).Export(conn); err != nil {
		log.Printf("portal dialogs: daemon: %v", err)
	}
	cups := ipp.NewClient(ipp.DefaultServer())
	printer := printdialog.New(printdialog.Deps{
		Config:       current,
		Open:         dialogOpener[printdialog.Window](application),
		Invoke:       application.Invoke,
		KeySym:       application.KeySym,
		Font:         font,
		Sheet:        sheet,
		CUPS:         cups,
		LPOptions:    filepath.Join(home, ".cups", "lpoptions"),
		RefreshEvery: printerRefresh,
		PresetsFile:  presetsFile,
		DNSSD: func() *dnssd.Client {
			if conn := system(); conn != nil {
				return dnssd.New(conn)
			}
			return nil
		},
		Device:     ipp.DeviceAttributes,
		PickOutput: func(suggested string, chosen func(string)) { pickOutput(application, chooser, suggested, chosen) },
		Home:       home,
		User:       currentUser(),
	})
	if _, err := printdialog.NewDaemon(printer).Export(conn); err != nil {
		log.Printf("print: daemon: %v", err)
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
		uris := chooser.Open(portaldialogs.Caller{Modal: true}, filechooser.OpenRequest{Title: "Choose a picture", Filters: []filechooser.Filter{images}}).URIs
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
		uris := chooser.Save(portaldialogs.Caller{Modal: true}, filechooser.SaveRequest{
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

// dialogOpener maps a host's dialog (dialogsurface.Open) as its own Window
// type; a failure is a nil interface, never a typed nil.
func dialogOpener[W interface{ Close() }](application *app.Application) func(dialogsurface.Spec) (W, error) {
	return func(s dialogsurface.Spec) (W, error) {
		w, err := dialogsurface.Open(application, s)
		if err != nil {
			var none W
			return none, err
		}
		// Always holds: the assertions below pin every host's Window.
		win, _ := any(w).(W)
		return win, nil
	}
}

// Every host's Window is a dialog surface.
var (
	_ dialoghost.Window  = dialogsurface.Window(nil)
	_ filechooser.Window = dialogsurface.Window(nil)
	_ printdialog.Window = dialogsurface.Window(nil)
)
