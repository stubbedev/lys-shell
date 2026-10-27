package bar

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/draw"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/transfer"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/dbustest"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/internal/swaytest"
	"github.com/stubbedev/lys-shell/service/clipboard"
	"github.com/stubbedev/lys-shell/shell/screenshot"
)

func runLoop(t *testing.T, application *app.Application) func(func()) {
	t.Helper()
	application.Hold()
	done := make(chan error, 1)
	go func() { done <- application.Run() }()
	t.Cleanup(func() {
		application.Invoke(application.Quit)
		if err := <-done; err != nil && !errors.Is(err, app.ErrClosed) {
			t.Errorf("the loop ended with %v", err)
		}
	})
	return func(fn func()) {
		ran := make(chan struct{})
		application.Invoke(func() {
			fn()
			close(ran)
		})
		<-ran
	}
}

type selectionRead struct {
	data []byte
	err  error
}

func watchClipboardPNG(t *testing.T) <-chan selectionRead {
	t.Helper()
	sess, err := app.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sess.Close)
	reader := app.NewApplication(sess)
	dc, err := reader.DataControl()
	if err != nil {
		t.Fatalf("the data-control reader: %v", err)
	}
	reads := make(chan selectionRead, 8)
	dc.OnSelection(func(sel app.Selection, offer *app.SelectionOffer) {
		if sel != app.SelectionClipboard || offer == nil || !offer.Has(transfer.MimePNG) {
			return
		}
		if err := offer.Read(transfer.MimePNG, 1<<30, func(data []byte, err error) { reads <- selectionRead{data, err} }); err != nil {
			reads <- selectionRead{err: err}
		}
	})
	runLoop(t, reader)
	return reads
}

func decodePNG(t *testing.T, data []byte) *image.RGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	rgba := image.NewRGBA(img.Bounds())
	draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)
	return rgba
}

func TestHeadlessScreenshotOverDBusLandsOnTheClipboardWithoutInput(t *testing.T) {
	swaytest.Start(t)
	bus := dbustest.Session(t)
	sess, err := app.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sess.Close)
	application := app.NewApplication(sess)
	application.SetClipboard(app.NewClipboard(sess))
	history, err := clipboard.DefaultHistory(clipboard.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	clip, err := clipboard.Start(application, history)
	if err != nil {
		t.Fatalf("the shell's clipboard on sway: %v", err)
	}
	cfg := config.Defaults()
	cfg.Screenshot.OutputDirectory = t.TempDir()
	ctx := styledContext(t, cfg)
	svc := startCapture(application, sess.Outputs, func() *config.Config { return cfg }, nil, clip, ctx.Font, ctx.Theme.Sheet)
	defer svc.close()
	notices := make(chan screenshot.Notice, 1)
	svc.host.Notify = func(n screenshot.Notice) { notices <- n }
	outputs := sess.Outputs()
	if len(outputs) == 0 || outputs[0].Name == "" {
		t.Fatalf("the headless sway shows outputs %v", outputs)
	}
	onLoop := runLoop(t, application)
	defer onLoop(clip.Close)

	var fallbackErr error
	onLoop(func() {
		fallbackErr = putOnClipboard(application, nil, transfer.Bytes(transfer.MimePNG, []byte("\x89PNG")))
	})
	if fallbackErr == nil {
		t.Fatal("wl_data_device claimed the selection with no input event behind it")
	}

	reads := watchClipboardPNG(t)
	ctxCall, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	path, err := screenshot.NewClient(bus.Conn(t)).Capture(ctxCall, portalreq.Caller{}, screenshot.ModeOutput, outputs[0].Name)
	if err != nil {
		t.Fatalf("Capture over D-Bus: %v", err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := decodePNG(t, saved)

	for i := range 3 {
		if i > 0 {
			reads = watchClipboardPNG(t)
		}
		select {
		case r := <-reads:
			if r.err != nil {
				t.Fatalf("reader %d reading the selection: %v", i, r.err)
			}
			got := decodePNG(t, r.data)
			if got.Bounds() != want.Bounds() || !bytes.Equal(got.Pix, want.Pix) {
				t.Fatalf("reader %d got a %v image unlike the saved %v capture", i, got.Bounds(), want.Bounds())
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("no image/png selection reached data-control reader %d", i)
		}
	}

	select {
	case n := <-notices:
		if n.Body != path {
			t.Fatalf("the notification reads %q, want just the path", n.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notification")
	}
	snap := clip.Entries()
	defer snap.Release()
	if entries := snap.Entries(); len(entries) != 1 || entries[0].Mime != transfer.MimePNG {
		t.Fatalf("the history holds %v, want the screenshot as image/png", entries)
	}
}

func TestHeadlessScreenshotCopyFailureReachesTheNotification(t *testing.T) {
	swaytest.Start(t)
	bus := dbustest.Session(t)
	sess, err := app.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sess.Close)
	application := app.NewApplication(sess)
	application.SetClipboard(app.NewClipboard(sess))
	cfg := config.Defaults()
	cfg.Screenshot.OutputDirectory = t.TempDir()
	ctx := styledContext(t, cfg)
	svc := startCapture(application, sess.Outputs, func() *config.Config { return cfg }, nil, nil, ctx.Font, ctx.Theme.Sheet)
	defer svc.close()
	notices := make(chan screenshot.Notice, 1)
	svc.host.Notify = func(n screenshot.Notice) { notices <- n }
	outputs := sess.Outputs()
	if len(outputs) == 0 {
		t.Fatal("the headless sway shows no outputs")
	}
	runLoop(t, application)

	ctxCall, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	path, err := screenshot.NewClient(bus.Conn(t)).Capture(ctxCall, portalreq.Caller{}, screenshot.ModeOutput, outputs[0].Name)
	if err != nil {
		t.Fatalf("a failed copy failed the capture: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a failed copy lost the saved file: %v", err)
	}
	select {
	case n := <-notices:
		if n.Summary != "Screenshot saved" || !strings.HasPrefix(n.Body, path+"\nNot copied to the clipboard: ") {
			t.Fatalf("the notification reads %q / %q, want the path and the failed copy", n.Summary, n.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notification")
	}
}
