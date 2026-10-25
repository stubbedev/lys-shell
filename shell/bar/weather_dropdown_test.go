package bar

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/weather"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

func TestWeatherSettingsMapsTheConfig(t *testing.T) {
	t.Setenv("LYS_SHELL_TEST_WEATHER_KEY", "s3cret")
	cfg := config.Defaults().Weather
	cfg.Location = "52.5,13.4"
	cfg.Provider = config.WeatherProviderWeatherApi
	ref := "$LYS_SHELL_TEST_WEATHER_KEY"
	cfg.WeatherapiKey = &ref
	cfg.RefreshIntervalSeconds = 90
	s := weatherSettings(cfg)
	if s.Provider != weather.WeatherAPI || !s.Location.Coordinates || s.Interval != 90*time.Second {
		t.Errorf("settings = %+v", s)
	}
	if s.WeatherAPIKey != "s3cret" {
		t.Errorf("weatherapi key = %q, want the resolved $LYS_SHELL_TEST_WEATHER_KEY", s.WeatherAPIKey)
	}
	if s.VisualCrossingKey != "" {
		t.Errorf("an unset key resolved to %q", s.VisualCrossingKey)
	}
	// An unset variable leaves the key empty (the provider then reports
	// the missing key) rather than sending "$NAME" as the key.
	missing := "$LYS_SHELL_TEST_WEATHER_UNSET"
	cfg.WeatherapiKey = &missing
	if got := weatherSettings(cfg).WeatherAPIKey; got != "" {
		t.Errorf("unset variable key = %q", got)
	}
}

func TestWeatherConditionIconsHaveDayAndNight(t *testing.T) {
	if weatherConditionIcon(weather.CondClear, true) != "ld-sun-symbolic" || weatherConditionIcon(weather.CondClear, false) != "ld-moon-symbolic" {
		t.Error("clear day/night icons")
	}
	if weatherConditionIcon(weather.CondRain, true) != weatherConditionIcon(weather.CondRain, false) {
		t.Error("rain has a single glyph")
	}
	if weatherConditionIcon(weather.CondUnknown, true) != "ld-cloud-symbolic" {
		t.Error("unknown condition icon")
	}
	if weatherConditionClass(weather.CondHail) != "stormy" || weatherConditionClass(weather.CondMist) != "cloudy" {
		t.Error("condition classes")
	}
}

func TestWeatherTimeLabels(t *testing.T) {
	at := time.Date(2026, 10, 1, 15, 0, 0, 0, time.Local)
	if got := weatherHourLabel(at, config.TimeFormat12h); got != "3PM" {
		t.Errorf("12h hour = %q", got)
	}
	if got := weatherHourLabel(at, config.TimeFormat24h); got != "15:00" {
		t.Errorf("24h hour = %q", got)
	}
	for _, tc := range []struct {
		at   weather.TimeOfDay
		want string
	}{
		{weather.TimeOfDay{Hour: 0, Minute: 5}, "12:05 AM"},
		{weather.TimeOfDay{Hour: 12, Minute: 0}, "12:00 PM"},
		{weather.TimeOfDay{Hour: 18, Minute: 41}, "6:41 PM"},
	} {
		if got := weatherSunTime(tc.at, config.TimeFormat12h); got != tc.want {
			t.Errorf("12h %v = %q, want %q", tc.at, got, tc.want)
		}
	}
	if got := weatherSunTime(weather.TimeOfDay{Hour: 6, Minute: 5}, config.TimeFormat24h); got != "06:05" {
		t.Errorf("24h sun = %q", got)
	}
}

func TestWeatherBarOffsets(t *testing.T) {
	if l, w := weatherBarOffsets(5, 10, 0, 20); l != 25 || w != 25 {
		t.Errorf("span = %v %v, want 25 25", l, w)
	}
	// A one-degree day still shows at least 5%.
	if _, w := weatherBarOffsets(10, 10.1, 0, 100); w != 5 {
		t.Errorf("min width = %v", w)
	}
	// A flat week fills the bar.
	if l, w := weatherBarOffsets(3, 3, 3, 3); l != 0 || w != 100 {
		t.Errorf("flat = %v %v", l, w)
	}
	if lo, hi := weatherTempRange(nil); lo != 0 || hi != 0 {
		t.Errorf("empty range = %v %v", lo, hi)
	}
	lo, hi := weatherTempRange([]weather.Daily{{TempLow: 2, TempHigh: 9}, {TempLow: -1, TempHigh: 7}})
	if lo != -1 || hi != 9 {
		t.Errorf("range = %v %v", lo, hi)
	}
}

func TestWeatherErrorTextNamesTheCause(t *testing.T) {
	if got := weatherErrorText(weather.Status{Kind: weather.Failed, Error: weather.ErrAPIKeyMissing, Provider: "weatherapi"}); got != i18n.T("dropdown-weather-error-api-key", i18n.Str("provider", "weatherapi")) {
		t.Errorf("api key = %q", got)
	}
	if got := weatherErrorText(weather.Status{Kind: weather.Failed, Error: weather.ErrLocationNotFound, Query: "Atlantis"}); got != i18n.T("dropdown-weather-error-location", i18n.Str("query", "Atlantis")) {
		t.Errorf("location = %q", got)
	}
	if got := weatherErrorText(weather.Status{Kind: weather.Failed}); got != i18n.T("dropdown-weather-error-unknown") {
		t.Errorf("other = %q", got)
	}
	if weatherPage(weather.Status{Kind: weather.Loading}) != "loading" || weatherPage(weather.Status{Kind: weather.Failed}) != "error" {
		t.Error("pages")
	}
	if got := weatherUpdatedAgo(time.Now().Add(time.Minute), time.Now()); got != i18n.T("dropdown-weather-updated-ago", i18n.Str("minutes", "0")) {
		t.Errorf("future update = %q, want 0 minutes", got)
	}
}

func TestDropdownDimsResolveTheOverrides(t *testing.T) {
	cfg := config.Defaults()
	cfg.Styling.Scale = 1
	if w, h, ok := dropdownDims("weather", cfg); !ok || w != 395 || h != 695 {
		t.Errorf("weather base = %d×%d %v", w, h, ok)
	}
	// A content-height dropdown has a natural height.
	if _, h, _ := dropdownDims("calendar", cfg); h != -1 {
		t.Errorf("calendar height = %d, want natural", h)
	}
	if _, _, ok := dropdownDims("power", cfg); ok {
		t.Error("the power menu has no panel geometry")
	}
	cfg.Styling.Scale = 2
	scale := config.Size{Value: 1.5}
	px := config.Size{Value: 480, Unit: config.SizePixels}
	cfg.Dropdowns.Weather = config.DropdownSize{Width: &scale, Height: &px}
	if w, h, _ := dropdownDims("weather", cfg); w != 1185 || h != 480 {
		t.Errorf("weather override = %d×%d, want 1185×480", w, h)
	}
	// A scale height on a content-height dropdown has nothing to scale.
	cfg.Dropdowns.Calendar = config.DropdownSize{Height: &scale}
	if w, h, _ := dropdownDims("calendar", cfg); w != 680 || h != -1 {
		t.Errorf("calendar = %d×%d, want 680×natural", w, h)
	}
	cfg.Dropdowns.Calendar = config.DropdownSize{Height: &px}
	if _, h, _ := dropdownDims("calendar", cfg); h != 480 {
		t.Errorf("calendar px height = %d", h)
	}
}

func fixedWeather(w *weather.Weather, err error) *weather.Service {
	return weather.NewWith(weather.Settings{Interval: time.Hour}, func(context.Context, weather.Settings, time.Time) (*weather.Weather, error) {
		return w, err
	})
}

func TestWeatherModuleFollowsTheService(t *testing.T) {
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	if _, err := newWeather(ctx); err == nil {
		t.Fatal("a weather module without the service was built")
	}
	svc := fixedWeather(&weather.Weather{Zone: time.Local, Current: weather.Current{Temperature: 20, Condition: weather.CondClear, IsDay: false}}, nil)
	defer svc.Close()
	ctx.Weather = svc
	ctx.gen = newMountGen()
	defer ctx.gen.retire()
	m, err := newWeather(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wm := m.(*weatherModule)
	waitForText(t, wm.label, "20°C")
	waitHeadless(t, "the night icon", func() bool { return wm.icon.Name() == "ld-moon-symbolic" })
}

func TestWeatherDropdownShowsTheStatusPage(t *testing.T) {
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	svc := fixedWeather(nil, &weather.Error{Kind: weather.ErrLocationNotFound, Query: "Atlantis"})
	defer svc.Close()
	ctx.Weather = svc
	var v *weatherView
	waitHeadless(t, "the failed status", func() bool { return svc.Status().Kind == weather.Failed })
	v = weatherDropdown(ctx).(*weatherView)
	if got := v.pages.Visible(); got != "error" {
		t.Errorf("page = %q, want error", got)
	}
	if got := v.errorText.Text(); got != i18n.T("dropdown-weather-error-location", i18n.Str("query", "Atlantis")) {
		t.Errorf("error text = %q", got)
	}

	ok := fixedWeather(&weather.Weather{
		Zone:    time.Local,
		Current: weather.Current{Temperature: 11, Condition: weather.CondRain},
		Hourly:  make([]weather.Hourly, 8),
		Daily:   []weather.Daily{{Date: time.Now(), TempLow: 4, TempHigh: 12}},
	}, nil)
	defer ok.Close()
	ctx.Weather = ok
	waitHeadless(t, "the loaded status", func() bool { return ok.Status().Kind == weather.Loaded })
	v = weatherDropdown(ctx).(*weatherView)
	if got := v.pages.Visible(); got != "loaded" {
		t.Errorf("page = %q, want loaded", got)
	}
	// Header, stats, hourly, daily, and sun times.
	if got := len(v.loaded.Children()); got != 5 {
		t.Errorf("loaded sections = %d, want 5", got)
	}
	if got := len(treetest.WithClass(v.loaded, "hourly-item")); got != weatherHourlyItems {
		t.Errorf("hourly items = %d, want %d of 8", got, weatherHourlyItems)
	}
}

// arrangeDropdown measures and lays a dropdown out at a fixed panel
// size over two passes — the first settles parenting, the second the
// rebuilt subtrees' bounds.
func arrangeDropdown(t *testing.T, v widget.Widget, w, h int) {
	t.Helper()
	for range 2 {
		sz := v.Measure(widget.Constraints{Max: widget.Size{W: w, H: h}})
		v.Arrange(render.Rect{W: max(sz.W, w), H: h})
	}
}

// classedWidget finds the first widget carrying class, failing the
// test when it is missing.
func classedWidget(t *testing.T, root widget.Widget, class string) widget.Widget {
	t.Helper()
	w := findByClass(root, class)
	if w == nil {
		t.Fatalf("no %s widget under the tree", class)
	}
	return w
}

// classedBox is classedWidget for boxes.
func classedBox(t *testing.T, root widget.Widget, class string) *widget.Box {
	t.Helper()
	w := classedWidget(t, root, class)
	b, ok := w.(*widget.Box)
	if !ok {
		t.Fatalf("the %s widget is %T, want a box", class, w)
	}
	return b
}

// abs is the integer distance helper the bounds assertions share.
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// The loading and error pages fill the stack and center their status
// page; the error page's child carries the reason and the retry.
func TestWeatherPagesCenterTheirContent(t *testing.T) {
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	// A fetch that never lands holds the loading page up.
	svc := weather.NewWith(weather.Settings{Interval: time.Hour}, func(ctx context.Context, _ weather.Settings, _ time.Time) (*weather.Weather, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	defer svc.Close()
	ctx.Weather = svc
	v := weatherDropdown(ctx).(*weatherView)
	if got := v.pages.Visible(); got != "loading" {
		t.Fatalf("page = %q, want loading", got)
	}
	arrangeDropdown(t, v, 400, 600)
	centered := func(class string) {
		t.Helper()
		pb, sb := boundsOf(classedWidget(t, v, class)), boundsOf(v.pages)
		if abs((pb.Y+pb.H/2)-(sb.Y+sb.H/2)) > 3 {
			t.Errorf("the %s status page centers at %d, the stack at %d", class, pb.Y+pb.H/2, sb.Y+sb.H/2)
		}
	}
	centered("loading-weather")

	fail := fixedWeather(nil, &weather.Error{Kind: weather.ErrLocationNotFound, Query: "Atlantis"})
	defer fail.Close()
	waitHeadless(t, "the failed status", func() bool { return fail.Status().Kind == weather.Failed })
	ctx.Weather = fail
	v = weatherDropdown(ctx).(*weatherView)
	arrangeDropdown(t, v, 400, 600)
	centered("error-weather")
	page := classedWidget(t, v, "error-weather")
	retry, isBtn := classedWidget(t, v, "weather-retry-btn").(*widget.Button)
	if !isBtn || !isUnder(retry, page) || !isUnder(v.errorText, page) {
		t.Error("the reason and the retry are not the error page's own")
	}
}

// Every refetch crossfades to the loading page — after a success too
// — and the loaded page keeps its sections at the top until the new
// data lands.
func TestWeatherRefetchShowsTheLoadingPage(t *testing.T) {
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	w := &weather.Weather{
		Zone:    time.Local,
		Current: weather.Current{Temperature: 11, Condition: weather.CondRain},
		Hourly:  make([]weather.Hourly, 8),
		Daily:   []weather.Daily{{Date: time.Now(), TempLow: 4, TempHigh: 12}},
	}
	var mu sync.Mutex
	gates := []chan struct{}{make(chan struct{}), make(chan struct{})}
	calls := 0
	svc := weather.NewWith(weather.Settings{Interval: time.Hour}, func(context.Context, weather.Settings, time.Time) (*weather.Weather, error) {
		mu.Lock()
		i := calls
		calls++
		var gate chan struct{}
		if i < len(gates) {
			gate = gates[i]
		}
		mu.Unlock()
		if gate != nil {
			<-gate
		}
		return w, nil
	})
	defer svc.Close()
	ctx.Weather = svc
	v := weatherDropdown(ctx).(*weatherView)
	if got := v.pages.Visible(); got != "loading" {
		t.Fatalf("page = %q, want loading before the first fetch lands", got)
	}
	close(gates[0])
	waitHeadless(t, "the loaded status", func() bool { return svc.Status().Kind == weather.Loaded })
	v.refresh()
	if got := v.pages.Visible(); got != "loaded" {
		t.Fatalf("page = %q, want loaded", got)
	}
	// Negative: the loaded sections stay top-anchored — centering is
	// the loading and error pages' job.
	arrangeDropdown(t, v, 400, 600)
	scrollTop := classedWidget(t, v, "weather-scroll").(*widget.Scroll).Bounds().Y
	headerTop := classedBox(t, v, "weather-header").Bounds().Y
	if abs(headerTop-scrollTop) > 3 {
		t.Errorf("the loaded header sits at y=%d, the scroll content at %d", headerTop, scrollTop)
	}
	// A refetch after a success shows the loading page again.
	svc.Refresh()
	waitHeadless(t, "the refetching status", func() bool { return svc.Status().Kind == weather.Loading })
	v.refresh()
	if got := v.pages.Visible(); got != "loading" {
		t.Errorf("a refetch after a success kept page %q, want loading", got)
	}
	close(gates[1])
	waitHeadless(t, "the reloaded status", func() bool { return svc.Status().Kind == weather.Loaded })
	v.refresh()
	if got := v.pages.Visible(); got != "loaded" {
		t.Errorf("page = %q after the refetch landed, want loaded", got)
	}
}

// The header's refresh carries its i18n tooltip, the current weather
// reads the temperature beside its unit over the condition, and a daily
// row is an Item whose actions are its span and its high and low.
func TestWeatherHeaderAndDailyRows(t *testing.T) {
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	svc := fixedWeather(&weather.Weather{
		Zone:      time.Local,
		Current:   weather.Current{Temperature: 11, Condition: weather.CondRain},
		Hourly:    make([]weather.Hourly, 8),
		Daily:     []weather.Daily{{Date: time.Now(), TempLow: 4, TempHigh: 12}},
		Astronomy: weather.Astronomy{Sunrise: weather.TimeOfDay{Hour: 6}, Sunset: weather.TimeOfDay{Hour: 18}},
	}, nil)
	defer svc.Close()
	waitHeadless(t, "the loaded status", func() bool { return svc.Status().Kind == weather.Loaded })
	ctx.Weather = svc
	v := weatherDropdown(ctx).(*weatherView)

	var refresh *widget.Button
	walkTree(v, func(w widget.Widget) bool {
		if b, isBtn := w.(*widget.Button); isBtn && b.HasClass(css.Icon) {
			refresh = b
		}
		return true
	})
	if refresh == nil || refresh.TooltipText() != i18n.T("dropdown-weather-refresh") {
		t.Fatal("no refresh button with its tooltip")
	}
	header := classedBox(t, v, "weather-header")
	if temp := classedWidget(t, header, "weather-temp").(*widget.Label); temp.Text() != "11" {
		t.Errorf("the temperature reads %q", temp.Text())
	}
	if len(treetest.WithClass(header, "weather-temp-unit")) != 1 || len(treetest.WithClass(header, "weather-condition")) != 1 {
		t.Error("the header lacks the unit or the condition")
	}
	rows := treetest.WithClass(v.loaded, "daily-item")
	if len(rows) != 1 {
		t.Fatalf("daily rows = %d, want 1", len(rows))
	}
	row, isItem := rows[0].(*widget.Item)
	if !isItem {
		t.Fatalf("the daily row is %T, want lys-ui's Item", rows[0])
	}
	for _, class := range []string{"daily-bar", "daily-high", "daily-low"} {
		if len(treetest.WithClass(row, class)) != 1 {
			t.Errorf("the daily row lacks its %s", class)
		}
	}
}

// The loaded trees: the stats and hourly rows are homogeneous with no
// gaps between their cells, the cells center their image and text as
// GTK's fill-width image and labels do, the header leaves its icon
// hidden, and the forecast temperatures carry no degree sign
// (format_temp_value).
func TestWeatherDropdownTreesMatchRust(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	ok := fixedWeather(&weather.Weather{
		Zone:    time.Local,
		Current: weather.Current{Temperature: 11, Condition: weather.CondRain},
		Hourly:  []weather.Hourly{{Temperature: 13}, {Temperature: 12}},
		Daily:   []weather.Daily{{Date: time.Now(), TempLow: 4, TempHigh: 12}},
	}, nil)
	defer ok.Close()
	ctx.Weather = ok
	waitHeadless(t, "the loaded status", func() bool { return ok.Status().Kind == weather.Loaded })
	v := weatherDropdown(ctx).(*weatherView)
	defer v.dropdownClosed()

	for _, class := range []string{"weather-stats", "hourly-forecast"} {
		row := classedBox(t, v.loaded, class)
		if !row.Homogeneous() || row.Spacing() != 0 {
			t.Errorf(".%s: homogeneous %v spacing %d, want homogeneous and 0", class, row.Homogeneous(), row.Spacing())
		}
	}
	for _, class := range []string{"weather-stat", "hourly-item"} {
		cell := classedBox(t, v.loaded, class)
		for _, l := range treetest.All[*widget.Label](cell) {
			if l.Alignment() != render.AlignCenter {
				t.Errorf(".%s label %q aligns %v, want centered", class, l.Text(), l.Alignment())
			}
		}
	}
	for _, class := range []string{"hourly-temp", "daily-high", "daily-low"} {
		if l := classedWidget(t, v.loaded, class).(*widget.Label); strings.Contains(l.Text(), "°") {
			t.Errorf(".%s = %q, want the bare number", class, l.Text())
		}
	}
	header := classedBox(t, v, "dropdown-header")
	if icons := treetest.WithClass(header, "dropdown-icon"); len(icons) != 1 || icons[0].(*widget.Icon).Visible() {
		t.Error("the weather header shows its icon; the Rust header leaves it hidden")
	}
}

// "Today" is the location's today: for a location whose date is not the
// viewer's, the day it is there carries the label and the viewer's own
// date does not.
func TestWeatherTodayIsTheLocations(t *testing.T) {
	now := time.Now()
	zone := time.FixedZone("UTC+14", 14*3600)
	if now.In(zone).YearDay() == now.YearDay() {
		zone = time.FixedZone("UTC-12", -12*3600)
	}
	there := now.In(zone)
	midnight := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, zone) }
	locToday := midnight(there)
	other := midnight(now.In(time.Local))
	w := &weather.Weather{Zone: zone, Daily: []weather.Daily{{Date: other}, {Date: locToday}}}
	if other.Equal(locToday) {
		w.Daily = w.Daily[1:]
	}
	v := weatherDropdown(newTestContext(t, config.Defaults())).(*weatherView)
	days := treetest.WithClass(v.daily(w, false), "daily-item")
	for i, d := range w.Daily {
		got := itemText(days[i], "title")
		isToday := d.Date.Equal(locToday)
		if isToday != (got == i18n.T("dropdown-weather-today")) {
			t.Errorf("day %v reads %q (location today %v)", d.Date, got, locToday)
		}
	}
}
