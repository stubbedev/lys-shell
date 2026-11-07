package screenshot

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stubbedev/lys-lib/portaldialogs"

	"github.com/stubbedev/lys-shell/shell/regionoverlay"
)

func plain(n Notice) [3]string { return [3]string{n.Summary, n.Body, n.Icon} }

func regionHost(t *testing.T) (*Host, *[]Notice) {
	t.Helper()
	h, notified, _ := testHost(t, &fakeRegion{sel: regionoverlay.Selection{Output: "DP-1", Width: 5, Height: 5}, ok: true})
	return h, notified
}

func TestHostPresentsTheThumbnailAndNotifiesWithoutButtons(t *testing.T) {
	h, notified := regionHost(t)
	var shown []Saved
	h.Present = func(s Saved) { shown = append(shown, s) }

	path, err := h.Capture(portaldialogs.Caller{}, ModeRegion, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(shown) != 1 || shown[0].Path != path || shown[0].Image == nil || shown[0].Image.Bounds().Dx() != 10 {
		t.Fatalf("presented %+v, want the saved 10x10 capture", shown)
	}
	if len(*notified) != 1 || len((*notified)[0].Actions) != 0 || (*notified)[0].Respond != nil {
		t.Fatalf("with a thumbnail the notification carries buttons: %+v", *notified)
	}
}

func TestHostWithoutThumbnailNotifiesWithButtons(t *testing.T) {
	h, notified := regionHost(t)
	cfg := h.Config()
	cfg.ThumbnailMs = 0
	h.Config = fixed(cfg)
	h.Present = func(Saved) { t.Fatal("a disabled thumbnail was presented") }
	retracted := 0
	h.Copy = func(image.Image) (func(), error) { return func() { retracted++ }, nil }
	var edited []Saved
	var folders []string
	h.Edit = func(s Saved) { edited = append(edited, s) }
	h.OpenFolder = func(dir string) { folders = append(folders, dir) }

	path, err := h.Capture(portaldialogs.Caller{}, ModeRegion, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(*notified) != 1 {
		t.Fatalf("notified %d times", len(*notified))
	}
	n := (*notified)[0]
	if !slices.Equal(n.Actions, []Action{ActionEdit, ActionOpenFolder, ActionDelete}) || n.Respond == nil {
		t.Fatalf("buttons %v", n.Actions)
	}

	n.Respond(ActionEdit)
	n.Respond(ActionOpenFolder)
	if len(edited) != 1 || edited[0].Path != path || edited[0].Image == nil {
		t.Fatalf("Edit got %+v", edited)
	}
	if !slices.Equal(folders, []string{filepath.Dir(path)}) {
		t.Fatalf("OpenFolder got %v", folders)
	}
	if _, err := os.Stat(path); err != nil || retracted != 0 {
		t.Fatalf("Edit or Open folder touched the capture: %v, %d retracts", err, retracted)
	}

	n.Respond(ActionDelete)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Delete left the file: %v", err)
	}
	if retracted != 1 {
		t.Fatalf("Delete retracted the clipboard copy %d times, want once", retracted)
	}
}

func TestDiscardOfAMissingFileFails(t *testing.T) {
	retracted := false
	s := Saved{Path: filepath.Join(t.TempDir(), "gone.png"), Retract: func() { retracted = true }}
	if err := s.Discard(); err == nil {
		t.Fatal("discarding a missing file succeeded")
	}
	if !retracted {
		t.Fatal("the clipboard copy was kept because the file was gone")
	}
}

type panicking struct {
	image.RGBA
	after int
}

func (p *panicking) At(x, y int) color.Color {
	if p.after--; p.after < 0 {
		panic("interrupted")
	}
	return p.RGBA.At(x, y)
}

func interrupted(t *testing.T, write func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("the write was not interrupted")
		}
	}()
	write()
}

func TestSaveNewNeverExposesAPartialImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	img := &panicking{RGBA: *image.NewRGBA(image.Rect(0, 0, 64, 64)), after: 2000}

	interrupted(t, func() { _, _ = saveNew(img, path) })

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("an interrupted save left %v", entries)
	}

	whole := image.NewRGBA(image.Rect(0, 0, 4, 4))
	got, err := saveNew(whole, path)
	if err != nil || got != path {
		t.Fatalf("saveNew = %q, %v", got, err)
	}
	if info, err := os.Stat(got); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("saved %v, %v; want mode 0644", info, err)
	}
	second, err := saveNew(whole, path)
	if err != nil || second != filepath.Join(dir, "shot-2.png") {
		t.Fatalf("a second save = %q, %v; want the numbered name", second, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatalf("temporaries left behind: %v", entries)
	}
}

func TestWritePNGReplacesWholeOrNotAtAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shot.png")
	before := image.NewRGBA(image.Rect(0, 0, 3, 3))
	if err := WritePNG(before, path); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	interrupted(t, func() { _ = WritePNG(&panicking{RGBA: *image.NewRGBA(image.Rect(0, 0, 64, 64)), after: 2000}, path) })

	if now, _ := os.ReadFile(path); !bytes.Equal(now, old) {
		t.Fatal("an interrupted replace changed the file")
	}
	after := image.NewRGBA(image.Rect(0, 0, 7, 5))
	if err := WritePNG(after, path); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil || img.Bounds().Dx() != 7 {
		t.Fatalf("replaced file decodes to %v, %v", img, err)
	}
}
