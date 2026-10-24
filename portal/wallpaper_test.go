package portal

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/fileuri"
)

// wallpaperRig is a rig whose data directory is a temporary one, with
// the shell's wallpaper service.
func wallpaperRig(t *testing.T) (*rig, *fakeWallpaperd, string) {
	t.Helper()
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	r := newRig(t, nil)
	return r, serveWallpaperd(t, r), filepath.Join(data, "lys-shell", "wallpapers")
}

// picture writes a picture at a path that needs URI escaping and
// returns its URI and where its copy lands under dir.
func picture(t *testing.T, dir string, content string) (uri, kept string) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "My Walls", "a#b.PNG")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return fileuri.FromPath(src), filepath.Join(dir, hex.EncodeToString(sum[:])+".png")
}

// The picture is copied into the data directory and the copy is what
// the desktop gets, so the app's file may go away; the same picture
// twice keeps one copy. Only file:// URIs are taken.
func TestWallpaperKeepsACopy(t *testing.T) {
	r, w, dir := wallpaperRig(t)
	uri, kept := picture(t, dir, "picture bytes")
	if code := r.setWallpaper(t, uri, Vardict{}); code != ResponseSuccess {
		t.Fatalf("code = %d", code)
	}
	if got := w.set.Load(); !slices.Equal(got, []string{kept, ""}) {
		t.Errorf("SetWallpaper = %q, want the copy %s on every monitor", got, kept)
	}
	src, _ := fileuri.Path(uri)
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(kept); err != nil || string(got) != "picture bytes" {
		t.Errorf("the copy after the original went: %q, %v", got, err)
	}
	if _, kept2 := picture(t, dir, "picture bytes"); kept2 != kept {
		t.Fatal("the same picture names another copy")
	}
	uri2, _ := picture(t, dir, "picture bytes")
	r.setWallpaper(t, uri2, Vardict{})
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the same picture twice left %d files", len(entries))
	}
	if code := r.setWallpaper(t, "https://example.com/x.png", Vardict{}); code != ResponseOther {
		t.Errorf("https code = %d", code)
	}
	if code := r.setWallpaper(t, "file:///no/such/picture.png", Vardict{}); code != ResponseOther {
		t.Errorf("a missing picture = %d", code)
	}
}

// set-on routes the picture: lockscreen leaves the desktop alone and
// saves the lock screen's background, both does both, absent is the
// desktop only, and anything else fails without setting anything.
func TestWallpaperSetOn(t *testing.T) {
	r, w, dir := wallpaperRig(t)
	uri, kept := picture(t, dir, "lock picture")
	lock := func() config.Background { return r.cfg.Config().Lock.Background }
	before := lock()
	setOn := func(v string) Vardict { return Vardict{"set-on": dbus.MakeVariant(v)} }

	if code := r.setWallpaper(t, uri, setOn("everywhere")); code != ResponseOther || w.set.Load() != nil || lock() != before {
		t.Errorf("an unknown set-on = %d, desktop %v, lock %+v", code, w.set.Load(), lock())
	}
	if code := r.setWallpaper(t, uri, setOn("lockscreen")); code != ResponseSuccess || w.set.Load() != nil {
		t.Errorf("lockscreen = %d, desktop %v", code, w.set.Load())
	}
	if got := lock(); got.Mode != config.BackgroundImage || got.Image != kept {
		t.Errorf("lock background = %+v, want the copy %s as an image", got, kept)
	}
	saved, err := os.ReadFile(r.cfg.RuntimePath())
	if err != nil || !strings.Contains(string(saved), kept) {
		t.Errorf("runtime overrides = %q, %v; want the lock picture saved", saved, err)
	}

	r2, w2, dir2 := wallpaperRig(t)
	uri2, kept2 := picture(t, dir2, "both picture")
	if code := r2.setWallpaper(t, uri2, setOn("both")); code != ResponseSuccess || !slices.Equal(w2.set.Load(), []string{kept2, ""}) || r2.cfg.Config().Lock.Background.Image != kept2 {
		t.Errorf("both = %d, desktop %v, lock %+v", code, w2.set.Load(), r2.cfg.Config().Lock.Background)
	}
	r3, w3, dir3 := wallpaperRig(t)
	lockBefore := r3.cfg.Config().Lock.Background
	uri3, kept3 := picture(t, dir3, "desktop picture")
	if code := r3.setWallpaper(t, uri3, Vardict{}); code != ResponseSuccess || !slices.Equal(w3.set.Load(), []string{kept3, ""}) || r3.cfg.Config().Lock.Background != lockBefore {
		t.Errorf("no set-on = %d, desktop %v, lock %+v", code, w3.set.Load(), r3.cfg.Config().Lock.Background)
	}
}

// The preview comes first: no dialog host fails, a decline cancels, and
// only an accepted preview sets the picture.
func TestWallpaperPreview(t *testing.T) {
	r, w, dir := wallpaperRig(t)
	uri, kept := picture(t, dir, "preview picture")
	preview := Vardict{"show-preview": dbus.MakeVariant(true)}
	if code := r.setWallpaper(t, uri, preview); code != ResponseOther || w.set.Load() != nil {
		t.Errorf("no host: code %d, set %v", code, w.set.Load())
	}
	d := serveDialogs(t, r, false)
	if code := r.setWallpaper(t, uri, preview); code != ResponseCancelled || w.set.Load() != nil {
		t.Errorf("declined: code %d, set %v", code, w.set.Load())
	}
	if got := d.asked.Load(); got[0] != uri {
		t.Errorf("previewed %v", got)
	}
	if _, err := os.Stat(kept); !os.IsNotExist(err) {
		t.Error("a declined picture was copied")
	}
	d.yes.Store(true)
	if code := r.setWallpaper(t, uri, preview); code != ResponseSuccess || w.set.Load()[0] != kept {
		t.Errorf("accepted: code %d, set %v", code, w.set.Load())
	}
}

// Without the shell's wallpaper service, or with nowhere to keep the
// copy, the call fails.
func TestWallpaperFailsWithoutTheShellOrADataDir(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	uri, _ := picture(t, t.TempDir(), "x")
	if code := newRig(t, nil).setWallpaper(t, uri, Vardict{}); code != ResponseOther {
		t.Errorf("without the shell = %d", code)
	}
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "")
	r := newRig(t, nil)
	w := serveWallpaperd(t, r)
	if code := r.setWallpaper(t, uri, Vardict{}); code != ResponseOther || w.set.Load() != nil {
		t.Errorf("without a data directory = %d, set %v", code, w.set.Load())
	}
}
