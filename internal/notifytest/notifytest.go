// Package notifytest holds the notification history helpers the shell's
// tests share: a store on a temporary file, and the history, the store
// and the popup list read back as pages and id sets.
package notifytest

import (
	"path/filepath"
	"testing"

	"github.com/stubbedev/lys-shell/service/notifications"
)

// AttachStore persists svc's history in a store on a temporary file,
// closed when the test ends; call it before the first notification.
func AttachStore(t *testing.T, svc *notifications.Service) *notifications.Store {
	t.Helper()
	st, err := notifications.OpenStore(filepath.Join(t.TempDir(), "notifications.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := svc.AttachStore(st); err != nil {
		t.Fatal(err)
	}
	return st
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

// Stored is the first page of what st holds, newest first.
func Stored(t *testing.T, st *notifications.Store) []*notifications.Notification {
	t.Helper()
	got, err := st.Page(nil, notifications.MaxPage)
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
