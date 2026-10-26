package bar

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/strftime"
)

// calendarWeekStart maps calendar-weekday-start.
func calendarWeekStart(w config.WeekStart) time.Weekday {
	switch w {
	case config.WeekStartMonday:
		return time.Monday
	case config.WeekStartTuesday:
		return time.Tuesday
	case config.WeekStartWednesday:
		return time.Wednesday
	case config.WeekStartThursday:
		return time.Thursday
	case config.WeekStartFriday:
		return time.Friday
	case config.WeekStartSaturday:
		return time.Saturday
	}
	return time.Sunday
}

var (
	calendarWeekdayIDs = [7]string{"cal-weekday-sun", "cal-weekday-mon", "cal-weekday-tue", "cal-weekday-wed", "cal-weekday-thu", "cal-weekday-fri", "cal-weekday-sat"}
	calendarDayIDs     = [7]string{"cal-day-sunday", "cal-day-monday", "cal-day-tuesday", "cal-day-wednesday", "cal-day-thursday", "cal-day-friday", "cal-day-saturday"}
	calendarMonthIDs   = [12]string{
		"cal-month-january", "cal-month-february", "cal-month-march", "cal-month-april", "cal-month-may", "cal-month-june",
		"cal-month-july", "cal-month-august", "cal-month-september", "cal-month-october", "cal-month-november", "cal-month-december",
	}
)

// calendarNames gives widget.Calendar the shell's localized month and
// weekday names and its configured week start.
type calendarNames struct{ weekStart time.Weekday }

func (calendarNames) MonthName(m time.Month) string { return i18n.T(calendarMonthIDs[m-1]) }

func (calendarNames) WeekdayName(w time.Weekday) string { return i18n.T(calendarWeekdayIDs[w]) }

func (n calendarNames) FirstWeekday() time.Weekday { return n.weekStart }

func (calendarNames) FormatDate(d widget.Date) string {
	return i18n.T("cal-clock-date-rest",
		i18n.Str("month", i18n.T(calendarMonthIDs[d.Month-1])),
		i18n.Str("day", strconv.Itoa(d.Day)),
		i18n.Str("year", strconv.Itoa(d.Year)))
}

// calendarIs12h is is_12h_format: the clock format uses %I or %p.
func calendarIs12h(format string) bool {
	return strings.Contains(format, "%I") || strings.Contains(format, "%p")
}

func strftimeOf(layout string, t time.Time) string {
	f, err := strftime.Compile(layout)
	if err != nil {
		return ""
	}
	return f.Format(t)
}

// calendarView is the calendar dropdown: the clock hero over the month
// grid, ticking every second while open.
type calendarView struct {
	ctx  ModuleContext
	font render.Font
	px   float64

	*widget.Box
	hours, minutes, seconds, ampm *widget.Label
	secSep                        *widget.Label
	dayName, dateRest             *widget.Label
	cal                           *widget.Calendar
	names                         calendarNames
	timeRow                       *widget.Box

	today widget.Date

	once sync.Once
	stop chan struct{}
	now  func() time.Time
}

func calendarDropdown(ctx ModuleContext) widget.Widget {
	font, px := dropdownFont(ctx)
	v := &calendarView{ctx: ctx, font: font, px: px, stop: make(chan struct{}), now: time.Now}
	v.Box = widget.NewBox(widget.Column, 0, 0)
	v.AddClass("dropdown", "calendar-dropdown")
	v.Append(dropdownHeader(font, "ld-calendar-clock-symbolic", i18n.T("dropdown-calendar-title")), false)
	content := dropdownContent()
	content.Append(v.hero(), false)
	content.Append(widget.NewSeparator(widget.Horizontal), false)
	content.Append(v.calendar(), false)
	v.Append(content, false)
	v.tick()
	v.follow()
	return v
}

// label paints with the constructor ink unset (0): the cascade's
// color — a classed rule, or the content box's default — supplies it.
func (v *calendarView) label(text string, scale float64) *widget.Label {
	return widget.NewLabel(v.font, v.px*scale, text, 0)
}

// clockTimeScale is the hero clock's display size over the dropdown's
// text size.
const clockTimeScale = 4.5

// hero is the clock-hero: HH:MM[:SS][ AM] in the display size over
// "Weekday, Month D, YYYY", the day in the heading weight and the rest
// dimmed.
func (v *calendarView) hero() widget.Widget {
	th := widget.Current()
	col := widget.NewBox(widget.Column, th.Spacing/2, 0)
	col.AddClass("clock-hero")
	row := widget.NewBox(widget.Row, 0, 0)
	row.AddClass("clock-time-row")
	v.timeRow = row
	timeLabel := func(text string, classes ...string) *widget.Label {
		l := v.label(text, clockTimeScale)
		l.AddClass(append([]string{"clock-time", css.Numeric}, classes...)...)
		return l
	}
	v.hours, v.minutes, v.seconds = timeLabel(""), timeLabel(""), timeLabel("")
	v.secSep = timeLabel(":", "clock-separator")
	v.ampm = v.label("", 1)
	v.ampm.AddClass("clock-ampm", css.Heading, css.DimLabel)
	// The time and the ampm share the row's text baseline.
	for _, w := range []widget.Widget{v.hours, timeLabel(":", "clock-separator"), v.minutes, v.secSep, v.seconds, v.ampm} {
		row.AppendAligned(w, false, widget.AlignBaseline)
	}
	col.AppendAligned(row, false, widget.AlignCenter)
	date := widget.NewBox(widget.Row, 0, 0)
	date.AddClass("clock-date")
	v.dayName = v.label("", 1)
	v.dayName.AddClass("clock-date-day", css.Heading)
	v.dateRest = v.label("", 1)
	v.dateRest.AddClass(css.DimLabel)
	date.Append(v.dayName, false)
	date.Append(v.dateRest, false)
	col.AppendAligned(date, false, widget.AlignCenter)
	return col
}

// calendar is the Calendar component: the Today button over lys-ui's
// month grid, which carries the month title and its navigation. Today
// opens picked, so it wears the accent until another day is picked.
func (v *calendarView) calendar() widget.Widget {
	col := widget.NewBox(widget.Column, widget.Current().Spacing, 0)
	col.AddClass("cal-section")
	header := widget.NewBox(widget.Row, 0, 0)
	header.AddClass("cal-header")
	today := dropdownButton(v.label(i18n.T("cal-today"), 1), v.goToToday, css.Ghost, css.Small, "cal-today-btn")
	header.Append(widget.NewSpacer(0, 0), true)
	header.Append(today, false)
	col.Append(header, false)
	v.today = widget.DateOf(v.now())
	v.cal = widget.NewCalendar(v.font, v.px, v.today)
	v.names = calendarNames{weekStart: calendarWeekStart(v.ctx.Config.Clock.CalendarWeekdayStart)}
	v.cal.SetNames(v.names)
	v.cal.SetToday(v.today)
	col.Append(v.cal, false)
	return col
}

// goToToday is GoToToday: back to this month, today picked.
func (v *calendarView) goToToday() { v.cal.Select(v.today) }

// tick is TimeTick: the hero's fields and, across midnight, today.
func (v *calendarView) tick() {
	now := v.now()
	clock := v.ctx.Config.Clock
	use12h := calendarIs12h(clock.Format)
	if use12h {
		v.hours.SetText(strftimeOf("%I", now))
	} else {
		v.hours.SetText(strftimeOf("%H", now))
	}
	v.minutes.SetText(strftimeOf("%M", now))
	v.seconds.SetText(strftimeOf("%S", now))
	v.ampm.SetText(strftimeOf("%p", now))
	v.seconds.SetVisible(clock.DropdownShowSeconds)
	v.secSep.SetVisible(clock.DropdownShowSeconds)
	v.ampm.SetVisible(use12h)
	// show-seconds stops the separator blink.
	setClass(v.timeRow, "show-seconds", clock.DropdownShowSeconds)
	v.dayName.SetText(i18n.T(calendarDayIDs[now.Weekday()]))
	v.dateRest.SetText(i18n.T("cal-clock-date-rest",
		i18n.Str("month", i18n.T(calendarMonthIDs[now.Month()-1])),
		i18n.Str("day", strconv.Itoa(now.Day())),
		i18n.Str("year", strconv.Itoa(now.Year()))))
	if today := widget.DateOf(now); today != v.today {
		v.today = today
		v.cal.SetToday(today)
	}
}

func (v *calendarView) follow() {
	if v.ctx.App == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-v.stop:
				return
			case <-ticker.C:
				v.ctx.Invoke(v.tick)
			}
		}
	}()
}

// dropdownClosed implements dropdownCloser.
func (v *calendarView) dropdownClosed() { v.once.Do(func() { close(v.stop) }) }
