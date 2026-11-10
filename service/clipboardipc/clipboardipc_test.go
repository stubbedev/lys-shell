package clipboardipc

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
)

func TestCopyTextReachesTheShellsClipboard(t *testing.T) {
	bus := dbustest.Start(t)
	var (
		mu      sync.Mutex
		copied  []string
		failing bool
	)
	snapshot := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(copied)
	}
	release, err := NewDaemon(func(text string) error {
		mu.Lock()
		defer mu.Unlock()
		if failing {
			return errors.New("no data-control")
		}
		copied = append(copied, text)
		return nil
	}).Export(bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	obj := bus.Conn(t).Object(ServiceName, ServicePath)
	call := func(text string) error {
		return obj.CallWithContext(t.Context(), ServiceName+".CopyText", 0, text).Err
	}

	if err := call("ld-folder-symbolic"); err != nil {
		t.Fatal(err)
	}
	if copied := snapshot(); !slices.Equal(copied, []string{"ld-folder-symbolic"}) {
		t.Errorf("copied %q, want the one name", copied)
	}
	if err := call(""); err == nil || !strings.Contains(err.Error(), "no text") {
		t.Errorf("an empty copy = %v, want refused", err)
	}
	mu.Lock()
	failing = true
	mu.Unlock()
	if err := call("x"); err == nil || !strings.Contains(err.Error(), "no data-control") {
		t.Errorf("a failed copy = %v, want the clipboard's error", err)
	}
	if copied := snapshot(); len(copied) != 1 {
		t.Errorf("refused copies reached the clipboard: %q", copied)
	}
}
