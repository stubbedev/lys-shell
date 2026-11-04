package search

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/launcher"
)

const firefoxSchema = `
CREATE TABLE moz_places (id INTEGER PRIMARY KEY, url TEXT, title TEXT, visit_count INTEGER DEFAULT 0, hidden INTEGER DEFAULT 0 NOT NULL);
CREATE INDEX moz_places_visitcount ON moz_places (visit_count);
CREATE TABLE moz_bookmarks (id INTEGER PRIMARY KEY, type INTEGER, fk INTEGER, parent INTEGER, title TEXT, guid TEXT);
INSERT INTO moz_bookmarks (id, type, parent, guid) VALUES (1, 2, 0, 'root________'), (2, 2, 1, 'tags________'), (3, 2, 2, 'atagfolder__'), (4, 2, 1, 'menu________');
`

func openFixture(t *testing.T, path, schema string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	run(t, db, schema)
	return db
}

func run(t *testing.T, db *sql.DB, statements string, args ...any) {
	t.Helper()
	if _, err := db.Exec(statements, args...); err != nil {
		t.Fatalf("%s: %v", statements, err)
	}
}

func firefoxProfile(t *testing.T, root, name string) (string, *sql.DB) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db := openFixture(t, filepath.Join(dir, "places.sqlite"), firefoxSchema)
	return dir, db
}

func addPlace(t *testing.T, db *sql.DB, id int, url, title string, visits int) {
	t.Helper()
	run(t, db, `INSERT INTO moz_places (id, url, title, visit_count) VALUES (?, ?, ?, ?)`, id, url, title, visits)
}

func addBookmark(t *testing.T, db *sql.DB, place int, title string) {
	t.Helper()
	run(t, db, `INSERT INTO moz_bookmarks (type, fk, parent, title) VALUES (1, ?, 4, ?)`, place, title)
}

func urlsOf(pages []browserPage) []string {
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = p.url
	}
	return out
}

func firefoxIndex(t *testing.T) (*BrowserIndex, string) {
	t.Helper()
	home := t.TempDir()
	return NewBrowserIndex(home, filepath.Join(home, ".config"), filepath.Join(home, "icons")), filepath.Join(home, ".mozilla", "firefox")
}

func TestFirefoxBookmarksAndHistory(t *testing.T) {
	index, root := firefoxIndex(t)
	_, db := firefoxProfile(t, root, "abc.default")
	addPlace(t, db, 1, "https://rust-lang.org/", "Rust", 3)
	addPlace(t, db, 2, "https://go.dev/doc", "Go docs", 40)
	addPlace(t, db, 3, "place:sort=8", "Smart folder", 0)
	addPlace(t, db, 4, "javascript:alert(1)", "Bookmarklet", 0)
	addPlace(t, db, 5, "file:///etc/passwd", "A file", 9)
	addPlace(t, db, 6, "https://never-visited.example/", "Unvisited", 0)
	addBookmark(t, db, 1, "Rust lang")
	addBookmark(t, db, 3, "Smart")
	addBookmark(t, db, 4, "Bookmarklet")
	addBookmark(t, db, 6, "")
	run(t, db, `INSERT INTO moz_places (id, url, title, visit_count, hidden) VALUES (7, 'https://redirect.example/', 'Hidden', 99, 1)`)
	run(t, db, `INSERT INTO moz_places (id, url, title, visit_count) VALUES (8, 'https://tagged-only.example/', 'Tagged', 0)`)
	run(t, db, `INSERT INTO moz_bookmarks (type, fk, parent, title) VALUES (1, 8, 3, NULL)`)

	pages := index.read(context.Background())
	if got, want := urlsOf(pages.bookmarks), []string{"https://rust-lang.org/", "https://never-visited.example/"}; !slices.Equal(got, want) {
		t.Errorf("bookmarks = %q, want %q", got, want)
	}
	if pages.bookmarks[0].title != "Rust lang" || pages.bookmarks[1].title != "Unvisited" {
		t.Errorf("a bookmark's own title wins, then the page's: %+v", pages.bookmarks)
	}
	if got, want := urlsOf(pages.visited), []string{"https://go.dev/doc", "https://rust-lang.org/"}; !slices.Equal(got, want) {
		t.Errorf("visited = %q, want %q", got, want)
	}
}

// A running browser holds its database exclusively, its newest rows in
// the write-ahead log.
func TestReadsADatabaseARunningBrowserHoldsLocked(t *testing.T) {
	index, root := firefoxIndex(t)
	_, db := firefoxProfile(t, root, "locked.default")
	run(t, db, `PRAGMA journal_mode = WAL; PRAGMA wal_autocheckpoint = 0; PRAGMA locking_mode = EXCLUSIVE;`)
	addPlace(t, db, 1, "https://in-the-wal.example/", "In the WAL", 5)
	addBookmark(t, db, 1, "In the WAL")

	live, err := sql.Open("sqlite", "file:"+filepath.Join(root, "locked.default", "places.sqlite")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := live.Close(); err != nil {
			t.Error(err)
		}
	}()
	var n int
	if err := live.QueryRow(`SELECT count(*) FROM moz_places`).Scan(&n); err == nil {
		t.Fatal("the fixture is not locked: this test proves nothing")
	}

	pages := index.read(context.Background())
	if got, want := urlsOf(pages.visited), []string{"https://in-the-wal.example/"}; !slices.Equal(got, want) {
		t.Errorf("visited = %q, want %q", got, want)
	}
	if got, want := urlsOf(pages.bookmarks), []string{"https://in-the-wal.example/"}; !slices.Equal(got, want) {
		t.Errorf("bookmarks = %q, want %q", got, want)
	}
	run(t, db, `INSERT INTO moz_places (id, url, title, visit_count) VALUES (2, 'https://still-writable.example/', '', 1)`)
}

func TestChromiumBookmarksAndHistory(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".config")
	dir := filepath.Join(config, "google-chrome", "Default")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	bookmarks := `{"checksum":"x","version":1,"roots":{
		"bookmark_bar":{"type":"folder","name":"Bar","children":[
			{"type":"url","name":"Top","url":"https://top.example/"},
			{"type":"folder","name":"Nested","children":[{"type":"url","name":"Deep","url":"https://deep.example/"}]},
			{"type":"url","name":"Internal","url":"chrome://settings/"}]},
		"other":{"type":"folder","children":[{"type":"url","name":"Other","url":"http://other.example/"}]},
		"synced":{"type":"folder","children":[]}}}`
	if err := os.WriteFile(filepath.Join(dir, "Bookmarks"), []byte(bookmarks), 0o600); err != nil {
		t.Fatal(err)
	}
	db := openFixture(t, filepath.Join(dir, "History"), `CREATE TABLE urls (id INTEGER PRIMARY KEY, url TEXT, title TEXT, visit_count INTEGER NOT NULL, hidden INTEGER NOT NULL);`)
	run(t, db, `INSERT INTO urls VALUES (1, 'https://a.example/', 'A', 2, 0), (2, 'https://b.example/', 'B', 8, 0), (3, 'https://c.example/', 'C', 50, 1), (4, 'chrome://version/', 'V', 70, 0)`)

	pages := NewBrowserIndex(home, config, filepath.Join(home, "icons")).read(context.Background())
	if got, want := urlsOf(pages.bookmarks), []string{"https://deep.example/", "http://other.example/", "https://top.example/"}; !slices.Equal(got, want) {
		t.Errorf("bookmarks = %q, want %q", got, want)
	}
	if got, want := urlsOf(pages.visited), []string{"https://b.example/", "https://a.example/"}; !slices.Equal(got, want) {
		t.Errorf("visited = %q, want %q", got, want)
	}
}

func TestChromiumWithoutABookmarksFileStillHasHistory(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".config")
	dir := filepath.Join(config, "chromium", "Default")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db := openFixture(t, filepath.Join(dir, "History"), `CREATE TABLE urls (id INTEGER PRIMARY KEY, url TEXT, title TEXT, visit_count INTEGER NOT NULL, hidden INTEGER NOT NULL);`)
	run(t, db, `INSERT INTO urls VALUES (1, 'https://a.example/', 'A', 2, 0)`)
	pages := NewBrowserIndex(home, config, filepath.Join(home, "icons")).read(context.Background())
	if len(pages.bookmarks) != 0 || len(pages.visited) != 1 {
		t.Errorf("pages = %+v", pages)
	}
}

func TestProfilesMergeIntoOnePagePerURL(t *testing.T) {
	got := mergeProfiles([]browserPages{
		{visited: []browserPage{{url: "https://a.example/", visits: 2}, {url: "https://b.example/", title: "B  \n title", visits: 3}}},
		{visited: []browserPage{{url: "https://a.example/", title: "A", visits: 5}, {url: "https://c.example/", visits: 3}}},
	}).visited
	want := []browserPage{
		{url: "https://a.example/", title: "A", visits: 7},
		{url: "https://b.example/", title: "B title", visits: 3},
		{url: "https://c.example/", visits: 3},
	}
	if !slices.Equal(got, want) {
		t.Errorf("merged = %+v, want %+v", got, want)
	}
}

func TestDisplayURLHidesTheQuery(t *testing.T) {
	for raw, want := range map[string]string{
		"https://www.example.com/":                "example.com",
		"https://example.com/a/b?token=hunter2#x": "example.com/a/b",
		"http://localhost:3000/x":                 "localhost:3000/x",
		"https://%zz":                             "%zz",
	} {
		if got := displayURL(raw); got != want {
			t.Errorf("displayURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestVisitRankGrowsWithVisitsAndIsCapped(t *testing.T) {
	if visitRank(0) != 0 {
		t.Errorf("no visits rank %d", visitRank(0))
	}
	if visitRank(100) <= visitRank(2) {
		t.Errorf("visits do not lift a page: %d, %d", visitRank(100), visitRank(2))
	}
	if visitRank(1<<62) != visitRankMax {
		t.Errorf("rank not capped: %d", visitRank(1<<62))
	}
}

// The index rereads a profile only when its files changed.
func TestIndexRereadsOnlyAChangedProfile(t *testing.T) {
	index, root := firefoxIndex(t)
	dir, db := firefoxProfile(t, root, "p.default")
	addPlace(t, db, 1, "https://one.example/", "One", 1)
	if got := urlsOf(index.read(context.Background()).visited); !slices.Equal(got, []string{"https://one.example/"}) {
		t.Fatalf("first read = %q", got)
	}
	path := filepath.Join(dir, "places.sqlite")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	garbage := make([]byte, info.Size())
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got := urlsOf(index.read(context.Background()).visited); !slices.Equal(got, []string{"https://one.example/"}) {
		t.Errorf("an unchanged stamp was reread: %q", got)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := urlsOf(index.read(context.Background()).visited); !slices.Equal(got, []string{"https://one.example/"}) {
		t.Errorf("an unreadable profile lost its last reading: %q", got)
	}
}

func TestIndexSeesNewRowsAndDropsARemovedProfile(t *testing.T) {
	index, root := firefoxIndex(t)
	dir, db := firefoxProfile(t, root, "p.default")
	addPlace(t, db, 1, "https://one.example/", "One", 1)
	index.read(context.Background())
	addPlace(t, db, 2, "https://two.example/", "Two", 2)
	if got, want := urlsOf(index.read(context.Background()).visited), []string{"https://two.example/", "https://one.example/"}; !slices.Equal(got, want) {
		t.Errorf("after a write = %q, want %q", got, want)
	}
	if err := os.Rename(dir, filepath.Join(t.TempDir(), "gone")); err != nil {
		t.Fatal(err)
	}
	if got := index.read(context.Background()).visited; len(got) != 0 {
		t.Errorf("a removed profile's pages remain: %+v", got)
	}
}

func TestACorruptProfileDoesNotHideAnother(t *testing.T) {
	index, root := firefoxIndex(t)
	_, db := firefoxProfile(t, root, "good.default")
	addPlace(t, db, 1, "https://good.example/", "Good", 1)
	bad := filepath.Join(root, "bad.default")
	if err := os.MkdirAll(bad, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "places.sqlite"), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := urlsOf(index.read(context.Background()).visited); !slices.Equal(got, []string{"https://good.example/"}) {
		t.Errorf("visited = %q", got)
	}
}

func TestNoBrowsersInstalledIsEmpty(t *testing.T) {
	index, _ := firefoxIndex(t)
	if got := index.read(context.Background()); len(got.bookmarks) != 0 || len(got.visited) != 0 {
		t.Errorf("pages = %+v", got)
	}
}

// browserFixture is a Firefox profile with a bookmarked page, a visited
// one that is also bookmarked, and two only visited.
func browserFixture(t *testing.T) *BrowserIndex {
	t.Helper()
	index, root := firefoxIndex(t)
	_, db := firefoxProfile(t, root, "p.default")
	addPlace(t, db, 1, "https://gitlab.example/", "Gitlab Marked", 7)
	addPlace(t, db, 2, "https://github.example/", "Github Often", 90)
	addPlace(t, db, 3, "https://gitea.example/", "Gitea Rarely", 2)
	addBookmark(t, db, 1, "Gitlab Marked")
	return index
}

type spawned struct {
	mu   sync.Mutex
	argv [][]string
}

func (s *spawned) env() Env {
	return Env{Spawn: func(argv []string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.argv = append(s.argv, argv)
	}}
}

func rootOver(t *testing.T, env Env, index *BrowserIndex, providers ...config.LauncherSearchProvider) (*Engine, *sink) {
	t.Helper()
	cfg := config.DefaultsLauncher().Search
	cfg.Providers = providers
	s := &sink{}
	e := Start(NewRoot(cfg, config.DefaultsPower(), Deps{Env: env, Browser: index}, t.TempDir()), nil, s, func(fn func()) { fn() })
	t.Cleanup(func() {
		e.Stop()
		select {
		case <-e.Done():
		case <-time.After(liveness):
			t.Error("the engine did not stop")
		}
	})
	return e, s
}

func TestBrowserRowsAtTheRoot(t *testing.T) {
	e, s := rootOver(t, Env{}, browserFixture(t), config.SearchBookmarks, config.SearchVisited)
	e.SetQuery("gitlab", 1)
	until(t, s, "#Browser", "Gitlab Marked")
	e.SetQuery("git", 2)
	until(t, s, "#Browser", "Github Often", "Gitlab Marked", "Gitea Rarely")
}

func TestShortQueriesLeaveBrowserRowsOut(t *testing.T) {
	e, s := rootOver(t, Env{}, browserFixture(t), config.SearchBookmarks, config.SearchVisited)
	e.SetQuery("gi", 1)
	until(t, s, "#Browser", "Gitlab Marked")
	e.SetQuery("g", 2)
	until(t, s)
	e.SetQuery("", 3)
	until(t, s)
}

func TestHistoryAloneListsWhatABookmarksProviderWouldHide(t *testing.T) {
	e, s := rootOver(t, Env{}, browserFixture(t), config.SearchVisited)
	e.SetQuery("gitlab", 1)
	until(t, s, "#Browser", "Gitlab Marked")
}

func TestBookmarksAloneOfferNoHistory(t *testing.T) {
	e, s := rootOver(t, Env{}, browserFixture(t), config.SearchBookmarks)
	e.SetQuery("github", 1)
	until(t, s)
	e.SetQuery("gitlab", 2)
	until(t, s, "#Browser", "Gitlab Marked")
}

func TestLeftOutOfTheConfigNothingIsRead(t *testing.T) {
	e, s := rootOver(t, Env{}, browserFixture(t), config.SearchCalculator)
	e.SetQuery("gitlab", 1)
	until(t, s)
}

func TestWithoutAnIndexNoBrowserProviders(t *testing.T) {
	cfg := config.DefaultsLauncher().Search
	cfg.Providers = []config.LauncherSearchProvider{config.SearchBookmarks, config.SearchVisited}
	if root := NewRoot(cfg, config.DefaultsPower(), Deps{}, t.TempDir()); len(root.Providers) != 0 {
		t.Errorf("providers = %+v", root.Providers)
	}
}

func TestActivatingABrowserRowOpensItsFullURL(t *testing.T) {
	index, root := firefoxIndex(t)
	_, db := firefoxProfile(t, root, "p.default")
	const target = "https://marked.example/page?id=7#top"
	addPlace(t, db, 1, target, "Marked", 1)
	addBookmark(t, db, 1, "Marked")
	opened := &spawned{}
	e, s := rootOver(t, opened.env(), index, config.SearchBookmarks)
	e.SetQuery("marked", 1)
	until(t, s, "#Browser", "Marked")
	row := s.last().Rows[1]
	if row.Entry.Subtitle != "marked.example/page" {
		t.Errorf("subtitle = %q", row.Entry.Subtitle)
	}
	e.Activate(row.Entry.ID, 0)
	waitFor(t, "finish", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.finished) == 1
	})
	opened.mu.Lock()
	defer opened.mu.Unlock()
	if len(opened.argv) != 1 || !slices.Equal(opened.argv[0], []string{"xdg-open", target}) {
		t.Errorf("spawned %q", opened.argv)
	}
}

func TestCopyURLCopiesTheFullURL(t *testing.T) {
	b := browserRows{}
	const target = "https://marked.example/page?id=7"
	actions := b.actions(Entry{Data: browserPage{url: target}})
	if len(actions) != 2 {
		t.Fatalf("actions = %d", len(actions))
	}
	if c, ok := actions[1].Run(context.Background()).(Copy); !ok || c.Text != target {
		t.Errorf("copy = %+v", actions[1].Run(context.Background()))
	}
}

func TestVisitedPickerListsMostVisitedFirstOnAnEmptyQuery(t *testing.T) {
	e, s := rootOver(t, Env{}, browserFixture(t), config.SearchVisited)
	e.SetQuery("most visited", 1)
	until(t, s, "#Commands", "Most Visited Sites")
	e.Activate(s.last().Rows[1].Entry.ID, 0)
	waitFor(t, "the level", func() bool { return s.last().Depth == 1 })
	until(t, s, "Github Often", "Gitlab Marked", "Gitea Rarely")
}

func TestBookmarksPickerListsEveryBookmark(t *testing.T) {
	e, s := rootOver(t, Env{}, browserFixture(t), config.SearchBookmarks)
	e.SetQuery("browser bookm", 1)
	until(t, s, "#Commands", "Browser Bookmarks")
	e.Activate(s.last().Rows[1].Entry.ID, 0)
	waitFor(t, "the level", func() bool { return s.last().Depth == 1 })
	until(t, s, "Gitlab Marked")
}

func TestTheBrowserSectionKeepsItsBestRowsWhicheverListFoundThem(t *testing.T) {
	index, root := firefoxIndex(t)
	_, db := firefoxProfile(t, root, "p.default")
	for i := 1; i <= SectionBrowser.rowCap(); i++ {
		addPlace(t, db, i, fmt.Sprintf("https://mark%d.example/", i), fmt.Sprintf("Needle mark %d", i), 0)
		addBookmark(t, db, i, fmt.Sprintf("Needle mark %d", i))
		addPlace(t, db, 100+i, fmt.Sprintf("https://seen%d.example/", i), fmt.Sprintf("Needle seen %d", i), 1)
	}
	e, s := rootOver(t, Env{}, index, config.SearchBookmarks, config.SearchVisited)
	e.SetQuery("needle", 1)
	want := []string{"#Browser"}
	for i := 1; i <= SectionBrowser.rowCap(); i++ {
		want = append(want, fmt.Sprintf("Needle mark %d", i))
	}
	until(t, s, want...)
}

func testPNG(t *testing.T, side int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, side, side))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const firefoxFaviconSchema = `
CREATE TABLE moz_icons (id INTEGER PRIMARY KEY, icon_url TEXT NOT NULL, width INTEGER NOT NULL DEFAULT 0, root INTEGER NOT NULL DEFAULT 0, data BLOB);
CREATE TABLE moz_pages_w_icons (id INTEGER PRIMARY KEY, page_url TEXT NOT NULL);
CREATE TABLE moz_icons_to_pages (page_id INTEGER NOT NULL, icon_id INTEGER NOT NULL, PRIMARY KEY (page_id, icon_id));
`

func addPageIcon(t *testing.T, db *sql.DB, id int, page string, width int, data []byte) {
	t.Helper()
	run(t, db, `INSERT INTO moz_icons (id, icon_url, width, data) VALUES (?, ?, ?, ?)`, id, "https://cdn.example/"+strconv.Itoa(id), width, data)
	run(t, db, `INSERT OR IGNORE INTO moz_pages_w_icons (id, page_url) VALUES ((SELECT id FROM moz_pages_w_icons WHERE page_url = ?), ?)`, page, page)
	run(t, db, `INSERT INTO moz_icons_to_pages (page_id, icon_id) VALUES ((SELECT id FROM moz_pages_w_icons WHERE page_url = ?), ?)`, page, id)
}

func TestFaviconsOfFirefoxPages(t *testing.T) {
	index, root := firefoxIndex(t)
	dir, db := firefoxProfile(t, root, "p.default")
	addPlace(t, db, 1, "https://www.sized.example/a", "Sized", 1)
	addPlace(t, db, 2, "https://svg.example/", "Vector only", 1)
	addPlace(t, db, 3, "https://root.example/page", "Root icon", 1)
	addPlace(t, db, 4, "https://bare.example/", "No icon", 1)
	addPlace(t, db, 5, "https://huge.example/", "Too large", 1)
	icons := openFixture(t, filepath.Join(dir, "favicons.sqlite"), firefoxFaviconSchema)
	small, want, large := testPNG(t, 16), testPNG(t, 33), testPNG(t, 64)
	addPageIcon(t, icons, 1, "https://www.sized.example/a", 16, small)
	addPageIcon(t, icons, 2, "https://sized.example/b", 64, large)
	addPageIcon(t, icons, 3, "https://sized.example/c", 33, want)
	addPageIcon(t, icons, 4, "https://svg.example/", 65535, []byte("<svg/>"))
	addPageIcon(t, icons, 5, "https://unlisted.example/", 32, small)
	addPageIcon(t, icons, 6, "https://huge.example/", 32, testPNG(t, faviconMaxSide+1))
	run(t, icons, `INSERT INTO moz_icons (id, icon_url, width, root, data) VALUES (7, 'https://root.example/favicon.ico', 32, 1, ?)`, small)

	got := index.read(context.Background()).icons
	if saved, err := os.ReadFile(got["sized.example"]); err != nil || !bytes.Equal(saved, want) {
		t.Errorf("sized.example kept %q (%v), not the 33 pixel icon", got["sized.example"], err)
	}
	if _, ok := got["root.example"]; !ok {
		t.Error("a site's root icon was not found")
	}
	for _, host := range []string{"svg.example", "bare.example", "unlisted.example", "huge.example"} {
		if path, ok := got[host]; ok {
			t.Errorf("%s has an icon: %s", host, path)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(got["sized.example"]))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "example") {
			t.Errorf("the cache names a host: %s", e.Name())
		}
	}
}

func TestFaviconsOfChromiumPages(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(home, ".config")
	dir := filepath.Join(configHome, "chromium", "Default")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	history := openFixture(t, filepath.Join(dir, "History"), `CREATE TABLE urls (id INTEGER PRIMARY KEY, url TEXT, title TEXT, visit_count INTEGER NOT NULL, hidden INTEGER NOT NULL);`)
	run(t, history, `INSERT INTO urls VALUES (1, 'https://a.example/x', 'A', 2, 0), (2, 'https://empty.example/', 'Empty', 1, 0)`)
	icons := openFixture(t, filepath.Join(dir, "Favicons"), `
		CREATE TABLE icon_mapping (id INTEGER PRIMARY KEY, page_url TEXT NOT NULL, icon_id INTEGER);
		CREATE TABLE favicon_bitmaps (id INTEGER PRIMARY KEY, icon_id INTEGER NOT NULL, image_data BLOB, width INTEGER DEFAULT 0);`)
	want := testPNG(t, 32)
	run(t, icons, `INSERT INTO icon_mapping VALUES (1, 'https://a.example/y', 9), (2, 'https://empty.example/', 10)`)
	run(t, icons, `INSERT INTO favicon_bitmaps VALUES (1, 9, ?, 16), (2, 9, ?, 32), (3, 10, x'', 0)`, testPNG(t, 16), want)
	got := NewBrowserIndex(home, configHome, filepath.Join(home, "icons")).read(context.Background()).icons
	if saved, err := os.ReadFile(got["a.example"]); err != nil || !bytes.Equal(saved, want) {
		t.Errorf("a.example: %q, %v", got["a.example"], err)
	}
	if _, ok := got["empty.example"]; ok {
		t.Error("an empty bitmap became an icon")
	}
}

func TestACorruptIconDatabaseStillListsThePages(t *testing.T) {
	index, root := firefoxIndex(t)
	dir, db := firefoxProfile(t, root, "p.default")
	addPlace(t, db, 1, "https://one.example/", "One", 1)
	if err := os.WriteFile(filepath.Join(dir, "favicons.sqlite"), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	pages := index.read(context.Background())
	if len(pages.visited) != 1 || len(pages.icons) != 0 {
		t.Errorf("pages = %+v", pages)
	}
}

func TestBrowserRowsShowTheSitesIconElseTheGenericOne(t *testing.T) {
	index, root := firefoxIndex(t)
	dir, db := firefoxProfile(t, root, "p.default")
	addPlace(t, db, 1, "https://iconed.example/", "Needle Iconed", 2)
	addPlace(t, db, 2, "https://plain.example/", "Needle Plain", 1)
	icons := openFixture(t, filepath.Join(dir, "favicons.sqlite"), firefoxFaviconSchema)
	addPageIcon(t, icons, 1, "https://iconed.example/", 32, testPNG(t, 32))
	var entries []Entry
	NewVisitedPages(Env{}, index, false).Load(context.Background(), func(e []Entry) { entries = e })
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	if _, ok := entries[0].Icon.(launcher.IconFile); !ok {
		t.Errorf("iconed row: %#v", entries[0].Icon)
	}
	if entries[1].Icon != launcher.Icon(launcher.IconName("ld-history-symbolic")) {
		t.Errorf("plain row: %#v", entries[1].Icon)
	}
}

func publishedTitles(load func(publish func([]Entry))) [][]string {
	var out [][]string
	load(func(entries []Entry) {
		row := make([]string, len(entries))
		for i, e := range entries {
			row[i] = e.Title
		}
		out = append(out, row)
	})
	return out
}

func TestTheLastReadingShowsAtOnceThenTheFreshOne(t *testing.T) {
	index, root := firefoxIndex(t)
	_, db := firefoxProfile(t, root, "p.default")
	addPlace(t, db, 1, "https://one.example/", "One", 1)
	visited := NewVisitedPages(Env{}, index, false)
	load := func(publish func([]Entry)) { visited.Load(context.Background(), publish) }

	first := publishedTitles(load)
	if len(first) != 1 || !slices.Equal(first[0], []string{"One"}) {
		t.Fatalf("the first open published %q", first)
	}
	if again := publishedTitles(load); len(again) != 1 || !slices.Equal(again[0], []string{"One"}) {
		t.Errorf("nothing changed, yet it published %q", again)
	}
	addPlace(t, db, 2, "https://two.example/", "Two", 5)
	changed := publishedTitles(load)
	if len(changed) != 2 || !slices.Equal(changed[0], []string{"One"}) || !slices.Equal(changed[1], []string{"Two", "One"}) {
		t.Errorf("after a write it published %q", changed)
	}
}

// iconedProfile has one visited page with its icon saved, and its icon
// database still to be written to.
func iconedProfile(t *testing.T) (index *BrowserIndex, dir string, db, icons *sql.DB, iconFile string) {
	t.Helper()
	index, root := firefoxIndex(t)
	dir, db = firefoxProfile(t, root, "p.default")
	addPlace(t, db, 1, "https://one.example/", "One", 1)
	addPlace(t, db, 2, "https://unknown.example/", "Icon later", 1)
	icons = openFixture(t, filepath.Join(dir, "favicons.sqlite"), firefoxFaviconSchema)
	addPageIcon(t, icons, 1, "https://one.example/", 32, testPNG(t, 32))
	iconFile = index.read(context.Background()).icons["one.example"]
	if iconFile == "" {
		t.Fatal("no icon saved")
	}
	return index, dir, db, icons, iconFile
}

func TestAVisitToAKnownSiteLeavesTheIconsAlone(t *testing.T) {
	index, _, db, _, iconFile := iconedProfile(t)
	if err := os.Remove(iconFile); err != nil {
		t.Fatal(err)
	}
	addPlace(t, db, 3, "https://one.example/other", "Another page", 1)
	pages := index.read(context.Background())
	if len(pages.visited) != 3 {
		t.Fatalf("the history was not reread: %+v", pages.visited)
	}
	if _, err := os.Stat(iconFile); err == nil {
		t.Error("the icons were read again")
	}
}

func TestAVisitToANewSiteLooksForItsIcon(t *testing.T) {
	index, _, db, icons, _ := iconedProfile(t)
	addPageIcon(t, icons, 2, "https://new.example/", 32, testPNG(t, 32))
	index.read(context.Background())
	addPlace(t, db, 3, "https://new.example/", "New", 1)
	if _, ok := index.read(context.Background()).icons["new.example"]; !ok {
		t.Error("the new site has no icon")
	}
}

func TestANewIconLeavesTheHistoryAlone(t *testing.T) {
	index, dir, _, icons, _ := iconedProfile(t)
	path := filepath.Join(dir, "places.sqlite")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, info.Size()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	addPageIcon(t, icons, 2, "https://unknown.example/", 32, testPNG(t, 32))
	pages := index.read(context.Background())
	if _, ok := pages.icons["unknown.example"]; !ok {
		t.Error("the new icon was not read")
	}
	if len(pages.visited) != 2 {
		t.Errorf("the unchanged history was lost: %+v", pages.visited)
	}
}

func TestAFailedIconReadIsTriedAgain(t *testing.T) {
	home := t.TempDir()
	iconDir := filepath.Join(home, "icons")
	index := NewBrowserIndex(home, filepath.Join(home, ".config"), iconDir)
	dir, db := firefoxProfile(t, filepath.Join(home, ".mozilla", "firefox"), "p.default")
	addPlace(t, db, 1, "https://one.example/", "One", 1)
	icons := openFixture(t, filepath.Join(dir, "favicons.sqlite"), firefoxFaviconSchema)
	addPageIcon(t, icons, 1, "https://one.example/", 32, testPNG(t, 32))
	if err := os.WriteFile(iconDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := index.read(context.Background()); len(got.icons) != 0 || len(got.visited) != 1 {
		t.Fatalf("pages = %+v", got)
	}
	if err := os.Remove(iconDir); err != nil {
		t.Fatal(err)
	}
	if _, ok := index.read(context.Background()).icons["one.example"]; !ok {
		t.Error("with nothing changed in the browser, the icons were not tried again")
	}
}
