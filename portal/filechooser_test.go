package portal

import (
	"io"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbustest"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/shell/filechooser"
	"github.com/stubbedev/lys-shell/shell/printdialog"
)

// fakeChooser is the shell's file dialog.
type fakeChooser struct {
	gate
	uris   dbustest.Var[[]string]
	opened dbustest.Var[filechooser.OpenRequest]
	saved  dbustest.Var[filechooser.SaveRequest]
}

func (f *fakeChooser) Open(c portalreq.Caller, r filechooser.OpenRequest) []string {
	f.opened.Store(r)
	if f.enter(c) {
		return nil
	}
	return f.uris.Load()
}

func (f *fakeChooser) Save(c portalreq.Caller, r filechooser.SaveRequest) []string {
	f.saved.Store(r)
	if f.enter(c) {
		return nil
	}
	return f.uris.Load()
}

func serveChooser(t *testing.T, r *rig, uris ...string) *fakeChooser {
	t.Helper()
	f := &fakeChooser{}
	f.uris.Store(uris)
	release, err := filechooser.NewDaemon(f).Export(r.bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return f
}

const fileChooserIfaceName = "org.freedesktop.impl.portal.FileChooser."

func TestFileChooserOpen(t *testing.T) {
	r := newRig(t, nil)
	f := serveChooser(t, r, "file:///a.png", "file:///b.png")
	filters := []filechooser.Filter{{Name: "Images", Rules: []filechooser.Rule{{Kind: filechooser.RuleGlob, Value: "*.png"}, {Kind: filechooser.RuleMIME, Value: "image/jpeg"}}}}
	code, res := r.interactive(t, fileChooserIfaceName+"OpenFile", handle, "org.app", "", "Pick", Vardict{
		"multiple": dbus.MakeVariant(true), "filters": dbus.MakeVariant(filters),
		"current_folder": dbus.MakeVariant([]byte("/home/u\x00")),
	})
	if code != ResponseSuccess || !slices.Equal(res["uris"].Value().([]string), []string{"file:///a.png", "file:///b.png"}) {
		t.Errorf("OpenFile = %d %v", code, res)
	}
	got := f.opened.Load()
	if got.Title != "Pick" || !got.Multiple || got.Directory || got.CurrentFolder != "/home/u" || len(got.Filters) != 1 || got.Filters[0].Name != "Images" || !slices.Equal(got.Filters[0].Rules, filters[0].Rules) {
		t.Errorf("open request = %+v", got)
	}
	f.uris.Store(nil)
	if code, _ := r.interactive(t, fileChooserIfaceName+"OpenFile", handle, "org.app", "", "Pick", Vardict{}); code != ResponseCancelled {
		t.Errorf("cancel = %d", code)
	}
}

func TestFileChooserSave(t *testing.T) {
	r := newRig(t, nil)
	f := serveChooser(t, r, "file:///home/u/out.txt")
	code, res := r.interactive(t, fileChooserIfaceName+"SaveFile", handle, "org.app", "", "Save", Vardict{"current_name": dbus.MakeVariant("out.txt")})
	if code != ResponseSuccess || res["uris"].Value().([]string)[0] != "file:///home/u/out.txt" || f.saved.Load().CurrentName != "out.txt" {
		t.Errorf("SaveFile = %d %v (%+v)", code, res, f.saved.Load())
	}

	// SaveFiles picks a folder and names each file inside it.
	f.uris.Store([]string{"file:///home/u/Docs/"})
	code, res = r.interactive(t, fileChooserIfaceName+"SaveFiles", handle, "org.app", "", "Save all", Vardict{
		"files": dbus.MakeVariant([][]byte{[]byte("a.txt\x00"), []byte("my notes#1.txt\x00")}),
	})
	want := []string{"file:///home/u/Docs/a.txt", "file:///home/u/Docs/my%20notes%231.txt"}
	if code != ResponseSuccess || !slices.Equal(res["uris"].Value().([]string), want) {
		t.Errorf("SaveFiles = %d %v, want %v", code, res, want)
	}
	if req := f.opened.Load(); !req.Directory || req.Multiple || len(req.Filters) != 0 {
		t.Errorf("folder pick = %+v", req)
	}
	f.uris.Store(nil)
	if code, _ := r.interactive(t, fileChooserIfaceName+"SaveFiles", handle, "org.app", "", "x", Vardict{}); code != ResponseCancelled {
		t.Errorf("SaveFiles cancel = %d", code)
	}
}

func TestFileChooserWithoutTheShell(t *testing.T) {
	r := newRig(t, nil)
	for _, m := range []string{"OpenFile", "SaveFile", "SaveFiles"} {
		if code, _ := r.interactive(t, fileChooserIfaceName+m, handle, "org.app", "", "x", Vardict{}); code != ResponseOther {
			t.Errorf("%s without a shell = %d", m, code)
		}
	}
	// Malformed filters are dropped rather than failing the dialog.
	if got := fileFilters(Vardict{"filters": dbus.MakeVariant("nope")}); got != nil {
		t.Errorf("filters = %v", got)
	}
}

// fakePrinter is the shell's print host.
type fakePrinter struct {
	gate
	granted bool
	sent    bool
	got     dbustest.Var[[]any]
}

func (f *fakePrinter) Prepare(c portalreq.Caller, r printdialog.PrepareRequest) (bool, []printdialog.Setting, printdialog.PageSetup, uint32) {
	f.got.Store([]any{r})
	if f.enter(c) {
		return false, nil, printdialog.PageSetup{}, 0
	}
	setup := printdialog.PageSetup{Name: "iso_a4", DisplayName: "A4", Width: 210, Height: 297, MarginTop: 6.35, Orientation: "landscape"}
	return f.granted, []printdialog.Setting{{Key: "printer", Value: "Office"}, {Key: "n-copies", Value: "2"}}, setup, 7
}

func (f *fakePrinter) Print(_ portalreq.Caller, title string, doc *os.File, token uint32) bool {
	defer doc.Close()
	b, _ := io.ReadAll(doc)
	f.got.Store([]any{title, string(b), token})
	return f.sent
}

func servePrinter(t *testing.T, r *rig, f *fakePrinter) {
	t.Helper()
	release, err := printdialog.NewDaemon(f).Export(r.bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
}

func TestPrintPrepareAndSpool(t *testing.T) {
	r := newRig(t, nil)
	host := &fakePrinter{granted: true, sent: true}
	servePrinter(t, r, host)
	in := Vardict{"n-copies": dbus.MakeVariant("3"), "printer": dbus.MakeVariant("Office"), "bogus": dbus.MakeVariant(uint32(1))}
	inSetup := Vardict{"Name": dbus.MakeVariant("na_letter"), "Width": dbus.MakeVariant(215.9), "Orientation": dbus.MakeVariant("portrait")}
	opts := Vardict{"accept_label": dbus.MakeVariant("Export"), "has_current_page": dbus.MakeVariant(true), "supported_output_file_formats": dbus.MakeVariant([]string{"ps", "pdf"})}
	code, res := r.interactive(t, "org.freedesktop.impl.portal.Print.PreparePrint", handle, "org.app", "", "Doc", in, inSetup, opts)
	settings, _ := res["settings"].Value().(map[string]dbus.Variant)
	if code != ResponseSuccess || res["token"].Value() != uint32(7) || settings["printer"].Value() != "Office" || settings["n-copies"].Value() != "2" {
		t.Errorf("PreparePrint = %d %v", code, res)
	}
	// The app's settings, page setup and options reach the dialog.
	want := printdialog.PrepareRequest{
		Title:       "Doc",
		Settings:    []printdialog.Setting{{Key: "n-copies", Value: "3"}, {Key: "printer", Value: "Office"}},
		PageSetup:   printdialog.PageSetup{Name: "na_letter", Width: 215.9, Orientation: "portrait"},
		AcceptLabel: "Export", HasCurrentPage: true, OutputFormats: []string{"ps", "pdf"},
	}
	if got := host.got.Load()[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("the dialog got %+v, want %+v", got, want)
	}
	// The page setup comes back under the spec's key, typed as GTK's.
	ps, ok := res["page-setup"].Value().(map[string]dbus.Variant)
	if !ok || ps["Name"].Value() != "iso_a4" || ps["Width"].Value() != 210.0 || ps["Height"].Value() != 297.0 || ps["MarginTop"].Value() != 6.35 || ps["Orientation"].Value() != "landscape" {
		t.Errorf("page-setup = %v", res["page-setup"])
	}
	if _, has := res["page_setup"]; has {
		t.Error("the page setup is still under page_setup")
	}

	doc, err := os.CreateTemp(t.TempDir(), "doc")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = doc.WriteString("%PDF")
	_, _ = doc.Seek(0, io.SeekStart)
	defer doc.Close()
	f := &fakePrinter{sent: true}
	r2 := newRig(t, nil)
	servePrinter(t, r2, f)
	code, _ = r2.interactive(t, "org.freedesktop.impl.portal.Print.Print", handle, "org.app", "", "Doc", dbus.UnixFD(doc.Fd()), Vardict{"token": dbus.MakeVariant(uint32(7))})
	if got := f.got.Load(); code != ResponseSuccess || got[0] != "Doc" || got[1] != "%PDF" || got[2] != uint32(7) {
		t.Errorf("Print = %d, host got %v", code, got)
	}
}

func TestPrintCancelAndFailure(t *testing.T) {
	r := newRig(t, nil)
	prepare := func() uint32 {
		code, _ := r.interactive(t, "org.freedesktop.impl.portal.Print.PreparePrint", handle, "org.app", "", "Doc", Vardict{}, Vardict{}, Vardict{})
		return code
	}
	spool := func() uint32 {
		pr, pw, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer pr.Close()
		_ = pw.Close()
		code, _ := r.interactive(t, "org.freedesktop.impl.portal.Print.Print", handle, "org.app", "", "Doc", dbus.UnixFD(pr.Fd()), Vardict{})
		return code
	}
	if prepare() != ResponseOther || spool() != ResponseOther {
		t.Error("without a shell the print calls do not fail")
	}
	servePrinter(t, r, &fakePrinter{})
	if code := prepare(); code != ResponseCancelled {
		t.Errorf("declined prepare = %d", code)
	}
	if code := spool(); code != ResponseOther {
		t.Errorf("an unsent job = %d", code)
	}
}

// The app's modal option reaches the shell; absent, the dialog stays
// modal.
func TestFileChooserPassesModal(t *testing.T) {
	r := newRig(t, nil)
	f := serveChooser(t, r, "file:///x")
	for _, tc := range []struct {
		options Vardict
		want    bool
	}{{Vardict{}, true}, {Vardict{"modal": dbus.MakeVariant(false)}, false}} {
		r.interactive(t, fileChooserIfaceName+"OpenFile", handle, "org.app", "", "Open", tc.options)
		callers := f.callers.Load()
		if got := callers[len(callers)-1]; got.Modal != tc.want {
			t.Errorf("options %v: modal %v, want %v", tc.options, got.Modal, tc.want)
		}
	}
}
