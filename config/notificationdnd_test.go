package config

import (
	"errors"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/service/notifications"
)

func TestDndWindowsParseAndRoundTrip(t *testing.T) {
	var w DndWindow
	if err := w.UnmarshalConfig(map[string]any{"days": []any{"mon", "fri"}, "from": "22:00", "to": "07:00"}); err != nil {
		t.Fatal(err)
	}
	days := w.window.Days()
	if !days.Has(time.Monday) || !days.Has(time.Friday) || days.Has(time.Sunday) {
		t.Errorf("days = %b", days)
	}
	got, _ := w.MarshalConfig().(map[string]any)
	if got["from"] != "22:00" || got["to"] != "07:00" || len(got["days"].([]any)) != 2 {
		t.Errorf("marshal = %v", got)
	}
	var every DndWindow
	if err := every.UnmarshalConfig(map[string]any{"from": "12:00", "to": "13:00"}); err != nil {
		t.Fatal(err)
	}
	for d := time.Sunday; d <= time.Saturday; d++ {
		if !every.window.Days().Has(d) {
			t.Errorf("a window without days misses %s", d)
		}
	}
}

func TestDndWindowsRefuseWhatCannotBeScheduled(t *testing.T) {
	for _, raw := range []map[string]any{
		{"from": "07:00", "to": "07:00"},
		{"from": "25:00", "to": "07:00"},
		{"from": "22:00"},
		{"days": []any{"someday"}, "from": "22:00", "to": "07:00"},
	} {
		var w DndWindow
		if err := w.UnmarshalConfig(raw); err == nil {
			t.Errorf("%v accepted", raw)
		}
	}
	var w DndWindow
	if err := w.UnmarshalConfig(map[string]any{"from": "07:00", "to": "07:00"}); !errors.Is(err, notifications.ErrEmptyWindow) {
		t.Errorf("empty window: %v", err)
	}
}

func TestDndDefaultsLetCriticalThroughAndFollowSharing(t *testing.T) {
	r := DefaultsNotification().Dnd.Rules()
	if !r.AllowCritical || !r.WhileSharing || r.WhileFullscreen || len(r.Schedule) != 0 {
		t.Errorf("default rules = %+v", r)
	}
}
