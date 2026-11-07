package calendar

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/stubbedev/lys-shell/internal/fakeclock"
)

func vevent(lines ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VEVENT\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
}

var fixture = map[string]string{
	"single.ics":   vevent("UID:single", "SUMMARY:Lunch\\, with Ann", "LOCATION:Cafe", "DTSTART:20261020T120000Z", "DTEND:20261020T130000Z"),
	"allday.ics":   vevent("UID:allday", "SUMMARY:Holiday", "DTSTART;VALUE=DATE:20261024", "DTEND;VALUE=DATE:20261025"),
	"trip.ics":     vevent("UID:trip", "SUMMARY:Trip", "DTSTART;VALUE=DATE:20261030", "DTEND;VALUE=DATE:20261102"),
	"newyork.ics":  vevent("UID:ny", "SUMMARY:NY standup", "DTSTART;TZID=America/New_York:20261023T090000", "DURATION:PT15M", "RRULE:FREQ=DAILY;COUNT=12"),
	"weekly.ics":   vevent("UID:weekly", "SUMMARY:Gym", "DTSTART;TZID=Europe/Copenhagen:20261019T100000", "DTEND;TZID=Europe/Copenhagen:20261019T110000", "RRULE:FREQ=WEEKLY;BYDAY=MO,WE;UNTIL=20261104T235959Z", "EXDATE;TZID=Europe/Copenhagen:20261026T100000"),
	"monthly.ics":  vevent("UID:monthly", "SUMMARY:Review", "DTSTART:20260925T160000", "DURATION:PT1H", "RRULE:FREQ=MONTHLY;BYDAY=-1FR"),
	"floating.ics": vevent("UID:floating", "SUMMARY:Dentist", "DTSTART:20261103T083000", "DTEND:20261103T090000"),
	"override.ics": "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:sync\r\nSUMMARY:Sync\r\nDTSTART;TZID=Europe/Copenhagen:20261027T090000\r\nDTEND;TZID=Europe/Copenhagen:20261027T093000\r\nRRULE:FREQ=WEEKLY;COUNT=3\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:sync\r\nRECURRENCE-ID;TZID=Europe/Copenhagen:20261103T090000\r\nSUMMARY:Sync (moved)\r\nDTSTART;TZID=Europe/Copenhagen:20261104T130000\r\nDTEND;TZID=Europe/Copenhagen:20261104T133000\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n",
	"unsupported.ics": vevent("UID:odd", "SUMMARY:Odd", "DTSTART:20261021T100000Z", "RRULE:FREQ=MONTHLY;BYSETPOS=1;BYDAY=MO"),
	"broken.ics":      "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nSUMMARY:Never\r\nDTSTART:20261021T100000Z\r\n",
	"notes.txt":       "not a calendar",
}

func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func copenhagen(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Copenhagen")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *logCapture) lines() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Split(strings.TrimSpace(c.buf.String()), "\n")
}

func captureLog(t *testing.T) *logCapture {
	t.Helper()
	c := &logCapture{}
	prev, flags := log.Writer(), log.Flags()
	log.SetOutput(c)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prev); log.SetFlags(flags) })
	return c
}

func newFixtureStore(t *testing.T, files map[string]string) (*Store, string) {
	t.Helper()
	dir := writeFixture(t, files)
	s := NewStore(fakeclock.New(), copenhagen(t))
	t.Cleanup(s.Close)
	if err := s.Configure([]Source{{Dir: dir, Color: "blue"}}); err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func dayText(m *Month, d int) []string {
	var out []string
	for _, o := range m.Day(d) {
		if o.AllDay {
			out = append(out, o.Summary)
			continue
		}
		out = append(out, o.Start.Format("15:04")+" "+o.Summary)
	}
	return out
}

func TestFixtureVdirShowsExactlyTheExpectedEventsPerDay(t *testing.T) {
	logs := captureLog(t)
	s, dir := newFixtureStore(t, fixture)
	oct := s.Month(2026, time.October)
	nov := s.Month(2026, time.November)
	want := []struct {
		m    *Month
		name string
		day  int
		want []string
	}{
		{oct, "Oct", 19, []string{"10:00 Gym"}},
		{oct, "Oct", 20, []string{"14:00 Lunch, with Ann"}},
		{oct, "Oct", 21, []string{"10:00 Gym"}},
		{oct, "Oct", 22, nil},
		{oct, "Oct", 23, []string{"15:00 NY standup"}},
		{oct, "Oct", 24, []string{"Holiday", "15:00 NY standup"}},
		{oct, "Oct", 25, []string{"14:00 NY standup"}},
		{oct, "Oct", 26, []string{"14:00 NY standup"}},
		{oct, "Oct", 27, []string{"09:00 Sync", "14:00 NY standup"}},
		{oct, "Oct", 28, []string{"10:00 Gym", "14:00 NY standup"}},
		{oct, "Oct", 30, []string{"Trip", "14:00 NY standup", "16:00 Review"}},
		{oct, "Oct", 31, []string{"Trip", "14:00 NY standup"}},
		{nov, "Nov", 1, []string{"Trip", "15:00 NY standup"}},
		{nov, "Nov", 2, []string{"10:00 Gym", "15:00 NY standup"}},
		{nov, "Nov", 3, []string{"08:30 Dentist", "15:00 NY standup"}},
		{nov, "Nov", 4, []string{"10:00 Gym", "13:00 Sync (moved)"}},
		{nov, "Nov", 5, nil},
		{nov, "Nov", 9, nil},
		{nov, "Nov", 10, []string{"09:00 Sync"}},
		{nov, "Nov", 27, []string{"16:00 Review"}},
	}
	for _, tc := range want {
		if got := dayText(tc.m, tc.day); !slices.Equal(got, tc.want) {
			t.Errorf("%s %d = %q, want %q", tc.name, tc.day, got, tc.want)
		}
	}
	if got := s.Month(2026, time.September).Day(25); len(got) != 1 || got[0].Summary != "Review" {
		t.Errorf("Sep 25 = %+v, want the first Review", got)
	}
	if o := oct.Day(20)[0]; o.Location != "Cafe" || o.Color != "blue" || o.End.Sub(o.Start) != time.Hour {
		t.Errorf("Lunch = %+v", o)
	}

	broken, unsupported := 0, 0
	for _, line := range logs.lines() {
		if strings.Contains(line, filepath.Join(dir, "broken.ics")) {
			broken++
		}
		if strings.Contains(line, filepath.Join(dir, "unsupported.ics")) && strings.Contains(line, "BYSETPOS") {
			unsupported++
		}
	}
	if broken != 1 || unsupported != 1 {
		t.Errorf("log lines naming the malformed file: %d, the unsupported event: %d; want one each\n%s", broken, unsupported, strings.Join(logs.lines(), "\n"))
	}
}

func TestAMonthIsExpandedOnceUntilAFileChanges(t *testing.T) {
	s, dir := newFixtureStore(t, map[string]string{
		"a.ics": vevent("UID:a", "SUMMARY:Before", "DTSTART:20261110T090000Z"),
		"b.ics": vevent("UID:b", "SUMMARY:Other", "DTSTART:20261111T090000Z"),
	})
	changes, stop := s.Changes()
	defer stop()
	parses, _ := s.counts()
	for range 3 {
		s.Month(2026, time.November)
	}
	if p, e := s.counts(); e != 1 || p != parses {
		t.Fatalf("three opens of an unchanged month: %d expansions, %d parses; want 1 and none", e, p-parses)
	}

	if err := os.WriteFile(filepath.Join(dir, "a.ics"), []byte(vevent("UID:a", "SUMMARY:After", "DTSTART:20261110T090000Z")), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-changes:
		case <-deadline:
			t.Fatal("editing a file did not update the store")
		}
		if got := s.Month(2026, time.November).Day(10); len(got) == 1 && got[0].Summary == "After" {
			break
		}
	}
	now, _ := s.counts()
	reparsed := now - parses
	if reparsed < 1 || reparsed > 2 {
		t.Errorf("an edit of one file parsed %d files, want only that one", reparsed)
	}

	if err := os.Remove(filepath.Join(dir, "b.ics")); err != nil {
		t.Fatal(err)
	}
	for len(s.Month(2026, time.November).Day(11)) != 0 {
		select {
		case <-changes:
		case <-time.After(5 * time.Second):
			t.Fatal("removing a file did not remove its event")
		}
	}
}

func TestNextIsTheFirstTimedEventFromNow(t *testing.T) {
	s, _ := newFixtureStore(t, fixture)
	loc := copenhagen(t)
	o, ok := s.Next(time.Date(2026, 11, 3, 8, 0, 0, 0, loc))
	if !ok || o.Summary != "Dentist" {
		t.Errorf("next at 08:00 Nov 3 = %+v, %v; want the dentist", o, ok)
	}
	o, ok = s.Next(time.Date(2026, 11, 3, 8, 31, 0, 0, loc))
	if !ok || o.Summary != "NY standup" {
		t.Errorf("next at 08:31 Nov 3 = %+v, %v; want the standup", o, ok)
	}
	if o, ok := s.Next(time.Date(2027, 6, 1, 0, 0, 0, 0, loc)); ok && o.Summary != "Review" {
		t.Errorf("next in June 2027 = %+v", o)
	}
	empty, _ := newFixtureStore(t, map[string]string{})
	if _, ok := empty.Next(time.Date(2026, 11, 3, 8, 0, 0, 0, loc)); ok {
		t.Error("an empty calendar has a next event")
	}
}

func TestAnAlarmFiresOneNotificationAtItsTrigger(t *testing.T) {
	dir := writeFixture(t, map[string]string{
		"call.ics": "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:call\r\nSUMMARY:Call\r\nDTSTART:20260101T100000Z\r\nDTEND:20260101T103000Z\r\n" +
			"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT15M\r\nEND:VALARM\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n",
		"late.ics": "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:late\r\nSUMMARY:Wrap up\r\nDTSTART:20260101T110000Z\r\nDTEND:20260101T113000Z\r\n" +
			"BEGIN:VALARM\r\nTRIGGER;RELATED=END:PT0S\r\nEND:VALARM\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n",
	})
	clock := fakeclock.New()
	s := NewStore(clock, time.UTC)
	t.Cleanup(s.Close)
	if err := s.Configure([]Source{{Dir: dir}}); err != nil {
		t.Fatal(err)
	}
	var fired []string
	s.RunReminders(func(r Reminder) { fired = append(fired, r.At.Format("15:04")+" "+r.Summary) })

	clock.Advance(9*time.Hour + 44*time.Minute)
	if len(fired) != 0 {
		t.Fatalf("fired %q before the trigger", fired)
	}
	clock.Advance(time.Minute)
	if !slices.Equal(fired, []string{"09:45 Call"}) {
		t.Fatalf("at 09:45 fired %q, want the call's reminder once", fired)
	}
	clock.Advance(24 * time.Hour)
	if !slices.Equal(fired, []string{"09:45 Call", "11:30 Wrap up"}) {
		t.Errorf("after a day fired %q, want each reminder exactly once", fired)
	}
}

func TestParseRejectsWhatItCannotRead(t *testing.T) {
	for name, doc := range map[string]string{
		"unclosed":    "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\n",
		"mismatched":  "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nEND:VCALENDAR\r\n",
		"no colon":    "BEGIN:VCALENDAR\r\nnonsense\r\nEND:VCALENDAR\r\n",
		"stray":       "SUMMARY:x\r\n",
		"not ical":    "hello",
		"bad quoting": "BEGIN:VCALENDAR\r\nX-A;P=\"open:1\r\nEND:VCALENDAR\r\n",
	} {
		if _, _, err := Parse(doc, time.UTC); err == nil {
			t.Errorf("%s: parsed without an error", name)
		}
	}
	events, skips, err := Parse(vevent("UID:x", "SUMMARY:Folded and", "  continued", "DTSTART;TZID=Mars/Olympus:20260101T100000"), time.UTC)
	if err != nil || len(events) != 0 || len(skips) != 1 || !strings.Contains(skips[0].Reason, "Mars/Olympus") {
		t.Errorf("an unknown TZID: %v %v %v", events, skips, err)
	}
	events, _, err = Parse(vevent("UID:y", "SUMMARY:Folded and", "  continued", "DTSTART:20260101T100000Z"), time.UTC)
	if err != nil || len(events) != 1 || events[0].Summary != "Folded and continued" {
		t.Errorf("a folded line = %+v, %v", events, err)
	}
}

func TestRecurrenceRules(t *testing.T) {
	loc := copenhagen(t)
	for _, tc := range []struct {
		rule  string
		start string
		want  []string
	}{
		{"FREQ=DAILY;INTERVAL=2;COUNT=3", "20261230T080000", []string{"12-30", "01-01", "01-03"}},
		{"FREQ=WEEKLY;BYDAY=TU,TH;COUNT=4", "20261229T080000", []string{"12-29", "12-31", "01-05", "01-07"}},
		{"FREQ=MONTHLY;COUNT=4", "20261031T080000", []string{"10-31", "12-31", "01-31", "03-31"}},
		{"FREQ=MONTHLY;BYDAY=2MO;COUNT=2", "20261109T080000", []string{"11-09", "12-14"}},
		{"FREQ=YEARLY;COUNT=2", "20280229T080000", []string{"02-29", "02-29"}},
		{"FREQ=DAILY;UNTIL=20261102", "20261031T230000", []string{"10-31", "11-01", "11-02"}},
	} {
		events, skips, err := Parse(vevent("UID:r", "SUMMARY:r", "DTSTART;TZID=Europe/Copenhagen:"+tc.start, "RRULE:"+tc.rule), loc)
		if err != nil || len(skips) != 0 || len(events) != 1 {
			t.Fatalf("%s: %v %v %v", tc.rule, events, skips, err)
		}
		var got []string
		events[0].occurrences(time.Date(2026, 1, 1, 0, 0, 0, 0, loc), time.Date(2033, 1, 1, 0, 0, 0, 0, loc), func(s time.Time) bool {
			got = append(got, fmt.Sprintf("%02d-%02d", s.Month(), s.Day()))
			return true
		})
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s from %s = %q, want %q", tc.rule, tc.start, got, tc.want)
		}
	}
	for _, bad := range []string{"FREQ=HOURLY", "FREQ=DAILY;BYMONTH=1", "INTERVAL=2", "FREQ=WEEKLY;BYDAY=2MO", "FREQ=DAILY;COUNT=0", "FREQ=DAILY;COUNT=2;UNTIL=20270101", "FREQ=YEARLY;BYDAY=MO"} {
		if _, skips, err := Parse(vevent("UID:b", "DTSTART:20260101T100000Z", "RRULE:"+bad), loc); err != nil || len(skips) != 1 {
			t.Errorf("RRULE:%s was not skipped (%v, %v)", bad, skips, err)
		}
	}
}

func TestLongRunningSeriesExpandWithoutWalkingFromTheStart(t *testing.T) {
	events, _, err := Parse(vevent("UID:old", "SUMMARY:Daily", "DTSTART:19900101T090000Z", "RRULE:FREQ=DAILY"), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	events[0].occurrences(from, from.AddDate(0, 1, 0), func(time.Time) bool { calls++; return true })
	if calls != 30 {
		t.Errorf("%d occurrences in November, want 30", calls)
	}
	allocs := testing.AllocsPerRun(10, func() {
		events[0].occurrences(from, from.AddDate(0, 1, 0), func(time.Time) bool { return true })
	})
	if allocs > 0 {
		t.Errorf("expanding a month of a 36-year series allocated %v times", allocs)
	}
}

func (s *Store) counts() (parses, expansions int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.parses, s.expansions
}
