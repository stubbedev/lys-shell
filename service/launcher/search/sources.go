package search

import (
	"context"
	"slices"
	"sync"
	"time"
)

// WatchFunc calls fn when anything under paths changes (app.WatchFiles).
type WatchFunc func(paths []string, recursive bool, fn func()) (stop func(), err error)

// Sources keeps what the panel searches and rarely changes for the
// shell's lifetime: the rows of the apps, scripts, settings, files and
// emoji, and the frecency store. A session shows the rows kept from the
// last one in its first frame and loads a source again only once a watch
// saw its files change, so opening the panel reads nothing from the disk.
type Sources struct {
	watch WatchFunc
	now   func() time.Time

	mu   sync.Mutex
	kept map[string]*keptRows

	freqMu sync.Mutex
	freq   *Frecency
}

// NewSources keeps rows refreshed through watch; nil watches nothing, so
// every session loads its sources again behind the rows it starts with.
func NewSources(watch WatchFunc, now func() time.Time) *Sources {
	return &Sources{watch: watch, now: now, kept: map[string]*keptRows{}}
}

// Frecency opens the store once with open and hands every later session
// the same one.
func (s *Sources) Frecency(open func() (*Frecency, error)) func() (*Frecency, error) {
	return func() (*Frecency, error) {
		s.freqMu.Lock()
		defer s.freqMu.Unlock()
		if s.freq != nil {
			return s.freq, nil
		}
		f, err := open()
		s.freq = f
		return f, err
	}
}

// keepSpec says when a kept source is stale: something under paths
// changed, or it is older than maxAge. No paths and no maxAge is never.
type keepSpec struct {
	paths     []string
	recursive bool
	maxAge    time.Duration
}

// keptRows is one source's rows between sessions.
type keptRows struct {
	mu      sync.Mutex
	entries []Entry
	has     bool
	// gen counts the changes seen; loaded is the gen the rows were loaded
	// at, so a change during a load leaves them stale.
	gen, loaded uint64
	at          time.Time
	maxAge      time.Duration
	// unwatched is a source whose files cannot be watched: stale in every
	// session.
	unwatched bool
	stop      func()
}

func (k *keptRows) invalidate() {
	k.mu.Lock()
	k.gen++
	k.mu.Unlock()
}

// snapshot is the rows kept, whether there are any, and whether they are
// still current.
func (k *keptRows) snapshot(now time.Time) (entries []Entry, has, fresh bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	fresh = k.has && !k.unwatched && k.loaded == k.gen && (k.maxAge == 0 || now.Sub(k.at) < k.maxAge)
	return k.entries, k.has, fresh
}

func (k *keptRows) begin() uint64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.gen
}

func (k *keptRows) store(gen uint64, entries []Entry, at time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.entries, k.has, k.loaded, k.at = entries, true, gen, at
}

// keep is p with its rows kept under key. A key no session asked for since
// the last retain is dropped with its watch.
func (s *Sources) keep(key string, p Provider, spec keepSpec) Provider {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := s.kept[key]
	if k == nil {
		k = &keptRows{maxAge: spec.maxAge}
		if len(spec.paths) > 0 {
			k.unwatched = true
			if s.watch != nil {
				if stop, err := s.watch(spec.paths, spec.recursive, k.invalidate); err == nil {
					k.stop, k.unwatched = stop, false
				}
			}
		}
		s.kept[key] = k
	}
	return &kept{Provider: p, rows: k, now: s.now}
}

// retain drops every kept source but keys, stopping their watches.
func (s *Sources) retain(keys []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, k := range s.kept {
		if slices.Contains(keys, key) {
			continue
		}
		if k.stop != nil {
			k.stop()
		}
		delete(s.kept, key)
	}
}

// kept is a session's provider over rows kept between sessions: its Load
// is the provider's, remembering the rows a load that ran to its end
// published last.
type kept struct {
	Provider
	rows *keptRows
	now  func() time.Time
}

// Load implements Provider.
func (k *kept) Load(ctx context.Context, publish func([]Entry)) {
	gen := k.rows.begin()
	var last []Entry
	got := false
	k.Provider.Load(ctx, func(entries []Entry) {
		last, got = entries, true
		publish(entries)
	})
	if got && ctx.Err() == nil {
		k.rows.store(gen, last, k.now())
	}
}

// Answer implements Answerer for a provider that answers.
func (k *kept) Answer(query string) []Entry {
	if a, ok := k.Provider.(Answerer); ok {
		return a.Answer(query)
	}
	return nil
}
