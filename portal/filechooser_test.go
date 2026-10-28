package portal

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/fileuri"
	"github.com/stubbedev/lys-shell/shell/filechooser"
	"github.com/stubbedev/lys-shell/shell/printdialog"
)

// fakeChooser is the shell's file dialog: it answers the URIs, the
// filter the request started on, and each choice's start value.
type fakeChooser struct {
	gate
	uris   dbustest.Var[[]string]
	opened dbustest.Var[filechooser.OpenRequest]
	saved  dbustest.Var[filechooser.SaveRequest]
}

func answerChooser(uris []string, filters []filechooser.Filter, current int32, choices []portaldialogs.Choice) filechooser.Result {
	res := filechooser.Result{URIs: uris}
	if int(current) < len(filters) {
		res.Filter = filters[current]
	}
	for _, c := range choices {
		res.Choices = append(res.Choices, portaldialogs.Selection{ID: c.ID, Value: c.Start()})
	}
	return res
}

func (f *fakeChooser) Open(c portaldialogs.Caller, r filechooser.OpenRequest) filechooser.Result {
	f.opened.Store(r)
	if f.enter(c) {
		return filechooser.Result{}
	}
	return answerChooser(f.uris.Load(), r.Filters, r.CurrentFilter, r.Choices)
}

func (f *fakeChooser) Save(c portaldialogs.Caller, r filechooser.SaveRequest) filechooser.Result {
	f.saved.Store(r)
	if f.enter(c) {
		return filechooser.Result{}
	}
	return answerChooser(f.uris.Load(), r.Filters, r.CurrentFilter, r.Choices)
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
	docs := t.TempDir()
	f.uris.Store([]string{fileuri.FromPath(docs)})
	code, res = r.interactive(t, fileChooserIfaceName+"SaveFiles", handle, "org.app", "", "Save all", Vardict{
		"files": dbus.MakeVariant([][]byte{[]byte("a.txt\x00"), []byte("my notes#1.txt\x00")}),
	})
	want := []string{fileuri.FromPath(filepath.Join(docs, "a.txt")), fileuri.FromPath(filepath.Join(docs, "my notes#1.txt"))}
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

func (f *fakePrinter) Prepare(c portaldialogs.Caller, r printdialog.PrepareRequest) (bool, []printdialog.Setting, printdialog.PageSetup, uint32) {
	f.got.Store([]any{r})
	if f.enter(c) {
		return false, nil, printdialog.PageSetup{}, 0
	}
	setup := printdialog.PageSetup{Name: "iso_a4", DisplayName: "A4", Width: 210, Height: 297, MarginTop: 6.35, Orientation: "landscape"}
	return f.granted, []printdialog.Setting{{Key: "printer", Value: "Office"}, {Key: "n-copies", Value: "2"}}, setup, 7
}

func (f *fakePrinter) Print(_ portaldialogs.Caller, title string, doc *os.File, token uint32) bool {
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

// The app's accept label, filters with the current one, and choices
// reach the dialog, and the filter and choices come back; a
// current_filter not among the filters starts on the first, and with
// no filters it is the one filter.
func TestFileChooserOptions(t *testing.T) {
	r := newRig(t, nil)
	f := serveChooser(t, r, "file:///a.png")
	txt := filechooser.Filter{Name: "Text", Rules: []filechooser.Rule{{Kind: filechooser.RuleGlob, Value: "*.txt"}}}
	png := filechooser.Filter{Name: "Images", Rules: []filechooser.Rule{{Kind: filechooser.RuleGlob, Value: "*.png"}}}
	choices := []portaldialogs.Choice{{ID: "ro", Label: "Read-only", Options: []portaldialogs.Option{}, Initial: "true"}}
	code, res := r.interactive(t, fileChooserIfaceName+"OpenFile", handle, "org.app", "", "Pick", Vardict{
		"accept_label": dbus.MakeVariant("_Import"), "filters": dbus.MakeVariant([]filechooser.Filter{txt, png}),
		"current_filter": dbus.MakeVariant(png), "choices": dbus.MakeVariant(choices),
	})
	got := f.opened.Load()
	if got.AcceptLabel != "_Import" || got.CurrentFilter != 1 || len(got.Choices) != 1 || got.Choices[0].ID != "ro" {
		t.Errorf("open request = %+v", got)
	}
	var cf filechooser.Filter
	if err := dbus.Store([]any{res["current_filter"].Value()}, &cf); err != nil || code != ResponseSuccess || cf.Name != "Images" {
		t.Errorf("current_filter = %v, %v", res["current_filter"], err)
	}
	var picked []portaldialogs.Selection
	if err := dbus.Store([]any{res["choices"].Value()}, &picked); err != nil || !slices.Equal(picked, []portaldialogs.Selection{{ID: "ro", Value: "true"}}) {
		t.Errorf("choices = %v, %v", res["choices"], err)
	}
	other := filechooser.Filter{Name: "Other", Rules: []filechooser.Rule{{Kind: filechooser.RuleGlob, Value: "*.x"}}}
	r.interactive(t, fileChooserIfaceName+"OpenFile", handle, "org.app", "", "Pick", Vardict{
		"filters": dbus.MakeVariant([]filechooser.Filter{txt, png}), "current_filter": dbus.MakeVariant(other),
	})
	if got := f.opened.Load(); got.CurrentFilter != 0 || len(got.Filters) != 2 {
		t.Errorf("a stray current_filter = %+v", got)
	}
	_, res = r.interactive(t, fileChooserIfaceName+"OpenFile", handle, "org.app", "", "Pick", Vardict{"current_filter": dbus.MakeVariant(png)})
	if got := f.opened.Load(); len(got.Filters) != 1 || got.Filters[0].Name != "Images" {
		t.Errorf("current_filter alone = %+v", got)
	}
	if _, has := res["choices"]; has {
		t.Errorf("choices without any asked: %v", res)
	}
	if code, _ := r.interactive(t, fileChooserIfaceName+"OpenFile", handle, "org.app", "", "Pick", Vardict{"current_filter": dbus.MakeVariant("nope")}); code != ResponseOther {
		t.Errorf("a malformed current_filter = %d, want other", code)
	}
}

// Saving an existing file again starts in its folder with its name.
func TestFileChooserSaveCurrentFile(t *testing.T) {
	r := newRig(t, nil)
	f := serveChooser(t, r, "file:///home/u/Docs/report.odt")
	r.interactive(t, fileChooserIfaceName+"SaveFile", handle, "org.app", "", "Save As", Vardict{
		"current_name": dbus.MakeVariant("Untitled"), "current_file": dbus.MakeVariant([]byte("/home/u/Docs/report.odt\x00")),
	})
	if got := f.saved.Load(); got.CurrentFolder != "/home/u/Docs" || got.CurrentName != "report.odt" {
		t.Errorf("save request = %+v", got)
	}
}

// SaveFiles numbers a name taken in the folder, or earlier in the list,
// and fails names that are not plain file names.
func TestFileChooserSaveFilesNames(t *testing.T) {
	r := newRig(t, nil)
	docs := t.TempDir()
	if err := os.WriteFile(filepath.Join(docs, "a.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	serveChooser(t, r, fileuri.FromPath(docs))
	files := func(names ...string) Vardict {
		raw := make([][]byte, len(names))
		for i, n := range names {
			raw[i] = []byte(n + "\x00")
		}
		return Vardict{"files": dbus.MakeVariant(raw)}
	}
	code, res := r.interactive(t, fileChooserIfaceName+"SaveFiles", handle, "org.app", "", "x", files("a.txt", "b", "b", ".bashrc"))
	var got []string
	for _, u := range res["uris"].Value().([]string) {
		p, _ := fileuri.Path(u)
		got = append(got, filepath.Base(p))
	}
	if want := []string{"a (2).txt", "b", "b (2)", ".bashrc"}; code != ResponseSuccess || !slices.Equal(got, want) {
		t.Errorf("SaveFiles = %d %v, want %v", code, got, want)
	}
	for _, bad := range []string{"..", ".", "", "sub/x.txt"} {
		if code, _ := r.interactive(t, fileChooserIfaceName+"SaveFiles", handle, "org.app", "", "x", files(bad)); code != ResponseOther {
			t.Errorf("SaveFiles %q = %d, want other", bad, code)
		}
	}
}
