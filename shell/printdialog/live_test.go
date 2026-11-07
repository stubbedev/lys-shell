package printdialog

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/ipp"
	"github.com/stubbedev/lys-shell/internal/ipp/ipptest"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// idlePrinter is an idle, accepting queue's attributes.
func idlePrinter(extra ...ipp.Attr) []ipp.Attr {
	return append([]ipp.Attr{
		ipp.Enum("printer-state", ipp.StateIdle),
		{Name: "printer-is-accepting-jobs", Values: []ipp.Value{{Tag: ipp.TagBoolean, Int: 1}}},
		ipp.String(ipp.TagKeyword, "media-supported", "iso_a4_210x297mm", "iso_a5_148x210mm"),
	}, extra...)
}

// overIPP points the harness's dialog at a fake scheduler on loopback,
// reading printer state again every refresh.
func overIPP(t *testing.T, hs *harness, refresh time.Duration) *ipptest.Server {
	t.Helper()
	srv := ipptest.Start(t)
	hs.h.d.CUPS = srv.Client()
	hs.h.d.RefreshEvery = refresh
	return srv
}

// The dialog maps before the scheduler answers, listing Print to File
// under a "looking" row, and fills the list when the answer comes.
func TestTheDialogShowsBeforeTheScheduler(t *testing.T) {
	hs := newHarness(t)
	srv := overIPP(t, hs, time.Hour)
	srv.SetPrinter("office", idlePrinter()...)
	srv.Hold()
	go hs.h.Prepare(testCaller, PrepareRequest{Title: "Doc"})
	var c app.LayerConfig
	select {
	case c = <-hs.opened:
	case <-time.After(treetest.Liveness):
		t.Fatal("the dialog waited for the scheduler")
	}
	if replies := srv.Replies(); replies != 0 {
		t.Fatalf("the dialog mapped after %d replies, want before any", replies)
	}
	var labels []string
	hs.do(func() { labels = treetest.Labels(c.Root) })
	if !slices.Contains(labels, lookingForPrinters) || !slices.Contains(labels, fileQueue) || slices.Contains(labels, "office") {
		t.Errorf("before the answer the list reads %q", labels)
	}
	srv.Release()
	for deadline := time.Now().Add(treetest.Liveness); ; time.Sleep(time.Millisecond) {
		hs.do(func() { labels = treetest.Labels(c.Root) })
		if slices.Contains(labels, "office") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the list never filled: %q", labels)
		}
	}
	if slices.Contains(labels, lookingForPrinters) {
		t.Error("the looking row stayed after the answer")
	}
	hs.do(func() { treetest.Button(t, c.Root, "Cancel").OnClick() })
}

// The scheduler's default is selected, the user's lpoptions default
// over it, and the app's printer over both.
func TestTheDefaultPrinterIsSelected(t *testing.T) {
	selected := func(hs *harness, r PrepareRequest) string {
		t.Helper()
		got, c := hs.prepareWith(t, testCaller, r)
		var name string
		hs.do(func() {
			q, _ := hs.h.sheet.selected()
			name = q.name
			treetest.Button(t, c.Root, "Cancel").OnClick()
		})
		<-got
		return name
	}
	hs := newHarness(t)
	hs.printers = []ipp.Printer{{Name: "first"}, {Name: "second"}, {Name: "third"}}
	if got := selected(hs, PrepareRequest{}); got != "first" {
		t.Errorf("without a default %s is selected, want the first", got)
	}
	hs.def = "second"
	if got := selected(hs, PrepareRequest{}); got != "second" {
		t.Errorf("CUPS' default second: %s is selected", got)
	}
	lp := filepath.Join(t.TempDir(), "lpoptions")
	if err := os.WriteFile(lp, []byte("Dest second sides=one-sided\nDefault third/draft media=A4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hs.h.d.LPOptions = lp
	if got := selected(hs, PrepareRequest{}); got != "third" {
		t.Errorf("lpoptions' Default third: %s is selected", got)
	}
	if got := selected(hs, PrepareRequest{Settings: []Setting{{"printer", "first"}}}); got != "first" {
		t.Errorf("the app's printer first: %s is selected", got)
	}
	if got := selected(hs, PrepareRequest{Settings: []Setting{{"printer", "gone"}}}); got != "third" {
		t.Errorf("an app printer not listed: %s is selected, want the user's default", got)
	}
}

// The selected printer's state follows the scheduler while the dialog
// is open, in its row and without touching the form; closing the
// dialog stops asking.
func TestPrinterStateIsLive(t *testing.T) {
	hs := newHarness(t)
	srv := overIPP(t, hs, 10*time.Millisecond)
	srv.SetPrinter("office", idlePrinter(ipp.String(ipp.TagText, "printer-location", "Hall"))...)
	got, c := hs.prepare(t)
	dropdowns := treetest.All[*widget.Dropdown](c.Root)
	copies := treetest.All[*widget.SpinButton](c.Root)[0]
	var detail *widget.Label
	hs.do(func() {
		copies.SetValue(3)
		dropdowns[dropPaper].SetSelected(1)
		detail = hs.h.sheet.details[0]
	})
	srv.SetPrinter("office", ipp.Enum("printer-state", ipp.StateStopped),
		ipp.String(ipp.TagText, "printer-location", "Hall"),
		ipp.String(ipp.TagKeyword, "printer-state-reasons", "media-empty-error"),
		ipp.String(ipp.TagKeyword, "media-supported", "iso_a4_210x297mm", "iso_a5_148x210mm"))
	for deadline := time.Now().Add(treetest.Liveness); ; time.Sleep(time.Millisecond) {
		var text string
		hs.do(func() { text = detail.Text() })
		if text == "Hall · Paused · Out of paper" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the row still reads %q", text)
		}
	}
	hs.do(func() {
		if copies.Value() != 3 || dropdowns[dropPaper].Selection() != "A5" || hs.h.sheet.details[0] != detail {
			t.Errorf("the refresh rebuilt the form: %v copies on %q", copies.Value(), dropdowns[dropPaper].Selection())
		}
	})
	hs.do(func() { treetest.Button(t, c.Root, "Cancel").OnClick() })
	<-got
	asked := len(srv.Ops())
	time.Sleep(100 * time.Millisecond)
	if after := srv.Ops()[asked:]; len(after) != 0 {
		t.Errorf("the closed dialog asked %v", after)
	}
	for _, op := range srv.Ops() {
		if !slices.Contains([]uint16{ipp.OpCUPSGetPrinters, ipp.OpCUPSGetDefault, ipp.OpGetPrinterAttributes}, op) {
			t.Errorf("the dialog asked %#04x", op)
		}
	}
	if !strings.Contains(printerDetail(ipp.Printer{State: ipp.StateIdle, Accepting: true, Markers: []ipp.Marker{{Name: "Cyan", Level: 4}}}), "Low: Cyan 4%") {
		t.Error("a low supply is not named")
	}
}
