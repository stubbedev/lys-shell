// Package lyrics finds the lyrics of the playing track on LRCLIB
// (lrclib.net) and caches them, misses included, on disk: synced
// lyrics as timed lines, plain lyrics as untimed ones.
package lyrics

import (
	"cmp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Line is one lyric line; At is when it starts, zero for plain lyrics.
type Line struct {
	At   time.Duration `json:"at"`
	Text string        `json:"text"`
}

// Lyrics is a track's lyrics: no lines when the track has none, timed
// lines when Synced.
type Lyrics struct {
	Lines  []Line `json:"lines"`
	Synced bool   `json:"synced"`
}

// Found reports whether there are lyrics to show.
func (l Lyrics) Found() bool { return len(l.Lines) > 0 }

// Current is the index of the line sung at pos: -1 before the first
// line and for plain lyrics.
func (l Lyrics) Current(pos time.Duration) int {
	if !l.Synced {
		return -1
	}
	return sort.Search(len(l.Lines), func(i int) bool { return l.Lines[i].At > pos }) - 1
}

// ParseLRC reads LRC text into lines ordered by time: every [mm:ss.xx]
// tag on a line times a copy of its text, [offset:±ms] shifts them all
// (positive is earlier), and metadata tags and untimed lines are
// dropped.
func ParseLRC(text string) []Line {
	var lines []Line
	var offset time.Duration
	for raw := range strings.Lines(text) {
		rest := strings.TrimRight(raw, "\r\n")
		start := len(lines)
		for strings.HasPrefix(rest, "[") {
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				break
			}
			tag := rest[1:end]
			rest = rest[end+1:]
			if at, ok := parseStamp(tag); ok {
				lines = append(lines, Line{At: at})
				continue
			}
			if v, ok := strings.CutPrefix(tag, "offset:"); ok {
				if ms, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
					offset = time.Duration(ms) * time.Millisecond
				}
			}
		}
		text := strings.TrimSpace(rest)
		for i := start; i < len(lines); i++ {
			lines[i].Text = text
		}
	}
	for i := range lines {
		lines[i].At = max(lines[i].At-offset, 0)
	}
	slices.SortStableFunc(lines, func(a, b Line) int { return cmp.Compare(a.At, b.At) })
	return lines
}

func parseStamp(tag string) (time.Duration, bool) {
	minutes, rest, ok := strings.Cut(tag, ":")
	if !ok {
		return 0, false
	}
	m, err := strconv.ParseUint(minutes, 10, 32)
	if err != nil {
		return 0, false
	}
	seconds, fraction, hasFraction := strings.Cut(strings.Replace(rest, ":", ".", 1), ".")
	s, err := strconv.ParseUint(seconds, 10, 8)
	if err != nil || s >= 60 {
		return 0, false
	}
	at := time.Duration(m)*time.Minute + time.Duration(s)*time.Second
	if !hasFraction {
		return at, true
	}
	if fraction == "" || len(fraction) > 3 {
		return 0, false
	}
	f, err := strconv.ParseUint(fraction, 10, 16)
	if err != nil {
		return 0, false
	}
	for range 3 - len(fraction) {
		f *= 10
	}
	return at + time.Duration(f)*time.Millisecond, true
}

// plainLines is unsynced lyrics as untimed lines.
func plainLines(text string) []Line {
	var lines []Line
	for raw := range strings.Lines(text) {
		lines = append(lines, Line{Text: strings.TrimSpace(raw)})
	}
	for len(lines) > 0 && lines[len(lines)-1].Text == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
