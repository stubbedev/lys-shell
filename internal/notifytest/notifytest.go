// Package notifytest holds the notification history helpers the shell's
// tests share: a store on a temporary file, and the history, the store
// and the popup list read back as pages and id sets.
package notifytest

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/service/notifications"
)

// settleTimeout bounds the wait for the service's queued store writes.
const settleTimeout = 10 * time.Second

// Store is a service's store as a test reads it back: through the
// service, whose writes reach the disk after its calls return.
type Store struct {
	svc *notifications.Service
	st  *notifications.Store
}

// AttachStore persists svc's history in a store on a temporary file,
// closed when the test ends; call it before the first notification.
func AttachStore(t *testing.T, svc *notifications.Service) Store {
	t.Helper()
	st, err := notifications.OpenStore(filepath.Join(t.TempDir(), "notifications.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := svc.AttachStore(st); err != nil {
		t.Fatal(err)
	}
	return Store{svc: svc, st: st}
}

// Stored is the first page of what the store holds once every write the
// service queued so far has landed, newest first.
func (s Store) Stored(t *testing.T) []*notifications.Notification {
	t.Helper()
	s.svc.FlushWrites(settleTimeout)
	got, err := s.st.Page(nil, notifications.MaxPage)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// History is the first page of svc's history, newest first.
func History(t *testing.T, svc *notifications.Service) []*notifications.Notification {
	t.Helper()
	got, err := svc.Page(nil, notifications.MaxPage)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// IDs is the set of the notifications' ids.
func IDs(notifs []*notifications.Notification) map[uint32]bool {
	out := make(map[uint32]bool, len(notifs))
	for _, n := range notifs {
		out[n.ID] = true
	}
	return out
}
