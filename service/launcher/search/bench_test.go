package search

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stubbedev/lys-shell/service/launcher"
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
	e := &Engine{}
	st := &levelState{level: &Level{}}
	c := newCorpus(&fakeProvider{spec: Spec{Section: SectionFiles, Limit: 6}})
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
			b.ReportAllocs()
			for b.Loop() {
				st.corpora[0].scanned = false
				st.query = "fxrpt"
				if rows := e.compute(st, false); rows == nil && n == 0 {
					b.Fatal("no rows")
				}
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
			b.ReportAllocs()
			for b.Loop() {
				st.corpora[0].scanned = false
				for _, q := range []string{"f", "fi", "fir", "fire", "firef"} {
					st.query = q
					e.compute(st, false)
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
	b.ReportAllocs()
	for b.Loop() {
		_ = q.Positions(hay)
	}
}
