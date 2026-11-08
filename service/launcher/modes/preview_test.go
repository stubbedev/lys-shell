package modes

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stubbedev/lys-desktop/i18n"
	"github.com/stubbedev/lys-desktop/service/clipboard"

	"github.com/stubbedev/lys-shell/service/launcher"
)

func factValues(p launcher.Preview) map[string]string {
	out := map[string]string{}
	for _, f := range p.Facts {
		out[f.Label] = f.Value
	}
	return out
}

// A window row previews its picture with its title, application and
// workspace beside it; a row past the list has none.
func TestAWindowRowPreviewsItsPictureTitleAppAndWorkspace(t *testing.T) {
	w := &Window{cfg: DefaultWindowConfig(), windows: []WindowInfo{{ID: "1", Title: "notes.md", Class: "foot", Workspace: "3"}}}
	p, ok := w.RowPreview(0)
	if !ok || p.Image == nil {
		t.Fatalf("preview %+v, %v: want a capture", p, ok)
	}
	got := factValues(p)
	want := map[string]string{
		i18n.T("launcher-preview-title"): "notes.md", i18n.T("launcher-preview-app"): "foot", i18n.T("launcher-preview-workspace"): "3",
	}
	if len(got) != len(want) {
		t.Errorf("facts %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("fact %q = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := w.RowPreview(1); ok {
		t.Error("a row past the list has a preview")
	}
}

// A window without a workspace or title says only what it knows.
func TestAWindowPreviewLeavesOutWhatItDoesNotKnow(t *testing.T) {
	p := WindowInfo{ID: "1", Class: "foot"}.Preview()
	if len(p.Facts) != 1 || p.Facts[0].Value != "foot" {
		t.Errorf("facts %+v, want the app alone", p.Facts)
	}
}

func clipboardPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	img.Set(0, 0, color.NRGBA{G: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Each kind of clipboard entry previews in full: text read whole, an
// image decoded with its type and size, one file as that file, several
// as their paths, and anything else as its type and size.
func TestClipboardRowsPreviewEachKindInFull(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(file, []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pngData := clipboardPNG(t)
	f, _ := newFakeClipboard(t,
		[2]string{"application/pdf", "%PDF-1.7"},
		[2]string{"text/uri-list", "file:///x/one\nfile:///y/two\n"},
		[2]string{"text/uri-list", "file://" + file + "\n"},
		[2]string{"image/png", string(pngData)},
		[2]string{"text/plain", "line one\nline two"},
	)
	m := NewClipboard(f)
	m.Load(bg)
	load := func(row uint32) (launcher.Preview, launcher.PreviewContent, error) {
		t.Helper()
		p, ok := m.RowPreview(row)
		if !ok {
			t.Fatalf("row %d has no preview", row)
		}
		c, err := launcher.LoadPreview(bg, p, nil)
		return p, c, err
	}
	if p, c, err := load(0); err != nil || c.Text != "line one\nline two" || len(p.Facts) != 0 {
		t.Errorf("text: %+v %q %v", p.Facts, c.Text, err)
	}
	p, c, err := load(1)
	if err != nil || c.Image == nil || c.Image.Bounds().Dx() != 3 {
		t.Errorf("image: %v %v", c.Image, err)
	}
	if got := factValues(p); got[i18n.T("launcher-preview-type")] != "image/png" || got[i18n.T("launcher-preview-size")] != clipboard.HumanSize(len(pngData)) {
		t.Errorf("image facts %v", got)
	}
	if _, c, err := load(2); err != nil || c.Text != "package a\n" || c.Name != file {
		t.Errorf("one file: %+v %v", c, err)
	}
	if _, c, err := load(3); err != nil || c.Text != "/x/one\n/y/two" {
		t.Errorf("several files: %q %v", c.Text, err)
	}
	p, _, err = load(4)
	if !errors.Is(err, launcher.ErrNoPreview) {
		t.Errorf("other: loading gave %v, want nothing to load", err)
	}
	if got := factValues(p); got[i18n.T("launcher-preview-type")] != "application/pdf" || got[i18n.T("launcher-preview-size")] != "8 B" {
		t.Errorf("other facts %v", got)
	}
}

// An entry gone from the history since the list was drawn has no
// preview, and one gone after its preview was described fails to load.
func TestAClipboardEntryGoneHasNoPreview(t *testing.T) {
	f, ids := newFakeClipboard(t, [2]string{"text/plain", "a"}, [2]string{"text/plain", "b"})
	m := NewClipboard(f)
	m.Load(bg)
	p, ok := m.RowPreview(0)
	if !ok {
		t.Fatal("no preview")
	}
	f.Forget(ids[1])
	if _, err := launcher.LoadPreview(bg, p, nil); !errors.Is(err, clipboard.ErrEntryGone) {
		t.Errorf("loading a forgotten entry: %v", err)
	}
	if _, ok := m.RowPreview(0); ok {
		t.Error("a forgotten entry still previews")
	}
	if _, ok := m.RowPreview(9); ok {
		t.Error("a row past the list previews")
	}
	if _, ok := NewClipboard(nil).RowPreview(0); ok {
		t.Error("no history previews")
	}
	if !slices.Equal(f.copied, nil) {
		t.Error("previewing restored an entry")
	}
}
