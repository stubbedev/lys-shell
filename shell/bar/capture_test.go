package bar

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/transfer"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/clipboard"
	"github.com/stubbedev/lys-shell/shell/screenshot"
	"github.com/stubbedev/lys-shell/swaytest"
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

// pastePNG reads the regular selection as image/png through wl-paste, a
// data-control client in a process of its own, as cliphist and
// wl-clip-persist are.
func pastePNG(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "wl-paste", "--no-newline", "--type", transfer.MimePNG)
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("wl-paste: %w: %s", err, stderr.Bytes())
	}
	return out, nil
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

	ctxCall, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	path, err := screenshot.NewClient(bus.Conn(t)).Capture(ctxCall, portaldialogs.Caller{}, screenshot.ModeOutput, outputs[0].Name)
	if err != nil {
		t.Fatalf("Capture over D-Bus: %v", err)
	}
	select {
	case n := <-notices:
		if n.Body != path {
			t.Fatalf("the notification reads %q, want just the path", n.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notification")
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := decodePNG(t, saved)
	if _, err := exec.LookPath("wl-paste"); err != nil {
		t.Skip("wl-paste is not installed (the nix dev shell carries it)")
	}
	matches := func(reader string, data []byte, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", reader, err)
		}
		got := decodePNG(t, data)
		if got.Bounds() != want.Bounds() || !bytes.Equal(got.Pix, want.Pix) {
			t.Fatalf("%s got a %v image unlike the saved %v capture", reader, got.Bounds(), want.Bounds())
		}
	}
	for i := range 3 {
		data, err := pastePNG(t.Context())
		matches(fmt.Sprintf("reader %d", i), data, err)
	}
	type paste struct {
		data []byte
		err  error
	}
	for round := range 3 {
		pastes := make([]paste, 4)
		var wg sync.WaitGroup
		for i := range pastes {
			wg.Go(func() {
				data, err := pastePNG(t.Context())
				pastes[i] = paste{data, err}
			})
		}
		wg.Wait()
		for i, p := range pastes {
			matches(fmt.Sprintf("round %d, concurrent reader %d", round, i), p.data, p.err)
		}
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
	path, err := screenshot.NewClient(bus.Conn(t)).Capture(ctxCall, portaldialogs.Caller{}, screenshot.ModeOutput, outputs[0].Name)
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
