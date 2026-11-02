package launcher

import (
	"slices"
	"strings"
	"unicode"
)

// FuzzyQuery is typed text compiled for searching: each word a fuzzy
// atom taken literally, none of rofi's or fzf's syntax, so a query like
// "what's" or "!important" means those characters. It is case-sensitive
// only when the text has an uppercase letter, and folds accents.
type FuzzyQuery struct {
	atoms []atom
}

// NewFuzzyQuery compiles text.
func NewFuzzyQuery(text string) FuzzyQuery {
	ignoreCase := strings.IndexFunc(text, unicode.IsUpper) < 0
	var atoms []atom
	for word := range strings.FieldsSeq(text) {
		if a := newAtom(word, ignoreCase, true, atomFuzzy, false); len(a.needle) > 0 {
			atoms = append(atoms, a)
		}
	}
	return FuzzyQuery{atoms: atoms}
}

// Empty reports a query with no words.
func (q FuzzyQuery) Empty() bool { return len(q.atoms) == 0 }

// Score is hay's match quality, false when any word does not match.
func (q FuzzyQuery) Score(hay []rune) (int, bool) { return patternScore(q.atoms, hay) }

// Positions are the indices into hay of the characters to highlight,
// ascending and distinct: each word's match, preferring one that starts
// at a word boundary, then tightened from its end, as a person reading
// the row would pick them. Words that do not match add nothing.
func (q FuzzyQuery) Positions(hay []rune) []int {
	var out []int
	seen := map[int]bool{}
	for _, a := range q.atoms {
		for _, p := range a.positions(hay) {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	slices.Sort(out)
	return out
}

// positions aligns one atom against hay.
func (a atom) positions(hay []rune) []int {
	if len(a.needle) == 0 {
		return nil
	}
	first := -1
	for i := range hay {
		if a.cfg.norm(hay[i]) != a.needle[0] {
			continue
		}
		if first < 0 && a.subsequenceFrom(hay, i) >= 0 {
			first = i
		}
		if bonusFor(prevClass(hay, i), classOf(hay[i])) >= bonusBoundary && a.subsequenceFrom(hay, i) >= 0 {
			return a.tighten(hay, i)
		}
	}
	if first < 0 {
		return nil
	}
	return a.tighten(hay, first)
}

// subsequenceFrom is the index just past the needle's greedy match of
// hay from start, -1 when it does not fit.
func (a atom) subsequenceFrom(hay []rune, start int) int {
	n := 0
	for i := start; i < len(hay); i++ {
		if a.cfg.norm(hay[i]) == a.needle[n] {
			n++
			if n == len(a.needle) {
				return i + 1
			}
		}
	}
	return -1
}

// tighten walks back from the greedy match's end so the characters sit
// as close together as they can.
func (a atom) tighten(hay []rune, start int) []int {
	end := a.subsequenceFrom(hay, start)
	out := make([]int, len(a.needle))
	n := len(a.needle) - 1
	for i := end - 1; i >= start && n >= 0; i-- {
		if a.cfg.norm(hay[i]) == a.needle[n] {
			out[n] = i
			n--
		}
	}
	return out
}

// Haystack is text as the matcher reads it: one rune per grapheme
// cluster, its positions counting clusters.
func Haystack(text string) []rune { return graphemeRunes(text) }
