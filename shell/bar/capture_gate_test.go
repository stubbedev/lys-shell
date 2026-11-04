package bar

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/shell/screenshot"
	"github.com/stubbedev/lys-shell/swaytest"
)

// The shell's real wiring over the bus, with real captures on a private
// compositor: the overlays startCapture builds share one gate, so during a
// region selection a color pick and an instant capture are refused, and
// Dismiss closes the overlay of the handle it names. Once the sessions ended
// every kind of request works again.
func TestHeadlessCaptureServicesAdmitOneOverlaySession(t *testing.T) {
	swaytest.Start(t)
	bus := dbustest.Session(t)
	sess, err := app.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sess.Close)
	application := app.NewApplication(sess)
	cfg := config.Defaults()
	cfg.Screenshot.OutputDirectory = t.TempDir()
	cfg.Screenshot.CopyToClipboard = false
	cfg.Screenshot.Notify = false
	ctx := styledContext(t, cfg)
	svc := startCapture(application, sess.Outputs, func() *config.Config { return cfg }, nil, nil, ctx.Font, ctx.Theme.Sheet)
	defer svc.close()
	runLoop(t, application)
	open := func() bool {
		return svc.host.Gate.Open()
	}
	awaitOpen := func(want bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); open() != want; time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("an overlay session open never became %v", want)
			}
		}
	}
	call, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := screenshot.NewClient(bus.Conn(t))
	mine := portaldialogs.Caller{Handle: "/r/mine"}
	other := portaldialogs.Caller{Handle: "/r/other"}

	selection := make(chan string, 1)
	go func() {
		path, err := client.Capture(call, mine, screenshot.ModeRegion, "")
		if err != nil {
			t.Errorf("the region request failed: %v", err)
		}
		selection <- path
	}()
	awaitOpen(true)
	if _, picked, err := client.PickColor(call, other); !errors.Is(err, screenshot.ErrBusy) || picked {
		t.Fatalf("a pick during a selection = %v %v, want ErrBusy", picked, err)
	}
	if path, err := client.Capture(call, other, screenshot.ModeRegion, ""); !errors.Is(err, screenshot.ErrBusy) || path != "" {
		t.Fatalf("a second region = %q %v, want ErrBusy", path, err)
	}
	if path, err := client.Capture(call, other, screenshot.ModeOutput, sess.Outputs()[0].Name); !errors.Is(err, screenshot.ErrBusy) || path != "" {
		t.Fatalf("an output capture during a selection = %q %v, want ErrBusy", path, err)
	}
	if err := client.Dismiss(call, other.Handle); err != nil {
		t.Fatal(err)
	}
	apptest.Never(t, "a refused request's Dismiss ended the selection", selection)
	if err := client.Dismiss(call, mine.Handle); err != nil {
		t.Fatal(err)
	}
	select {
	case path := <-selection:
		if path != "" {
			t.Fatalf("the dismissed selection saved %q", path)
		}
	case <-call.Done():
		t.Fatal("Dismiss left the selection open")
	}

	awaitOpen(false)
	pick := make(chan bool, 1)
	go func() {
		_, picked, err := client.PickColor(call, mine)
		if err != nil {
			t.Errorf("the pick after the selection failed: %v", err)
		}
		pick <- picked
	}()
	awaitOpen(true)
	if path, err := client.Capture(call, other, screenshot.ModeRegion, ""); !errors.Is(err, screenshot.ErrBusy) || path != "" {
		t.Fatalf("a region during a pick = %q %v, want ErrBusy", path, err)
	}
	if err := client.Dismiss(call, mine.Handle); err != nil {
		t.Fatal(err)
	}
	select {
	case picked := <-pick:
		if picked {
			t.Fatal("the dismissed pick picked")
		}
	case <-call.Done():
		t.Fatal("Dismiss left the pick open")
	}
	path, err := client.Capture(call, portaldialogs.Caller{}, screenshot.ModeOutput, sess.Outputs()[0].Name)
	if err != nil || path == "" {
		t.Fatalf("an output capture after the sessions = %q %v", path, err)
	}
}
