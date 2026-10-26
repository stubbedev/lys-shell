package bar

import (
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// The calendar speaks the shell's catalog and week start: month and
// weekday names come from the cal-* messages, the first column from
// calendar-weekday-start.
func TestCalendarNamesAreTheShells(t *testing.T) {
	cfg := config.Defaults()
	cfg.Clock.CalendarWeekdayStart = config.WeekStartMonday
	v := calendarDropdown(newTestContext(t, cfg)).(*calendarView)
	names := v.names
	if got := names.MonthName(time.October); got != i18n.T("cal-month-october") {
		t.Errorf("October = %q, want the catalog's", got)
	}
	if got := names.WeekdayName(time.Saturday); got != i18n.T("cal-weekday-sat") {
		t.Errorf("Saturday = %q, want the catalog's", got)
	}
	if names.FirstWeekday() != time.Monday {
		t.Errorf("first weekday = %v, want the configured Monday", names.FirstWeekday())
	}
	def := calendarDropdown(newTestContext(t, config.Defaults())).(*calendarView)
	if n := def.names; n.FirstWeekday() != time.Sunday {
		t.Errorf("the default week starts %v, want Sunday", n.FirstWeekday())
	}
}

// The calendar opens on today's month with today picked; Today brings
// both back after a month step and another pick.
func TestCalendarViewNavigatesAndSelects(t *testing.T) {
	cfg := config.Defaults()
	cfg.Clock.Format = "%I:%M %p"
	cfg.Clock.DropdownShowSeconds = false
	v := calendarDropdown(newTestContext(t, cfg)).(*calendarView)
	today := widget.DateOf(time.Now())
	if v.cal.Selection() != today || v.cal.Shown() != today.FirstOfMonth() {
		t.Errorf("opens on %v picking %v, want today %v", v.cal.Shown(), v.cal.Selection(), today)
	}
	if !v.ampm.Visible() || v.seconds.Visible() {
		t.Error("12h without seconds: the AM/PM shows and the seconds hide")
	}
	v.cal.ShowMonth(today.AddMonths(2))
	v.cal.Select(today.AddMonths(2))
	v.goToToday()
	if v.cal.Shown() != today.FirstOfMonth() || v.cal.Selection() != today {
		t.Errorf("Today left %v picking %v, want today's month and today", v.cal.Shown(), v.cal.Selection())
	}
	if got := v.dayName.Text(); got != i18n.T(calendarDayIDs[time.Now().Weekday()]) {
		t.Errorf("day name = %q", got)
	}
}

// Across midnight the tick moves today; within a day it leaves it.
func TestCalendarTodayFollowsTheClock(t *testing.T) {
	v := calendarDropdown(newTestContext(t, config.Defaults())).(*calendarView)
	start := v.today
	v.tick()
	if v.today != start {
		t.Errorf("a same-day tick moved today to %v", v.today)
	}
	tomorrow := time.Now().AddDate(0, 0, 1)
	v.now = func() time.Time { return tomorrow }
	v.tick()
	if v.today != widget.DateOf(tomorrow) {
		t.Errorf("after midnight today = %v, want %v", v.today, widget.DateOf(tomorrow))
	}
}

// The section is the Today button over lys-ui's Calendar, which owns
// the month title, the navigation and the grid: no hand-built grid.
func TestCalendarSectionIsTheLysUICalendar(t *testing.T) {
	v := calendarDropdown(newTestContext(t, config.Defaults())).(*calendarView)
	section := classedBox(t, v, "cal-section")
	var cals int
	walkTree(section, func(w widget.Widget) bool {
		if _, ok := w.(*widget.Calendar); ok {
			cals++
		}
		return true
	})
	if cals != 1 {
		t.Fatalf("cal-section holds %d calendars, want one", cals)
	}
	for _, gone := range []string{"cal-grid", "cal-grid-wrap", "cal-day", "cal-nav"} {
		if findByClass(v, gone) != nil {
			t.Errorf("the hand-built %s survived", gone)
		}
	}
	today := findButton(section, "cal-today-btn")
	if today == nil || !today.HasClass(css.Ghost) || !today.HasClass(css.Small) {
		t.Error("the Today button is not a small ghost button")
	}
}

// The clock row's show-seconds class follows the clock config and
// stops the separator blink; the ampm shows only in 12h.
func TestCalendarClockRowCarriesTheStateClasses(t *testing.T) {
	cfg := config.Defaults()
	cfg.Clock.Format = "%I:%M %p"
	cfg.Clock.DropdownShowSeconds = true
	ctx := newTestContext(t, cfg)
	v := calendarDropdown(ctx).(*calendarView)
	if !v.timeRow.HasClass("show-seconds") || !v.ampm.Visible() {
		t.Errorf("12h with seconds: classes = %v, ampm %v; want show-seconds and the ampm", v.timeRow.Classes(), v.ampm.Visible())
	}
	cfg.Clock.Format = "%H:%M"
	cfg.Clock.DropdownShowSeconds = false
	v.tick()
	if v.timeRow.HasClass("show-seconds") || v.ampm.Visible() {
		t.Errorf("24h without seconds: classes = %v, ampm %v; want neither", v.timeRow.Classes(), v.ampm.Visible())
	}
}

// The month grid takes the whole width of its section, wider than the
// grid's natural size, and never more than the section has.
func TestCalendarFillsItsSection(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	v := calendarDropdown(ctx).(*calendarView)
	w, _, _ := dropdownDims("calendar", ctx.Config)
	card, _ := dropdownTree(dropdownPanel("calendar", ctx.Config, v), ctx.Config)
	ctx.Theme.Attach(card)
	sz := widget.MeasureAsChild(card, widget.Constraints{Max: widget.Size{W: w, H: 4000}})
	card.Arrange(render.Rect{W: sz.W, H: sz.H})
	section := treetest.WithClass(v, "cal-section")[0].(*widget.Box).Bounds()
	cal := v.cal.Bounds()
	natural := widget.MeasureAsChild(v.cal, widget.Constraints{Max: widget.Size{W: 4000, H: 4000}})
	if natural.W >= section.W {
		t.Fatalf("the section (%dpx) leaves the grid (%dpx) no room to fill", section.W, natural.W)
	}
	if cal.X != section.X || cal.W != section.W {
		t.Errorf("the grid spans x %d..%d, want the section's %d..%d", cal.X, cal.X+cal.W, section.X, section.X+section.W)
	}
}
