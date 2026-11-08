package filechooser

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stubbedev/lys-desktop/fileuri"
	"github.com/stubbedev/lys-desktop/shell/treetest"
	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-lib/xdg"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
)

// A save dialog selects its name's stem, so typing keeps the
// extension; a dot file is selected whole.
func TestSaveNameSelectsTheStem(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"Report.final.pdf", "x.pdf"},
		{".bashrc", "x"},
		{"Makefile", "x"},
		{"résumé.odt", "x.odt"},
	} {
		hs := newHarness(t, "")
		got := hs.ask(t, func() []string {
			return hs.c.Save(testCaller, SaveRequest{Title: "Save", CurrentName: c.name, CurrentFolder: hs.root}).URIs
		})
		var text string
		hs.do(func() {
			hs.c.ui.name.InsertRune('x')
			text = hs.c.ui.name.Text()
		})
		if text != c.want {
			t.Errorf("typing x over %q gives %q, want %q", c.name, text, c.want)
		}
		hs.click(t, "Cancel")
		answer(t, got)
	}
}

// The location entry completes the segment typed from its folder: Tab
// takes a single match whole and two matches' common prefix; hidden
// names stay out unless typed.
func TestLocationCompletes(t *testing.T) {
	hs := newHarness(t, "")
	touch(t, hs.root, "Documents/", ".Dotfolder/")
	got := hs.open(t, OpenRequest{})
	typeLocation := func(text string) []string {
		hs.do(func() { hs.c.showLocation(text) })
		var items []string
		hs.do(func() { items = slices.Clone(hs.c.pc.items) })
		return items
	}
	tab := func() (bool, string) {
		var handled bool
		var text string
		hs.do(func() {
			handled = hs.c.ui.location.TrapTab(false)
			text = hs.c.ui.location.Text()
		})
		return handled, text
	}
	if items := typeLocation("~/Doc"); !slices.Equal(items, []string{"~/Documents/"}) {
		t.Errorf("~/Doc offers %v", items)
	}
	if handled, text := tab(); !handled || text != "~/Documents/" {
		t.Errorf("Tab on one match: %v %q", handled, text)
	}
	touch(t, hs.root, "Downloads/")
	if items := typeLocation("~/D"); !slices.Equal(items, []string{"~/Documents/", "~/Downloads/"}) {
		t.Errorf("~/D offers %v", items)
	}
	if handled, text := tab(); !handled || text != "~/Do" {
		t.Errorf("Tab on two matches: %v %q", handled, text)
	}
	if items := typeLocation("~/"); slices.Contains(items, "~/.Dotfolder/") {
		t.Errorf("a hidden folder is offered: %v", items)
	}
	if items := typeLocation("~/.D"); !slices.Equal(items, []string{"~/.Dotfolder/"}) {
		t.Errorf("~/.D offers %v", items)
	}
	if items := typeLocation("~/zzz"); len(items) != 0 {
		t.Errorf("~/zzz offers %v", items)
	}
	if handled, _ := tab(); handled {
		t.Error("Tab without matches did not move on")
	}
	hs.click(t, "Cancel")
	answer(t, got)
}

// An app's request without a folder starts where its last pick was,
// across restarts; another app starts at home, and a folder the
// request names always wins.
func TestFolderIsKeptPerApp(t *testing.T) {
	viewFile := filepath.Join(t.TempDir(), "state", "view")
	hs := newHarness(t, viewFile)
	ask := func(caller portaldialogs.Caller, folder string) chan []string {
		return hs.ask(t, func() []string { return hs.c.Open(caller, OpenRequest{CurrentFolder: folder}).URIs })
	}
	app, other := testCaller, portaldialogs.Caller{Handle: "/r/other", AppID: "org.other", Modal: true}
	docs := filepath.Join(hs.root, "docs")
	touch(t, docs, "letter.txt")
	got := ask(app, docs)
	hs.pick(t, "letter.txt")
	hs.click(t, "Open")
	answer(t, got)
	for _, c := range []struct {
		caller       portaldialogs.Caller
		folder, want string
	}{{app, "", docs}, {other, "", hs.root}, {app, filepath.Join(hs.root, "pics"), filepath.Join(hs.root, "pics")}} {
		got := ask(c.caller, c.folder)
		if dir := hs.dir(); dir != c.want {
			t.Errorf("%s asking for %q starts in %s, want %s", c.caller.AppID, c.folder, dir, c.want)
		}
		hs.click(t, "Cancel")
		answer(t, got)
	}
	hs.c.folders.file.Wait()
	if dir := loadFolderMemory(foldersFile(viewFile)).folder(app.AppID); dir != docs {
		t.Errorf("after a restart org.app's folder is %q, want %s", dir, docs)
	}
	if err := os.RemoveAll(docs); err != nil {
		t.Fatal(err)
	}
	got = ask(app, "")
	if dir := hs.dir(); dir != hs.root {
		t.Errorf("a remembered folder that is gone starts in %s, want home", dir)
	}
	hs.click(t, "Cancel")
	answer(t, got)
}

// The memory keeps the 64 most recent apps.
func TestFolderMemoryIsCapped(t *testing.T) {
	m := loadFolderMemory("")
	for i := range maxAppFolders + 10 {
		m.remember("app"+string(rune('A'+i)), "/d")
	}
	if len(m.apps) != maxAppFolders || m.folder("appA") != "" || m.folder("app"+string(rune('A'+maxAppFolders+9))) != "/d" {
		t.Errorf("%d apps kept, oldest %q", len(m.apps), m.folder("appA"))
	}
}

// Ctrl+D bookmarks the folder shown once, a folder dropped on the
// sidebar too, and its menu removes it again, every other line of the
// bookmarks file left as it was.
func TestBookmarksAreEditable(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	file := BookmarksFile(xdg.OS)
	original := "# kept as is\nsftp://host/srv  Server  \nfile:///nonexistent%20dir odd\n"
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	hs := newHarness(t, "")
	hs.c.d.BookmarksFile = file
	hs.c.d.Places = func() []Place { p, _ := UserPlaces(xdg.OS, hs.root); return p }
	docs := filepath.Join(hs.root, "docs")
	got := hs.open(t, OpenRequest{CurrentFolder: docs})
	read := func() string {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	hs.key('d', app.ModCtrl, nil)
	want := original + fileuri.FromPath(docs) + "\n"
	if got := read(); got != want {
		t.Errorf("after Ctrl+D the file reads %q, want %q", got, want)
	}
	hs.key('d', app.ModCtrl, nil)
	if got := read(); got != want {
		t.Errorf("a second Ctrl+D made %q", got)
	}
	pics := filepath.Join(hs.root, "pics")
	hs.do(func() { hs.c.droppedOnSidebar(pics) })
	hs.flush()
	if got := read(); got != want+fileuri.FromPath(pics)+"\n" {
		t.Errorf("a folder dropped on the sidebar left %q", got)
	}
	var menus []*widget.ContextMenu
	hs.run(func() { menus = treetest.All[*widget.ContextMenu](hs.c.ui.favorites) })
	if len(menus) != 2 {
		t.Fatalf("%d bookmark rows have a menu, want 2", len(menus))
	}
	for _, m := range menus {
		hs.do(m.Items()[0].OnClick)
	}
	if got := read(); got != original {
		t.Errorf("after removing both the file reads %q, want %q", got, original)
	}
	hs.flush()
	hs.run(func() { menus = treetest.All[*widget.ContextMenu](hs.c.ui.favorites) })
	if len(menus) != 0 {
		t.Errorf("%d bookmark rows left after removing them", len(menus))
	}
	hs.click(t, "Cancel")
	answer(t, got)
}
