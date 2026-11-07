package calendar

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stubbedev/lys-shell/internal/feed"
	"github.com/stubbedev/lys-shell/internal/fswatch"
)

const (
	maxFileBytes  = 4 << 20
	nextHorizon   = 7 * 24 * time.Hour
	alarmLookBack = 7 * 24 * time.Hour
	alarmHorizon  = 14 * 24 * time.Hour
)

// Source is one vdir directory of .ics files and the color its events
// wear.
type Source struct {
	Dir   string
	Color string
}

// Occurrence is one instance of an event, in the store's location.
type Occurrence struct {
	Summary  string
	Location string
	Start    time.Time
	End      time.Time
	AllDay   bool
	Color    string
}

// Reminder is an alarm of an occurrence coming due.
type Reminder struct {
	Occurrence
	At time.Time
}

// Clock is the time source reminders are scheduled on.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, fn func()) (stop func() bool)
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

func (wallClock) AfterFunc(d time.Duration, fn func()) func() bool { return time.AfterFunc(d, fn).Stop }

// WallClock is the real clock.
var WallClock Clock = wallClock{}

type calFile struct {
	source int
	events []Event
}

type monthKey struct {
	year  int
	month time.Month
}

// Month is a month's occurrences by day of the month.
type Month struct {
	days [31][]Occurrence
}

// Day lists day d's occurrences: all-day ones first, then by start.
func (m *Month) Day(d int) []Occurrence {
	if d < 1 || d > len(m.days) {
		return nil
	}
	return m.days[d-1]
}

// Store holds the events of the configured vdir directories, follows
// their changes, expands them per month on demand, and keeps each
// expansion until a file changes.
type Store struct {
	clock Clock
	loc   *time.Location

	mu       sync.Mutex
	sources  []Source
	files    map[string]calFile
	months   map[monthKey]*Month
	watchers []*fswatch.Watcher
	changes  *feed.Tick

	parses     int
	expansions int

	alarmNotify func(Reminder)
	alarmLast   time.Time
	alarmStop   func() bool
}

// NewStore is an empty store reading times in loc and scheduling
// reminders on clock.
func NewStore(clock Clock, loc *time.Location) *Store {
	return &Store{clock: clock, loc: loc, files: map[string]calFile{}, months: map[monthKey]*Month{}, changes: feed.NewTick()}
}

// Changes ticks after every change of the events; stop ends the feed.
func (s *Store) Changes() (<-chan struct{}, func()) { return s.changes.Subscribe() }

// Configured reports whether any source is configured.
func (s *Store) Configured() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sources) > 0
}

// Configure replaces the sources: every .ics file under each directory
// is read and the directories are watched. A directory that cannot be
// read or watched is reported and left out; the others still load.
func (s *Store) Configure(sources []Source) error {
	s.mu.Lock()
	for _, w := range s.watchers {
		_ = w.Close()
	}
	s.watchers = nil
	s.sources = slices.Clone(sources)
	s.files = map[string]calFile{}
	var errs []error
	for i := range s.sources {
		dir, err := expandHome(s.sources[i].Dir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		s.sources[i].Dir = dir
		if err := s.loadDirLocked(i); err != nil {
			errs = append(errs, err)
			continue
		}
		w, err := fswatch.NewNamed(fswatch.Changes)
		if err == nil {
			err = w.Add(dir)
		}
		if err != nil {
			if w != nil {
				_ = w.Close()
			}
			errs = append(errs, fmt.Errorf("calendar: watch %s: %w", dir, err))
			continue
		}
		s.watchers = append(s.watchers, w)
		go s.follow(w, i)
	}
	s.changedLocked()
	s.mu.Unlock()
	feed.Notify(s.changes)
	return errors.Join(errs...)
}

// Close stops following the directories and the reminders.
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.watchers {
		_ = w.Close()
	}
	s.watchers = nil
	if s.alarmStop != nil {
		s.alarmStop()
		s.alarmStop = nil
	}
	s.alarmNotify = nil
}

func expandHome(dir string) (string, error) {
	rest, ok := strings.CutPrefix(dir, "~/")
	if !ok && dir != "~" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("calendar: expand %s: %w", dir, err)
	}
	return filepath.Join(home, rest), nil
}

func (s *Store) loadDirLocked(source int) error {
	dir := s.sources[source].Dir
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("calendar: read %s: %w", dir, err)
	}
	for path, f := range s.files {
		if f.source == source {
			delete(s.files, path)
		}
	}
	for _, e := range entries {
		if e.Type().IsRegular() && strings.EqualFold(filepath.Ext(e.Name()), ".ics") {
			s.loadFileLocked(source, filepath.Join(dir, e.Name()))
		}
	}
	return nil
}

func (s *Store) loadFileLocked(source int, path string) {
	delete(s.files, path)
	data, err := readFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	s.parses++
	if err != nil {
		log.Printf("calendar: skipping %s: %v", path, err)
		return
	}
	events, skips, err := Parse(data, s.loc)
	if err != nil {
		log.Printf("calendar: skipping %s: %v", path, err)
		return
	}
	if len(skips) > 0 {
		reasons := make([]string, len(skips))
		for i, sk := range skips {
			reasons[i] = fmt.Sprintf("%q: %s", sk.UID, sk.Reason)
		}
		log.Printf("calendar: %s: skipping %d unsupported event(s): %s", path, len(skips), strings.Join(reasons, "; "))
	}
	s.files[path] = calFile{source: source, events: events}
}

func readFile(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // a file of a configured calendar directory
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxFileBytes {
		return "", fmt.Errorf("larger than %d bytes", maxFileBytes)
	}
	return string(data), nil
}

func (s *Store) follow(w *fswatch.Watcher, source int) {
	for range w.Changed() {
		batch := w.Take()
		s.mu.Lock()
		if !slices.Contains(s.watchers, w) {
			s.mu.Unlock()
			return
		}
		if batch.Rescan {
			if err := s.loadDirLocked(source); err != nil {
				log.Print(err)
			}
		}
		for _, ev := range batch.Events {
			if strings.EqualFold(filepath.Ext(ev.Name), ".ics") {
				s.loadFileLocked(source, filepath.Join(s.sources[source].Dir, ev.Name))
			}
		}
		s.changedLocked()
		s.mu.Unlock()
		feed.Notify(s.changes)
	}
}

func (s *Store) changedLocked() {
	clear(s.months)
	if s.alarmNotify != nil {
		s.scheduleLocked()
	}
}

func (s *Store) sortedPathsLocked() []string {
	return slices.Sorted(maps.Keys(s.files))
}

func (s *Store) occurrence(ev *Event, source int, start time.Time) Occurrence {
	return Occurrence{
		Summary: ev.Summary, Location: ev.Location, AllDay: ev.AllDay,
		Start: start.In(s.loc), End: ev.endOf(start).In(s.loc), Color: s.sources[source].Color,
	}
}

// Month returns the occurrences of year's month, expanding them once
// until the files change.
func (s *Store) Month(year int, month time.Month) *Month {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.monthLocked(year, month)
}

func (s *Store) monthLocked(year int, month time.Month) *Month {
	key := monthKey{year, month}
	if m, ok := s.months[key]; ok {
		return m
	}
	s.expansions++
	first := time.Date(year, month, 1, 0, 0, 0, 0, s.loc)
	next := first.AddDate(0, 1, 0)
	m := &Month{}
	for _, path := range s.sortedPathsLocked() {
		f := s.files[path]
		for i := range f.events {
			ev := &f.events[i]
			ev.occurrences(first, next, func(start time.Time) bool {
				o := s.occurrence(ev, f.source, start)
				for day := first; day.Before(next); day = day.AddDate(0, 0, 1) {
					if overlaps(o.Start, o.End, day, day.AddDate(0, 0, 1)) {
						m.days[day.Day()-1] = append(m.days[day.Day()-1], o)
					}
				}
				return true
			})
		}
	}
	for d := range m.days {
		slices.SortStableFunc(m.days[d], compareOccurrences)
	}
	s.months[key] = m
	return m
}

func compareOccurrences(a, b Occurrence) int {
	switch {
	case a.AllDay != b.AllDay:
		if a.AllDay {
			return -1
		}
		return 1
	case !a.Start.Equal(b.Start):
		return a.Start.Compare(b.Start)
	}
	return cmp.Compare(a.Summary, b.Summary)
}

// Next is the first timed occurrence starting at now or later, within a
// week.
func (s *Store) Next(now time.Time) (Occurrence, bool) {
	now = now.In(s.loc)
	until := now.Add(nextHorizon)
	s.mu.Lock()
	defer s.mu.Unlock()
	for first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, s.loc); first.Before(until); first = first.AddDate(0, 1, 0) {
		m := s.monthLocked(first.Year(), first.Month())
		startDay := 1
		if first.Month() == now.Month() && first.Year() == now.Year() {
			startDay = now.Day()
		}
		for d := startDay; d <= len(m.days); d++ {
			for _, o := range m.days[d-1] {
				if !o.AllDay && !o.Start.Before(now) && o.Start.Before(until) {
					return o, true
				}
			}
		}
	}
	return Occurrence{}, false
}

// RunReminders fires notify for every alarm coming due from now on,
// one timer to the next alarm, until Close. A change of the files
// reschedules it.
func (s *Store) RunReminders(notify func(Reminder)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alarmNotify = notify
	s.alarmLast = s.clock.Now()
	s.scheduleLocked()
}

func (s *Store) remindersLocked(after, upTo time.Time, yield func(Reminder)) {
	for _, path := range s.sortedPathsLocked() {
		f := s.files[path]
		for i := range f.events {
			ev := &f.events[i]
			if len(ev.Alarms) == 0 {
				continue
			}
			ev.occurrences(after.Add(-alarmLookBack), upTo.Add(alarmLookBack), func(start time.Time) bool {
				o := s.occurrence(ev, f.source, start)
				for _, a := range ev.Alarms {
					if at := a.trigger(o.Start, o.End); at.After(after) && !at.After(upTo) {
						yield(Reminder{Occurrence: o, At: at})
					}
				}
				return true
			})
		}
	}
}

func (s *Store) scheduleLocked() {
	if s.alarmStop != nil {
		s.alarmStop()
		s.alarmStop = nil
	}
	horizon := s.alarmLast.Add(alarmHorizon)
	next := horizon
	s.remindersLocked(s.alarmLast, horizon, func(r Reminder) {
		if r.At.Before(next) {
			next = r.At
		}
	})
	s.alarmStop = s.clock.AfterFunc(max(next.Sub(s.clock.Now()), 0), s.fire)
}

func (s *Store) fire() {
	s.mu.Lock()
	notify := s.alarmNotify
	if notify == nil {
		s.mu.Unlock()
		return
	}
	now := s.clock.Now()
	var due []Reminder
	s.remindersLocked(s.alarmLast, now, func(r Reminder) { due = append(due, r) })
	s.alarmLast = now
	s.scheduleLocked()
	s.mu.Unlock()
	slices.SortStableFunc(due, func(a, b Reminder) int { return a.At.Compare(b.At) })
	for _, r := range due {
		notify(r)
	}
}
