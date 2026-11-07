package main

import (
	"context"
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"

	"github.com/stubbedev/lys-shell/shell/regionoverlay"
	"github.com/stubbedev/lys-shell/shell/screenshot"
)

// stubRegion answers every region request err, not ok.
type stubRegion struct{ err error }

func (s stubRegion) Request(context.Context, regionoverlay.Frames) (regionoverlay.Selection, bool, error) {
	return regionoverlay.Selection{}, false, s.err
}

// A request refused while a capture overlay is open is an error naming that,
// not the "Screenshot cancelled" the user's cancel reports.
func TestScreenshotRefusedIsNotACancel(t *testing.T) {
	dbustest.Session(t)
	export(t, screenshot.NewDaemon(&screenshot.Host{Region: stubRegion{err: screenshot.ErrBusy}}).Export)
	stdout, stderr, code := runCaptured(t, false, "screenshot", "region")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "Screenshot not taken: another capture is in progress") {
		t.Fatalf("refused: code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestScreenshotCancelledStaysACancel(t *testing.T) {
	dbustest.Session(t)
	export(t, screenshot.NewDaemon(&screenshot.Host{Region: stubRegion{}}).Export)
	stdout, stderr, code := runCaptured(t, false, "screenshot", "region")
	if code != 0 || stdout != "Screenshot cancelled\n" || stderr != "" {
		t.Fatalf("cancelled: code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestScreenshotSubcommandsReachTheService(t *testing.T) {
	bus := dbustest.Start(t)
	bus.UseAsSessionBus(t)
	for _, args := range [][]string{{"region"}, {"window"}, {"output"}, {"output", "DP-1"}, {"text"}, {"qr"}} {
		_, stderr, code := runCaptured(t, false, append([]string{"screenshot"}, args...)...)
		if code != 1 || !strings.Contains(stderr, "Screenshot service not running. Start lys-shell shell first.") {
			t.Errorf("screenshot %v: code %d stderr %q, want the not-running error from the bus", args, code, stderr)
		}
	}
}

func TestScreenshotRejectsBadArguments(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"screen"}, // the daemon's composite mode is not a CLI subcommand
		{"region", "extra"},
		{"output", "DP-1", "DP-2"},
		{"selfie"},
	} {
		if _, _, code := runCaptured(t, false, append([]string{"screenshot"}, args...)...); code != 2 {
			t.Errorf("screenshot %v exited %d, want the usage error 2", args, code)
		}
	}
}

func TestScreenshotTextWithoutTesseractNamesIt(t *testing.T) {
	dbustest.Session(t)
	t.Setenv("PATH", t.TempDir())
	export(t, screenshot.NewDaemon(&screenshot.Host{Region: stubRegion{}}).Export)
	stdout, stderr, code := runCaptured(t, false, "screenshot", "text")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "tesseract is not installed") {
		t.Fatalf("code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestScreenshotQRCancelledStaysACancel(t *testing.T) {
	dbustest.Session(t)
	export(t, screenshot.NewDaemon(&screenshot.Host{Region: stubRegion{}}).Export)
	stdout, stderr, code := runCaptured(t, false, "screenshot", "qr")
	if code != 0 || stdout != "Screen reading cancelled\n" || stderr != "" {
		t.Fatalf("code %d stdout %q stderr %q", code, stdout, stderr)
	}
}
