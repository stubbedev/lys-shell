package search

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/atomicfile"
	"github.com/stubbedev/lys-lib/third_party/gomath"
)

const (
	// frecencyHalfLife is how long a use takes to count half as much:
	// a tool used daily last month sinks below one used twice this week.
	frecencyHalfLife = 14 * 24 * time.Hour
	// frecencyMaxEntries bounds the file; the weakest go first.
	frecencyMaxEntries = 2000
	// frecencyMaxQueries bounds what one entry remembers being found by.
	frecencyMaxQueries = 8
	// frecencyQueryChars is the longest query prefix remembered: past
	// this a person has said enough for the matcher to find it unaided.
	frecencyQueryChars = 8
	// frecencyFileVersion marks the format.
	frecencyFileVersion = 1
)

// useRecord is one key's learned weight: decayed use counts as of At,
// overall and per query that found it.
type useRecord struct {
	Score   float64            `json:"score"`
	At      int64              `json:"at"`
	Queries map[string]float64 `json:"queries,omitempty"`
}

// Frecency learns what gets picked: every use adds one to a count that
// halves every two weeks, kept overall and for the query it was found
// by, so "f" comes to mean Firefox for someone who keeps choosing it.
// The file lives in the state directory and is replaced atomically. The
// shell keeps one for its lifetime, so a session closing and the next
// opening may use it at once.
type Frecency struct {
	mu      sync.Mutex
	path    string
	now     func() time.Time
	entries map[string]*useRecord
}

type frecencyFile struct {
	Version int                   `json:"version"`
	Entries map[string]*useRecord `json:"entries"`
}

// OpenFrecency reads path. A missing file is an empty store; a corrupt
// one is an error, and a store that starts empty and replaces it on the
// next use (the launcher still has to open).
func OpenFrecency(path string, now func() time.Time) (*Frecency, error) {
	f := &Frecency{path: path, now: now, entries: map[string]*useRecord{}}
	data, err := os.ReadFile(path) //nolint:gosec // the launcher's own state file
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("frecency: read %s: %w", path, err)
	}
	var file frecencyFile
	if err := json.Unmarshal(data, &file); err != nil {
		return f, fmt.Errorf("frecency: parse %s: %w", path, err)
	}
	if file.Version != frecencyFileVersion {
		return f, fmt.Errorf("frecency: %s has version %d, want %d", path, file.Version, frecencyFileVersion)
	}
	for k, r := range file.Entries {
		if r != nil {
			f.entries[k] = r
		}
	}
	return f, nil
}

func decay(score float64, from, to int64) float64 {
	if to <= from {
		return score
	}
	return score * gomath.Pow(2, -float64(to-from)/frecencyHalfLife.Seconds())
}

// queryKey is the remembered form of a query: folded and cut short.
func queryKey(query string) string {
	q := strings.ToLower(strings.TrimSpace(query))
	n := 0
	for i := range q {
		if n == frecencyQueryChars {
			return q[:i]
		}
		n++
	}
	return q
}

// Record counts one use of key, found by query, and saves.
func (f *Frecency) Record(key, query string) error {
	if key == "" {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now().Unix()
	r := f.entries[key]
	if r == nil {
		r = &useRecord{At: now}
		f.entries[key] = r
	}
	r.Score = decay(r.Score, r.At, now) + 1
	for q, s := range r.Queries {
		r.Queries[q] = decay(s, r.At, now)
	}
	r.At = now
	if q := queryKey(query); q != "" {
		if r.Queries == nil {
			r.Queries = map[string]float64{}
		}
		r.Queries[q]++
		for len(r.Queries) > frecencyMaxQueries {
			delete(r.Queries, weakest(r.Queries))
		}
	}
	f.prune(now)
	return f.save()
}

// weakest is the lowest-scored key, ties by name, so pruning does not
// depend on map order.
func weakest(m map[string]float64) string {
	keys := slices.Sorted(maps.Keys(m))
	return slices.MinFunc(keys, func(a, b string) int { return cmp.Compare(m[a], m[b]) })
}

func (f *Frecency) prune(now int64) {
	over := len(f.entries) - frecencyMaxEntries
	if over <= 0 {
		return
	}
	keys := slices.Sorted(maps.Keys(f.entries))
	slices.SortStableFunc(keys, func(a, b string) int {
		ra, rb := f.entries[a], f.entries[b]
		return cmp.Compare(decay(ra.Score, ra.At, now), decay(rb.Score, rb.At, now))
	})
	for _, k := range keys[:over] {
		delete(f.entries, k)
	}
}

// Forget drops key and saves.
func (f *Frecency) Forget(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.entries[key]; !ok {
		return nil
	}
	delete(f.entries, key)
	return f.save()
}

func (f *Frecency) save() error {
	data, err := json.Marshal(frecencyFile{Version: frecencyFileVersion, Entries: f.entries})
	if err != nil {
		return fmt.Errorf("frecency: encode: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return fmt.Errorf("frecency: create the state directory: %w", err)
	}
	if err := atomicfile.WriteFile(f.path, data, 0o600); err != nil {
		return fmt.Errorf("frecency: write %s: %w", f.path, err)
	}
	return nil
}

// boostPerDoubling is the score a key gains each time its use count
// doubles: a daily app lifts past a slightly better match but never past
// a plainly better one.
const (
	boostPerDoubling = 10.0
	boostMax         = 60
	// queryBoost is the extra for having been picked for this very query.
	queryBoost = 40
)

func boostOf(count float64) int {
	if count <= 0 {
		return 0
	}
	return min(int(math.Round(boostPerDoubling*gomath.Log2(1+count))), boostMax)
}

// Boost is the score key's history adds, overall.
func (f *Frecency) Boost(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.entries[key]
	if r == nil {
		return 0
	}
	return boostOf(decay(r.Score, r.At, f.now().Unix()))
}

// QueryBoostsInto fills into, empty, with each key picked for query (or a
// query it starts with) and the score that adds, so typing what you typed
// last time finds what you chose last time. It reports whether it found
// any.
func (f *Frecency) QueryBoostsInto(into map[string]int, query string) bool {
	q := queryKey(query)
	if q == "" {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var now int64
	found := false
	for key, r := range f.entries {
		best := 0.0
		for prefix, s := range r.Queries {
			if strings.HasPrefix(prefix, q) {
				if now == 0 {
					now = f.now().Unix()
				}
				best = max(best, decay(s, r.At, now))
			}
		}
		if best > 0 {
			into[key] = min(int(math.Round(queryBoost*gomath.Log2(1+best))), 2*queryBoost)
			found = true
		}
	}
	return found
}
