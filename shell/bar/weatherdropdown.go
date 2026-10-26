package bar

import (
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/weather"
	"github.com/stubbedev/lys-shell/shell/widgets"
	"github.com/stubbedev/lys-shell/styling"
)

// The dropdown's list lengths and the temperature bar track
// (hourly_forecast MAX_ITEMS, daily_forecast MAX_DAYS, BAR_WIDTH_REM).
const (
	weatherHourlyItems  = 5
	weatherDailyDays    = 5
	weatherBarWidthRem  = 4.0
	weatherUpdatedEvery = time.Minute
)

// weatherHourLabel is hourly_time_label: "3PM" or "15:00".
func weatherHourLabel(t time.Time, format config.TimeFormat) string {
	if format == config.TimeFormat24h {
		return fmt.Sprintf("%02d:%02d", t.Hour(), t.Minute())
	}
	return t.Format("3PM")
}

// weatherSunTime is sun_times format_time: "6:05 AM" or "06:05".
func weatherSunTime(t weather.TimeOfDay, format config.TimeFormat) string {
	if format == config.TimeFormat24h {
		return t.String()
	}
	period := "AM"
	if t.Hour >= 12 {
		period = "PM"
	}
	hour := t.Hour % 12
	if hour == 0 {
		hour = 12
	}
	return fmt.Sprintf("%d:%02d %s", hour, t.Minute, period)
}

// weatherDayLabel is day_label: the weekday's abbreviation.
func weatherDayLabel(date time.Time) string {
	ids := [...]string{
		"dropdown-weather-day-sun", "dropdown-weather-day-mon", "dropdown-weather-day-tue",
		"dropdown-weather-day-wed", "dropdown-weather-day-thu", "dropdown-weather-day-fri",
		"dropdown-weather-day-sat",
	}
	return i18n.T(ids[date.Weekday()])
}

// weatherLocationDisplay is location_display: "city, region", else
// "city, country".
func weatherLocationDisplay(l weather.Location) string {
	if l.Region != "" {
		return l.City + ", " + l.Region
	}
	return l.City + ", " + l.Country
}

// weatherUpdatedAgo is the header's "Updated Nm ago" (never negative).
func weatherUpdatedAgo(updated, now time.Time) string {
	minutes := max(int64(now.Sub(updated)/time.Minute), 0)
	return i18n.T("dropdown-weather-updated-ago", i18n.Str("minutes", strconv.FormatInt(minutes, 10)))
}

// weatherTempRange is temp_range: the coldest low and warmest high,
// (0, 0) without days.
func weatherTempRange(days []weather.Daily) (lo, hi float32) {
	if len(days) == 0 {
		return 0, 0
	}
	lo, hi = math.MaxFloat32, -math.MaxFloat32
	for _, d := range days {
		lo = min(lo, d.TempLow.Celsius())
		hi = max(hi, d.TempHigh.Celsius())
	}
	return lo, hi
}

// weatherBarOffsets is temp_bar_offsets: where a day's low-to-high
// span sits on the week's range, in percent, at least 5% wide; a flat
// range fills the bar.
func weatherBarOffsets(low, high, rangeMin, rangeMax float32) (leftPct, widthPct float32) {
	span := rangeMax - rangeMin
	if span <= 0 {
		return 0, 100
	}
	clamp := func(v float32) float32 { return max(0, min(100, v)) }
	left := clamp((low - rangeMin) / span * 100)
	right := clamp((high - rangeMin) / span * 100)
	return left, max(right-left, 5)
}

// weatherErrorText is error_description.
func weatherErrorText(st weather.Status) string {
	switch st.Error {
	case weather.ErrAPIKeyMissing:
		return i18n.T("dropdown-weather-error-api-key", i18n.Str("provider", st.Provider))
	case weather.ErrLocationNotFound:
		return i18n.T("dropdown-weather-error-location", i18n.Str("query", st.Query))
	case weather.ErrNetwork:
		return i18n.T("dropdown-weather-error-network")
	case weather.ErrRateLimited:
		return i18n.T("dropdown-weather-error-rate-limit")
	}
	return i18n.T("dropdown-weather-error-unknown")
}

// weatherPage is the page a status shows.
func weatherPage(st weather.Status) string {
	switch st.Kind {
	case weather.Loaded:
		return "loaded"
	case weather.Failed:
		return "error"
	}
	return "loading"
}

// weatherView is the weather dropdown (dropdowns/weather): a loading,
// error, or loaded page following the service while open.
type weatherView struct {
	ctx  ModuleContext
	svc  *weather.Service
	font render.Font
	px   float64

	*widget.Box
	pages     *widget.Stack
	errorText *widget.Label
	loaded    *widget.Box

	once sync.Once
	stop chan struct{}
}

func weatherDropdown(ctx ModuleContext) widget.Widget {
	font, px := dropdownFont(ctx)
	v := &weatherView{ctx: ctx, svc: ctx.Weather, font: font, px: px, stop: make(chan struct{})}
	v.Box = widget.NewBox(widget.Column, 0, 0)
	v.AddClass("dropdown", "weather-dropdown")
	v.Append(v.header(), false)
	v.pages = pageStack()
	v.pages.SetTransition(widget.StackCrossfade, gtkStackDuration)
	v.pages.Add("loading", v.loadingPage())
	v.pages.Add("error", v.errorPage())
	v.loaded = widget.NewBox(widget.Column, 2*widget.Current().Spacing, 0)
	// The loaded child is vexpand inside the scroll (weather/mod.rs):
	// the sections fill the viewport, top-anchored.
	scroll := dropdownScroll(v.loaded, "weather-scroll")
	scroll.FillY = true
	// propagate_natural_height (weather/mod.rs:119): the panel grows to
	// the content's height instead of scrolling at the base height.
	scroll.PropagateNaturalHeight = true
	v.pages.Add("loaded", scroll)
	content := dropdownContent()
	content.Append(v.pages, true)
	v.Append(content, true)
	v.refresh()
	v.follow()
	return v
}

// icon sizes a theme icon and carries the rule's classes; its ink
// follows the cascade color, and the stylesheet's -gtk-icon-size
// overrides the constructor size.
func (v *weatherView) icon(name string, scale float64, classes ...string) *widget.Icon {
	icon := widget.NewThemeIcon(name, int(math.Round(v.px*scale)))
	icon.AddClass(classes...)
	return icon
}

// label paints with the constructor ink unset (0): the classed rule
// the class selects supplies the color.
func (v *weatherView) label(text string, scale float64, class string) *widget.Label {
	l := widget.NewLabel(v.font, v.px*scale, text, 0)
	l.AddClass(class)
	return l
}

// header is the DropdownHeader: the title and the refresh action. It is
// the one header that leaves the template's icon hidden (weather/mod.rs
// never sets it visible).
func (v *weatherView) header() widget.Widget {
	refresh := widget.NewButton(v.icon("ld-refresh-cw-symbolic", 1), 0, 0)
	refresh.AddClass(css.Ghost, css.Icon)
	refresh.OnClick = v.svc.Refresh
	refresh.SetTooltip(i18n.T("dropdown-weather-refresh"))
	row, glyph := dropdownHeaderIcon(v.font, "ld-sun-symbolic", i18n.T("dropdown-weather-title"), refresh)
	glyph.SetVisible(false)
	return row
}

// vcentered fills a column page's height and centers its kids as a
// group — a filling stack page's valign Center, with each kid at the
// natural size the page's halign Fill renders centered.
func vcentered(col *widget.Box, kids ...widget.Widget) *widget.Box {
	col.Append(widget.NewBox(widget.Column, 0, 0), true)
	for _, k := range kids {
		col.AppendAligned(k, false, widget.AlignCenter)
	}
	col.Append(widget.NewBox(widget.Column, 0, 0), true)
	return col
}

// loadingPage is the status page the fetch shows.
func (v *weatherView) loadingPage() widget.Widget {
	page := widgets.EmptyState(v.font, v.px, "ld-sun-symbolic", i18n.T("dropdown-weather-loading"), "")
	page.AddClass("loading-weather")
	return vcentered(widget.NewBox(widget.Column, 0, 0), page)
}

// errorPage is the failure's status page: its reason and a retry as
// the page's child.
func (v *weatherView) errorPage() widget.Widget {
	page := widgets.EmptyState(v.font, v.px, "ld-info-symbolic", i18n.T("dropdown-weather-error-title"), "")
	page.AddClass("error-weather")
	v.errorText = v.label("", 1, "error-text")
	v.errorText.AddClass(css.DimLabel)
	v.errorText.SetAlignment(render.AlignCenter)
	v.errorText.SetWrap(true)
	retry := dropdownButton(widget.NewLabel(v.font, v.px, i18n.T("dropdown-weather-retry"), 0), v.svc.Refresh, "weather-retry-btn", css.Outline)
	child := widget.NewBox(widget.Column, widget.Current().Spacing, 0)
	child.Append(v.errorText, false)
	child.AppendAligned(retry, false, widget.AlignCenter)
	page.SetChild(child)
	return vcentered(widget.NewBox(widget.Column, 0, 0), page)
}

// refresh shows the status's page and rebuilds the loaded sections
// from the latest data (every section's refresh at once). Every
// refetch crossfades to the loading page, after a success too; the
// loaded page keeps its last render until the new data lands.
func (v *weatherView) refresh() {
	if v.svc == nil {
		// No service: nothing to read (the Rust factory never builds the
		// dropdown without one; the builders sweep must not panic).
		return
	}
	st := v.svc.Status()
	w := v.svc.Weather()
	v.errorText.SetText(weatherErrorText(st))
	v.pages.Show(weatherPage(st))
	if w == nil {
		return
	}
	cfg := v.ctx.Config.Weather
	imperial := cfg.Units == config.TemperatureUnitImperial
	v.loaded.Clear()
	v.loaded.Append(v.currentHeader(w, imperial), false)
	v.loaded.Append(v.statsGrid(w, imperial), false)
	v.loaded.Append(v.hourly(w, imperial, cfg.TimeFormat), false)
	v.loaded.Append(v.daily(w, imperial), false)
	v.loaded.Append(v.sunTimes(w, cfg.TimeFormat), false)
}

// condIcon is a condition's glyph at scale times the text size,
// colored by its condition class.
func (v *weatherView) condIcon(c weather.Condition, isDay bool, scale float64) *widget.Icon {
	return v.icon(weatherConditionIcon(c, isDay), scale, "weather-cond", weatherConditionClass(c))
}

// currentHeader is weather_header: the condition icon, the
// temperature in the display size over the condition, and the place
// and the data's age on the right.
func (v *weatherView) currentHeader(w *weather.Weather, imperial bool) widget.Widget {
	th := widget.Current()
	c := w.Current
	row := widget.NewBox(widget.Row, 2*th.Spacing, 0)
	row.AddClass("weather-header")
	row.AppendAligned(v.condIcon(c.Condition, c.IsDay, 3), false, widget.AlignCenter)
	temps := widget.NewBox(widget.Column, 0, 0)
	temp := widget.NewBox(widget.Row, th.Spacing/4, 0)
	temp.AppendAligned(v.label(weatherTemp(c.Temperature, imperial), 2.4, "weather-temp"), false, widget.AlignBaseline)
	unit := v.label(weatherUnitSymbol(imperial), 1, "weather-temp-unit")
	unit.AddClass(css.DimLabel)
	temp.AppendAligned(unit, false, widget.AlignBaseline)
	temps.Append(temp, false)
	cond := v.label(weatherConditionLabel(c.Condition), 1, "weather-condition")
	cond.AddClass(css.DimLabel)
	temps.Append(cond, false)
	row.AppendAligned(temps, false, widget.AlignCenter)
	// The place takes the free width; its right-aligned labels sit
	// mid-height.
	place := widget.NewBox(widget.Column, 0, 0)
	place.AddClass("weather-location")
	city := v.label(weatherLocationDisplay(w.Location), 1, "weather-city")
	city.AddClass(css.Heading)
	city.SetEllipsize(widget.EllipsizeEnd)
	city.SetAlignment(render.AlignEnd)
	place.Append(city, false)
	ago := v.label(weatherUpdatedAgo(w.UpdatedAt, time.Now()), 1, "weather-updated")
	ago.AddClass(css.Caption, css.DimLabel)
	ago.SetAlignment(render.AlignEnd)
	place.Append(ago, false)
	row.AppendAligned(place, true, widget.AlignCenter)
	return row
}

// tileRow is a card around a homogeneous row (carrying class) of equal
// cells: the stats and the hourly forecast.
func tileRow(class string, cells ...widget.Widget) widget.Widget {
	row := widget.NewBox(widget.Row, 0, widget.Current().Padding)
	row.AddClass(class)
	row.SetHomogeneous(true)
	for _, c := range cells {
		row.Append(c, true)
	}
	card := widget.NewBox(widget.Column, 0, 0)
	card.AddClass(css.Card)
	card.Append(row, false)
	return card
}

// tile is one tileRow cell: its parts stacked the theme's half
// spacing apart and centered, the labels centering their text.
func tile(class string, parts ...widget.Widget) *widget.Box {
	th := widget.Current()
	cell := widget.NewBox(widget.Column, th.Spacing/2, th.Padding)
	cell.AddClass(class)
	for _, p := range parts {
		if l, ok := p.(*widget.Label); ok {
			l.SetAlignment(render.AlignCenter)
		}
		cell.AppendAligned(p, false, widget.AlignCenter)
	}
	return cell
}

// statsGrid is stats_grid: humidity, wind, UV, and today's rain chance.
func (v *weatherView) statsGrid(w *weather.Weather, imperial bool) widget.Widget {
	rain := "--"
	if len(w.Daily) > 0 {
		rain = strconv.Itoa(int(w.Daily[0].RainChance)) + "%"
	}
	stats := []struct {
		icon, value, label, class string
	}{
		{"ld-droplets-symbolic", strconv.Itoa(int(w.Current.Humidity)) + "%", "dropdown-weather-humidity", "humidity"},
		{"ld-wind-symbolic", weatherSpeed(w.Current.WindSpeed, imperial), "dropdown-weather-wind", "wind"},
		{"ld-sun-symbolic", strconv.Itoa(int(w.Current.UVIndex)), "dropdown-weather-uv", "uv"},
		{"ld-cloud-rain-symbolic", rain, "dropdown-weather-rain", "rain"},
	}
	cells := make([]widget.Widget, 0, len(stats))
	for _, s := range stats {
		value := v.label(s.value, 1, "weather-stat-value")
		value.AddClass(css.Heading)
		name := v.label(i18n.T(s.label), 1, "weather-stat-label")
		name.AddClass(css.Caption, css.DimLabel)
		cells = append(cells, tile("weather-stat", v.icon(s.icon, 1.3, "weather-stat-icon", s.class), value, name))
	}
	return tileRow("weather-stats", cells...)
}

// hourly is hourly_forecast: "Now" then the next four hours.
func (v *weatherView) hourly(w *weather.Weather, imperial bool, format config.TimeFormat) widget.Widget {
	var cells []widget.Widget
	for i, h := range w.Hourly[:min(len(w.Hourly), weatherHourlyItems)] {
		when := i18n.T("dropdown-weather-now")
		if i > 0 {
			when = weatherHourLabel(h.Time, format)
		}
		at := v.label(when, 1, "hourly-time")
		at.AddClass(css.Caption, css.DimLabel)
		temp := v.label(weatherTemp(h.Temperature, imperial), 1, "hourly-temp")
		temp.AddClass(css.Heading)
		cells = append(cells, tile("hourly-item", at, v.condIcon(h.Condition, h.IsDay, 1.4), temp))
	}
	return dropdownSection(sectionLabel(v.font, i18n.T("dropdown-weather-hourly")), tileRow("hourly-forecast", cells...))
}

// daily is daily_forecast: five days in an item card, each day an Item
// (its condition icon, the day over the condition) with its span on
// the week's temperature range and its high and low. The span is a
// .daily-bar track with a .daily-bar-fill the stylesheet paints; the
// fill's offset and width are per-day geometry, the Rust
// set_margin_start/set_width_request pair.
func (v *weatherView) daily(w *weather.Weather, imperial bool) widget.Widget {
	card, list := itemCard()
	card.AddClass("daily-forecast")
	days := w.Daily[:min(len(w.Daily), weatherDailyDays)]
	lo, hi := weatherTempRange(days)
	barPx := int(math.Round(weatherBarWidthRem * float64(v.ctx.Config.Styling.Scale) * styling.RemBase))
	today := time.Now().In(w.Zone)
	for _, d := range days {
		isToday := d.Date.Year() == today.Year() && d.Date.YearDay() == today.YearDay()
		name := weatherDayLabel(d.Date)
		if isToday {
			name = i18n.T("dropdown-weather-today")
		}
		row := widget.NewItem(v.font, name, weatherConditionLabel(d.Condition))
		row.SetSmall(true)
		row.SetEllipsize(true)
		row.AddClass("daily-item")
		setClass(row, "today", isToday)
		row.SetMedia(v.condIcon(d.Condition, true, 1.2), true)
		left, width := weatherBarOffsets(d.TempLow.Celsius(), d.TempHigh.Celsius(), lo, hi)
		leftPx := int(left / 100 * float32(barPx))
		fillPx := min(int(max(width/100*float32(barPx), 3)), barPx-leftPx)
		bar := widget.NewBox(widget.Row, 0, 0)
		bar.AddClass("daily-bar")
		bar.SetSizeRequest(barPx, weatherBarPx)
		fill := widget.NewBox(widget.Row, 0, 0)
		fill.AddClass("daily-bar-fill")
		fill.SetMargin(render.Insets{Left: leftPx})
		fill.SetSizeRequest(fillPx, weatherBarPx)
		bar.Append(fill, false)
		row.AddAction(bar)
		high := v.label(weatherTemp(d.TempHigh, imperial), 1, "daily-high")
		high.AddClass(css.Heading, css.Numeric)
		low := v.label(weatherTemp(d.TempLow, imperial), 1, "daily-low")
		low.AddClass(css.DimLabel, css.Numeric)
		row.AddAction(high)
		row.AddAction(low)
		list.Append(row)
	}
	return dropdownSection(sectionLabel(v.font, i18n.T("dropdown-weather-daily")), card)
}

// weatherBarPx is the temperature span bar's thickness.
const weatherBarPx = 4

// sunTimes is sun_times: today's sunrise and sunset, two tiles.
func (v *weatherView) sunTimes(w *weather.Weather, format config.TimeFormat) widget.Widget {
	var cells []widget.Widget
	for _, s := range []struct {
		icon, label, class string
		at                 weather.TimeOfDay
	}{
		{"ld-sunrise-symbolic", "dropdown-weather-sunrise", "sunrise", w.Astronomy.Sunrise},
		{"ld-sunset-symbolic", "dropdown-weather-sunset", "sunset", w.Astronomy.Sunset},
	} {
		at := v.label(weatherSunTime(s.at, format), 1, "sun-value")
		at.AddClass(css.Heading)
		name := v.label(i18n.T(s.label), 1, "sun-label")
		name.AddClass(css.Caption, css.DimLabel)
		cells = append(cells, tile("sun-time", v.icon(s.icon, 1.4, "sun-icon", s.class), at, name))
	}
	return tileRow("sun-times", cells...)
}

// follow keeps the view current while open: service changes rebuild
// it, and the data age re-renders every minute (UPDATED_AGO_INTERVAL).
func (v *weatherView) follow() {
	if v.ctx.App == nil {
		return
	}
	changes, unsubscribe := v.svc.Subscribe()
	go func() {
		defer unsubscribe()
		ticker := time.NewTicker(weatherUpdatedEvery)
		defer ticker.Stop()
		for {
			select {
			case <-v.stop:
				return
			case <-changes:
			case <-ticker.C:
			}
			v.ctx.Invoke(v.refresh)
		}
	}()
}

// dropdownClosed implements dropdownCloser.
func (v *weatherView) dropdownClosed() { v.once.Do(func() { close(v.stop) }) }
