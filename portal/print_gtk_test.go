package portal

import (
	"bytes"
	"compress/zlib"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/shell/treetest"
	"github.com/stubbedev/lys-desktop/swaytest"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/ipp"
	"github.com/stubbedev/lys-shell/internal/ipp/ipptest"
	"github.com/stubbedev/lys-shell/shell/dialogsurface"
	"github.com/stubbedev/lys-shell/shell/printdialog"
)

type gtkWindow struct{}

func (gtkWindow) Close() {}

// gtkPrintRig is a real GTK app's road to a printer: the real frontend,
// the backend, the shell's print dialog spooling to a fake scheduler,
// and a headless compositor for the app's display.
type gtkPrintRig struct {
	r      *rig
	srv    *ipptest.Server
	home   string
	opened chan widget.Widget
}

func newGTKPrintRig(t *testing.T) *gtkPrintRig {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatal("python3 is not on PATH: the dev shell carries it with PyGObject, GTK 3 and GTK 4")
	}
	swaytest.Start(t)
	g := &gtkPrintRig{r: newRig(t, nil), srv: ipptest.Start(t), home: t.TempDir(), opened: make(chan widget.Widget, 1)}
	g.srv.SetPrinter("office",
		ipp.Enum("printer-state", ipp.StateIdle),
		ipp.Attr{Name: "printer-is-accepting-jobs", Values: []ipp.Value{{Tag: ipp.TagBoolean, Int: 1}}},
		ipp.String(ipp.TagKeyword, "media-supported", "iso_a4_210x297mm"),
		ipp.Attr{Name: "number-up-supported", Values: []ipp.Value{{Tag: ipp.TagInteger, Int: 1}, {Tag: ipp.TagInteger, Int: 2}, {Tag: ipp.TagInteger, Int: 4}}},
	)
	cfg := config.Defaults()
	cfg.Animations.Enabled = false
	dialog := printdialog.New(printdialog.Deps{
		Config: func() *config.Config { return cfg },
		Open: func(s dialogsurface.Spec) (printdialog.Window, error) {
			g.opened <- s.Overlay.Root
			return gtkWindow{}, nil
		},
		Invoke: func(fn func()) {
			treetest.Loop.Lock()
			defer treetest.Loop.Unlock()
			fn()
		},
		KeySym:       treetest.KeySym,
		Font:         treetest.Font(t),
		CUPS:         g.srv.Client(),
		RefreshEvery: time.Hour,
		Home:         g.home,
		User:         "u",
	})
	release, err := printdialog.NewDaemon(dialog).Export(g.r.bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	startFrontend(t, g.r, t.TempDir())
	return g
}

// gtkPages is how many pages the app's document has.
const gtkPages = 6

// print runs a GTK app of version gtk printing its document through
// the portal, lets choose fill in the dialog once it lists the office
// printer, confirms it, waits for landed to hold (the app keeps running,
// as GTK 3 never says its job finished), and returns the pages the app
// drew, in the order it drew them.
func (g *gtkPrintRig) print(t *testing.T, gtk string, choose func(root widget.Widget), landed func() bool) []int {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr lockedBuffer
	cmd := exec.CommandContext(ctx, "python3", "-I", "testdata/gtkprint.py", gtk, strconv.Itoa(gtkPages))
	cmd.Env = append(os.Environ(),
		"DBUS_SESSION_BUS_ADDRESS="+g.r.bus.Address,
		"GDK_BACKEND=wayland", "GDK_DEBUG=portals", "GTK_USE_PORTAL=1",
		"GSK_RENDERER=cairo", "GTK_A11Y=none", "NO_AT_BRIDGE=1",
	)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer func() {
		cancel()
		<-exited
	}()
	var root widget.Widget
	select {
	case root = <-g.opened:
	case err := <-exited:
		exited <- err
		t.Fatalf("GTK %s ended before its dialog opened: %v\n%s", gtk, err, stderr.String())
	case <-time.After(30 * time.Second):
		t.Fatalf("GTK %s never opened the dialog:\n%s", gtk, stderr.String())
	}
	for deadline := time.Now().Add(treetest.Liveness); ; time.Sleep(time.Millisecond) {
		treetest.Loop.Lock()
		listed := slices.Contains(treetest.Labels(root), "office")
		treetest.Loop.Unlock()
		if listed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the dialog never listed the office printer")
		}
	}
	treetest.Loop.Lock()
	choose(root)
	treetest.Button(t, root, "Print").OnClick()
	treetest.Loop.Unlock()
	sent := func() bool { return strings.HasSuffix(stdout.String(), "sent\n") }
	for deadline := time.Now().Add(30 * time.Second); !landed() || !sent(); time.Sleep(5 * time.Millisecond) {
		select {
		case err := <-exited:
			exited <- err
			t.Fatalf("GTK %s ended before its document landed: %v\n%s%s", gtk, err, stdout.String(), stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("GTK %s's document never landed:\n%s%s", gtk, stdout.String(), stderr.String())
		}
	}
	lines := strings.Fields(stdout.String())
	if len(lines) == 0 || lines[len(lines)-1] != "sent" {
		t.Fatalf("GTK %s printed %q", gtk, stdout.String())
	}
	drawn := make([]int, 0, len(lines)-1)
	for _, line := range lines[:len(lines)-1] {
		n, err := strconv.Atoi(line)
		if err != nil {
			t.Fatalf("GTK %s printed %q", gtk, stdout.String())
		}
		drawn = append(drawn, n)
	}
	return drawn
}

// dropdownWith is the form's dropdown offering item.
func dropdownWith(t *testing.T, root widget.Widget, item string) *widget.Dropdown {
	t.Helper()
	for _, d := range treetest.All[*widget.Dropdown](root) {
		if slices.Contains(d.Items(), item) {
			return d
		}
	}
	t.Fatalf("no dropdown offers %q", item)
	return nil
}

func choose(t *testing.T, root widget.Widget, item string) {
	t.Helper()
	d := dropdownWith(t, root, item)
	d.SetSelected(slices.Index(d.Items(), item))
}

var (
	pdfStream   = regexp.MustCompile(`(?s)stream\r?\n(.*?)endstream`)
	pdfPage     = regexp.MustCompile(`/Type\s*/Page[^s]`)
	pdfMediaBox = regexp.MustCompile(`/MediaBox\s*\[\s*[\d.]+\s+[\d.]+\s+([\d.]+)\s+([\d.]+)\s*\]`)
)

// sheets reads a PDF's page count and whether every page is wider than
// tall, through its compressed object streams.
func sheets(t *testing.T, pdf []byte) (int, bool) {
	t.Helper()
	text := slices.Clone(pdf)
	for _, m := range pdfStream.FindAllSubmatch(pdf, -1) {
		zr, err := zlib.NewReader(bytes.NewReader(m[1]))
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(zr)
		text = append(text, b...)
	}
	boxes := pdfMediaBox.FindAllSubmatch(text, -1)
	landscape := len(boxes) > 0
	for _, b := range boxes {
		w, _ := strconv.ParseFloat(string(b[1]), 64)
		h, _ := strconv.ParseFloat(string(b[2]), 64)
		landscape = landscape && w > h
	}
	return len(pdfPage.FindAll(text, -1)), landscape
}

// Each layout choice is applied once, by the app or by the printer,
// never both: a GTK 3 or GTK 4 app printing through the real portal
// draws its pages, and the shell spools what it sent with the job
// attributes for the rest. Print to File leaves the whole layout to
// the app.
func TestGTKAppsApplyTheLayoutOnce(t *testing.T) {
	g := newGTKPrintRig(t)
	all := []int{0, 1, 2, 3, 4, 5}
	for _, tc := range []struct {
		name   string
		choose func(t *testing.T, root widget.Widget)
		// drawn are the pages the app draws; sheets the pages of the
		// document it sends, landscapeIn the GTK versions whose pages are
		// wider than tall (GTK 3 turns a landscape page on a portrait one).
		drawn       []int
		sheets      int
		landscapeIn []string
		// attrs are the job attributes the printer gets, absent those
		// it must not; nil attrs and absent mean no job at all.
		attrs  map[string]string
		absent []string
		// file is whether the document goes to Print to File.
		file bool
	}{
		{
			name:   "four pages per sheet",
			choose: func(t *testing.T, root widget.Widget) { choose(t, root, "4") },
			drawn:  all, sheets: gtkPages,
			attrs:  map[string]string{"number-up": "4", "number-up-layout": "lrtb"},
			absent: []string{"page-set", "output-order", "page-ranges", "orientation-requested"},
		},
		{
			name: "even pages reversed",
			choose: func(t *testing.T, root widget.Widget) {
				choose(t, root, "Even pages only")
				treetest.All[*widget.CheckButton](root)[0].SetChecked(true)
			},
			drawn: all, sheets: gtkPages,
			attrs:  map[string]string{"page-set": "even", "output-order": "reverse-order"},
			absent: []string{"number-up"},
		},
		{
			name: "two collated copies",
			choose: func(t *testing.T, root widget.Widget) {
				treetest.All[*widget.SpinButton](root)[0].SetValue(2)
				treetest.All[*widget.CheckButton](root)[1].SetChecked(true)
			},
			drawn: all, sheets: gtkPages,
			attrs: map[string]string{"copies": "2", "multiple-document-handling": "separate-documents-collated-copies"},
		},
		{
			name: "pages 2 to 3",
			choose: func(t *testing.T, root widget.Widget) {
				choose(t, root, "Range")
				treetest.First[*widget.Entry](t, root).SetText("2-3")
			},
			drawn: []int{1, 2}, sheets: 2,
			attrs:  map[string]string{"copies": "1"},
			absent: []string{"page-ranges"},
		},
		{
			name:   "landscape",
			choose: func(t *testing.T, root widget.Widget) { choose(t, root, "Landscape") },
			drawn:  all, sheets: gtkPages, landscapeIn: []string{"4.0"},
			attrs:  map[string]string{"copies": "1"},
			absent: []string{"orientation-requested"},
		},
		{
			name: "four pages per sheet to a file",
			choose: func(t *testing.T, root widget.Widget) {
				list := treetest.First[*widget.ListBox](t, root)
				list.Select(slices.Index(treetest.Labels(list), "Print to File"))
				choose(t, root, "4")
			},
			drawn: all, sheets: 2, file: true,
		},
	} {
		for _, gtk := range []string{"3.0", "4.0"} {
			t.Run(tc.name+"/GTK "+gtk, func(t *testing.T) {
				jobs := len(g.srv.Jobs())
				output := filepath.Join(g.home, "output.pdf")
				_ = os.Remove(output)
				landed := func() bool { return len(g.srv.Jobs()) > jobs }
				if tc.file {
					landed = func() bool { _, err := os.Stat(output); return err == nil }
				}
				drawn := g.print(t, gtk, func(root widget.Widget) { tc.choose(t, root) }, landed)
				if !slices.Equal(drawn, tc.drawn) {
					t.Errorf("the app drew pages %v, want %v", drawn, tc.drawn)
				}
				spooled := g.srv.Jobs()[jobs:]
				var doc []byte
				switch {
				case tc.file && len(spooled) != 0:
					t.Fatalf("Print to File spooled %d jobs", len(spooled))
				case tc.file:
					b, err := os.ReadFile(output)
					if err != nil {
						t.Fatal(err)
					}
					doc = b
				case len(spooled) != 1:
					t.Fatalf("%d jobs spooled, want 1", len(spooled))
				default:
					doc = spooled[0].Document
					job := slices.IndexFunc(spooled[0].Groups, func(g ipp.Group) bool { return g.Tag == ipp.GroupJob })
					if job < 0 {
						t.Fatal("the job carries no job attributes")
					}
					attrs := spooled[0].Groups[job]
					for name, want := range tc.attrs {
						got := attrs.Text(name)
						if a, ok := attrs.Get(name); ok && a.Values[0].Tag == ipp.TagInteger {
							got = strconv.Itoa(int(a.Values[0].Int))
						}
						if got != want {
							t.Errorf("%s = %q, want %q", name, got, want)
						}
					}
					for _, name := range tc.absent {
						if _, ok := attrs.Get(name); ok {
							t.Errorf("the job carries %s", name)
						}
					}
				}
				n, landscape := sheets(t, doc)
				wantLandscape := slices.Contains(tc.landscapeIn, gtk)
				if n != tc.sheets || landscape != wantLandscape {
					t.Errorf("the document has %d sheets (landscape %v), want %d (landscape %v)", n, landscape, tc.sheets, wantLandscape)
				}
			})
		}
	}
}
