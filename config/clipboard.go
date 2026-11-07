package config

import (
	"reflect"

	"github.com/stubbedev/lys-shell/service/clipboard"
)

// ClipboardConfig is the clipboard section: the session's clipboard
// history, which the launcher's clipboard mode lists.
//
// Clipboard history settings.
type ClipboardConfig struct {
	// Most memory the history keeps, in MiB, across every entry that is
	// not pinned. The oldest entries are forgotten to make room for a
	// new one. The budget is never less than the largest single entry
	// kept.
	HistoryMiB ClipboardMiB `cfg:"history-mib"`
	// Most the pinned entries hold, in MiB. Pinned entries are listed
	// first, kept in `$XDG_DATA_HOME/lys-shell/clipboard-pinned/` across
	// restarts and never forgotten to make room; a pin past this budget
	// is refused.
	PinnedMiB ClipboardMiB `cfg:"pinned-mib"`
	// Keep the whole history across restarts, in
	// `$XDG_DATA_HOME/lys-shell/clipboard-history/`, rather than in
	// memory only. Turning it off removes what was kept. Selections a
	// password manager marks secret are never kept either way.
	Persist bool `cfg:"persist"`
}

// DefaultsClipboard returns the schema defaults.
func DefaultsClipboard() ClipboardConfig {
	return ClipboardConfig{
		HistoryMiB: ClipboardMiB{budget: clipboard.DefaultBudget()},
		PinnedMiB:  ClipboardMiB{budget: clipboard.DefaultPinnedBudget()},
	}
}

// ClipboardMiB is a clipboard byte budget, refused at load rather than
// clamped below the largest entry the history keeps or above the most
// it can count.
//
// Clipboard budget in MiB.
type ClipboardMiB struct{ budget clipboard.Budget }

// Budget is the validated budget.
func (m ClipboardMiB) Budget() clipboard.Budget { return m.budget }

// UnmarshalConfig implements Unmarshaler.
func (m *ClipboardMiB) UnmarshalConfig(v any) error {
	var mib int64
	if err := decodeInto(reflect.ValueOf(&mib).Elem(), v); err != nil {
		return err
	}
	budget, err := clipboard.NewBudget(mib)
	if err != nil {
		return err
	}
	m.budget = budget
	return nil
}

// MarshalConfig implements Marshaler.
func (m ClipboardMiB) MarshalConfig() any { return m.budget.MiB() }

func (ClipboardMiB) configSchema(*schemaGen) Schema {
	return Schema{
		"description": typeDoc(typeOf[ClipboardMiB]()),
		"type":        "integer",
		"format":      "uint32",
		"minimum":     int64(clipboard.MinBudgetMiB),
		"maximum":     int64(clipboard.MaxBudgetMiB),
	}
}
