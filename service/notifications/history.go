package notifications

import (
	"cmp"
	"fmt"
)

// MaxPage is the most notifications one page returns.
const MaxPage = 500

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
