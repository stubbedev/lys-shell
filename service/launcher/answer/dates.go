package answer

import (
	"strings"
	"time"
)

const day = 24 * time.Hour

func civil(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func sameDay(a, b time.Time) bool { return civil(a.In(b.Location())).Equal(civil(b)) }

func daysBetween(from, to time.Time) int { return int(to.Sub(from) / day) }

// DateKind tells a date answer from a count of days.
type DateKind uint8

// The kinds of date answer.
const (
	DateIsDay DateKind = iota + 1
	DateIsSpan
)

// DateAnswer is an evaluated date query: a calendar day (Day, midnight UTC)
// or a number of days.
type DateAnswer struct {
	Kind DateKind
	Day  time.Time
	Days int
}

type dateUnit uint8

const (
	unitDays dateUnit = iota + 1
	unitWeeks
	unitMonths
	unitYears
)

var dateUnits = map[string]dateUnit{
	"d": unitDays, "day": unitDays, "days": unitDays,
	"w": unitWeeks, "wk": unitWeeks, "week": unitWeeks, "weeks": unitWeeks,
	"month": unitMonths, "months": unitMonths,
	"y": unitYears, "yr": unitYears, "year": unitYears, "years": unitYears,
}

type tokKind uint8

const (
	tokWord tokKind = iota + 1
	tokNumber
	tokDate
	tokPlus
	tokMinus
)

type token struct {
	kind tokKind
	text string
	n    int
	date time.Time
}

const maxTokens = 5

type tokens struct {
	list [maxTokens]token
	n    int
}

func (t *tokens) kinds(want ...tokKind) bool {
	if t.n != len(want) {
		return false
	}
	for i, k := range want {
		if t.list[i].kind != k {
			return false
		}
	}
	return true
}

func (t *tokens) word(i int, w string) bool {
	return t.list[i].kind == tokWord && strings.EqualFold(t.list[i].text, w)
}

const maxCount = 100000

// scanDate splits q into words, counts, ISO dates and signs; false past
// maxTokens or on anything else.
func scanDate(q string) (tokens, bool) {
	var t tokens
	i := 0
	for i < len(q) {
		c := q[i]
		if c == ' ' || c == '\t' {
			i++
			continue
		}
		if t.n == maxTokens {
			return tokens{}, false
		}
		tok := &t.list[t.n]
		switch {
		case c == '+':
			*tok = token{kind: tokPlus}
			i++
		case c == '-':
			*tok = token{kind: tokMinus}
			i++
		case c >= '0' && c <= '9':
			if isISODate(q[i:]) {
				d, err := time.Parse(time.DateOnly, q[i:i+len(time.DateOnly)])
				if err != nil {
					return tokens{}, false
				}
				*tok = token{kind: tokDate, date: d}
				i += len(time.DateOnly)
				break
			}
			n := 0
			for i < len(q) && q[i] >= '0' && q[i] <= '9' {
				if n > maxCount {
					return tokens{}, false
				}
				n = n*10 + int(q[i]-'0')
				i++
			}
			*tok = token{kind: tokNumber, n: n}
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(q) && (q[j] >= 'a' && q[j] <= 'z' || q[j] >= 'A' && q[j] <= 'Z') {
				j++
			}
			*tok = token{kind: tokWord, text: q[i:j]}
			i = j
		default:
			return tokens{}, false
		}
		t.n++
	}
	return t, true
}

// dayOf reads a token naming a day: an ISO date, today, tomorrow or
// yesterday.
func dayOf(tok token, today time.Time) (time.Time, bool) {
	if tok.kind == tokDate {
		return tok.date, true
	}
	if tok.kind != tokWord {
		return time.Time{}, false
	}
	switch {
	case strings.EqualFold(tok.text, "today"):
		return today, true
	case strings.EqualFold(tok.text, "tomorrow"):
		return today.AddDate(0, 0, 1), true
	case strings.EqualFold(tok.text, "yesterday"):
		return today.AddDate(0, 0, -1), true
	}
	return time.Time{}, false
}

func unitOf(tok token) (dateUnit, bool) {
	if tok.kind != tokWord || len(tok.text) > maxCurrencyName {
		return 0, false
	}
	var buf [maxCurrencyName]byte
	u, ok := dateUnits[string(foldInto(&buf, tok.text))]
	return u, ok
}

// addDate moves d by n units; a month or year step that lands past the end
// of the month keeps to its last day, as Jan 31 + 1 month is Feb 28.
func addDate(d time.Time, n int, u dateUnit) time.Time {
	switch u {
	case unitDays:
		return d.AddDate(0, 0, n)
	case unitWeeks:
		return d.AddDate(0, 0, 7*n)
	}
	months := n
	if u == unitYears {
		months = 12 * n
	}
	y, m, dd := d.Date()
	total := int(m) - 1 + months
	y += total / 12
	if total%12 < 0 {
		y--
	}
	month := time.Month((total%12+12)%12 + 1)
	last := time.Date(y, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return time.Date(y, month, min(dd, last), 0, 0, 0, 0, time.UTC)
}

// EvalDate answers date arithmetic in now's calendar: "today + 90 days",
// "2026-12-24 - today", "days until 2027-01-01", "days since 2026-01-01",
// "3 weeks from tomorrow", "10 days ago".
func EvalDate(query string, now time.Time) (DateAnswer, bool) {
	t, ok := scanDate(query)
	if !ok || t.n < 2 {
		return DateAnswer{}, false
	}
	today := civil(now)
	switch {
	case t.kinds(tokWord, tokWord, tokDate) || t.kinds(tokWord, tokWord, tokWord):
		if !t.word(0, "days") {
			return DateAnswer{}, false
		}
		target, ok := dayOf(t.list[2], today)
		if !ok {
			return DateAnswer{}, false
		}
		switch {
		case t.word(1, "until") || t.word(1, "till") || t.word(1, "to"):
			return DateAnswer{Kind: DateIsSpan, Days: daysBetween(today, target)}, true
		case t.word(1, "since"):
			return DateAnswer{Kind: DateIsSpan, Days: daysBetween(target, today)}, true
		}
	case t.n == 4 && (t.list[1].kind == tokPlus || t.list[1].kind == tokMinus) && t.list[2].kind == tokNumber:
		from, ok := dayOf(t.list[0], today)
		u, uok := unitOf(t.list[3])
		if !ok || !uok {
			return DateAnswer{}, false
		}
		n := t.list[2].n
		if t.list[1].kind == tokMinus {
			n = -n
		}
		return DateAnswer{Kind: DateIsDay, Day: addDate(from, n, u)}, true
	case t.n == 3 && t.list[1].kind == tokMinus:
		a, aok := dayOf(t.list[0], today)
		b, bok := dayOf(t.list[2], today)
		if !aok || !bok {
			return DateAnswer{}, false
		}
		return DateAnswer{Kind: DateIsSpan, Days: daysBetween(b, a)}, true
	case t.kinds(tokNumber, tokWord, tokWord, tokWord) && t.word(2, "from"):
		u, uok := unitOf(t.list[1])
		from, ok := dayOf(t.list[3], today)
		if !ok || !uok {
			return DateAnswer{}, false
		}
		return DateAnswer{Kind: DateIsDay, Day: addDate(from, t.list[0].n, u)}, true
	case t.kinds(tokNumber, tokWord, tokWord) && t.word(2, "ago"):
		u, ok := unitOf(t.list[1])
		if !ok {
			return DateAnswer{}, false
		}
		return DateAnswer{Kind: DateIsDay, Day: addDate(today, -t.list[0].n, u)}, true
	}
	return DateAnswer{}, false
}

func isISODate(s string) bool {
	const n = len(time.DateOnly)
	if len(s) < n || len(s) > n && s[n] >= '0' && s[n] <= '9' {
		return false
	}
	for i := range n {
		if (i == 4 || i == 7) != (s[i] == '-') || s[i] != '-' && (s[i] < '0' || s[i] > '9') {
			return false
		}
	}
	return true
}
