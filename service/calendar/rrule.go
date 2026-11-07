package calendar

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

type frequency uint8

const (
	daily frequency = iota
	weekly
	monthly
	yearly
)

type weekdayNum struct {
	nth int
	day time.Weekday
}

type rule struct {
	freq     frequency
	interval int
	count    int
	until    time.Time
	hasUntil bool
	byDay    []weekdayNum
	wkst     time.Weekday
}

var weekdays = map[string]time.Weekday{
	"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday,
	"TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday,
}

func parseRule(value string, local *time.Location) (rule, error) {
	r := rule{interval: 1, wkst: time.Monday}
	hasFreq := false
	for part := range strings.SplitSeq(value, ";") {
		key, v, ok := strings.Cut(part, "=")
		if !ok {
			return rule{}, fmt.Errorf("part %q", part)
		}
		switch strings.ToUpper(key) {
		case "FREQ":
			f, ok := map[string]frequency{"DAILY": daily, "WEEKLY": weekly, "MONTHLY": monthly, "YEARLY": yearly}[strings.ToUpper(v)]
			if !ok {
				return rule{}, fmt.Errorf("FREQ=%s is not supported", v)
			}
			r.freq, hasFreq = f, true
		case "INTERVAL":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return rule{}, fmt.Errorf("INTERVAL=%s", v)
			}
			r.interval = n
		case "COUNT":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return rule{}, fmt.Errorf("COUNT=%s", v)
			}
			r.count = n
		case "UNTIL":
			t, _, err := parseTime(property{value: v}, local)
			if err != nil {
				return rule{}, fmt.Errorf("UNTIL: %w", err)
			}
			if len(v) == 8 {
				t = t.AddDate(0, 0, 1).Add(-time.Nanosecond)
			}
			r.until, r.hasUntil = t, true
		case "BYDAY":
			r.byDay = []weekdayNum{}
			for day := range strings.SplitSeq(v, ",") {
				wd, err := parseWeekdayNum(day)
				if err != nil {
					return rule{}, err
				}
				r.byDay = append(r.byDay, wd)
			}
		case "WKST":
			wd, ok := weekdays[strings.ToUpper(v)]
			if !ok {
				return rule{}, fmt.Errorf("WKST=%s", v)
			}
			r.wkst = wd
		default:
			return rule{}, fmt.Errorf("%s is not supported", key)
		}
	}
	if !hasFreq {
		return rule{}, errors.New("no FREQ")
	}
	if r.count > 0 && r.hasUntil {
		return rule{}, errors.New("both COUNT and UNTIL")
	}
	for _, wd := range r.byDay {
		if wd.nth != 0 && r.freq != monthly {
			return rule{}, fmt.Errorf("BYDAY=%d%v needs FREQ=MONTHLY", wd.nth, wd.day)
		}
	}
	return r, nil
}

func parseWeekdayNum(s string) (weekdayNum, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) < 2 {
		return weekdayNum{}, fmt.Errorf("BYDAY %q", s)
	}
	day, ok := weekdays[s[len(s)-2:]]
	if !ok {
		return weekdayNum{}, fmt.Errorf("BYDAY %q", s)
	}
	wd := weekdayNum{day: day}
	if prefix := s[:len(s)-2]; prefix != "" {
		n, err := strconv.Atoi(prefix)
		if err != nil || n == 0 || n > 5 || n < -5 {
			return weekdayNum{}, fmt.Errorf("BYDAY %q", s)
		}
		wd.nth = n
	}
	return wd, nil
}

func atClock(day, clock time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), clock.Hour(), clock.Minute(), clock.Second(), 0, clock.Location())
}

func (r *rule) periodStart(start time.Time, k int) time.Time {
	n := k * r.interval
	switch r.freq {
	case daily:
		return atClock(start.AddDate(0, 0, n), start)
	case weekly:
		back := (int(start.Weekday()) - int(r.wkst) + 7) % 7
		return atClock(start.AddDate(0, 0, 7*n-back), start)
	case monthly:
		return time.Date(start.Year(), start.Month()+time.Month(n), 1, start.Hour(), start.Minute(), start.Second(), 0, start.Location())
	}
	return time.Date(start.Year()+n, start.Month(), 1, start.Hour(), start.Minute(), start.Second(), 0, start.Location())
}

func (r *rule) candidates(start time.Time, k int, buf []time.Time) []time.Time {
	buf = buf[:0]
	period := r.periodStart(start, k)
	switch r.freq {
	case daily:
		if r.byDay == nil || r.hasDay(period.Weekday()) {
			buf = append(buf, period)
		}
	case weekly:
		if r.byDay == nil {
			return append(buf, atClock(start.AddDate(0, 0, 7*k*r.interval), start))
		}
		for i := range 7 {
			day := period.AddDate(0, 0, i)
			if r.hasDay(day.Weekday()) {
				buf = append(buf, atClock(day, start))
			}
		}
	case monthly:
		if r.byDay == nil {
			day := time.Date(period.Year(), period.Month(), start.Day(), start.Hour(), start.Minute(), start.Second(), 0, start.Location())
			if day.Month() == period.Month() {
				buf = append(buf, day)
			}
			return buf
		}
		days := time.Date(period.Year(), period.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
		for d := 1; d <= days; d++ {
			day := time.Date(period.Year(), period.Month(), d, start.Hour(), start.Minute(), start.Second(), 0, start.Location())
			if r.monthlyMatch(day, days) {
				buf = append(buf, day)
			}
		}
	case yearly:
		day := time.Date(period.Year(), start.Month(), start.Day(), start.Hour(), start.Minute(), start.Second(), 0, start.Location())
		if day.Month() == start.Month() {
			buf = append(buf, day)
		}
	}
	return buf
}

func (r *rule) hasDay(d time.Weekday) bool {
	return slices.ContainsFunc(r.byDay, func(w weekdayNum) bool { return w.day == d })
}

func (r *rule) monthlyMatch(day time.Time, daysInMonth int) bool {
	for _, w := range r.byDay {
		if w.day != day.Weekday() {
			continue
		}
		switch {
		case w.nth == 0:
			return true
		case w.nth > 0 && (day.Day()-1)/7+1 == w.nth:
			return true
		case w.nth < 0 && (daysInMonth-day.Day())/7+1 == -w.nth:
			return true
		}
	}
	return false
}

func (r *rule) skipPeriods(start, from time.Time) int {
	if r.count > 0 || !from.After(start) {
		return 0
	}
	var periods int
	switch r.freq {
	case daily:
		periods = int(from.Sub(start).Hours()/24) / r.interval
	case weekly:
		periods = int(from.Sub(start).Hours()/24/7) / r.interval
	case monthly:
		periods = ((from.Year()-start.Year())*12 + int(from.Month()-start.Month())) / r.interval
	case yearly:
		periods = (from.Year() - start.Year()) / r.interval
	}
	return max(periods-1, 0)
}

func (e *Event) excluded(t time.Time) bool {
	return slices.ContainsFunc(e.except, func(x time.Time) bool {
		if e.AllDay {
			y1, m1, d1 := x.Date()
			y2, m2, d2 := t.Date()
			return y1 == y2 && m1 == m2 && d1 == d2
		}
		return x.Equal(t)
	})
}

func (e *Event) endOf(start time.Time) time.Time {
	if !e.AllDay {
		return start.Add(e.End.Sub(e.Start))
	}
	days := int(time.Date(e.End.Year(), e.End.Month(), e.End.Day(), 0, 0, 0, 0, time.UTC).Sub(time.Date(e.Start.Year(), e.Start.Month(), e.Start.Day(), 0, 0, 0, 0, time.UTC)).Hours() / 24)
	return start.AddDate(0, 0, days)
}

func overlaps(start, end, from, to time.Time) bool {
	if !start.Before(to) {
		return false
	}
	if end.Equal(start) {
		return !start.Before(from)
	}
	return end.After(from)
}

// occurrences calls yield with the start of every occurrence that
// overlaps [from, to), in order, until yield returns false.
func (e *Event) occurrences(from, to time.Time, yield func(start time.Time) bool) {
	if e.rule == nil {
		if overlaps(e.Start, e.End, from, to) && !e.excluded(e.Start) {
			yield(e.Start)
		}
		return
	}
	r := e.rule
	lead := e.End.Sub(e.Start) + 24*time.Hour
	var buf [31]time.Time
	seen := 0
	for k := r.skipPeriods(e.Start, from.Add(-lead)); ; k++ {
		if !r.periodStart(e.Start, k).Before(to) && r.periodStart(e.Start, k).After(e.Start) {
			return
		}
		for _, t := range r.candidates(e.Start, k, buf[:0]) {
			if t.Before(e.Start) {
				continue
			}
			if r.hasUntil && t.After(r.until) {
				return
			}
			seen++
			if r.count > 0 && seen > r.count {
				return
			}
			if !t.Before(to) {
				return
			}
			if overlaps(t, e.endOf(t), from, to) && !e.excluded(t) && !yield(t) {
				return
			}
		}
	}
}
