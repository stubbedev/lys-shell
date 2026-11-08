package filechooser

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stubbedev/lys-desktop/fileuri"

	"github.com/stubbedev/lys-shell/internal/mime"
)

// session is one open request's state, free of widgets: where it is,
// how the listing is filtered and sorted, the history, and what a
// confirm answers. The host renders entries and feeds gestures in.
type session struct {
	mode    Mode
	db      *mime.Database
	dir     string
	filters []Filter
	active  int

	// all is the unfiltered listing of listed, re-read only when the
	// directory changes (or changed on disk): every other repaint
	// partitions it in memory; hiddenNames are the names its .hidden
	// lists, listErr is why it could not be read, and loading is set
	// while the listing is being read.
	all         []Entry
	hiddenNames map[string]bool
	listed      string
	listErr     error
	loading     bool
	// reader lists folders; pending are the changes that came while
	// the listing was being read, applied once it lands.
	reader  *dirReader
	pending []dirChange
	// request reads dir's listing and hands it to setListing, at once
	// or later (the host reads off the loop).
	request func(dir string)
	// recentFile is the recently-used list the Recent place reads.
	recentFile string
	// trashDirs are the trash directories the Trash place lists.
	trashDirs func() []string

	search    string
	recursive bool
	hidden    bool
	// found accumulates the streamed recursive matches for search.
	found []Entry

	view view
	// entries is what the view shows, in order.
	entries []Entry

	back, forward []string
}

// newSession starts at folder when it is a directory, else home, else
// /. Nothing is listed until the first refresh; listings are read at
// once until request is replaced.
func newSession(reader *dirReader, mode Mode, folder string, filters []Filter, db *mime.Database, home string, v view) *session {
	s := &session{reader: reader, mode: mode, db: db, filters: withAllFiles(filters), view: v}
	s.request = func(dir string) { s.setListing(dir, s.read(dir)) }
	s.dir = startDir(folder, home)
	return s
}

// usableFolder is folder when it is one, else "": a stat, for off the loop.
func usableFolder(folder string) string {
	if folder == "" {
		return ""
	}
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return ""
	}
	return folder
}

func startDir(folder, home string) string {
	if folder != "" {
		return folder
	}
	if home != "" {
		return home
	}
	return "/"
}

// searching reports a recursive search in progress: the rows are its
// matches, labelled relative to dir.
func (s *session) searching() bool { return s.recursive && s.search != "" }

// refresh recomputes entries: the cached listing (or the recursive
// matches) through the hidden rules, the type filter (files only) and
// the search text, then sorted by the column, or by relevance while
// searching. A folder not listed yet is requested, and shows nothing
// until its listing arrives.
func (s *session) refresh() {
	if !s.searching() && s.listed != s.dir {
		s.listed, s.all, s.hiddenNames, s.listErr, s.loading, s.pending = s.dir, nil, nil, nil, true, nil
		s.entries = s.entries[:0]
		s.request(s.dir)
		return
	}
	s.partition()
}

// reload reads the folder shown again (it changed on disk), showing
// the listing it has until the new one arrives.
func (s *session) reload() {
	s.loading = true
	s.request(s.dir)
}

// listing is one folder read: its entries, the names its .hidden
// lists, and why it could not be read.
type listing struct {
	entries []Entry
	hidden  map[string]bool
	err     error
}

// read lists dir, or the Recent or trashed files; safe off the loop.
func (s *session) read(dir string) listing {
	switch dir {
	case recentLocation:
		entries, err := ListRecent(s.recentFile, s.mode)
		return listing{entries: entries, err: err}
	case trashLocation:
		entries, err := ListTrash(s.trashDirs(), s.mode)
		return listing{entries: entries, err: err}
	}
	entries, _, err := s.reader.List(dir, s.mode)
	return listing{entries: entries, hidden: hiddenNames(dir), err: err}
}

// setListing takes dir's listing when dir is still the folder awaited;
// false for one superseded.
func (s *session) setListing(dir string, l listing) bool {
	if dir != s.listed || !s.loading {
		return false
	}
	s.all, s.hiddenNames, s.listErr, s.loading = l.entries, l.hidden, l.err, false
	pending := s.pending
	s.pending = nil
	s.patch(pending)
	s.partition()
	return true
}

// dirChange is one name in a listed folder as stated after it changed:
// its entry, or gone; hidden is the folder's .hidden list read again
// when the change was to that file.
type dirChange struct {
	name    string
	entry   Entry
	present bool
	hidden  map[string]bool
}

// statChanges states the names of a change batch in dir; safe off the
// loop.
func statChanges(dir string, names []string, mode Mode) []dirChange {
	changes := make([]dirChange, len(names))
	for i, name := range names {
		e, ok := statEntry(filepath.Join(dir, name), mode)
		changes[i] = dirChange{name: name, entry: e, present: ok}
		if name == ".hidden" {
			changes[i].hidden = hiddenNames(dir)
		}
	}
	return changes
}

// applyChanges applies changes to dir's listing, row by row; false
// when dir is not the folder listed. Changes coming while the listing
// is read wait for it: each was stated after its event, so applied
// after the listing in order they leave every name as it last was.
func (s *session) applyChanges(dir string, changes []dirChange) bool {
	if dir != s.listed || virtualLocation(dir) {
		return false
	}
	if s.loading {
		s.pending = append(s.pending, changes...)
		return false
	}
	s.patch(changes)
	s.refresh()
	return true
}

// patch applies changes to the listing, kept sorted by path.
func (s *session) patch(changes []dirChange) {
	prefix := dirPrefix(s.listed)
	for _, c := range changes {
		if c.hidden != nil || c.name == ".hidden" {
			s.hiddenNames = c.hidden
		}
		path := prefix + c.name
		i, found := slices.BinarySearchFunc(s.all, path, func(e Entry, p string) int { return strings.Compare(e.Path, p) })
		switch {
		case c.present && found:
			s.all[i] = c.entry
		case c.present:
			s.all = slices.Insert(s.all, i, c.entry)
		case found:
			s.all = slices.Delete(s.all, i, i+1)
		}
	}
}

// partition filters and sorts the listing (or the matches) into
// entries.
func (s *session) partition() {
	source := s.all
	if s.searching() {
		source = s.found
	}
	match := newTextFilter(s.search)
	s.entries = s.entries[:0]
	for _, e := range source {
		name := e.Name()
		if !s.hidden && hiddenName(name, s.hiddenNames) {
			continue
		}
		if !e.IsDir && !MatchesFilter(s.db, name, s.filters, s.active) {
			continue
		}
		// Recursive matches already matched the query on the walk.
		if !s.searching() && !match.matches(name) {
			continue
		}
		s.entries = append(s.entries, e)
	}
	if s.searching() {
		SortSearch(s.entries, s.dir, s.search)
	} else {
		SortEntries(s.entries, s.view.sort, s.view.asc)
	}
}

// displayName is a row's label: the basename, or the path below dir
// for a recursive match.
func (s *session) displayName(e Entry) string {
	if s.searching() {
		if rel, err := filepath.Rel(s.dir, e.Path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return e.Name()
}

// setSearch changes the query, dropping the previous matches.
func (s *session) setSearch(query string) {
	s.search, s.found = query, nil
	s.refresh()
}

// addFound appends a batch of recursive matches for query; a batch
// for a superseded query, or after the toggle went off, is dropped.
func (s *session) addFound(query string, batch []Entry) bool {
	if query != s.search || !s.recursive {
		return false
	}
	s.found = append(s.found, batch...)
	s.refresh()
	return true
}

// goTo navigates to dir, recording the departure in the back history
// (a fresh navigation forks it: forward clears) and dropping the
// search so the directory lists in full.
func (s *session) goTo(dir string) {
	if dir != s.dir {
		s.back = append(s.back, s.dir)
		s.forward = nil
		s.dir = dir
	}
	s.search, s.found = "", nil
	s.refresh()
}

// step walks the history one way; false at its end.
func (s *session) step(back bool) bool {
	from, to := &s.forward, &s.back
	if back {
		from, to = &s.back, &s.forward
	}
	if len(*from) == 0 {
		return false
	}
	dir := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	*to = append(*to, s.dir)
	s.dir = dir
	s.search, s.found = "", nil
	s.refresh()
	return true
}

// up goes to the parent; false at the root, and in Recent or Trash.
func (s *session) up() bool {
	parent := filepath.Dir(s.dir)
	if parent == s.dir || virtualLocation(s.dir) {
		return false
	}
	s.goTo(parent)
	return true
}

// dropped navigates to a dropped folder, or reveals a dropped file in
// its folder.
func (s *session) dropped(path string, isDir bool) {
	if isDir {
		s.goTo(path)
		return
	}
	s.goTo(filepath.Dir(path))
}

// selectFilter makes filter i the active one.
func (s *session) selectFilter(i int) {
	if i >= 0 && i < len(s.filters) {
		s.active = i
		s.refresh()
	}
}

// sortBy sorts by column, flipping the direction when it already is.
func (s *session) sortBy(column SortColumn) {
	if s.view.sort == column {
		s.view.asc = !s.view.asc
	} else {
		s.view.sort, s.view.asc = column, true
	}
	s.refresh()
}

// activation is what activating a row does.
type activation uint8

const (
	activateNone activation = iota
	// activateDescend entered the folder.
	activateDescend
	// activateName put the file's name in the save entry.
	activateName
	// activateConfirm opens: the row is selected, confirm picks it up.
	activateConfirm
)

// activate handles a double-click or Enter on row i.
func (s *session) activate(i int) activation {
	if i < 0 || i >= len(s.entries) {
		return activateNone
	}
	e := s.entries[i]
	switch {
	case e.IsDir:
		s.goTo(e.Path)
		return activateDescend
	case s.mode == ModeSave:
		return activateName
	}
	return activateConfirm
}

// savePath resolves the save entry's text to the path it names: an
// absolute path, one under ~, or one relative to the folder shown, its
// . and .. resolved; "" for no text.
func (s *session) savePath(name, home string) string {
	switch {
	case name == "":
		return ""
	case name == "~" || strings.HasPrefix(name, "~/"):
		name = filepath.Join(home, strings.TrimPrefix(name, "~"))
	case !filepath.IsAbs(name):
		name = filepath.Join(s.dir, name)
	}
	return filepath.Clean(name)
}

// result is what confirming an open answers given the selected rows;
// false keeps the dialog open (nothing to answer yet). A folder pick
// answers the selected folders, else the one shown, as GTK's chooser
// does; an open answers the selected files, never folders.
func (s *session) result(selected []int) ([]string, bool) {
	if s.mode.folders() {
		var uris []string
		for _, i := range selected {
			if i >= 0 && i < len(s.entries) && s.entries[i].IsDir {
				uris = append(uris, fileuri.FromPath(s.entries[i].Path))
				if !s.mode.multiple() {
					break
				}
			}
		}
		if len(uris) == 0 {
			uris = []string{fileuri.FromPath(s.dir)}
		}
		return uris, true
	}
	var uris []string
	for _, i := range selected {
		if i >= 0 && i < len(s.entries) && !s.entries[i].IsDir {
			uris = append(uris, fileuri.FromPath(s.entries[i].Path))
		}
	}
	return uris, len(uris) > 0
}
