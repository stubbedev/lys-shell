package mime

import (
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) *Database {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "mime"), 0o755); err != nil {
		t.Fatal(err)
	}
	globs := "# generated\n" +
		"50:image/png:*.png\n" +
		"50:text/plain:*.txt\n" +
		"60:text/x-readme:README*\n" +
		"50:application/x-compressed-tar:*.tar.gz\n" +
		"45:application/gzip:*.gz\n" +
		"50:text/x-makefile:Makefile\n" +
		"50:text/x-csrc:*.C:cs\n"
	if err := os.WriteFile(filepath.Join(dir, "mime", "globs2"), []byte(globs), 0o600); err != nil {
		t.Fatal(err)
	}
	icons := "image/png:image-x-generic\napplication/x-compressed-tar:package-x-generic\n"
	if err := os.WriteFile(filepath.Join(dir, "mime", "generic-icons"), []byte(icons), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"aliases":    "video/x-matroska video/matroska\n",
		"subclasses": "application/x-compressed-tar application/gzip\nimage/svg+xml application/xml\napplication/xml text/plain\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, "mime", name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Load([]string{dir})
}

func TestTypeByName(t *testing.T) {
	db := fixture(t)
	for name, want := range map[string]string{
		"/home/u/me.png":    "image/png",
		"/home/u/ME.PNG":    "image/png",
		"notes.txt":         "text/plain",
		"src.tar.gz":        "application/x-compressed-tar",
		"plain.gz":          "application/gzip",
		"Makefile":          "text/x-makefile",
		"README.md":         "text/x-readme",
		"code.C":            "text/x-csrc",
		"nothing.unknownxx": Unknown,
	} {
		if got := db.TypeByName(name); got != want {
			t.Errorf("TypeByName(%q) = %q, want %q", name, got, want)
		}
	}
	// A case-sensitive glob does not claim the other case.
	if got := db.TypeByName("code.c"); got == "text/x-csrc" {
		t.Error("a cs glob matched a different case")
	}
}

// A file's glyph is the Lucide glyph of its type's generic icon: the
// database's entry, else gio's <media>-x-generic.
func TestFileIcon(t *testing.T) {
	db := fixture(t)
	for mimeType, want := range map[string]string{
		"image/png":  "ld-file-image-symbolic",
		"text/plain": "ld-file-text-symbolic",
		"video/mp4":  "ld-file-play-symbolic",
	} {
		if got := db.FileIcon(mimeType); got != want {
			t.Errorf("FileIcon(%q) = %q, want %q", mimeType, got, want)
		}
	}
}

// A type whose generic icon has no glyph, or no type at all, is the
// plain file, never a freedesktop name another theme must provide.
func TestFileIconOfAnUnmappedTypeIsThePlainFile(t *testing.T) {
	db := fixture(t)
	for _, mimeType := range []string{"model/stl", "garbage", ""} {
		if got := db.FileIcon(mimeType); got != plainFileGlyph {
			t.Errorf("FileIcon(%q) = %q, want %q", mimeType, got, plainFileGlyph)
		}
	}
}

func TestAMissingDatabaseGuessesUnknown(t *testing.T) {
	db := Load([]string{t.TempDir()})
	if got := db.TypeByName("a.png"); got != Unknown {
		t.Errorf("no database = %q, want %q", got, Unknown)
	}
}

func TestIsA(t *testing.T) {
	db := fixture(t)
	for _, c := range []struct {
		mime, base string
		want       bool
	}{
		{"image/png", "image/png", true},
		{"application/x-compressed-tar", "application/gzip", true},
		// Transitively, through application/xml.
		{"image/svg+xml", "text/plain", true},
		// Aliases resolve on either side.
		{"video/matroska", "video/x-matroska", true},
		{"video/x-matroska", "video/matroska", true},
		// The implicit roots.
		{"text/x-csrc", "text/plain", true},
		{"image/png", "application/octet-stream", true},
		{"inode/directory", "application/octet-stream", false},
		// Not the other way round, and not across families.
		{"application/gzip", "application/x-compressed-tar", false},
		{"image/png", "text/plain", false},
		{"image/png", "image/jpeg", false},
	} {
		if got := db.IsA(c.mime, c.base); got != c.want {
			t.Errorf("IsA(%q, %q) = %v, want %v", c.mime, c.base, got, c.want)
		}
	}
}

func TestIsATerminatesOnACycle(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "mime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mime", "subclasses"), []byte("a/x a/y\na/y a/x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if Load([]string{dir}).IsA("a/x", "a/z") {
		t.Error("a cycle claimed an unrelated base")
	}
}

// A type's comment is its untranslated <comment>, from the first data
// directory with one, through its alias; an undescribed type has none.
func TestComment(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	write := func(dir, typ, body string) {
		path := filepath.Join(dir, "mime", typ+".xml")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(first, "image/jpeg", `<?xml version="1.0" encoding="utf-8"?>
<mime-type xmlns="http://www.freedesktop.org/standards/shared-mime-info" type="image/jpeg">
  <comment xml:lang="de">JPEG-Bild</comment>
  <comment>JPEG image</comment>
</mime-type>`)
	write(second, "image/jpeg", `<mime-type type="image/jpeg"><comment>later</comment></mime-type>`)
	write(second, "video/x-matroska", `<mime-type type="video/x-matroska"><comment>Matroska video</comment></mime-type>`)
	if err := os.WriteFile(filepath.Join(first, "mime", "aliases"), []byte("video/matroska video/x-matroska\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	db := Load([]string{first, second})
	for typ, want := range map[string]string{"image/jpeg": "JPEG image", "video/matroska": "Matroska video", "text/x-nothing": ""} {
		if got := db.Comment(typ); got != want {
			t.Errorf("Comment(%s) = %q, want %q", typ, got, want)
		}
	}
	if err := os.Remove(filepath.Join(first, "mime", "image", "jpeg.xml")); err != nil {
		t.Fatal(err)
	}
	if got := db.Comment("image/jpeg"); got != "JPEG image" {
		t.Errorf("a second Comment read the file again: %q", got)
	}
}
