package notifications

import (
	"cmp"
	"errors"
	"fmt"
)

// History bounds: the notifications kept in memory, and on disk. The
// least kept on disk is the most kept in memory, so the in-memory
// window is always the newest part of what the store holds.
const (
	MinHistoryMemory     = 10
	MaxHistoryMemory     = 1000
	DefaultHistoryMemory = 200
	MinHistoryStored     = MaxHistoryMemory
	MaxHistoryStored     = 1_000_000
	DefaultHistoryStored = 10_000
)

// MaxPage is the most notifications one page returns.
const MaxPage = 500

// ErrHistoryLimit is a history bound outside its range.
var ErrHistoryLimit = errors.New("notification history limit out of range")

// MemoryLimit is how many of the newest notifications stay in memory,
// validated to [MinHistoryMemory, MaxHistoryMemory].
type MemoryLimit struct{ n int }

// StoredLimit is how many notifications the store keeps, validated to
// [MinHistoryStored, MaxHistoryStored].
type StoredLimit struct{ n int }

// NewMemoryLimit validates an in-memory bound.
func NewMemoryLimit(n int64) (MemoryLimit, error) {
	if err := checkLimit("in memory", n, MinHistoryMemory, MaxHistoryMemory); err != nil {
		return MemoryLimit{}, err
	}
	return MemoryLimit{n: int(n)}, nil
}

// NewStoredLimit validates an on-disk bound.
func NewStoredLimit(n int64) (StoredLimit, error) {
	if err := checkLimit("stored", n, MinHistoryStored, MaxHistoryStored); err != nil {
		return StoredLimit{}, err
	}
	return StoredLimit{n: int(n)}, nil
}

// DefaultMemoryLimit is DefaultHistoryMemory.
func DefaultMemoryLimit() MemoryLimit { return MemoryLimit{n: DefaultHistoryMemory} }

// DefaultStoredLimit is DefaultHistoryStored.
func DefaultStoredLimit() StoredLimit { return StoredLimit{n: DefaultHistoryStored} }

// Count is the bound.
func (l MemoryLimit) Count() int { return l.n }

// Count is the bound.
func (l StoredLimit) Count() int { return l.n }

func checkLimit(what string, n, lo, hi int64) error {
	if n < lo || n > hi {
		return fmt.Errorf("%w: %d notifications %s, valid %d-%d", ErrHistoryLimit, n, what, lo, hi)
	}
	return nil
}

// Key orders the history, the store's (timestamp, id) index: arrival in
// Unix milliseconds, then id, which breaks ties within a millisecond.
type Key struct {
	Stamp int64
	ID    uint32
}

// Key is the notification's place in the history.
func (n *Notification) Key() Key { return Key{Stamp: n.Added.UnixMilli(), ID: n.ID} }

// Compare orders keys oldest first.
func (k Key) Compare(o Key) int {
	return cmp.Or(cmp.Compare(k.Stamp, o.Stamp), cmp.Compare(k.ID, o.ID))
}

// checkPageLimit refuses a page size outside [1, MaxPage].
func checkPageLimit(limit int) error {
	if limit < 1 || limit > MaxPage {
		return fmt.Errorf("notification page of %d: valid 1-%d", limit, MaxPage)
	}
	return nil
}
