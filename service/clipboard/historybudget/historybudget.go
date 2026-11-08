package historybudget

import (
	"errors"
	"fmt"
)

// MiB is a mebibyte, the unit the history's byte budget is set in.
const MiB = 1 << 20

// MaxEntryBytes is the largest selection worth remembering. The
// history lives in memory for the session, and a copied image or a
// large file list is unbounded. Past this the entry is refused rather
// than truncated: half a payload pasted back is worse than not
// offering it.
const MaxEntryBytes = 8 * MiB

// The history's total byte budget, in MiB. The least is the largest
// entry the history keeps, so every entry it accepts fits; the most
// keeps the budget in an int on every platform.
const (
	MinBudgetMiB           = MaxEntryBytes / MiB
	DefaultBudgetMiB       = 64
	DefaultPinnedBudgetMiB = 16
	MaxBudgetMiB           = 1024
)

// ErrBudgetRange reports a byte budget outside MinBudgetMiB-MaxBudgetMiB.
var ErrBudgetRange = errors.New("clipboard budget out of range")

// Budget is the most the history holds in all, validated: never less
// than one entry of MaxEntryBytes.
type Budget struct{ mib uint32 }

// NewBudget validates a budget of mib mebibytes.
func NewBudget(mib int64) (Budget, error) {
	if mib < MinBudgetMiB || mib > MaxBudgetMiB {
		return Budget{}, fmt.Errorf("%w: %d MiB, valid %d-%d (at least the largest entry kept)", ErrBudgetRange, mib, MinBudgetMiB, MaxBudgetMiB)
	}
	return Budget{mib: uint32(mib)}, nil
}

// DefaultBudget is DefaultBudgetMiB.
func DefaultBudget() Budget { return Budget{mib: DefaultBudgetMiB} }

// DefaultPinnedBudget is DefaultPinnedBudgetMiB.
func DefaultPinnedBudget() Budget { return Budget{mib: DefaultPinnedBudgetMiB} }

// MiB is the budget in mebibytes.
func (b Budget) MiB() int64 { return int64(b.mib) }

// Bytes is the budget in bytes.
func (b Budget) Bytes() int { return int(b.mib) * MiB }
