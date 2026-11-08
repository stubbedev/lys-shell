package config

import (
	"fmt"
	"reflect"
	"time"

	"github.com/stubbedev/lys-shell/service/notifications/notifypolicy"
)

// NotificationDndConfig is modules.notifications.dnd.
//
// Do-not-disturb rules: what still pops up while it is on, and what turns
// it on by itself. Notifications held back still reach the history.
type NotificationDndConfig struct {
	// Critical notifications (a low battery, a failed backup, an alarm)
	// pop up even in do-not-disturb.
	AllowCritical bool `cfg:"allow-critical"`
	// App-name or desktop-entry globs whose notifications pop up even in
	// do-not-disturb. Supports `*` and `?`.
	//
	// Examples: `["org.gnome.Calendar", "*alarm*"]`
	AllowApps []string `cfg:"allow-apps"`
	// Windows of the week do-not-disturb turns itself on in, in the local
	// time zone. A window whose `to` is not after its `from` runs past
	// midnight into the next day. Toggling do-not-disturb by hand inside a
	// window overrides it until the window ends.
	//
	// ```yaml
	// schedule:
	//   - {days: [mon, tue, wed, thu, fri], from: "22:00", to: "07:00"}
	// ```
	Schedule []DndWindow `cfg:"schedule"`
	// Turn do-not-disturb on while the screen is shared or recorded.
	WhileSharing bool `cfg:"while-sharing"`
	// Turn do-not-disturb on while a window is fullscreen.
	WhileFullscreen bool `cfg:"while-fullscreen"`
}

// DefaultsNotificationDnd returns the schema defaults.
func DefaultsNotificationDnd() NotificationDndConfig {
	return NotificationDndConfig{AllowCritical: true, AllowApps: []string{}, Schedule: []DndWindow{}, WhileSharing: true}
}

// Rules is the section as the notification service applies it.
func (c NotificationDndConfig) Rules() notifypolicy.FocusRules {
	schedule := make([]notifypolicy.Window, len(c.Schedule))
	for i, w := range c.Schedule {
		schedule[i] = w.window
	}
	return notifypolicy.FocusRules{
		AllowCritical:   c.AllowCritical,
		AllowApps:       c.AllowApps,
		Schedule:        schedule,
		WhileSharing:    c.WhileSharing,
		WhileFullscreen: c.WhileFullscreen,
	}
}

// Weekday is a day of a do-not-disturb window.
//
// Day of the week.
type Weekday string

// Weekday values.
const (
	// Monday.
	WeekdayMon Weekday = "mon"
	// Tuesday.
	WeekdayTue Weekday = "tue"
	// Wednesday.
	WeekdayWed Weekday = "wed"
	// Thursday.
	WeekdayThu Weekday = "thu"
	// Friday.
	WeekdayFri Weekday = "fri"
	// Saturday.
	WeekdaySat Weekday = "sat"
	// Sunday.
	WeekdaySun Weekday = "sun"
)

var weekdays = []Weekday{WeekdayMon, WeekdayTue, WeekdayWed, WeekdayThu, WeekdayFri, WeekdaySat, WeekdaySun}

var _ = registerEnum(weekdays...)

func (d Weekday) weekday() time.Weekday {
	for i, w := range weekdays {
		if w == d {
			return time.Weekday((i + 1) % 7)
		}
	}
	panic(fmt.Sprintf("config: %q is not a registered weekday", string(d)))
}

// DndWindow is one scheduled do-not-disturb window, refused at load when
// it starts when it ends or a time is not HH:MM.
//
// A stretch of time on some days of the week: `days` (every day when
// left out), `from` and `to` as `HH:MM`.
type DndWindow struct{ window notifypolicy.Window }

// dndWindowFields is a DndWindow as written.
type dndWindowFields struct {
	// Days the window starts on; every day when left out.
	Days []Weekday `cfg:"days"`
	// When the window starts, as HH:MM.
	From string `cfg:"from,required"`
	// When the window ends, as HH:MM: the next day when not after `from`.
	To string `cfg:"to,required"`
}

func parseDndWindow(f dndWindowFields) (DndWindow, error) {
	from, err := notifypolicy.ParseClockTime(f.From)
	if err != nil {
		return DndWindow{}, fmt.Errorf("from: %w", err)
	}
	to, err := notifypolicy.ParseClockTime(f.To)
	if err != nil {
		return DndWindow{}, fmt.Errorf("to: %w", err)
	}
	days := f.Days
	if len(days) == 0 {
		days = weekdays
	}
	var set notifypolicy.DaySet
	for _, d := range days {
		set = set.With(d.weekday())
	}
	w, err := notifypolicy.NewWindow(set, from, to)
	if err != nil {
		return DndWindow{}, err
	}
	return DndWindow{window: w}, nil
}

// UnmarshalConfig implements Unmarshaler.
func (w *DndWindow) UnmarshalConfig(v any) error {
	var f dndWindowFields
	if err := decodeValueStruct(reflect.ValueOf(&f).Elem(), v); err != nil {
		return err
	}
	parsed, err := parseDndWindow(f)
	if err != nil {
		return err
	}
	*w = parsed
	return nil
}

// MarshalConfig implements Marshaler.
func (w DndWindow) MarshalConfig() any {
	days := make([]any, 0, len(weekdays))
	for _, d := range weekdays {
		if w.window.Days().Has(d.weekday()) {
			days = append(days, string(d))
		}
	}
	return map[string]any{"days": days, "from": w.window.From().String(), "to": w.window.To().String()}
}

func (DndWindow) configSchema(g *schemaGen) Schema {
	s := g.structSchema(typeOf[dndWindowFields]())
	s["description"] = typeDoc(typeOf[DndWindow]())
	return s
}
