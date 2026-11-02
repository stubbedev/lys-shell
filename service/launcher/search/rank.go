package search

import (
	"cmp"
	"slices"
	"strings"

	"github.com/stubbedev/lys-lib/intconv"

	"github.com/stubbedev/lys-shell/service/launcher"
)

const (
	// subtitlePenalty ranks a match only in a row's subtitle or keywords
	// below the same match in its title.
	subtitlePenalty = 24
	// sectionStickiness is how far a section must overtake another
	// before they swap, so sections do not trade places on every
	// keystroke.
	sectionStickiness = 12
	// flatRowCap bounds a flat list: past this nobody is reading, they
	// type another letter.
	flatRowCap = 200
	// recentRows is how many rows an empty query suggests.
	recentRows = 8
	// answerScore puts a provider's answers above its searched rows.
	answerScore = 1 << 20
)

// hit is one matched entry of a corpus.
type hit struct {
	index int32
	score int32
}

// corpus is one provider's rows prepared for matching, on the engine
// goroutine. titles and full are slices of one arena per publish.
type corpus struct {
	prov    Provider
	spec    Spec
	entries []Entry
	titles  [][]rune
	full    [][]rune
	boost   []int32
	// hits are every match of lastQuery with its match score, so a query
	// extending it rescans only them. Scores carry no query boost, so
	// they stay valid while the query narrows.
	hits      []hit
	lastQuery string
	scanned   bool
	// tested counts the rows matched against, the work a query costs.
	tested uint64
}

func newCorpus(p Provider) *corpus { return &corpus{prov: p, spec: p.Spec()} }

// set replaces the rows.
func (c *corpus) set(entries []Entry, boost func(string) int) {
	c.entries = entries
	c.scanned, c.lastQuery = false, ""
	c.titles = slices.Grow(c.titles[:0], len(entries))[:len(entries)]
	c.full = slices.Grow(c.full[:0], len(entries))[:len(entries)]
	c.boost = slices.Grow(c.boost[:0], len(entries))[:len(entries)]
	size := 0
	for i := range entries {
		e := &entries[i]
		size += len(e.Title) + len(e.Subtitle) + len(e.Keywords) + 2
	}
	arena := make([]rune, 0, size)
	// size counts bytes, so it holds every rune and the arena never moves
	// under the slices taken of it.
	for i := range entries {
		e := &entries[i]
		start := len(arena)
		arena = launcher.AppendHaystack(arena, e.Title)
		titleEnd := len(arena)
		if e.Subtitle != "" {
			arena = launcher.AppendHaystack(append(arena, ' '), e.Subtitle)
		}
		if e.Keywords != "" {
			arena = launcher.AppendHaystack(append(arena, ' '), e.Keywords)
		}
		c.titles[i] = arena[start:titleEnd:titleEnd]
		c.full[i] = arena[start:len(arena):len(arena)]
		c.boost[i] = intconv.Clamp[int32](boost(e.Learn))
	}
}

// matchOne scores one entry: its title, else the whole of it for less.
func (c *corpus) matchOne(q *launcher.FuzzyQuery, i int) (int32, bool) {
	c.tested++
	if s, ok := q.Score(c.titles[i]); ok {
		return intconv.Clamp[int32](s), true
	}
	if len(c.full[i]) == len(c.titles[i]) {
		return 0, false
	}
	if s, ok := q.Score(c.full[i]); ok {
		return intconv.Clamp[int32](max(s-subtitlePenalty, 1)), true
	}
	return 0, false
}

// scan brings hits up to query: only the last query's hits when query
// extends it, a query that narrows matching a subset by construction.
func (c *corpus) scan(q *launcher.FuzzyQuery, query string) {
	if c.scanned && c.lastQuery != "" && strings.HasPrefix(query, c.lastQuery) {
		kept := c.hits[:0]
		for _, h := range c.hits {
			if s, ok := c.matchOne(q, int(h.index)); ok {
				kept = append(kept, hit{index: h.index, score: s})
			}
		}
		c.hits = kept
	} else {
		c.hits = c.hits[:0]
		for i := range c.entries {
			if s, ok := c.matchOne(q, i); ok {
				c.hits = append(c.hits, hit{index: intconv.Clamp[int32](i), score: s})
			}
		}
	}
	c.scanned, c.lastQuery = true, query
}

// ranked is a candidate row on its way into the list.
type ranked struct {
	corpus *corpus
	index  int32
	score  int32
	// answer is an Answerer's row, which indexes nothing.
	answer *Entry
}

func (r ranked) entry() *Entry {
	if r.answer != nil {
		return r.answer
	}
	return &r.corpus.entries[r.index]
}

// better orders candidates: score, then the shorter title (a closer
// match of the same letters), then the title, then the id, so equal
// scores order the same on every run.
func better(a, b ranked) int {
	if a.score != b.score {
		return cmp.Compare(b.score, a.score)
	}
	ea, eb := a.entry(), b.entry()
	if c := cmp.Compare(len(ea.Title), len(eb.Title)); c != 0 {
		return c
	}
	if c := strings.Compare(ea.Title, eb.Title); c != 0 {
		return c
	}
	return strings.Compare(ea.ID, eb.ID)
}

// insertTop keeps out sorted and at most limit long, cheaper than
// sorting every hit of a large corpus for the few that show.
func insertTop(out []ranked, r ranked, limit int) []ranked {
	if len(out) == limit && better(r, out[len(out)-1]) >= 0 {
		return out
	}
	at, _ := slices.BinarySearchFunc(out, r, better)
	if len(out) == limit {
		copy(out[at+1:], out[at:])
		out[at] = r
		return out
	}
	return slices.Insert(out, at, r)
}

// sectionRows is one section's candidates, best first.
type sectionRows struct {
	section Section
	rows    []ranked
}

// orderSections places sections by their best row, Answer first and Web
// last. A section keeps its place in prev until another beats it by
// sectionStickiness; with pin set (rows that arrived late for a query
// already showing) prev's order stands outright and newcomers go after
// it, so what is on screen does not move.
func orderSections(sections []sectionRows, prev []Section, pin bool) []Section {
	// Insertion sort: a handful of sections, stable, and with no closure
	// to allocate per keystroke.
	for i := 1; i < len(sections); i++ {
		for j := i; j > 0 && sectionBefore(&sections[j], &sections[j-1], prev, pin); j-- {
			sections[j], sections[j-1] = sections[j-1], sections[j]
		}
	}
	out := prev[:0]
	for i := range sections {
		out = append(out, sections[i].section)
	}
	return out
}

func sectionFixed(sec Section) int {
	switch sec {
	case SectionAnswer:
		return -1
	case SectionWeb:
		return 1
	}
	return 0
}

// sectionBefore reports a strictly before b.
func sectionBefore(a, b *sectionRows, prev []Section, pin bool) bool {
	if fa, fb := sectionFixed(a.section), sectionFixed(b.section); fa != fb {
		return fa < fb
	}
	pa, pb := slices.Index(prev, a.section), slices.Index(prev, b.section)
	if pin {
		switch {
		case pa >= 0 && pb >= 0:
			return pa < pb
		case pa >= 0:
			return true
		case pb >= 0:
			return false
		}
	}
	diff := int(a.rows[0].score) - int(b.rows[0].score)
	if pa >= 0 && pb >= 0 && diff > -sectionStickiness && diff < sectionStickiness {
		return pa < pb
	}
	if diff != 0 {
		return diff > 0
	}
	return a.section < b.section
}
