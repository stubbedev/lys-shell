package dbus

import (
	"fmt"
	"sync"
	"testing"
)

type pinger struct{}

func (pinger) Ping() *Error { return nil }

// TestConcurrentExportsAndCalls is the fifth lys-shell patch: Export
// and unexport from many goroutines while calls and introspection look
// paths up. The handler's object table was read without its lock and
// an object's first interface was added in two steps, so under -race
// this reports a data race, and without it a call could miss an object
// mid-export.
func TestConcurrentExportsAndCalls(t *testing.T) {
	server, client, cleanup := pairedConns(t)
	defer cleanup()
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 50 {
				path := ObjectPath(fmt.Sprintf("/race/g%d/o%d", g, i%5))
				if err := server.Export(pinger{}, path, "a.ping"); err != nil {
					t.Error(err)
					return
				}
				if err := server.Export(pinger{}, path, "a.other"); err != nil {
					t.Error(err)
					return
				}
				obj := client.Object(server.Names()[0], path)
				_ = obj.Call("org.freedesktop.DBus.Introspectable.Introspect", 0).Err
				_ = server.Export(nil, path, "a.ping")
				_ = server.Export(nil, path, "a.other")
			}
		})
	}
	wg.Wait()
	h := server.handler.(*defaultHandler)
	h.RLock()
	left := len(h.objects)
	h.RUnlock()
	if left != 0 {
		t.Errorf("%d objects left after every interface was unexported", left)
	}
}
