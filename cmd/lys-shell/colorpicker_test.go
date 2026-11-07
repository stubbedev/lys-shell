package main

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/colorpicker"
	"github.com/stubbedev/lys-shell/shell/regionoverlay"
	"github.com/stubbedev/lys-shell/shell/screenshot"
)

// stubPicker answers every pick with its color in the format asked for,
// or cancels, recording the options it was given.
type stubPicker struct {
	color  colorpicker.RGB
	cancel bool
	opts   *colorpicker.Options
}

func (s stubPicker) Request(_ context.Context, _ regionoverlay.Frames, opts colorpicker.Options) (colorpicker.Pick, bool, error) {
	*s.opts = opts
	if s.cancel {
		return colorpicker.Pick{}, false, nil
	}
	return colorpicker.Pick{Color: s.color, Format: opts.Format}, true, nil
}

// waylandListener stands in for the compositor's socket, counting every
// client that connects to it.
func waylandListener(t *testing.T) *atomic.Int32 {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("WAYLAND_DISPLAY", "wayland-test")
	l, err := net.Listen("unix", filepath.Join(dir, "wayland-test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	var connects atomic.Int32
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			connects.Add(1)
			_ = c.Close()
		}
	}()
	return &connects
}

func colorpickerShell(t *testing.T, picker stubPicker) *[]string {
	t.Helper()
	dbustest.Session(t)
	var copied []string
	host := &screenshot.Host{Picker: picker, CopyText: func(s string) error {
		copied = append(copied, s)
		return nil
	}}
	export(t, screenshot.NewDaemon(host).Export)
	return &copied
}

func TestColorpickerPrintsTheFormatAskedForWithoutCopying(t *testing.T) {
	connects := waylandListener(t)
	var opts colorpicker.Options
	copied := colorpickerShell(t, stubPicker{color: colorpicker.RGB{R: 255, G: 128}, opts: &opts})
	stdout, stderr, code := runCaptured(t, false, "colorpicker", "--format", "rgb", "--no-copy", "--no-loupe")
	if code != 0 || stdout != "rgb(255, 128, 0)\n" || stderr != "" {
		t.Fatalf("code %d stdout %q stderr %q", code, stdout, stderr)
	}
	if len(*copied) != 0 {
		t.Errorf("--no-copy copied %q", *copied)
	}
	if opts != (colorpicker.Options{Format: config.ColorFormatRGB, NoLoupe: true}) {
		t.Errorf("the shell was asked for %+v", opts)
	}
	if n := connects.Load(); n != 0 {
		t.Errorf("the command made %d Wayland connections of its own, want the shell's overlay alone", n)
	}
}

func TestColorpickerCopiesWhatItPrints(t *testing.T) {
	var opts colorpicker.Options
	copied := colorpickerShell(t, stubPicker{color: colorpicker.RGB{R: 255}, opts: &opts})
	stdout, _, code := runCaptured(t, false, "colorpicker", "-f", "hsl")
	if code != 0 || stdout != "hsl(0, 100%, 50%)\n" || strings.Join(*copied, ",") != "hsl(0, 100%, 50%)" {
		t.Fatalf("code %d stdout %q copied %q", code, stdout, *copied)
	}
	if opts.NoLoupe {
		t.Error("the loupe was turned off unasked")
	}
}

func TestColorpickerCancelledPrintsNothingAndExits1(t *testing.T) {
	var opts colorpicker.Options
	copied := colorpickerShell(t, stubPicker{cancel: true, opts: &opts})
	stdout, stderr, code := runCaptured(t, false, "colorpicker")
	if code != 1 || stdout != "" || stderr != "" || len(*copied) != 0 {
		t.Fatalf("code %d stdout %q stderr %q copied %q", code, stdout, stderr, *copied)
	}
	if opts.Format != "" {
		t.Errorf("no --format asked for %q, want the picker's own", opts.Format)
	}
}

func TestColorpickerRefusesAnUnknownFormat(t *testing.T) {
	if _, stderr, code := runCaptured(t, false, "colorpicker", "--format", "lab"); code != 2 || !strings.Contains(stderr, "lab") {
		t.Errorf("code %d stderr %q, want the usage error", code, stderr)
	}
}

func TestColorpickerWithoutTheShellSaysSo(t *testing.T) {
	bus := dbustest.Start(t)
	bus.UseAsSessionBus(t)
	if _, stderr, code := runCaptured(t, false, "colorpicker"); code != 1 || !strings.Contains(stderr, "Screenshot service not running") {
		t.Errorf("code %d stderr %q", code, stderr)
	}
}
