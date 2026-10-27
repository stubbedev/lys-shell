package config

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stubbedev/lys-shell/service/clipboard"
)

func TestClipboardHistoryBudgetLoads(t *testing.T) {
	if got := Defaults().Clipboard.HistoryMiB.Budget(); got != clipboard.DefaultBudget() {
		t.Errorf("default budget = %d MiB, want %d", got.MiB(), clipboard.DefaultBudgetMiB)
	}
	for _, mib := range []int64{clipboard.MinBudgetMiB, 256, clipboard.MaxBudgetMiB} {
		path := writeFile(t, t.TempDir(), "config.yaml", "clipboard:\n  history-mib: "+strconv.FormatInt(mib, 10)+"\n")
		cfg, err := LoadFile(path)
		if err != nil {
			t.Fatalf("%d MiB was refused: %v", mib, err)
		}
		if got := cfg.Clipboard.HistoryMiB.Budget(); got.MiB() != mib || got.Bytes() != int(mib)*clipboard.MiB {
			t.Errorf("budget = %d MiB, want %d", got.MiB(), mib)
		}
	}
}

func TestClipboardHistoryBudgetRefusesWhatCannotHoldAnEntry(t *testing.T) {
	for _, mib := range []int64{clipboard.MinBudgetMiB - 1, 0, -1, clipboard.MaxBudgetMiB + 1} {
		path := writeFile(t, t.TempDir(), "config.yaml", "clipboard:\n  history-mib: "+strconv.FormatInt(mib, 10)+"\n")
		cfg, err := LoadFile(path)
		want := "Error: clipboard history budget out of range: " + strconv.FormatInt(mib, 10) + " MiB, valid 8-1024 (at least the largest entry kept)"
		if err == nil || !strings.Contains(err.Error(), "Field: clipboard.history-mib") || !strings.Contains(err.Error(), want) {
			t.Errorf("%d MiB: err = %v, want the field's range diagnostic", mib, err)
		}
		if got := cfg.Clipboard.HistoryMiB; got != DefaultsClipboard().HistoryMiB {
			t.Errorf("%d MiB: kept %d, want the default", mib, got.Budget().MiB())
		}
	}
}

func TestClipboardHistoryBudgetIsABoundedSpin(t *testing.T) {
	m, ok := Field("clipboard.history-mib")
	if !ok || m.Kind != FieldInt || m.Min != clipboard.MinBudgetMiB || m.Max != clipboard.MaxBudgetMiB {
		t.Errorf("meta = %+v, want an int field in %d-%d", m, clipboard.MinBudgetMiB, clipboard.MaxBudgetMiB)
	}
}
