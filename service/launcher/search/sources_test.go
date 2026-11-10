package search

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/config"
)

// countingProvider counts its loads.
type countingProvider struct {
	fakeProvider
	loads atomic.Int32
}

func (p *countingProvider) Load(ctx context.Context, publish func([]Entry)) {
	p.loads.Add(1)
	p.fakeProvider.Load(ctx, publish)
}

// fakeWatch records the watches asked for, so a test can say a path changed.
type fakeWatch struct {
	mu      sync.Mutex
	paths   [][]string
	fns     []func()
	stopped int
}

func (w *fakeWatch) watch(paths []string, _ bool, fn func()) (func(), error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.paths = append(w.paths, paths)
	w.fns = append(w.fns, fn)
	return func() {
		w.mu.Lock()
		w.stopped++
		w.mu.Unlock()
	}, nil
}

func (w *fakeWatch) changed() {
	w.mu.Lock()
	fns := slices.Clone(w.fns)
	w.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// session runs one engine over level until t ends or the returned stop.
func session(t *testing.T, s *sink, level *Level, open func() (*Frecency, error)) func() {
	t.Helper()
	e := startLevel(t, s, level, open)
	return func() { stopped(t, e) }
}

func firstFrame(t *testing.T, s *sink) Frame {
	t.Helper()
	waitFor(t, "the first frame", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.frames) > 0
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frames[0]
}

func keptLevel(src *Sources, p Provider, spec keepSpec) *Level {
	return &Level{Query: "fire", Providers: []Provider{src.keep("apps", p, spec)}}
}

func TestAKeptSourceLoadsOnceAndFillsTheNextSessionsFirstFrame(t *testing.T) {
	src := NewSources((&fakeWatch{}).watch, time.Now)
	apps := &countingProvider{spec: Spec{Section: SectionNone}, entries: []Entry{entry("Firefox")}}
	spec := keepSpec{paths: []string{"/apps"}}

	first := &sink{}
	stop := session(t, first, keptLevel(src, apps, spec), nil)
	until(t, first, "Firefox")
	stop()

	second := &sink{}
	session(t, second, keptLevel(src, apps, spec), nil)
	if got := titles(firstFrame(t, second)); !slices.Equal(got, []string{"Firefox"}) {
		t.Errorf("the second session's first frame = %q, want the kept rows", got)
	}
	if n := apps.loads.Load(); n != 1 {
		t.Errorf("loads = %d, want 1: an unchanged source is not read again", n)
	}
}

type heldProvider struct {
	countingProvider
	release chan struct{}
}

func (p *heldProvider) Load(ctx context.Context, publish func([]Entry)) {
	p.countingProvider.Load(ctx, publish)
	<-p.release
}

func TestASourceStillInItsLoadWhenTheSessionEndsIsKept(t *testing.T) {
	src := NewSources((&fakeWatch{}).watch, time.Now)
	apps := &heldProvider{spec: Spec{Section: SectionNone}, entries: []Entry{entry("Firefox")}, release: make(chan struct{})}
	spec := keepSpec{paths: []string{"/apps"}}

	first := &sink{}
	e := startLevel(t, first, keptLevel(src, apps, spec), nil)
	release := sync.OnceFunc(func() { close(apps.release) })
	t.Cleanup(release)
	until(t, first, "Firefox")
	e.Stop()
	release()
	stopped(t, e)

	second := &sink{}
	stop := session(t, second, keptLevel(src, apps, spec), nil)
	if got := titles(firstFrame(t, second)); !slices.Equal(got, []string{"Firefox"}) {
		t.Errorf("the second session's first frame = %q, want the rows the first one showed", got)
	}
	stop()
	if n := apps.loads.Load(); n != 1 {
		t.Errorf("loads = %d, want 1: the rows were whole when the session ended", n)
	}
}

type stagedProvider struct {
	spec    Spec
	entries []Entry
	rest    chan struct{}
	loads   atomic.Int32
}

func (p *stagedProvider) Spec() Spec { return p.spec }

func (p *stagedProvider) Load(ctx context.Context, publish func([]Entry)) {
	p.loads.Add(1)
	publish(p.entries[:1])
	select {
	case <-p.rest:
		publish(p.entries)
	case <-ctx.Done():
	}
}

func (*stagedProvider) Actions(Entry) []Action { return nil }

func TestAStagedSourceIsKeptOnlyOnceItsLoadRanToItsEnd(t *testing.T) {
	src := NewSources((&fakeWatch{}).watch, time.Now)
	files := &stagedProvider{spec: Spec{Section: SectionNone}, entries: []Entry{entry("Firefox"), entry("Firewall")}, rest: make(chan struct{})}
	spec := keepSpec{paths: []string{"/files"}}

	first := &sink{}
	stop := session(t, first, keptLevel(src, files, spec), nil)
	until(t, first, "Firefox")
	stop()

	second := &sink{}
	stop = session(t, second, keptLevel(src, files, spec), nil)
	if got := titles(firstFrame(t, second)); len(got) != 0 {
		t.Errorf("the second session's first frame = %q, want nothing kept of a load the session's end cut short", got)
	}
	close(files.rest)
	until(t, second, "Firefox", "Firewall")
	stop()
	if n := files.loads.Load(); n != 2 {
		t.Fatalf("loads = %d, want 2: the cut load is run again", n)
	}

	third := &sink{}
	stop = session(t, third, keptLevel(src, files, spec), nil)
	if got := titles(firstFrame(t, third)); !slices.Equal(got, []string{"Firefox", "Firewall"}) {
		t.Errorf("the third session's first frame = %q, want the rows of the load that ran to its end", got)
	}
	stop()
	if n := files.loads.Load(); n != 2 {
		t.Errorf("loads = %d, want 2: a staged load that ran to its end is kept", n)
	}
}

func TestAChangeUnderAWatchedPathLoadsTheSourceAgain(t *testing.T) {
	w := &fakeWatch{}
	src := NewSources(w.watch, time.Now)
	apps := &countingProvider{spec: Spec{Section: SectionNone}, entries: []Entry{entry("Firefox")}}
	spec := keepSpec{paths: []string{"/apps"}}

	first := &sink{}
	stop := session(t, first, keptLevel(src, apps, spec), nil)
	until(t, first, "Firefox")
	stop()
	if len(w.paths) != 1 || !slices.Equal(w.paths[0], []string{"/apps"}) {
		t.Fatalf("watched %q, want the source's paths once", w.paths)
	}

	w.changed()
	apps.entries = []Entry{entry("Firefox Nightly")}
	second := &sink{}
	session(t, second, keptLevel(src, apps, spec), nil)
	if got := titles(firstFrame(t, second)); !slices.Equal(got, []string{"Firefox"}) {
		t.Errorf("first frame = %q, want the kept rows while the source loads again", got)
	}
	until(t, second, "Firefox Nightly")
	if n := apps.loads.Load(); n != 2 {
		t.Errorf("loads = %d, want 2", n)
	}
}

func TestAnUnwatchableSourceLoadsInEverySession(t *testing.T) {
	src := NewSources(nil, time.Now)
	apps := &countingProvider{spec: Spec{Section: SectionNone}, entries: []Entry{entry("Firefox")}}
	spec := keepSpec{paths: []string{"/apps"}}
	for range 2 {
		s := &sink{}
		stop := session(t, s, keptLevel(src, apps, spec), nil)
		until(t, s, "Firefox")
		stop()
	}
	if n := apps.loads.Load(); n != 2 {
		t.Errorf("loads = %d, want one per session without a watch", n)
	}
}

func TestAKeptSourceGoesStaleWithAge(t *testing.T) {
	now := time.Unix(1000, 0)
	src := NewSources((&fakeWatch{}).watch, func() time.Time { return now })
	apps := &countingProvider{spec: Spec{Section: SectionNone}, entries: []Entry{entry("Firefox")}}
	spec := keepSpec{paths: []string{"/files"}, maxAge: time.Minute}
	run := func() {
		s := &sink{}
		stop := session(t, s, keptLevel(src, apps, spec), nil)
		until(t, s, "Firefox")
		stop()
	}
	run()
	now = now.Add(30 * time.Second)
	run()
	if n := apps.loads.Load(); n != 1 {
		t.Fatalf("loads = %d within the age, want 1", n)
	}
	now = now.Add(time.Minute)
	run()
	if n := apps.loads.Load(); n != 2 {
		t.Errorf("loads = %d past the age, want 2", n)
	}
}

func TestAnEmptyListSettlesInOneUpdateOnceTheSourcesAreKept(t *testing.T) {
	freq := tempFrecency(t, time.Now)
	if err := freq.Record("Firefox", ""); err != nil {
		t.Fatal(err)
	}
	open := func() (*Frecency, error) { return freq, nil }
	src := NewSources((&fakeWatch{}).watch, time.Now)
	apps := &countingProvider{spec: Spec{Section: SectionApps}, entries: []Entry{entry("Firefox"), entry("Files")}}
	level := func(live *fakeProvider) *Level {
		return &Level{Providers: []Provider{src.keep("apps", apps, keepSpec{paths: []string{"/apps"}}), live}}
	}

	first := &sink{}
	stop := session(t, first, level(&fakeProvider{spec: Spec{Section: SectionWindows}}), open)
	until(t, first, "#"+SectionRecent.Title(), "Firefox")
	stop()

	s := &sink{}
	windows := &fakeProvider{spec: Spec{Section: SectionWindows}, entries: []Entry{{ID: "w", Title: "a window"}}, published: make(chan struct{})}
	e := startLevel(t, s, level(windows), open)
	select {
	case <-windows.published:
	case <-time.After(liveness):
		t.Fatal("the windows never loaded")
	}
	e.SetQuery("", 1)
	waitFor(t, "the query's frame", func() bool { return s.last().QuerySeq == 1 })
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := len(s.frames) - 1; n != 1 {
		t.Errorf("updates before the empty list settled = %d, want 1", n)
	}
	if got := titles(s.frames[0]); !slices.Equal(got, []string{"#" + SectionRecent.Title(), "Firefox"}) {
		t.Errorf("first frame = %q", got)
	}
}

func TestALateRowThatChangesTheListIsStillDelivered(t *testing.T) {
	s := &sink{}
	gate := make(chan struct{})
	slow := &fakeProvider{spec: Spec{Section: SectionNone}, entries: []Entry{entry("Firefox")}, gate: gate}
	e := start(t, s, nil, slow)
	e.SetQuery("fire", 1)
	waitFor(t, "the query's frame", func() bool { return s.last().QuerySeq == 1 })
	close(gate)
	until(t, s, "Firefox")
	if !s.last().Late {
		t.Error("the row must arrive as a late frame")
	}
}

func TestTheFrecencyStoreOpensOnceForEverySession(t *testing.T) {
	src := NewSources(nil, time.Now)
	opens := 0
	open := src.Frecency(func() (*Frecency, error) {
		opens++
		return OpenFrecency(filepath.Join(t.TempDir(), "f.json"), time.Now)
	})
	a, _ := open()
	b, _ := open()
	if opens != 1 || a != b {
		t.Errorf("opens = %d, same store %t: want one open, one store", opens, a == b)
	}
}

func writeDesktop(t *testing.T, dir, name, title string) string {
	t.Helper()
	path := filepath.Join(dir, "applications", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[Desktop Entry]\nType=Application\nName="+title+"\nExec=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDesktopFilesAreParsedOnlyInTheFirstSession(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_DATA_DIRS", t.TempDir())
	path := writeDesktop(t, data, "zed.desktop", "Zebra Editor")
	w := &fakeWatch{}
	src := NewSources(w.watch, time.Now)
	cfg := config.DefaultsLauncher().Search
	cfg.Providers = []config.LauncherSearchProvider{config.SearchApps}
	root := func() *Level {
		l := NewRoot(cfg, config.DefaultsPower(), Deps{Env: Env{}, Sources: src}, t.TempDir())
		l.Query = "zebra"
		return l
	}

	first := &sink{}
	stop := session(t, first, root(), nil)
	until(t, first, "#"+SectionApps.Title(), "Zebra Editor")
	stop()

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	second := &sink{}
	stop = session(t, second, root(), nil)
	if got := titles(firstFrame(t, second)); !slices.Equal(got, []string{"#" + SectionApps.Title(), "Zebra Editor"}) {
		t.Errorf("second session = %q: the desktop file was read again", got)
	}
	stop()

	w.changed()
	third := &sink{}
	session(t, third, root(), nil)
	until(t, third)
}

func TestARootWithoutASourceStopsItsWatch(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_DATA_DIRS", t.TempDir())
	w := &fakeWatch{}
	src := NewSources(w.watch, time.Now)
	cfg := config.DefaultsLauncher().Search
	cfg.Providers = []config.LauncherSearchProvider{config.SearchApps}
	NewRoot(cfg, config.DefaultsPower(), Deps{Env: Env{}, Sources: src}, t.TempDir())
	NewRoot(cfg, config.DefaultsPower(), Deps{Env: Env{}, Sources: src}, t.TempDir())
	if len(w.paths) != 1 || w.stopped != 0 {
		t.Fatalf("watches %d stopped %d, want one kept across roots", len(w.paths), w.stopped)
	}
	cfg.Providers = []config.LauncherSearchProvider{config.SearchWeb}
	NewRoot(cfg, config.DefaultsPower(), Deps{Env: Env{}, Sources: src}, t.TempDir())
	if w.stopped != 1 {
		t.Errorf("stopped %d, want the apps' watch gone with the apps", w.stopped)
	}
}
