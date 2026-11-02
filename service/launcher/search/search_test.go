package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/modes"
)

// fakeProvider offers fixed rows under a section.
type fakeProvider struct {
	spec    Spec
	entries []Entry
	actions []Action
	// gate holds Load until closed, as a slow provider.
	gate chan struct{}
}

func (p *fakeProvider) Spec() Spec { return p.spec }
func (p *fakeProvider) Load(ctx context.Context, publish func([]Entry)) {
	if p.gate != nil {
		select {
		case <-p.gate:
		case <-ctx.Done():
			return
		}
	}
	publish(p.entries)
}

func (p *fakeProvider) Actions(Entry) []Action { return p.actions }

// sink records what the panel would be told, each run through the same
// channel the UI loop would drain.
type sink struct {
	mu       sync.Mutex
	frames   []Frame
	finished []Outcome
	notices  []string
}

func (s *sink) Results(f Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames = append(s.frames, f)
}

func (s *sink) Finish(o Outcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished = append(s.finished, o)
}

func (s *sink) Notice(m string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notices = append(s.notices, m)
}

func (s *sink) last() Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.frames) == 0 {
		return Frame{}
	}
	return s.frames[len(s.frames)-1]
}

const liveness = 5 * time.Second

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(liveness)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func start(t *testing.T, s *sink, freq *Frecency, providers ...Provider) *Engine {
	t.Helper()
	var open func() (*Frecency, error)
	if freq != nil {
		open = func() (*Frecency, error) { return freq, nil }
	}
	e := Start(&Level{Providers: providers}, open, s, func(fn func()) { fn() })
	t.Cleanup(func() {
		e.Stop()
		select {
		case <-e.Done():
		case <-time.After(liveness):
			t.Error("the engine did not stop")
		}
	})
	return e
}

func titles(f Frame) []string {
	var out []string
	for _, r := range f.Rows {
		if r.Header != "" {
			out = append(out, "#"+r.Header)
			continue
		}
		out = append(out, r.Entry.Title)
	}
	return out
}

func until(t *testing.T, s *sink, want ...string) {
	t.Helper()
	deadline := time.Now().Add(liveness)
	for !slices.Equal(titles(s.last()), want) {
		if time.Now().After(deadline) {
			t.Fatalf("rows = %q, want %q", titles(s.last()), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func entry(title string) Entry { return Entry{ID: title, Learn: title, Title: title} }

func tempFrecency(t *testing.T, now func() time.Time) *Frecency {
	t.Helper()
	f, err := OpenFrecency(filepath.Join(t.TempDir(), "state", "f.json"), now)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestOneListAcrossProvidersWithSections(t *testing.T) {
	s := &sink{}
	apps := &fakeProvider{spec: Spec{Section: SectionApps, Limit: 6, Weight: appWeight}, entries: []Entry{entry("Firefox"), entry("Files")}}
	windows := &fakeProvider{spec: Spec{Section: SectionWindows, Limit: 5}, entries: []Entry{entry("firefox: news")}}
	e := start(t, s, nil, apps, windows)
	waitFor(t, "both providers", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.frames) >= 3
	})
	e.SetQuery("firef", 1)
	until(t, s, "#"+SectionApps.Title(), "Firefox", "#"+SectionWindows.Title(), "firefox: news")
	e.SetQuery("zzz", 1)
	until(t, s)
}

func TestSubtitleAndKeywordsMatchBelowTitle(t *testing.T) {
	s := &sink{}
	p := &fakeProvider{spec: Spec{Section: SectionNone}, entries: []Entry{
		{ID: "a", Title: "Browser", Keywords: "web"},
		{ID: "b", Title: "web thing"},
	}}
	e := start(t, s, nil, p)
	e.SetQuery("web", 1)
	until(t, s, "web thing", "Browser")
}

func TestHighlightsMarkTheMatchedCharacters(t *testing.T) {
	s := &sink{}
	e := start(t, s, nil, &fakeProvider{spec: Spec{Section: SectionNone}, entries: []Entry{entry("Visual Studio Code")}})
	e.SetQuery("vsc", 1)
	until(t, s, "Visual Studio Code")
	got := s.last().Rows[0].Highlights()
	if want := []int{0, 7, 14}; !slices.Equal(got, want) {
		t.Errorf("highlights = %v, want the word starts %v", got, want)
	}
}

func TestFrecencyLiftsWhatGetsUsed(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	freq := tempFrecency(t, func() time.Time { return now })
	for range 8 {
		if err := freq.Record("Filezilla", ""); err != nil {
			t.Fatal(err)
		}
	}
	s := &sink{}
	e := start(t, s, freq, &fakeProvider{spec: Spec{Section: SectionNone}, entries: []Entry{entry("Files"), entry("Filezilla")}})
	e.SetQuery("fil", 1)
	until(t, s, "Filezilla", "Files")
}

func TestUntouchedRanksByMatchNotHistory(t *testing.T) {
	s := &sink{}
	e := start(t, s, nil, &fakeProvider{spec: Spec{Section: SectionNone}, entries: []Entry{entry("Filezilla"), entry("Files")}})
	e.SetQuery("fil", 1)
	until(t, s, "Files", "Filezilla")
}

func TestFrecencyLearnsTheQuery(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	freq := tempFrecency(t, func() time.Time { return now })
	if err := freq.Record("Zed", "ze"); err != nil {
		t.Fatal(err)
	}
	if got := freq.QueryBoosts("z")["Zed"]; got == 0 {
		t.Error("a query that starts what was typed before should find it")
	}
	if freq.QueryBoosts("q") != nil {
		t.Error("a different query must learn nothing from it")
	}
}

func TestFrecencyDecaysAndPersistsAtomically(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	clock := func() time.Time { return now }
	path := filepath.Join(t.TempDir(), "deep", "f.json")
	f, err := OpenFrecency(path, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Record("a", "query"); err != nil {
		t.Fatal(err)
	}
	fresh := f.Boost("a")
	if fresh == 0 {
		t.Fatal("a use must count")
	}
	listing, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(listing) != 1 {
		t.Errorf("left beside the file: %v", listing)
	}
	now = now.Add(4 * frecencyHalfLife)
	reread, err := OpenFrecency(path, clock)
	if err != nil {
		t.Fatal(err)
	}
	if got := reread.Boost("a"); got == 0 || got >= fresh {
		t.Errorf("boost after four half-lives = %d, fresh %d", got, fresh)
	}
	if err := reread.Forget("a"); err != nil {
		t.Fatal(err)
	}
	if reread.Boost("a") != 0 {
		t.Error("forgotten must be gone")
	}
}

func TestFrecencyCorruptFileIsAnErrorAndAnEmptyStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := OpenFrecency(path, time.Now)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("err = %v, want one naming the file", err)
	}
	if f == nil || f.Boost("x") != 0 {
		t.Error("the launcher still has to open")
	}
}

func TestFrecencyIsBounded(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	f := tempFrecency(t, func() time.Time { return now })
	for i := range frecencyMaxEntries {
		f.entries[fmt.Sprint("k", i)] = &useRecord{Score: 1, At: now.Unix()}
	}
	if err := f.Record("new", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.Record("new", ""); err != nil {
		t.Fatal(err)
	}
	if len(f.entries) > frecencyMaxEntries || f.Boost("new") == 0 {
		t.Errorf("%d entries, new boost %d", len(f.entries), f.Boost("new"))
	}
}

func TestEmptyQuerySuggestsWhatWasUsed(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	freq := tempFrecency(t, func() time.Time { return now })
	if err := freq.Record("Zed", ""); err != nil {
		t.Fatal(err)
	}
	s := &sink{}
	start(t, s, freq, &fakeProvider{spec: Spec{Section: SectionApps}, entries: []Entry{entry("Abc"), entry("Zed")}})
	until(t, s, "#"+SectionRecent.Title(), "Zed")
}

func TestEmptyQueryAtTheRootShowsNothingWithNoHistory(t *testing.T) {
	s := &sink{}
	start(t, s, nil, &fakeProvider{spec: Spec{Section: SectionApps}, entries: []Entry{entry("Abc")}})
	waitFor(t, "a frame", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.frames) > 1
	})
	if rows := s.last().Rows; len(rows) != 0 {
		t.Errorf("rows = %v", rows)
	}
}

func TestLateRowsLeaveWhatIsOnScreenInPlace(t *testing.T) {
	s := &sink{}
	gate := make(chan struct{})
	fast := &fakeProvider{spec: Spec{Section: SectionFiles}, entries: []Entry{entry("alpha one")}}
	slow := &fakeProvider{spec: Spec{Section: SectionApps, Weight: 1000}, entries: []Entry{entry("alpha two")}, gate: gate}
	e := start(t, s, nil, fast, slow)
	e.SetQuery("alpha", 1)
	until(t, s, "#"+SectionFiles.Title(), "alpha one")
	close(gate)
	until(t, s, "#"+SectionFiles.Title(), "alpha one", "#"+SectionApps.Title(), "alpha two")
	if !s.last().Late {
		t.Error("the frame must say its rows came late, so the selection stays")
	}
}

func TestSectionsDoNotSwapOnASmallLead(t *testing.T) {
	prev := []Section{SectionApps, SectionFiles}
	sections := []sectionRows{
		{section: SectionFiles, rows: []ranked{{score: 105}}},
		{section: SectionApps, rows: []ranked{{score: 100}}},
	}
	if got := orderSections(sections, prev, false); !slices.Equal(got, prev) {
		t.Errorf("a small lead swapped them: %v", got)
	}
	sections = []sectionRows{
		{section: SectionFiles, rows: []ranked{{score: 100 + sectionStickiness + 1}}},
		{section: SectionApps, rows: []ranked{{score: 100}}},
	}
	if got := orderSections(sections, prev, false); got[0] != SectionFiles {
		t.Errorf("a clear lead must win: %v", got)
	}
}

func TestAnswerLeadsAndWebClosesTheList(t *testing.T) {
	s := &sink{}
	web := NewWeb(Env{}, "E", "https://e/?q={query}")
	e := start(t, s, nil, web, Calculator{}, &fakeProvider{spec: Spec{Section: SectionApps}, entries: []Entry{entry("2+2 app")}})
	e.SetQuery("2+2", 1)
	waitFor(t, "rows", func() bool { return len(s.last().Rows) >= 5 })
	got := titles(s.last())
	if got[1] != "4" {
		t.Errorf("first row = %v", got)
	}
	if !strings.Contains(got[len(got)-1], "E") {
		t.Errorf("last row = %v", got)
	}
}

func TestWebTemplateWithoutQueryOffersNothing(t *testing.T) {
	if got := NewWeb(Env{}, "E", "https://e/").Answer("x"); got != nil {
		t.Errorf("answer = %v", got)
	}
	got := NewWeb(Env{}, "E", "https://e/?q={query}").Answer("a b&c")
	if len(got) != 1 || got[0].Data != "https://e/?q=a+b%26c" {
		t.Errorf("answer = %+v, want it escaped", got)
	}
}

func TestActionsLevelAndBackRestoreTheQuery(t *testing.T) {
	s := &sink{}
	ran := make(chan string, 1)
	p := &fakeProvider{spec: Spec{Section: SectionNone}, entries: []Entry{entry("Thing")}, actions: []Action{
		{Title: "Open", Run: func(context.Context) Outcome { ran <- "open"; return Close{} }},
		{Title: "Copy", Run: func(context.Context) Outcome { ran <- "copy"; return Copy{Text: "x"} }},
	}}
	e := start(t, s, nil, p)
	e.SetQuery("thi", 1)
	until(t, s, "Thing")
	e.ShowActions("Thing")
	until(t, s, "Open", "Copy")
	if f := s.last(); f.Depth != 1 || f.Title != "Thing" || !f.SetField || f.Field != "" {
		t.Errorf("actions frame = %+v", f)
	}
	e.SetQuery("cop", 1)
	until(t, s, "Copy")
	e.Back()
	until(t, s, "Thing")
	if f := s.last(); f.Depth != 0 || !f.SetField || f.Field != "thi" {
		t.Errorf("back frame = %+v", f)
	}
	e.ShowActions("Thing")
	until(t, s, "Open", "Copy")
	e.Activate("1", 0)
	select {
	case got := <-ran:
		if got != "copy" {
			t.Errorf("ran %s", got)
		}
	case <-time.After(liveness):
		t.Fatal("the action did not run")
	}
	waitFor(t, "finish", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.finished) == 1
	})
	if c, ok := s.finished[0].(Copy); !ok || c.Text != "x" {
		t.Errorf("finished %v", s.finished)
	}
}

func TestBackAtTheRootCloses(t *testing.T) {
	s := &sink{}
	e := start(t, s, nil)
	e.Back()
	waitFor(t, "finish", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.finished) == 1
	})
}

func TestActivatingRecordsTheUse(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	freq := tempFrecency(t, func() time.Time { return now })
	s := &sink{}
	done := make(chan struct{})
	p := &fakeProvider{spec: Spec{Section: SectionNone}, entries: []Entry{entry("Thing")}, actions: []Action{
		{Title: "Open", Run: func(context.Context) Outcome { close(done); return Close{} }},
	}}
	e := start(t, s, freq, p)
	e.SetQuery("thi", 1)
	until(t, s, "Thing")
	e.Activate("Thing", 0)
	<-done
	if freq.Boost("Thing") == 0 {
		t.Error("the use did not count")
	}
	data, err := os.ReadFile(freq.path)
	if err != nil || !strings.Contains(string(data), "Thing") {
		t.Errorf("file: %s, %v", data, err)
	}
}

func TestOvertakenQueriesAreSkippedButAcceptsSeeTheirText(t *testing.T) {
	q := func(s string) engineCmd { return cmdQuery{text: s} }
	if !overtaken(q("a"), []engineCmd{q("ab")}) {
		t.Error("an adjacent newer query overtakes")
	}
	if overtaken(q("a"), []engineCmd{cmdActivate{id: "x"}, q("ab")}) {
		t.Error("an accept between them must see the older text")
	}
	if overtaken(q("a"), nil) {
		t.Error("the last query stands")
	}
}

func TestStopCancelsProvidersAndLeavesNothingRunning(t *testing.T) {
	s := &sink{}
	gate := make(chan struct{})
	defer close(gate)
	e := Start(&Level{Providers: []Provider{&fakeProvider{spec: Spec{Section: SectionApps}, gate: gate}}}, nil, s, func(fn func()) { fn() })
	e.Stop()
	select {
	case <-e.Done():
	case <-time.After(liveness):
		t.Fatal("a provider kept the engine alive")
	}
}

func TestConvert(t *testing.T) {
	for in, want := range map[string]string{
		"5 km to mi":      "3.1068559612 mi",
		"100 f to c":      "37.7777777778°C",
		"0 c in f":        "32°F",
		"1 GB to MB":      "1000 MB",
		"1 GiB to MiB":    "1024 MiB",
		"8 Mbit to MB":    "1 MB",
		"2*(3+4)km to m":  "14000 m",
		"90 min to hours": "1.5 h",
		"1 kg in lb":      "2.2046226218 lb",
		"1 mph to km/h":   "1.609344 km/h",
		"180 deg to rad":  "3.1415926536 rad",
	} {
		got, ok := modes.Convert(in)
		if !ok || got.String() != want {
			t.Errorf("%q = %q (%v), want %q", in, got.String(), ok, want)
		}
	}
	for _, in := range []string{"5 km to kg", "5 km to", "km to mi", "go to mi", "5 km", "hello in there"} {
		if got, ok := modes.Convert(in); ok {
			t.Errorf("%q converted to %v", in, got)
		}
	}
}

func TestCalculatorAnswersOnlyWhatLooksLikeASum(t *testing.T) {
	c := Calculator{}
	for _, q := range []string{"2+2", "sqrt(16)", "5 km to mi", "10 % 3"} {
		if len(c.Answer(q)) != 1 {
			t.Errorf("%q: no answer", q)
		}
	}
	for _, q := range []string{"12", "e", "pi", "firefox", ""} {
		if got := c.Answer(q); got != nil {
			t.Errorf("%q answered %v", q, got)
		}
	}
}

func TestParseScript(t *testing.T) {
	src := `#!/bin/sh
# @lys.title Say Hello
# @lys.subtitle Greets
# @lys.keywords greet hi
# @lys.output copy
# @lys.argument name
# @lys.argument greeting optional
echo hi
# @lys.title Not read past the header
`
	s, ok, err := ParseScript("/x", strings.NewReader(src))
	if err != nil || !ok {
		t.Fatalf("ok %v err %v", ok, err)
	}
	if s.Title != "Say Hello" || s.Output != ScriptCopy || s.Keywords != "greet hi" || len(s.Args) != 2 || s.Args[0].Optional || !s.Args[1].Optional {
		t.Errorf("parsed %+v", s)
	}
	if _, ok, err := ParseScript("/x", strings.NewReader("#!/bin/sh\necho hi\n")); ok || err != nil {
		t.Errorf("no title: ok %v err %v", ok, err)
	}
	for _, bad := range []string{"# @lys.output loud\n", "# @lys.nope x\n", "# @lys.argument a b c\n", "# @lys.argument\n"} {
		_, _, err := ParseScript("/x", strings.NewReader(bad))
		if err == nil || !strings.Contains(err.Error(), "/x:1") {
			t.Errorf("%q: err = %v, want one naming the line", bad, err)
		}
	}
}

func TestScriptArgumentsStayData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c")
	body := "#!/bin/sh\n# @lys.title C\n# @lys.output copy\n# @lys.argument a\nprintf '%s' \"$1\"\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	s := &sink{}
	e := start(t, s, nil, NewScripts(Env{}, dir))
	e.SetQuery("c", 1)
	waitFor(t, "row", func() bool { return len(titles(s.last())) > 0 })
	id := ""
	for _, r := range s.last().Rows {
		if r.Header == "" {
			id = r.Entry.ID
		}
	}
	e.Activate(id, 0)
	waitFor(t, "the argument level", func() bool { return s.last().CanSubmit })
	e.Submit("")
	waitFor(t, "the refusal", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.notices) == 1
	})
	e.Submit("; rm -rf $HOME")
	waitFor(t, "finish", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.finished) == 1
	})
	if c, ok := s.finished[0].(Copy); !ok || c.Text != "; rm -rf $HOME" {
		t.Errorf("finished %+v", s.finished[0])
	}
}

func TestReadRecentFiles(t *testing.T) {
	dir := t.TempDir()
	older, newer := filepath.Join(dir, "older"), filepath.Join(dir, "newer")
	for _, p := range []string{older, newer} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	xbel := filepath.Join(dir, "r.xbel")
	body := fmt.Sprintf(`<?xml version="1.0"?><xbel><bookmark href="file://%s" modified="2026-01-01T00:00:00Z"/><bookmark href="file://%s" modified="2026-06-01T00:00:00Z"/><bookmark href="file://%s/gone" modified="2026-07-01T00:00:00Z"/><bookmark href="https://x" modified="2026-08-01T00:00:00Z"/></xbel>`, older, newer, dir)
	if err := os.WriteFile(xbel, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readRecentFiles(xbel, 10); !slices.Equal(got, []string{newer, older}) {
		t.Errorf("recent = %v", got)
	}
	if got := readRecentFiles(filepath.Join(dir, "none"), 10); got != nil {
		t.Errorf("missing file = %v", got)
	}
}

func TestWalkFilesRespectsItsBounds(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"a.txt", "sub/b.txt", ".hidden/c.txt", "node_modules/d.txt", "skipme/e.txt"} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(root, filepath.Join(root, "loop")); err != nil {
		t.Fatal(err)
	}
	got, complete := walkFiles(context.Background(), []string{root}, []string{"skipme"}, 100)
	slices.Sort(got)
	want := []string{filepath.Join(root, "a.txt"), filepath.Join(root, "sub") + "/", filepath.Join(root, "sub", "b.txt")}
	if !complete || !slices.Equal(got, want) {
		t.Errorf("walk = %v (complete %v), want %v", got, complete, want)
	}
	if got, complete := walkFiles(context.Background(), []string{root}, nil, 1); complete || len(got) != 1 {
		t.Errorf("limit: %v %v", got, complete)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, complete := walkFiles(ctx, []string{root}, nil, 100); complete {
		t.Error("a cancelled walk is not complete")
	}
}

func TestFilesLoadPublishesTheCacheThenIndexes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fresh.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(t.TempDir(), "idx")
	if err := os.WriteFile(cache, []byte("/old/cached.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(2 * fileIndexMaxAge)
	f := NewFiles(Env{}, FilesConfig{Roots: []string{root}, CachePath: cache, Home: "/home/x", Now: func() time.Time { return stale }})
	var published [][]Entry
	f.Load(context.Background(), func(e []Entry) { published = append(published, e) })
	if len(published) != 2 || published[0][0].Title != "cached.txt" || published[1][0].Title != "fresh.txt" {
		t.Fatalf("published %+v", published)
	}
	data, err := os.ReadFile(cache)
	if err != nil || !strings.Contains(string(data), "fresh.txt") {
		t.Errorf("cache: %s %v", data, err)
	}
	published = nil
	f.cfg.Now = time.Now
	f.Load(context.Background(), func(e []Entry) { published = append(published, e) })
	if len(published) != 1 {
		t.Errorf("a fresh index walks nothing: %d publishes", len(published))
	}
}

func TestTypedPathListsItsDirectory(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"Documents", "Downloads", "Pictures", ".hidden"} {
		if err := os.Mkdir(filepath.Join(home, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f := NewFiles(Env{}, FilesConfig{Home: home, Now: time.Now})
	got := f.Answer("~/do")
	if len(got) != 2 || got[0].Completion != "~/Documents/" {
		t.Fatalf("answer = %+v", got)
	}
	if rows := f.Answer("~/"); len(rows) != 3 {
		t.Errorf("hidden shows only when asked for: %+v", rows)
	}
	if f.Answer("documents") != nil {
		t.Error("only a path is a path")
	}
	if f.Answer("/does/not/exist/x") != nil {
		t.Error("a missing directory answers nothing")
	}
}

// fakeWindows records what the list asks of the compositor.
type fakeWindows struct {
	list    []modes.WindowInfo
	focused []string
	closed  []string
}

func (w *fakeWindows) List(context.Context) ([]modes.WindowInfo, error) { return w.list, nil }
func (w *fakeWindows) Focus(_ context.Context, id string) error {
	w.focused = append(w.focused, id)
	return nil
}

func (w *fakeWindows) Close(_ context.Context, id string) error {
	w.closed = append(w.closed, id)
	return nil
}

func TestWindowsLeaveOutTheFocusedAndJump(t *testing.T) {
	back := &fakeWindows{list: []modes.WindowInfo{
		{ID: "1", Title: "here", Class: "foot", Focused: true},
		{ID: "2", Title: "there", Class: "Firefox", Workspace: "3"},
	}}
	w := NewWindows(back)
	var got []Entry
	w.Load(context.Background(), func(e []Entry) { got = e })
	if len(got) != 1 || got[0].Title != "there" || got[0].Learn != "window:firefox" {
		t.Fatalf("entries %+v", got)
	}
	actions := w.Actions(got[0])
	if _, ok := actions[0].Run(context.Background()).(Close); !ok || !slices.Equal(back.focused, []string{"2"}) {
		t.Errorf("focused %v", back.focused)
	}
	if _, ok := actions[1].Run(context.Background()).(Close); !ok || !slices.Equal(back.closed, []string{"2"}) {
		t.Errorf("closed %v", back.closed)
	}
	var none []Entry
	NewWindows(nil).Load(context.Background(), func(e []Entry) { none = e })
	if none != nil {
		t.Errorf("no compositor offered %v", none)
	}
}

func TestSystemConfirmsWhatEndsTheSession(t *testing.T) {
	var ran []string
	env := Env{SpawnShell: func(l string) { ran = append(ran, l) }}
	p := config.DefaultsPower()
	p.ShowLock, p.ShowReboot, p.LockCommand, p.RebootCommand = true, true, "lockit", "rebootit"
	sys := NewSystem(env, p)
	var entries []Entry
	sys.Load(context.Background(), func(e []Entry) { entries = e })
	find := func(id string) Entry {
		for _, e := range entries {
			if e.ID == id {
				return e
			}
		}
		t.Fatalf("no %s in %+v", id, entries)
		return Entry{}
	}
	if _, ok := sys.Actions(find("system:lock"))[0].Run(context.Background()).(Close); !ok || !slices.Equal(ran, []string{"lockit"}) {
		t.Errorf("lock ran %v", ran)
	}
	ran = nil
	push, ok := sys.Actions(find("system:reboot"))[0].Run(context.Background()).(Push)
	if !ok || len(ran) != 0 {
		t.Fatalf("reboot must ask first: ran %v", ran)
	}
	var confirm []Entry
	prov := push.Level.Providers[0]
	prov.Load(context.Background(), func(e []Entry) { confirm = e })
	prov.Actions(confirm[0])[0].Run(context.Background())
	if !slices.Equal(ran, []string{"rebootit"}) {
		t.Errorf("confirmed ran %v", ran)
	}
}

// scriptedMode is a rofi mode whose reload changes what it shows.
type scriptedMode struct{ unlocked bool }

func (*scriptedMode) Name() string { return "m" }
func (m *scriptedMode) Load(context.Context) launcher.ModeState {
	if !m.unlocked {
		msg := "locked"
		return launcher.ModeState{Password: true, Message: &msg}
	}
	return launcher.ModeState{Items: []launcher.Item{{Display: "<b>item</b>", MatchText: "item"}}, MarkupRows: true, NoCustom: true}
}

func (m *scriptedMode) Activate(ctx context.Context, target launcher.Target, kind launcher.ActivateKind, _ string) launcher.Action {
	if c, ok := kind.(launcher.ActivateCustom); ok && c.Text == "pw" {
		m.unlocked = true
		return launcher.ActionReload{State: m.Load(ctx)}
	}
	if _, ok := target.Row(); ok {
		return launcher.ActionCopy{Text: "secret"}
	}
	return launcher.ActionNothing{}
}

func TestModeLevelHostsAModeWithItsPrompts(t *testing.T) {
	s := &sink{}
	e := start(t, s, nil, &fakeProvider{spec: Spec{Section: SectionNone}, entries: []Entry{entry("vault")}, actions: []Action{
		{Title: "open", Run: func(context.Context) Outcome { return Push{Level: ModeLevel(&scriptedMode{}, "Vault")} }},
	}})
	e.SetQuery("vault", 1)
	until(t, s, "vault")
	e.Activate("vault", 0)
	waitFor(t, "the masked prompt", func() bool { return s.last().Mask })
	if f := s.last(); f.Message != "locked" || !f.CanSubmit || f.Title != "Vault" {
		t.Errorf("prompt frame %+v", f)
	}
	e.Submit("pw")
	waitFor(t, "the items", func() bool { return !s.last().Mask && len(s.last().Rows) == 1 })
	f := s.last()
	if f.Rows[0].Entry.Title != "item" || f.CanSubmit {
		t.Errorf("items frame %+v", f)
	}
	e.Activate(f.Rows[0].Entry.ID, 0)
	waitFor(t, "finish", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.finished) == 1
	})
	if c, ok := s.finished[0].(Copy); !ok || c.Text != "secret" {
		t.Errorf("finished %+v", s.finished)
	}
}

func TestNewRootHonoursTheProviderList(t *testing.T) {
	cfg := config.DefaultsLauncher().Search
	root := NewRoot(cfg, config.DefaultsPower(), Deps{Env: Env{}}, t.TempDir())
	if len(root.Providers) < 7 {
		t.Errorf("defaults gave %d providers", len(root.Providers))
	}
	cfg.Providers = []config.LauncherSearchProvider{config.SearchWeb}
	root = NewRoot(cfg, config.DefaultsPower(), Deps{Env: Env{}}, t.TempDir())
	if len(root.Providers) != 1 {
		t.Errorf("providers = %d", len(root.Providers))
	}
}
