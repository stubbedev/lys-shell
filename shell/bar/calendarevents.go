package bar

import (
	"log"
	"strings"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/i18n"
	"github.com/stubbedev/lys-desktop/strftime"
	"github.com/stubbedev/lys-desktop/styling"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/internal/desktopnotify"
	"github.com/stubbedev/lys-shell/service/calendar"
)

const nextEventPlaceholder = "{{ next_event }}"

func calendarSources(cfg config.ClockConfig) []calendar.Source {
	out := make([]calendar.Source, len(cfg.Calendars))
	for i, c := range cfg.Calendars {
		out[i] = calendar.Source{Dir: c.Path, Color: c.Color.ToCSS()}
	}
	return out
}

func startCalendar(cfg config.ClockConfig, current func() *config.Config) *calendar.Store {
	store := calendar.NewStore(calendar.WallClock, time.Local)
	if err := store.Configure(calendarSources(cfg)); err != nil {
		log.Print(err)
	}
	store.RunReminders(func(r calendar.Reminder) {
		desktopnotify.Notify(i18n.T("dropdown-calendar-title"), r.Summary, reminderBody(r.Occurrence, calendarIs12h(current().Clock.Format.String())), "ld-calendar-symbolic")
	})
	return store
}

func reconfigureCalendar(store *calendar.Store, old, next config.ClockConfig) {
	if store == nil || sameCalendars(old.Calendars, next.Calendars) {
		return
	}
	if err := store.Configure(calendarSources(next)); err != nil {
		log.Print(err)
	}
}

func sameCalendars(a, b []config.CalendarSource) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Path != b[i].Path || a[i].Color.ToCSS() != b[i].Color.ToCSS() {
			return false
		}
	}
	return true
}

func clockText(t time.Time, use12h bool) string {
	if use12h {
		return strftimeOf("%-I:%M %p", t)
	}
	return strftimeOf("%H:%M", t)
}

func eventTimeText(o calendar.Occurrence, use12h bool) string {
	if o.AllDay {
		return i18n.T("dropdown-calendar-all-day")
	}
	if o.End.Equal(o.Start) {
		return clockText(o.Start, use12h)
	}
	return clockText(o.Start, use12h) + " – " + clockText(o.End, use12h)
}

func reminderBody(o calendar.Occurrence, use12h bool) string {
	body := eventTimeText(o, use12h)
	if o.Location != "" {
		body += "\n" + o.Location
	}
	return body
}

func nextEventText(store *calendar.Store, now time.Time, use12h bool, names strftime.Names) string {
	if store == nil {
		return ""
	}
	o, ok := store.Next(now)
	if !ok {
		return ""
	}
	when := clockText(o.Start, use12h)
	if y, m, d := o.Start.Date(); y != now.Year() || m != now.Month() || d != now.Day() {
		when = names.ShortWeekdays[o.Start.Weekday()] + " " + when
	}
	return when + " " + o.Summary
}

func clockLabel(cfg config.ClockConfig, store *calendar.Store, now time.Time, names strftime.Names) string {
	text := cfg.Format.Layout().FormatIn(now, names)
	if !strings.Contains(text, nextEventPlaceholder) {
		return text
	}
	return strings.ReplaceAll(text, nextEventPlaceholder, nextEventText(store, now, calendarIs12h(cfg.Format.String()), names))
}

type calendarAgenda struct {
	*widget.Box
	font render.Font
	px   float64
}

func newCalendarAgenda(font render.Font, px float64) *calendarAgenda {
	a := &calendarAgenda{Box: widget.NewBox(widget.Column, widget.Current().Spacing, 0), font: font, px: px}
	a.AddClass("cal-agenda")
	return a
}

func (a *calendarAgenda) show(events []calendar.Occurrence, use12h bool) {
	a.Clear()
	if len(events) == 0 {
		empty := widget.NewLabel(a.font, a.px, i18n.T("dropdown-calendar-no-events"), 0)
		empty.AddClass("cal-agenda-empty", css.DimLabel)
		a.Append(empty, false)
		return
	}
	th := widget.Current()
	for _, o := range events {
		row := widget.NewBox(widget.Row, th.Spacing, 0)
		row.AddClass("cal-event")
		stripe := widget.NewBox(widget.Row, 0, 0)
		stripe.AddClass("cal-event-color")
		stripe.SetInlineStyle("background-color: " + o.Color + ";")
		stripe.SetSizeRequest(int(styling.RemBase/4), 0)
		text := widget.NewBox(widget.Column, th.Spacing/4, 0)
		when := widget.NewLabel(a.font, a.px*styling.Caption.Ratio(), eventTimeText(o, use12h), 0)
		when.AddClass("cal-event-time", css.Caption, css.DimLabel, css.Numeric)
		summary := widget.NewLabel(a.font, a.px, o.Summary, 0)
		summary.AddClass("cal-event-summary")
		summary.SetWrap(true)
		text.Append(when, false)
		text.Append(summary, false)
		if o.Location != "" {
			place := widget.NewLabel(a.font, a.px*styling.Caption.Ratio(), o.Location, 0)
			place.AddClass("cal-event-location", css.Caption, css.DimLabel)
			place.SetEllipsize(widget.EllipsizeEnd)
			text.Append(place, false)
		}
		row.Append(stripe, false)
		row.Append(text, true)
		a.Append(row, false)
	}
}

func dayDetail(events []calendar.Occurrence) string {
	parts := make([]string, len(events))
	for i, o := range events {
		parts[i] = o.Summary
	}
	return strings.Join(parts, "\n")
}
