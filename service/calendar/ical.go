// Package calendar reads vdir calendars (one iCalendar file per event,
// as vdirsyncer and khal keep them), expands their events for a month,
// and fires their reminders. It never touches the network: syncing is
// left to the tool that fills the directories.
package calendar

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Event is one VEVENT: a single event, or the series an RRULE expands.
type Event struct {
	UID      string
	Summary  string
	Location string
	Start    time.Time
	End      time.Time
	AllDay   bool
	Alarms   []Alarm

	rule         *rule
	except       []time.Time
	recurrenceID time.Time
}

// Alarm is one VALARM: a trigger relative to the event's start (or its
// end), or at a fixed time.
type Alarm struct {
	Offset   time.Duration
	FromEnd  bool
	At       time.Time
	Absolute bool
}

func (a Alarm) trigger(start, end time.Time) time.Time {
	switch {
	case a.Absolute:
		return a.At
	case a.FromEnd:
		return end.Add(a.Offset)
	}
	return start.Add(a.Offset)
}

type property struct {
	name   string
	params map[string]string
	value  string
}

func (p property) param(name string) string { return p.params[name] }

var errMalformed = errors.New("malformed iCalendar")

func unfold(data string) []string {
	var lines []string
	for raw := range strings.Lines(data) {
		line := strings.TrimRight(raw, "\r\n")
		if (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) && len(lines) > 0 {
			lines[len(lines)-1] += line[1:]
			continue
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func parseProperty(line string) (property, error) {
	end := strings.IndexAny(line, ";:")
	if end <= 0 {
		return property{}, fmt.Errorf("%w: line %q has no property name", errMalformed, line)
	}
	p := property{name: strings.ToUpper(line[:end])}
	rest := line[end:]
	for strings.HasPrefix(rest, ";") {
		rest = rest[1:]
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 {
			return property{}, fmt.Errorf("%w: parameter without a value in %q", errMalformed, line)
		}
		key := strings.ToUpper(rest[:eq])
		rest = rest[eq+1:]
		var value string
		if strings.HasPrefix(rest, `"`) {
			closing := strings.IndexByte(rest[1:], '"')
			if closing < 0 {
				return property{}, fmt.Errorf("%w: unterminated quote in %q", errMalformed, line)
			}
			value, rest = rest[1:closing+1], rest[closing+2:]
		} else {
			stop := strings.IndexAny(rest, ";:")
			if stop < 0 {
				return property{}, fmt.Errorf("%w: line %q has no value", errMalformed, line)
			}
			value, rest = rest[:stop], rest[stop:]
		}
		if p.params == nil {
			p.params = map[string]string{}
		}
		p.params[key] = value
	}
	if !strings.HasPrefix(rest, ":") {
		return property{}, fmt.Errorf("%w: line %q has no value", errMalformed, line)
	}
	p.value = rest[1:]
	return p, nil
}

var textEscapes = strings.NewReplacer(`\n`, "\n", `\N`, "\n", `\,`, ",", `\;`, ";", `\\`, `\`)

// Skip is an event Parse left out, and why.
type Skip struct {
	UID    string
	Reason string
}

// Parse reads an iCalendar document's events, interpreting dates and
// floating times in local. A document that is not well-formed is an
// error; an event using something unsupported is left out and listed
// in the skips.
func Parse(data string, local *time.Location) ([]Event, []Skip, error) {
	var (
		events []Event
		skips  []Skip
		stack  []string
		props  []property
		alarm  []property
		alarms [][]property
	)
	for _, line := range unfold(data) {
		p, err := parseProperty(line)
		if err != nil {
			return nil, nil, err
		}
		switch p.name {
		case "BEGIN":
			stack = append(stack, strings.ToUpper(p.value))
			switch stack[len(stack)-1] {
			case "VEVENT":
				props, alarms = nil, nil
			case "VALARM":
				alarm = nil
			}
			continue
		case "END":
			if len(stack) == 0 || stack[len(stack)-1] != strings.ToUpper(p.value) {
				return nil, nil, fmt.Errorf("%w: END:%s does not close %v", errMalformed, p.value, stack)
			}
			closed := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			switch {
			case closed == "VALARM" && len(stack) > 0 && stack[len(stack)-1] == "VEVENT":
				alarms = append(alarms, alarm)
			case closed == "VEVENT":
				ev, err := buildEvent(props, alarms, local)
				if err != nil {
					skips = append(skips, Skip{UID: propValue(props, "UID"), Reason: err.Error()})
					continue
				}
				events = append(events, ev)
			}
			continue
		}
		if len(stack) == 0 {
			return nil, nil, fmt.Errorf("%w: %s outside any component", errMalformed, p.name)
		}
		switch stack[len(stack)-1] {
		case "VEVENT":
			props = append(props, p)
		case "VALARM":
			alarm = append(alarm, p)
		}
	}
	if len(stack) != 0 {
		return nil, nil, fmt.Errorf("%w: %v never ends", errMalformed, stack)
	}
	if len(events) == 0 && len(skips) == 0 && !strings.Contains(strings.ToUpper(data), "BEGIN:VCALENDAR") {
		return nil, nil, fmt.Errorf("%w: no VCALENDAR", errMalformed)
	}
	return applyOverrides(events), skips, nil
}

func applyOverrides(events []Event) []Event {
	for i := range events {
		if events[i].recurrenceID.IsZero() {
			continue
		}
		events[i].rule = nil
		for j := range events {
			if events[j].UID == events[i].UID && events[j].recurrenceID.IsZero() {
				events[j].except = append(events[j].except, events[i].recurrenceID)
			}
		}
	}
	return events
}

func propValue(props []property, name string) string {
	for _, p := range props {
		if p.name == name {
			return p.value
		}
	}
	return ""
}

func buildEvent(props []property, alarms [][]property, local *time.Location) (Event, error) {
	var (
		ev       Event
		hasStart bool
		hasEnd   bool
		duration span
		hasDur   bool
	)
	for _, p := range props {
		switch p.name {
		case "UID":
			ev.UID = p.value
		case "SUMMARY":
			ev.Summary = textEscapes.Replace(p.value)
		case "LOCATION":
			ev.Location = textEscapes.Replace(p.value)
		case "DTSTART":
			t, allDay, err := parseTime(p, local)
			if err != nil {
				return Event{}, fmt.Errorf("DTSTART: %w", err)
			}
			ev.Start, ev.AllDay, hasStart = t, allDay, true
		case "DTEND":
			t, _, err := parseTime(p, local)
			if err != nil {
				return Event{}, fmt.Errorf("DTEND: %w", err)
			}
			ev.End, hasEnd = t, true
		case "DURATION":
			d, err := parseDuration(p.value)
			if err != nil {
				return Event{}, fmt.Errorf("DURATION: %w", err)
			}
			duration, hasDur = d, true
		case "RRULE":
			r, err := parseRule(p.value, local)
			if err != nil {
				return Event{}, fmt.Errorf("RRULE: %w", err)
			}
			ev.rule = &r
		case "EXDATE":
			for value := range strings.SplitSeq(p.value, ",") {
				p.value = value
				t, _, err := parseTime(p, local)
				if err != nil {
					return Event{}, fmt.Errorf("EXDATE: %w", err)
				}
				ev.except = append(ev.except, t)
			}
		case "RECURRENCE-ID":
			t, _, err := parseTime(p, local)
			if err != nil {
				return Event{}, fmt.Errorf("RECURRENCE-ID: %w", err)
			}
			ev.recurrenceID = t
		case "RDATE":
			return Event{}, errors.New("RDATE is not supported")
		}
	}
	if !hasStart {
		return Event{}, errors.New("no DTSTART")
	}
	switch {
	case hasEnd:
	case hasDur:
		ev.End = duration.after(ev.Start)
	case ev.AllDay:
		ev.End = ev.Start.AddDate(0, 0, 1)
	default:
		ev.End = ev.Start
	}
	if ev.End.Before(ev.Start) {
		return Event{}, errors.New("DTEND before DTSTART")
	}
	if ev.rule != nil && ev.rule.byDay != nil && ev.rule.freq == yearly {
		return Event{}, errors.New("RRULE: BYDAY with FREQ=YEARLY is not supported")
	}
	for _, a := range alarms {
		alarm, err := parseAlarm(a, local)
		if err != nil {
			return Event{}, fmt.Errorf("VALARM: %w", err)
		}
		ev.Alarms = append(ev.Alarms, alarm)
	}
	return ev, nil
}

func parseAlarm(props []property, local *time.Location) (Alarm, error) {
	for _, p := range props {
		if p.name != "TRIGGER" {
			continue
		}
		if strings.EqualFold(p.param("VALUE"), "DATE-TIME") {
			t, _, err := parseTime(p, local)
			if err != nil {
				return Alarm{}, err
			}
			return Alarm{At: t, Absolute: true}, nil
		}
		d, err := parseDuration(p.value)
		if err != nil {
			return Alarm{}, err
		}
		return Alarm{Offset: d.approx(), FromEnd: strings.EqualFold(p.param("RELATED"), "END")}, nil
	}
	return Alarm{}, errors.New("no TRIGGER")
}

func parseTime(p property, local *time.Location) (time.Time, bool, error) {
	value := p.value
	if strings.EqualFold(p.param("VALUE"), "DATE") || len(value) == 8 {
		t, err := time.ParseInLocation("20060102", value, local)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("date %q: %w", value, err)
		}
		return t, true, nil
	}
	loc := local
	if utc, ok := strings.CutSuffix(value, "Z"); ok {
		value, loc = utc, time.UTC
	} else if tzid := p.param("TZID"); tzid != "" {
		l, err := time.LoadLocation(strings.TrimPrefix(tzid, "/"))
		if err != nil {
			return time.Time{}, false, fmt.Errorf("unknown TZID %q", tzid)
		}
		loc = l
	}
	t, err := time.ParseInLocation("20060102T150405", value, loc)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("date-time %q: %w", p.value, err)
	}
	return t, false, nil
}

type span struct {
	days  int
	clock time.Duration
}

func (s span) after(t time.Time) time.Time { return t.AddDate(0, 0, s.days).Add(s.clock) }

func (s span) approx() time.Duration { return time.Duration(s.days)*24*time.Hour + s.clock }

func parseDuration(value string) (span, error) {
	rest := value
	sign := 1
	switch {
	case strings.HasPrefix(rest, "-"):
		sign, rest = -1, rest[1:]
	case strings.HasPrefix(rest, "+"):
		rest = rest[1:]
	}
	rest, ok := strings.CutPrefix(rest, "P")
	if !ok || rest == "" {
		return span{}, fmt.Errorf("duration %q", value)
	}
	var s span
	inTime := false
	for rest != "" {
		if rest[0] == 'T' {
			inTime, rest = true, rest[1:]
			continue
		}
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i == 0 || i == len(rest) {
			return span{}, fmt.Errorf("duration %q", value)
		}
		n, err := strconv.Atoi(rest[:i])
		if err != nil || n > 1<<20 {
			return span{}, fmt.Errorf("duration %q", value)
		}
		switch unit := rest[i]; {
		case !inTime && unit == 'W':
			s.days += 7 * n
		case !inTime && unit == 'D':
			s.days += n
		case inTime && unit == 'H':
			s.clock += time.Duration(n) * time.Hour
		case inTime && unit == 'M':
			s.clock += time.Duration(n) * time.Minute
		case inTime && unit == 'S':
			s.clock += time.Duration(n) * time.Second
		default:
			return span{}, fmt.Errorf("duration %q", value)
		}
		rest = rest[i+1:]
	}
	s.days *= sign
	s.clock *= time.Duration(sign)
	return s, nil
}
