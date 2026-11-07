package filechooser

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/stubbedev/lys-lib/atomicfile"
	"github.com/stubbedev/lys-lib/xdg"

	"github.com/stubbedev/lys-shell/internal/fileuri"
	"github.com/stubbedev/lys-shell/internal/statefile"
)

// maxAppFolders is how many apps' last folders are kept.
const maxAppFolders = 64

// folderMemory is the folder each app last picked in, the most recent
// first, kept in a file of "app id<TAB>folder" lines. It is read and
// written under its lock, from the loop and from the requests' own
// goroutines.
type folderMemory struct {
	mu   sync.Mutex
	apps []appFolder
	file statefile.File
}

type appFolder struct{ app, dir string }

// loadFolderMemory reads the memory kept in path; a file that is not
// there is an empty memory.
func loadFolderMemory(path string) *folderMemory {
	m := &folderMemory{file: statefile.File{Path: path}}
	if path == "" {
		return m
	}
	data, err := os.ReadFile(path) //nolint:gosec // the chooser's own state file
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("file chooser: reading %s: %v", path, err)
		}
		return m
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() && len(m.apps) < maxAppFolders {
		app, dir, ok := strings.Cut(sc.Text(), "\t")
		if ok && app != "" && filepath.IsAbs(dir) {
			m.apps = append(m.apps, appFolder{app, dir})
		}
	}
	return m
}

// folder is where app last picked, "" for an app never seen.
func (m *folderMemory) folder(app string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.apps {
		if a.app == app {
			return a.dir
		}
	}
	return ""
}

// remember makes dir app's last folder and saves the memory, the
// least recent app dropped past maxAppFolders.
func (m *folderMemory) remember(app, dir string) {
	m.mu.Lock()
	apps := make([]appFolder, 0, maxAppFolders)
	apps = append(apps, appFolder{app, dir})
	for _, a := range m.apps {
		if a.app != app && len(apps) < maxAppFolders {
			apps = append(apps, a)
		}
	}
	m.apps = apps
	var b strings.Builder
	for _, a := range apps {
		b.WriteString(a.app + "\t" + a.dir + "\n")
	}
	m.mu.Unlock()
	m.file.Write([]byte(b.String()))
}

// foldersFile is where the folder memory is kept beside the view
// file, "" for none.
func foldersFile(viewFile string) string {
	if viewFile == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(viewFile), "file-chooser-folders")
}

// BookmarksFile is GTK 3's bookmarks file, which GTK 4 reads too; ""
// without a config directory.
func BookmarksFile(env xdg.Env) string {
	dir, ok := env.ConfigDir("gtk-3.0")
	if !ok {
		return ""
	}
	return filepath.Join(dir, "bookmarks")
}

// bookmarkLines are the bookmarks file's lines as they are, each with
// its line end, and its mode; none for a file that is not there.
func bookmarkLines(file string) ([]string, os.FileMode, error) {
	data, err := os.ReadFile(file) //nolint:gosec // the user's own GTK bookmarks
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0o644, nil
	}
	if err != nil {
		return nil, 0, err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(file); err == nil {
		mode = info.Mode().Perm()
	}
	return strings.SplitAfter(string(data), "\n"), mode, nil
}

// bookmarked reports a bookmark line naming dir.
func bookmarked(line, dir string) bool {
	uri, _, _ := strings.Cut(strings.TrimSpace(line), " ")
	path, ok := fileuri.Path(uri)
	return ok && filepath.Clean(path) == dir
}

// addBookmark appends dir to the bookmarks file unless a line names it
// already, which reports false. Every other line stays byte for byte.
func addBookmark(file, dir string) (bool, error) {
	lines, mode, err := bookmarkLines(file)
	if err != nil {
		return false, fmt.Errorf("bookmarking %s: %w", dir, err)
	}
	for _, l := range lines {
		if bookmarked(l, dir) {
			return false, nil
		}
	}
	if n := len(lines); n > 0 && lines[n-1] != "" && !strings.HasSuffix(lines[n-1], "\n") {
		lines[n-1] += "\n"
	}
	lines = append(lines, fileuri.FromPath(dir)+"\n")
	if err := writeBookmarks(file, lines, mode); err != nil {
		return false, fmt.Errorf("bookmarking %s: %w", dir, err)
	}
	return true, nil
}

// removeBookmark drops the lines naming dir from the bookmarks file;
// every other line stays byte for byte.
func removeBookmark(file, dir string) error {
	lines, mode, err := bookmarkLines(file)
	if err != nil {
		return fmt.Errorf("removing the bookmark of %s: %w", dir, err)
	}
	kept := lines[:0]
	for _, l := range lines {
		if !bookmarked(l, dir) {
			kept = append(kept, l)
		}
	}
	if err := writeBookmarks(file, kept, mode); err != nil {
		return fmt.Errorf("removing the bookmark of %s: %w", dir, err)
	}
	return nil
}

func writeBookmarks(file string, lines []string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	return atomicfile.WriteFile(file, []byte(strings.Join(lines, "")), mode)
}

// rememberFolder keeps where the app showing picked uri for its next
// request: the folder picked, or the folder of the file.
func (c *Chooser) rememberFolder(uri string) {
	path, ok := fileuri.Path(uri)
	if c.caller.AppID == "" || !ok {
		return
	}
	if !c.s.mode.folders() {
		path = filepath.Dir(path)
	}
	c.folders.remember(c.caller.AppID, path)
}

// bookmark adds dir to the GTK bookmarks off the loop, once, and lists
// it under Favorites.
func (c *Chooser) bookmark(dir string) {
	file := c.d.BookmarksFile
	if file == "" || virtualLocation(dir) {
		return
	}
	offLoopFor(c, func() error {
		_, err := addBookmark(file, dir)
		return err
	}, c.bookmarksChanged)
}

// unbookmark removes dir from the GTK bookmarks off the loop.
func (c *Chooser) unbookmark(dir string) {
	file := c.d.BookmarksFile
	if file == "" {
		return
	}
	offLoopFor(c, func() error { return removeBookmark(file, dir) }, c.bookmarksChanged)
}

// bookmarksChanged lists the bookmarks again, or warns that they could
// not be written.
func (c *Chooser) bookmarksChanged(err error) {
	if err != nil {
		c.warn("Cannot change the bookmarks: " + errorText(err))
		return
	}
	opening := c.opening()
	offLoopFor(c, c.d.Places, func(places []Place) { c.fillPlaces(c.ui.favorites, c.favorites(places, opening)) })
}

// droppedOnSidebar bookmarks a folder dropped on the sidebar; a file
// dropped there opens its folder, as a drop on the files does.
func (c *Chooser) droppedOnSidebar(path string) {
	offLoopFor(c, statPath(path), func(st pathStat) {
		if st.err == nil && st.info.IsDir() {
			c.bookmark(path)
			return
		}
		c.dropped(path)
	})
}
