package bar

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	shelllocales "github.com/stubbedev/lys-shell/i18n/locales/shell"
	"github.com/stubbedev/lys-shell/internal/fakeclock"
	"github.com/stubbedev/lys-shell/service/calendar"
	"github.com/stubbedev/lys-shell/shell/treetest"
	"github.com/stubbedev/lys-shell/strftime"
)

func icsEvent(uid, summary, start, end string) string {
	return "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:" + uid + "\r\nSUMMARY:" + summary + "\r\nLOCATION:Room 2\r\nDTSTART:" + start + "\r\nDTEND:" + end + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
}

func calendarFixture(t *testing.T) (string, *calendar.Store) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"standup.ics": icsEvent("standup", "Standup", "20270310T090000", "20270310T091500"),
		"review.ics":  icsEvent("review", "Review", "20270310T140000", "20270310T150000"),
		"offsite.ics": icsEvent("offsite", "Offsite", "20270322", "20270324"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store := calendar.NewStore(fakeclock.New(), time.Local)
	t.Cleanup(store.Close)
	if err := store.Configure([]calendar.Source{{Dir: dir, Color: "var(--accent)"}}); err != nil {
		t.Fatal(err)
	}
	return dir, store
}

func calendarViewWith(t *testing.T, store *calendar.Store) *calendarView {
	t.Helper()
	cfg := config.Defaults()
	cfg.Clock.Format = mustStrftimeFormat(t, "%H:%M")
	ctx := newTestContext(t, cfg)
	ctx.Calendar = store
	v := calendarDropdown(ctx).(*calendarView)
	t.Cleanup(v.dropdownClosed)
	return v
}

func TestCalendarDropdownMarksDaysAndListsTheAgenda(t *testing.T) {
	_, store := calendarFixture(t)
	v := calendarViewWith(t, store)
	if !widget.IsVisible(v.agenda) {
		t.Fatal("the agenda is hidden with a calendar configured")
	}
	v.cal.ShowMonth(widget.Date{Year: 2027, Month: time.March, Day: 1})
	var marked []int
	for d := 1; d <= 31; d++ {
		if v.cal.DayMarked(widget.Date{Year: 2027, Month: time.March, Day: d}) {
			marked = append(marked, d)
		}
	}
	if !slices.Equal(marked, []int{10, 22, 23}) {
		t.Errorf("marked days %v, want 10, 22 and 23", marked)
	}

	v.cal.Select(widget.Date{Year: 2027, Month: time.March, Day: 10})
	labels := treetest.Labels(v.agenda)
	want := []string{"09:00 – 09:15", "Standup", "Room 2", "14:00 – 15:00", "Review", "Room 2"}
	if !slices.Equal(labels, want) {
		t.Errorf("agenda for March 10 = %q, want %q", labels, want)
	}
	if got := v.cal.Detail(widget.Date{Year: 2027, Month: time.March, Day: 10}); got != "Standup\nReview" {
		t.Errorf("the day's tooltip = %q", got)
	}

	v.cal.Select(widget.Date{Year: 2027, Month: time.March, Day: 23})
	if labels := treetest.Labels(v.agenda); len(labels) != 3 || labels[0] != i18n.T("dropdown-calendar-all-day") || labels[1] != "Offsite" {
		t.Errorf("agenda for March 23 = %q, want the all-day offsite", labels)
	}
	v.cal.Select(widget.Date{Year: 2027, Month: time.March, Day: 11})
	if labels := treetest.Labels(v.agenda); !slices.Equal(labels, []string{i18n.T("dropdown-calendar-no-events")}) {
		t.Errorf("agenda for an empty day = %q", labels)
	}

	v.cal.ShowMonth(widget.Date{Year: 2027, Month: time.April, Day: 1})
	if v.cal.DayMarked(widget.Date{Year: 2027, Month: time.March, Day: 10}) {
		t.Error("a month change kept the previous month's marks")
	}
}

func TestCalendarDropdownFollowsAnEditedFile(t *testing.T) {
	dir, store := calendarFixture(t)
	changes, stop := store.Changes()
	defer stop()
	v := calendarViewWith(t, store)
	v.cal.Select(widget.Date{Year: 2027, Month: time.March, Day: 10})
	if err := os.WriteFile(filepath.Join(dir, "review.ics"), []byte(icsEvent("review", "Retro", "20270310T140000", "20270310T150000")), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for !slices.Contains(treetest.Labels(v.agenda), "Retro") {
		select {
		case <-changes:
			v.refreshEvents()
		case <-deadline:
			t.Fatalf("the agenda still reads %q after the edit", treetest.Labels(v.agenda))
		}
	}
}

func TestCalendarDropdownWithoutCalendarsShowsNoAgenda(t *testing.T) {
	store := calendar.NewStore(fakeclock.New(), time.Local)
	t.Cleanup(store.Close)
	v := calendarViewWith(t, store)
	if widget.IsVisible(v.agenda) || widget.IsVisible(v.agendaRule) {
		t.Error("an agenda shows without any calendar configured")
	}
	v.cal.Select(widget.Date{Year: 2027, Month: time.March, Day: 10})
	if v.cal.Detail(widget.Date{Year: 2027, Month: time.March, Day: 10}) != "" {
		t.Error("a day has a detail without calendars")
	}
}

func TestClockLabelShowsTheNextEvent(t *testing.T) {
	_, store := calendarFixture(t)
	cfg := config.DefaultsClock()
	cfg.Format = mustStrftimeFormat(t, "%H:%M {{ next_event }}")
	at := func(day, hour, minute int) time.Time {
		return time.Date(2027, time.March, day, hour, minute, 0, 0, time.Local)
	}
	for _, tc := range []struct {
		now  time.Time
		want string
	}{
		{at(10, 8, 0), "08:00 09:00 Standup"},
		{at(10, 9, 1), "09:01 14:00 Review"},
		{at(9, 20, 0), "20:00 Wed 09:00 Standup"},
		{at(10, 15, 0), "15:00 "},
	} {
		if got := clockLabel(cfg, store, tc.now, strftime.English); got != tc.want {
			t.Errorf("label at %v = %q, want %q", tc.now, got, tc.want)
		}
	}
	twelve := cfg
	twelve.Format = mustStrftimeFormat(t, "%I:%M %p {{ next_event }}")
	if got := clockLabel(twelve, store, at(10, 13, 0), strftime.English); got != "01:00 PM 2:00 PM Review" {
		t.Errorf("12-hour label = %q", got)
	}
	plain := cfg
	plain.Format = mustStrftimeFormat(t, "%H:%M")
	if got := clockLabel(plain, store, at(10, 8, 0), strftime.English); got != "08:00" {
		t.Errorf("a format without the placeholder = %q", got)
	}
	if got := clockLabel(cfg, nil, at(10, 8, 0), strftime.English); got != "08:00 " {
		t.Errorf("no calendar store = %q", got)
	}
}

func TestTheClockLabelNamesDaysAndMonthsInTheShellLanguage(t *testing.T) {
	_, store := calendarFixture(t)
	names := func(lang string) strftime.Names {
		l, err := i18n.Select(i18n.NewAssets(shelllocales.FS), []i18n.LangID{i18n.MustLangID(lang)})
		if err != nil {
			t.Fatal(err)
		}
		return clockNames(l)
	}
	french, english := names("fr"), names("en-US")
	if english != strftime.English {
		t.Errorf("the en-US names %+v are not strftime's English %+v", english, strftime.English)
	}
	cfg := config.DefaultsClock()
	cfg.Format = mustStrftimeFormat(t, "%a %b %d %I:%M %p")
	now := time.Date(2026, time.November, 7, 19, 55, 0, 0, time.Local)
	if got, want := clockLabel(cfg, nil, now, french), "sam. nov. 07 07:55 PM"; got != want {
		t.Errorf("fr label = %q, want %q", got, want)
	}
	if got, want := clockLabel(cfg, nil, now, english), "Sat Nov 07 07:55 PM"; got != want {
		t.Errorf("en-US label = %q, want %q", got, want)
	}
	cfg.Format = mustStrftimeFormat(t, "%A %-d %B {{ next_event }}")
	if got, want := clockLabel(cfg, store, time.Date(2027, time.March, 9, 20, 0, 0, 0, time.Local), french), "mardi 9 mars mer. 09:00 Standup"; got != want {
		t.Errorf("fr label with the next event = %q, want %q", got, want)
	}
}
