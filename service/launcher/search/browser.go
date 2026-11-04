package search

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math/bits"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/launcher"
)

const (
	// browserListMax bounds each list a profile yields and the merged
	// list: past this a person's next letter finds it, or nothing would.
	browserListMax = 2000
	// browserSectionRows caps the section both browser providers share: a
	// section keeps one cap, so they state the same one.
	browserSectionRows = 6
	// visitRankStep turns each doubling of a page's visits into rank.
	visitRankStep = 2
	// visitRankMax keeps visits from outranking a better match.
	visitRankMax = 16
)

const (
	firefoxBookmarksQuery = `SELECT p.url, COALESCE(NULLIF(b.title, ''), p.title, '')
		FROM moz_bookmarks b JOIN moz_places p ON p.id = b.fk
		WHERE b.type = 1 AND b.parent NOT IN (
			SELECT id FROM moz_bookmarks WHERE parent = (SELECT id FROM moz_bookmarks WHERE guid = 'tags________'))
		ORDER BY b.id LIMIT ?`
	visitedColumns = `SELECT url, COALESCE(title, ''), visit_count FROM `
	visitedOrder   = ` WHERE hidden = 0 AND visit_count > 0 ORDER BY visit_count DESC, id DESC LIMIT ?`
	firefoxVisited = visitedColumns + `moz_places` + visitedOrder
	chromeVisited  = visitedColumns + `urls` + visitedOrder
)

// browserKind is how a profile stores its data.
type browserKind uint8

const (
	browserFirefox browserKind = iota
	browserChromium
)

// browserRoot is a directory whose subdirectories are a browser's
// profiles.
type browserRoot struct {
	kind browserKind
	dir  string
}

// browserRoots are where the known browsers keep their profiles.
func browserRoots(home, configHome string) []browserRoot {
	firefox := []string{
		filepath.Join(home, ".mozilla", "firefox"),
		filepath.Join(configHome, "mozilla", "firefox"),
		filepath.Join(home, ".librewolf"),
		filepath.Join(configHome, "librewolf", "librewolf"),
		filepath.Join(home, ".zen"),
		filepath.Join(home, ".floorp"),
		filepath.Join(home, ".waterfox"),
	}
	chromium := []string{"google-chrome", "chromium", filepath.Join("BraveSoftware", "Brave-Browser"), "microsoft-edge", "vivaldi"}
	roots := make([]browserRoot, 0, len(firefox)+len(chromium))
	for _, dir := range firefox {
		roots = append(roots, browserRoot{browserFirefox, dir})
	}
	for _, dir := range chromium {
		roots = append(roots, browserRoot{browserChromium, filepath.Join(configHome, dir)})
	}
	return roots
}

// browserPage is one web page a browser knows.
type browserPage struct {
	url, title string
	visits     int64
}

// browserPages is what a profile, or all of them, holds.
type browserPages struct {
	bookmarks, visited []browserPage
}

// fileStamp tells whether a file changed without reading it.
type fileStamp struct {
	size    int64
	modTime time.Time
}

// browserProfile is a profile's last reading and the files it came from.
type browserProfile struct {
	stamps map[string]fileStamp
	pages  browserPages
}

// BrowserIndex reads the browsers' bookmarks and history, rereading a
// profile only when its files changed. It lives as long as the shell, so
// opening the panel costs a stat per file while nobody browses.
type BrowserIndex struct {
	roots []browserRoot

	mu       sync.Mutex
	profiles map[string]*browserProfile
	merged   browserPages
}

// NewBrowserIndex reads the known browsers' profiles under home and
// configHome.
func NewBrowserIndex(home, configHome string) *BrowserIndex {
	return &BrowserIndex{roots: browserRoots(home, configHome), profiles: map[string]*browserProfile{}}
}

// databaseOf is the sqlite file a profile keeps its pages in.
func databaseOf(kind browserKind) string {
	if kind == browserFirefox {
		return "places.sqlite"
	}
	return "History"
}

// watchedFiles are every file a profile's reading depends on: the
// database with its write-ahead log or rollback journal.
func watchedFiles(kind browserKind, dir string) []string {
	db := filepath.Join(dir, databaseOf(kind))
	files := []string{db, db + "-wal", db + "-journal"}
	if kind == browserChromium {
		files = append(files, filepath.Join(dir, "Bookmarks"))
	}
	return files
}

// stampsOf stamps the files that exist.
func stampsOf(files []string) (map[string]fileStamp, error) {
	stamps := make(map[string]fileStamp, len(files))
	for _, f := range files {
		info, err := os.Stat(f)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		stamps[f] = fileStamp{size: info.Size(), modTime: info.ModTime()}
	}
	return stamps, nil
}

func sameStamps(a, b map[string]fileStamp) bool {
	if len(a) != len(b) {
		return false
	}
	for f, s := range a {
		o, ok := b[f]
		if !ok || o.size != s.size || !o.modTime.Equal(s.modTime) {
			return false
		}
	}
	return true
}

// profileDirs lists root's subdirectories in name order; a browser that
// is not installed has none.
func profileDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(root, e.Name()))
		}
	}
	return dirs, nil
}

// read brings every profile up to date and returns all their pages. The
// result is never written again. A profile that cannot be read is logged
// and keeps its last reading: the next read tries it again.
func (b *BrowserIndex) read(ctx context.Context) browserPages {
	b.mu.Lock()
	defer b.mu.Unlock()
	changed := false
	seen := make(map[string]bool, len(b.profiles))
	var order []string
	for _, root := range b.roots {
		dirs, err := profileDirs(root.dir)
		if err != nil {
			log.Printf("launcher: listing browser profiles in %s: %v", root.dir, err)
			continue
		}
		for _, dir := range dirs {
			stamps, err := stampsOf(watchedFiles(root.kind, dir))
			if err != nil {
				log.Printf("launcher: browser profile %s: %v", dir, err)
				continue
			}
			if len(stamps) == 0 {
				continue
			}
			seen[dir] = true
			order = append(order, dir)
			if old, ok := b.profiles[dir]; ok && sameStamps(old.stamps, stamps) {
				continue
			}
			pages, err := readProfile(ctx, root.kind, dir)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("launcher: reading browser profile %s: %v", dir, err)
				}
				if _, ok := b.profiles[dir]; !ok {
					order = order[:len(order)-1]
				}
				continue
			}
			b.profiles[dir] = &browserProfile{stamps: stamps, pages: pages}
			changed = true
		}
	}
	for dir := range b.profiles {
		if !seen[dir] {
			delete(b.profiles, dir)
			changed = true
		}
	}
	if changed {
		lists := make([]browserPages, 0, len(order))
		for _, dir := range order {
			lists = append(lists, b.profiles[dir].pages)
		}
		b.merged = mergeProfiles(lists)
	}
	return b.merged
}

// readProfile reads one profile's pages.
func readProfile(ctx context.Context, kind browserKind, dir string) (browserPages, error) {
	if kind == browserFirefox {
		return readFirefox(ctx, dir)
	}
	return readChromium(ctx, dir)
}

func readFirefox(ctx context.Context, dir string) (browserPages, error) {
	var pages browserPages
	err := withDatabaseCopy(ctx, filepath.Join(dir, databaseOf(browserFirefox)), func(db *sql.DB) error {
		var err error
		if pages.bookmarks, err = queryPages(ctx, db, firefoxBookmarksQuery, false); err != nil {
			return fmt.Errorf("bookmarks: %w", err)
		}
		if pages.visited, err = queryPages(ctx, db, firefoxVisited, true); err != nil {
			return fmt.Errorf("history: %w", err)
		}
		return nil
	})
	return pages, err
}

func readChromium(ctx context.Context, dir string) (browserPages, error) {
	var pages browserPages
	raw, err := os.ReadFile(filepath.Join(dir, "Bookmarks")) //nolint:gosec // a browser's own file, found under its profile directory
	switch {
	case err == nil:
		if pages.bookmarks, err = parseChromiumBookmarks(raw); err != nil {
			return pages, fmt.Errorf("bookmarks: %w", err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return pages, err
	}
	err = withDatabaseCopy(ctx, filepath.Join(dir, databaseOf(browserChromium)), func(db *sql.DB) error {
		var err error
		if pages.visited, err = queryPages(ctx, db, chromeVisited, true); err != nil {
			return fmt.Errorf("history: %w", err)
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return pages, nil
	}
	return pages, err
}

// withDatabaseCopy runs read on a private copy of the database at path:
// a running browser holds its own locked, and a copy is a reflink on a
// copy-on-write disk.
func withDatabaseCopy(ctx context.Context, path string, read func(*sql.DB) error) (err error) {
	tmp, err := os.MkdirTemp("", "lys-shell-browser-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(tmp)) }()
	copied := filepath.Join(tmp, "copy.sqlite")
	if err := copyFile(path, copied); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-journal"} {
		if err := copyFile(path+suffix, copied+suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	db, err := sql.Open("sqlite", copied)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	db.SetMaxOpenConns(1)
	if err := ctx.Err(); err != nil {
		return err
	}
	return read(db)
}

func copyFile(from, to string) (err error) {
	src, err := os.Open(from) //nolint:gosec // a browser's own file, found under its profile directory
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, src.Close()) }()
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // inside a private temporary directory
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dst.Close()) }()
	_, err = io.Copy(dst, src)
	return err
}

// queryPages reads rows of url and title, and visits when counted.
func queryPages(ctx context.Context, db *sql.DB, query string, counted bool) (_ []browserPage, err error) {
	rows, err := db.QueryContext(ctx, query, browserListMax)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var pages []browserPage
	for rows.Next() {
		var p browserPage
		dest := []any{&p.url, &p.title}
		if counted {
			dest = append(dest, &p.visits)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	return pages, rows.Err()
}

// chromiumNode is a bookmark or a folder of Chromium's Bookmarks file.
type chromiumNode struct {
	Type     string         `json:"type"`
	Name     string         `json:"name"`
	URL      string         `json:"url"`
	Children []chromiumNode `json:"children"`
}

// parseChromiumBookmarks flattens a Bookmarks file.
func parseChromiumBookmarks(raw []byte) ([]browserPage, error) {
	var file struct {
		Roots struct {
			Bar    chromiumNode `json:"bookmark_bar"`
			Other  chromiumNode `json:"other"`
			Synced chromiumNode `json:"synced"`
		} `json:"roots"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, err
	}
	var pages []browserPage
	var walk func(chromiumNode)
	walk = func(n chromiumNode) {
		if n.Type == "url" {
			pages = append(pages, browserPage{url: n.URL, title: n.Name})
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(file.Roots.Bar)
	walk(file.Roots.Other)
	walk(file.Roots.Synced)
	return pages, nil
}

// isWebURL keeps what a browser and only a browser opens: not place:
// queries, javascript: bookmarklets or local files.
func isWebURL(raw string) bool {
	return strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://")
}

// mergeProfiles joins profiles' lists into one page per URL: the first
// title found wins and visits add up. Bookmarks sort by title, visited
// pages by visits, the URL breaking ties, so the order is the same on
// every run.
func mergeProfiles(profiles []browserPages) browserPages {
	var bookmarks, visited [][]browserPage
	for _, p := range profiles {
		bookmarks = append(bookmarks, p.bookmarks)
		visited = append(visited, p.visited)
	}
	merged := browserPages{bookmarks: mergePages(bookmarks), visited: mergePages(visited)}
	slices.SortFunc(merged.bookmarks, func(a, b browserPage) int {
		return cmp.Or(cmp.Compare(strings.ToLower(pageTitle(a)), strings.ToLower(pageTitle(b))), cmp.Compare(a.url, b.url))
	})
	slices.SortFunc(merged.visited, func(a, b browserPage) int {
		return cmp.Or(cmp.Compare(b.visits, a.visits), cmp.Compare(a.url, b.url))
	})
	merged.bookmarks = merged.bookmarks[:min(len(merged.bookmarks), browserListMax)]
	merged.visited = merged.visited[:min(len(merged.visited), browserListMax)]
	return merged
}

func mergePages(lists [][]browserPage) []browserPage {
	at := map[string]int{}
	var out []browserPage
	for _, list := range lists {
		for _, p := range list {
			if !isWebURL(p.url) {
				continue
			}
			p.title = strings.Join(strings.Fields(p.title), " ")
			i, ok := at[p.url]
			if !ok {
				at[p.url] = len(out)
				out = append(out, p)
				continue
			}
			out[i].visits += p.visits
			if out[i].title == "" {
				out[i].title = p.title
			}
		}
	}
	return out
}

// displayURL is a URL as a row shows and matches it: the host and path,
// with the scheme, query and fragment left out as they only add noise to
// a match and may hold tokens.
func displayURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	}
	return strings.TrimSuffix(strings.TrimPrefix(u.Host, "www.")+u.EscapedPath(), "/")
}

func pageTitle(p browserPage) string {
	if p.title != "" {
		return p.title
	}
	return displayURL(p.url)
}

// visitRank lifts a page by how often it is visited, on a log scale.
func visitRank(visits int64) int {
	if visits <= 0 {
		return 0
	}
	return min(bits.Len64(uint64(visits))*visitRankStep, visitRankMax)
}

// browserRowLearn is one key per page, whichever list found it.
const browserRowLearn = "browser:"

// browserRows is a provider's view of the index.
type browserRows struct {
	env   Env
	index *BrowserIndex
	flat  bool
}

func (b browserRows) entry(idPrefix string, icon launcher.Icon, p browserPage) Entry {
	return Entry{
		ID: idPrefix + p.url, Learn: browserRowLearn + p.url, Title: pageTitle(p), Subtitle: displayURL(p.url),
		Icon: icon, Rank: visitRank(p.visits), Data: p,
	}
}

func (b browserRows) actions(e Entry) []Action {
	p, ok := e.Data.(browserPage)
	if !ok {
		return nil
	}
	return []Action{
		{Title: i18n.T("launcher-action-open"), Icon: "ld-external-link-symbolic", Run: func(context.Context) Outcome {
			b.env.openURI(p.url)
			return Close{}
		}},
		copyAction(i18n.T("launcher-action-copy-url"), p.url),
	}
}

// Bookmarks offers the browsers' bookmarks.
type Bookmarks struct{ browserRows }

// NewBookmarks offers the bookmarks index holds.
func NewBookmarks(env Env, index *BrowserIndex) *Bookmarks {
	return &Bookmarks{browserRows{env: env, index: index}}
}

// Spec implements Provider.
func (b *Bookmarks) Spec() Spec {
	if b.flat {
		return Spec{Section: SectionNone}
	}
	return Spec{Section: SectionBrowser, MinQuery: 2, Limit: browserSectionRows}
}

// Load implements Provider.
func (b *Bookmarks) Load(ctx context.Context, publish func([]Entry)) {
	pages := b.index.read(ctx).bookmarks
	entries := make([]Entry, len(pages))
	for i, p := range pages {
		entries[i] = b.entry("bookmark:", launcher.IconName("ld-bookmark-symbolic"), p)
	}
	publish(entries)
}

// Actions implements Provider.
func (b *Bookmarks) Actions(e Entry) []Action { return b.actions(e) }

// VisitedPages offers the browsers' most visited pages.
type VisitedPages struct {
	browserRows
	// hideBookmarked leaves out what a Bookmarks beside it already lists.
	hideBookmarked bool
}

// NewVisitedPages offers the visited pages index holds; hideBookmarked
// leaves out those the bookmarks list too.
func NewVisitedPages(env Env, index *BrowserIndex, hideBookmarked bool) *VisitedPages {
	return &VisitedPages{env: env, index: index, hideBookmarked: hideBookmarked}
}

// Spec implements Provider.
func (v *VisitedPages) Spec() Spec {
	if v.flat {
		return Spec{Section: SectionNone}
	}
	return Spec{Section: SectionBrowser, MinQuery: 3, Limit: browserSectionRows, Weight: -12}
}

// Load implements Provider.
func (v *VisitedPages) Load(ctx context.Context, publish func([]Entry)) {
	pages := v.index.read(ctx)
	bookmarked := map[string]bool{}
	if v.hideBookmarked {
		for _, p := range pages.bookmarks {
			bookmarked[p.url] = true
		}
	}
	entries := make([]Entry, 0, len(pages.visited))
	for _, p := range pages.visited {
		if !bookmarked[p.url] {
			entries = append(entries, v.entry("visited:", launcher.IconName("ld-history-symbolic"), p))
		}
	}
	publish(entries)
}

// Actions implements Provider.
func (v *VisitedPages) Actions(e Entry) []Action { return v.actions(e) }

// bookmarksLevel lists every bookmark.
func bookmarksLevel(env Env, index *BrowserIndex) *Level {
	return &Level{
		Title: i18n.T("launcher-picker-bookmarks"), Placeholder: i18n.T("launcher-placeholder-filter"),
		Providers: []Provider{&Bookmarks{browserRows{env: env, index: index, flat: true}}},
	}
}

// visitedLevel lists the visited pages, most visited first.
func visitedLevel(env Env, index *BrowserIndex) *Level {
	return &Level{
		Title: i18n.T("launcher-picker-visited"), Placeholder: i18n.T("launcher-placeholder-filter"),
		Providers: []Provider{&VisitedPages{env: env, index: index, flat: true}},
	}
}
