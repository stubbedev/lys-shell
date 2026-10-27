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
	// Most memory the history keeps, in MiB, across every entry. The
	// oldest entries are forgotten to make room for a new one. The
	// budget is never less than the largest single entry kept.
	HistoryMiB ClipboardHistoryMiB `cfg:"history-mib"`
}

// DefaultsClipboard returns the schema defaults.
func DefaultsClipboard() ClipboardConfig {
	return ClipboardConfig{HistoryMiB: ClipboardHistoryMiB{budget: clipboard.DefaultBudget()}}
}

// ClipboardHistoryMiB is the history's byte budget, refused at load
// rather than clamped below the largest entry the history keeps or
// above the most it can count.
//
// Clipboard history budget in MiB.
type ClipboardHistoryMiB struct{ budget clipboard.Budget }

// Budget is the validated budget.
func (m ClipboardHistoryMiB) Budget() clipboard.Budget { return m.budget }

// UnmarshalConfig implements Unmarshaler.
func (m *ClipboardHistoryMiB) UnmarshalConfig(v any) error {
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
func (m ClipboardHistoryMiB) MarshalConfig() any { return m.budget.MiB() }

func (ClipboardHistoryMiB) configSchema(*schemaGen) Schema {
	return Schema{
		"description": typeDoc(typeOf[ClipboardHistoryMiB]()),
		"type":        "integer",
		"format":      "uint32",
		"minimum":     int64(clipboard.MinBudgetMiB),
		"maximum":     int64(clipboard.MaxBudgetMiB),
	}
}
