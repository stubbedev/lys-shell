package filechooser

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeTrashed puts a file in trash/files under name with the info
// text given ("" writes none: an orphan).
func writeTrashed(t *testing.T, trash, name, info string) string {
	t.Helper()
	for _, d := range []string{"files", "info"} {
		if err := os.MkdirAll(filepath.Join(trash, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(trash, "files", name)
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if info != "" {
		if err := os.WriteFile(filepath.Join(trash, "info", name+".trashinfo"), []byte(info), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func trashInfo(path string) string {
	return "[Trash Info]\nPath=" + path + "\nDeletionDate=2026-10-01T12:00:00\n"
}

// The home trash comes first; a mount adds its .Trash-$uid, and its
// .Trash/$uid only when .Trash is a real, sticky directory.
func TestTrashDirs(t *testing.T) {
	sticky, plain, linked := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(sticky, ".Trash"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(sticky, ".Trash"), 0o700|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(plain, ".Trash"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(sticky, ".Trash"), filepath.Join(linked, ".Trash")); err != nil {
		t.Fatal(err)
	}
	got := TrashDirs("/home/u/.local/share", []string{sticky, plain, linked}, 1000)
	want := []string{
		"/home/u/.local/share/Trash",
		filepath.Join(sticky, ".Trash", "1000"), filepath.Join(sticky, ".Trash-1000"),
		filepath.Join(plain, ".Trash-1000"),
		filepath.Join(linked, ".Trash-1000"),
	}
	if !slices.Equal(got, want) {
		t.Errorf("TrashDirs =\n%q\nwant\n%q", got, want)
	}
	if got := TrashDirs("", nil, 1000); len(got) != 0 {
		t.Errorf("no data home, no mounts = %q", got)
	}
}

// Trashed files list under their original names, typed by them; an
// orphan or malformed info leaves its file out, a trash that does not
// exist lists nothing, and folder modes list only folders.
func TestListTrash(t *testing.T) {
	home, mount := filepath.Join(t.TempDir(), "Trash"), filepath.Join(t.TempDir(), ".Trash-1000")
	report := writeTrashed(t, home, "report.2", trashInfo("/home/u/My%20Report.pdf"))
	writeTrashed(t, mount, "photo.png", trashInfo("pics/photo.png"))
	writeTrashed(t, home, "orphan.txt", "")
	writeTrashed(t, home, "badgroup", "[Desktop Entry]\nPath=/x\n")
	writeTrashed(t, home, "nopath", "[Trash Info]\nDeletionDate=2026-10-01T12:00:00\n")
	writeTrashed(t, home, "badescape", trashInfo("/home/u/%zz"))
	if err := os.MkdirAll(filepath.Join(home, "files", "folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "info", "folder.trashinfo"), []byte(trashInfo("/home/u/Old%20Folder")), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := ListTrash([]string{home, filepath.Join(t.TempDir(), "none"), mount}, ModeOpenFile)
	if err != nil {
		t.Fatal(err)
	}
	got := names(entries)
	slices.Sort(got)
	if want := []string{"My Report.pdf", "Old Folder", "photo.png"}; !slices.Equal(got, want) {
		t.Errorf("trash lists %q, want %q", got, want)
	}
	i := slices.IndexFunc(entries, func(e Entry) bool { return e.Path == report })
	if i < 0 || entries[i].Kind() != "PDF" {
		t.Errorf("the report is %+v, want the trashed file typed by its original name", entries)
	}

	folders, err := ListTrash([]string{home}, ModeFolders)
	if err != nil || !slices.Equal(names(folders), []string{"Old Folder"}) {
		t.Errorf("folder mode lists %q, %v", names(folders), err)
	}
	if os.Geteuid() != 0 {
		if err := os.Chmod(filepath.Join(home, "files"), 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(home, "files"), 0o700) })
		if _, err := ListTrash([]string{home}, ModeOpenFile); err == nil || !strings.Contains(err.Error(), filepath.Join(home, "files")) {
			t.Errorf("an unreadable trash = %v, want an error naming it", err)
		}
	}
}

func TestTrashedName(t *testing.T) {
	for info, want := range map[string]string{
		trashInfo("/home/u/a%20b.txt"):                       "a b.txt",
		trashInfo("relative/dir/c.txt"):                      "c.txt",
		"# comment\n\n[Trash Info]\nPath = /x/d.txt\n":       "d.txt",
		"[Trash Info]\nDeletionDate=x\n[Other]\nPath=/x/e\n": "",
		"[Other]\n[Trash Info]\nPath=/x/f\n":                 "",
		trashInfo("/"):                                       "",
		trashInfo("%zz"):                                     "",
		"":                                                   "",
	} {
		got, ok := trashedName([]byte(info))
		if got != want || ok != (want != "") {
			t.Errorf("trashedName(%q) = %q, %v; want %q", info, got, ok, want)
		}
	}
}

// gvfs's FUSE folders become network places named by share and
// server, else user and host; without gvfs there are none.
func TestNetworkLocations(t *testing.T) {
	runtime := t.TempDir()
	if got := NetworkLocations(runtime); len(got) != 0 {
		t.Errorf("no gvfs = %v", got)
	}
	for _, name := range []string{"sftp:host=example.com,user=me", "smb-share:server=nas,share=media", "dav:host=cloud.example.com,ssl=true"} {
		if err := os.MkdirAll(filepath.Join(runtime, "gvfs", name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(runtime, "gvfs", "stray"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range NetworkLocations(runtime) {
		got = append(got, p.Label+"="+filepath.Base(p.Path)+"|"+p.Icon)
	}
	want := []string{
		"cloud.example.com=dav:host=cloud.example.com,ssl=true|ld-server-symbolic",
		"me@example.com=sftp:host=example.com,user=me|ld-server-symbolic",
		"media on nas=smb-share:server=nas,share=media|ld-server-symbolic",
	}
	if !slices.Equal(got, want) {
		t.Errorf("network locations =\n%q\nwant\n%q", got, want)
	}
	for name, want := range map[string]string{
		"ftp:server=files.example.com":    "files.example.com",
		"afp-volume:host=mac%20mini":      "mac mini",
		"unknown":                         "unknown",
		"google-drive:token=x,mode=fancy": "google-drive:token=x,mode=fancy",
	} {
		if got := gvfsLabel(name); got != want {
			t.Errorf("gvfsLabel(%q) = %q, want %q", name, got, want)
		}
	}
	if got := NetworkLocations(""); got != nil {
		t.Errorf("no runtime dir = %v", got)
	}
}

func TestTrashCrumb(t *testing.T) {
	if crumbs := Crumbs(trashLocation, "/home/u"); len(crumbs) != 1 || crumbs[0] != (Crumb{"Trash", trashLocation, true}) {
		t.Errorf("trash crumbs = %v", crumbs)
	}
}
