package search

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/stubbedev/lys-shell/internal/desktopentry"
	"github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/modes"
)

// benchEntries are plausible rows: names with words, paths beneath them.
func benchEntries(n int) []Entry {
	words := []string{"report", "invoice", "holiday", "firefox", "notes", "archive", "project", "budget", "draft", "photo"}
	out := make([]Entry, n)
	for i := range out {
		title := fmt.Sprintf("%s-%s-%04d.txt", words[i%len(words)], words[(i/7)%len(words)], i)
		out[i] = Entry{ID: title, Learn: title, Title: title, Subtitle: "~/Documents/" + words[(i/3)%len(words)] + "/" + words[(i/11)%len(words)]}
	}
	return out
}

func benchEngine(n int) (*Engine, *levelState) {
	e := newEngine()
	st := &levelState{level: &Level{}}
	c := newCorpus(&fakeProvider{spec: Spec{Section: SectionFiles}})
	c.set(benchEntries(n), func(string) int { return 0 })
	st.corpora = []*corpus{c}
	e.stack = []*levelState{st}
	return e, st
}

// BenchmarkQuery is a full answer to one keystroke, with no earlier
// query to narrow from: the worst case.
func BenchmarkQuery(b *testing.B) {
	for _, n := range []int{1000, 5000, 30000} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			e, st := benchEngine(n)
			rows := make([]Row, 0, 64)
			b.ReportAllocs()
			for b.Loop() {
				st.corpora[0].scanned = false
				st.query = "fxrpt"
				rows = e.compute(st, false, rows[:0])
			}
		})
	}
}

// BenchmarkTyping is a word typed a letter at a time, each narrowing the
// last: what typing costs.
func BenchmarkTyping(b *testing.B) {
	for _, n := range []int{5000, 30000} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			e, st := benchEngine(n)
			rows := make([]Row, 0, 64)
			b.ReportAllocs()
			for b.Loop() {
				st.corpora[0].scanned = false
				for _, q := range []string{"f", "fi", "fir", "fire", "firef"} {
					st.query = q
					rows = e.compute(st, false, rows[:0])
				}
			}
		})
	}
}

// BenchmarkPublish prepares rows for matching.
func BenchmarkPublish(b *testing.B) {
	entries := benchEntries(30000)
	c := newCorpus(&fakeProvider{spec: Spec{Section: SectionFiles}})
	b.ReportAllocs()
	for b.Loop() {
		c.set(entries, func(string) int { return 0 })
	}
}

// BenchmarkHighlights is the cost of one visible row's highlights.
func BenchmarkHighlights(b *testing.B) {
	q := launcher.NewFuzzyQuery("vsc")
	hay := launcher.Haystack("Visual Studio Code - Insiders")
	dst := make([]int, 0, 8)
	b.ReportAllocs()
	for b.Loop() {
		dst = q.AppendPositions(dst[:0], hay)
	}
}

// BenchmarkLoadApps reads this machine's desktop entries, off the UI loop.
func BenchmarkLoadApps(b *testing.B) {
	a := NewApps(Env{}, desktopentry.ApplicationDirs())
	b.ReportAllocs()
	for b.Loop() {
		a.Load(context.Background(), func([]Entry) {})
	}
}

// BenchmarkLoadEmoji decompresses and prepares the emoji table.
func BenchmarkLoadEmoji(b *testing.B) {
	e := NewEmoji(modes.AvailableEmoji)
	b.ReportAllocs()
	for b.Loop() {
		e.Load(context.Background(), func([]Entry) {})
	}
}

// BenchmarkFilesEntries builds thirty thousand file rows.
func BenchmarkFilesEntries(b *testing.B) {
	paths := make([]string, 30000)
	for i := range paths {
		paths[i] = "/home/x/Documents/project/report-" + strconv.Itoa(i) + ".txt"
	}
	f := NewFiles(Env{}, FilesConfig{Home: "/home/x", Previews: true})
	b.ReportAllocs()
	for b.Loop() {
		f.entries(nil, paths)
	}
}

// Answering a keystroke allocates nothing once warm: a regression fails
// here rather than showing up as jank.
func TestAnsweringAQueryAllocatesNothing(t *testing.T) {
	e, st := benchEngine(5000)
	rows := make([]Row, 0, 64)
	queries := []string{"f", "fi", "fir", "fire", "firef", "report inv"}
	warm := func() {
		st.corpora[0].scanned = false
		for _, q := range queries {
			st.query = q
			rows = e.compute(st, false, rows[:0])
		}
	}
	warm()
	if got := testing.AllocsPerRun(20, warm); got != 0 {
		t.Errorf("%v allocations per typed word, want none", got)
	}
}

func TestHighlightsAllocateNothing(t *testing.T) {
	q := launcher.NewFuzzyQuery("vsc code")
	hay := launcher.Haystack("Visual Studio Code - Insiders")
	dst := make([]int, 0, 16)
	if got := testing.AllocsPerRun(20, func() { dst = q.AppendPositions(dst[:0], hay) }); got != 0 {
		t.Errorf("%v allocations per row", got)
	}
}

// The work one keystroke does per row it tests: a matcher that scans more of
// each row or fills a larger matrix fails here, however fast the machine.
func TestMatchingWorkPerRowStaysWithinItsLimit(t *testing.T) {
	const limit = 170
	e, st := benchEngine(5000)
	c := st.corpora[0]
	rows := make([]Row, 0, 64)
	var worst uint64
	for _, q := range []string{"f", "fi", "fir", "fire", "firef", "firefo", "firefox", "firefox-", "report", "notes 12", "project-1"} {
		tested, work := c.tested, e.query.Work()
		st.query = q
		rows = e.compute(st, false, rows[:0])
		if n := c.tested - tested; n > 0 {
			worst = max(worst, (e.query.Work()-work)/n)
		}
	}
	if worst > limit {
		t.Errorf("a keystroke did %d cells of matching work per row, limit %d", worst, limit)
	}
}

// A query extending the last one rematches only the rows that matched it:
// what makes typing over a long list cheap, counted rather than timed.
func TestTypingRescansOnlyWhatMatched(t *testing.T) {
	e, st := benchEngine(5000)
	c := st.corpora[0]
	rows := make([]Row, 0, 64)
	answer := func(q string) (tested uint64, hits int) {
		before := c.tested
		st.query = q
		rows = e.compute(st, false, rows[:0])
		return c.tested - before, len(c.hits)
	}
	tested, hits := answer("firefox")
	if tested != 5000 {
		t.Fatalf("a first query tests %d rows, want all 5000", tested)
	}
	if hits == 0 || hits >= 5000 {
		t.Fatalf("%d hits: the fixture no longer narrows", hits)
	}
	again, _ := answer("firefox-")
	if again != uint64(hits) {
		t.Errorf("extending the query tested %d rows, want the %d that matched", again, hits)
	}
	back, _ := answer("fire")
	if back != 5000 {
		t.Errorf("a shorter query tested %d rows, want all 5000: its matches are not a subset", back)
	}
}
