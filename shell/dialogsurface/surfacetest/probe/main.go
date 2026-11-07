// Command probe maps one app window for surfacetest, in a process of
// its own as a real app's is, and reports on stdout the handle it is
// exported as ("handle <h>") and whether it holds the keyboard focus
// ("keyboard true|false"), each when it changes, until it is killed.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
)

const reportEvery = 20 * time.Millisecond

func main() {
	appID := flag.String("app-id", "", "the window's app id")
	flag.Parse()
	if *appID == "" {
		log.Fatal("probe: -app-id is required")
	}
	sess, err := app.Connect()
	if err != nil {
		log.Fatalf("probe: %v", err)
	}
	defer sess.Close()
	a := app.NewApplication(sess)
	win, err := a.NewWindow(app.WindowConfig{Title: *appID, AppID: *appID, Root: widget.NewDrawingArea(240, 180)})
	if err != nil {
		log.Fatalf("probe: map the window: %v", err)
	}
	handle, keyboard, reported := "", false, false
	a.Every(reportEvery, func() {
		if h := win.ExportHandle(); h != handle {
			handle = h
			fmt.Println("handle", h)
		}
		focus := sess.KeyboardFocus()
		if k := focus != nil && focus == win.HostSurface(); k != keyboard || !reported {
			keyboard, reported = k, true
			fmt.Println("keyboard", k)
		}
	})
	if err := a.Run(); err != nil && !errors.Is(err, app.ErrClosed) {
		log.Fatalf("probe: %v", err)
	}
}
