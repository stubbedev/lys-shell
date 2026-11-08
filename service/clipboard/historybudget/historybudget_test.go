package historybudget

import (
	"errors"
	"testing"
)

func TestBudgetRange(t *testing.T) {
	for _, mib := range []int64{MinBudgetMiB, DefaultBudgetMiB, MaxBudgetMiB} {
		b, err := NewBudget(mib)
		if err != nil || b.MiB() != mib || b.Bytes() != int(mib)*MiB {
			t.Errorf("%d MiB: %+v, %v", mib, b, err)
		}
	}
	if b := DefaultBudget(); b.MiB() != DefaultBudgetMiB {
		t.Errorf("default budget = %d MiB", b.MiB())
	}
	for _, mib := range []int64{MinBudgetMiB - 1, MaxBudgetMiB + 1, 0, -1} {
		if _, err := NewBudget(mib); !errors.Is(err, ErrBudgetRange) {
			t.Errorf("%d MiB: err = %v, want ErrBudgetRange", mib, err)
		}
	}
	if MinBudgetMiB*MiB != MaxEntryBytes {
		t.Errorf("the least budget, %d MiB, is not the largest entry, %d bytes", MinBudgetMiB, MaxEntryBytes)
	}
}
