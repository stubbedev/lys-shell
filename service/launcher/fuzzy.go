package launcher

import (
	"slices"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// FuzzyQuery is typed text compiled for searching: each word a fuzzy
// atom taken literally, none of rofi's or fzf's syntax, so a query like
// "what's" or "!important" means those characters. It is case-sensitive
// only when the text has an uppercase letter, and folds accents. Set
// reuses its memory, so compiling per keystroke allocates nothing once
// warm; a query belongs to one goroutine.
type FuzzyQuery struct {
	atoms []atom
}

// NewFuzzyQuery compiles text.
func NewFuzzyQuery(text string) *FuzzyQuery {
	q := &FuzzyQuery{}
	q.Set(text)
	return q
}

// Set recompiles the query for text, over the memory it already has.
func (q *FuzzyQuery) Set(text string) {
	ignoreCase := !hasUpper(text)
	n := 0
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if unicode.IsSpace(r) {
			i += size
			continue
		}
		end := i + size
		for end < len(text) {
			r, size := utf8.DecodeRuneInString(text[end:])
			if unicode.IsSpace(r) {
				break
			}
			end += size
		}
		if n == cap(q.atoms) {
			q.atoms = append(q.atoms[:n], atom{})
		}
		q.atoms = q.atoms[:n+1]
		// A shorter query keeps the words past its end, memory and all, for
		// the next longer one.
		if q.atoms[n].cfg.scratch == nil {
			q.atoms[n] = atom{kind: atomFuzzy, cfg: matchConfig{scratch: &matchScratch{}}}
		}
		q.atoms[n].setLiteral(text[i:end], ignoreCase)
		n++
		i = end
	}
	q.atoms = q.atoms[:n]
}

func hasUpper(text string) bool {
	for _, r := range text {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// setLiteral makes the atom match word's characters, over its memory.
func (a *atom) setLiteral(word string, ignoreCase bool) {
	a.cfg.ignoreCase, a.cfg.normalize = ignoreCase, true
	a.needle = AppendHaystack(a.needle[:0], word)
	for i, c := range a.needle {
		if ignoreCase {
			c = unicode.ToLower(c)
			a.needle[i] = c
		}
		if c >= 0x80 && normalizeRune(c) != c {
			a.cfg.normalize = false
		}
	}
}

// Empty reports a query with no words.
func (q *FuzzyQuery) Empty() bool { return len(q.atoms) == 0 }

// Score is hay's match quality, false when any word does not match.
func (q *FuzzyQuery) Score(hay []rune) (int, bool) { return patternScore(q.atoms, hay) }

// AppendPositions appends to dst the indices into hay of the characters
// to highlight, ascending and distinct: each word's match, preferring
// one that starts at a word boundary, then tightened from its end, as a
// person reading the row would pick them. Words that do not match add
// nothing.
func (q *FuzzyQuery) AppendPositions(dst []int, hay []rune) []int {
	start := len(dst)
	for i := range q.atoms {
		dst = q.atoms[i].appendPositions(dst, hay)
	}
	slices.Sort(dst[start:])
	return dst[:start+len(slices.Compact(dst[start:]))]
}

// appendPositions aligns one atom against hay.
func (a *atom) appendPositions(dst []int, hay []rune) []int {
	if len(a.needle) == 0 {
		return dst
	}
	first := -1
	for i := range hay {
		if a.cfg.norm(hay[i]) != a.needle[0] {
			continue
		}
		if a.subsequenceFrom(hay, i) < 0 {
			// Later starts have even less left to match in.
			break
		}
		if first < 0 {
			first = i
		}
		if bonusFor(prevClass(hay, i), classOf(hay[i])) >= bonusBoundary {
			return a.tighten(dst, hay, i)
		}
	}
	if first < 0 {
		return dst
	}
	return a.tighten(dst, hay, first)
}

// subsequenceFrom is the index just past the needle's greedy match of
// hay from start, -1 when it does not fit.
func (a *atom) subsequenceFrom(hay []rune, start int) int {
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
func (a *atom) tighten(dst []int, hay []rune, start int) []int {
	end := a.subsequenceFrom(hay, start)
	at := len(dst)
	dst = slices.Grow(dst, len(a.needle))[:at+len(a.needle)]
	n := len(a.needle) - 1
	for i := end - 1; i >= start && n >= 0; i-- {
		if a.cfg.norm(hay[i]) == a.needle[n] {
			dst[at+n] = i
			n--
		}
	}
	return dst
}

// AppendHaystack appends text as the matcher reads it to dst: one rune
// per grapheme cluster, its positions counting clusters.
func AppendHaystack(dst []rune, text string) []rune {
	i := 0
	for i < len(text) && text[i] < utf8.RuneSelf {
		i++
	}
	if i == len(text) {
		dst = slices.Grow(dst, len(text))
		for k := range len(text) {
			dst = append(dst, rune(text[k]))
		}
		return dst
	}
	state := -1
	for len(text) > 0 {
		var cluster string
		cluster, text, _, state = uniseg.FirstGraphemeClusterInString(text, state)
		r, _ := utf8.DecodeRuneInString(cluster)
		dst = append(dst, r)
	}
	return dst
}

// Haystack is AppendHaystack over fresh memory.
func Haystack(text string) []rune { return AppendHaystack(nil, text) }
