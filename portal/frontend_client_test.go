package portal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"

	portalclient "github.com/stubbedev/lys-shell/internal/portal"
)

// lys-shell's own portal client takes a request's Response only from
// the owner of the portal's name, on the request path it predicted, and
// only as the spec's (ua{sv}): the real xdg-desktop-portal, routed to
// this backend and a file chooser, answers it that way, and the client
// leaves no match rule on its connection.
func TestFrontendAnswersLysShellsOwnPortalClient(t *testing.T) {
	r := newRig(t, nil)
	picked := filepath.Join(t.TempDir(), "wg0.conf")
	if err := os.WriteFile(picked, []byte("[Interface]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	chooser := serveChooser(t, r, "file://"+picked)
	startFrontend(t, r, t.TempDir())
	conn := r.bus.Conn(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	got, err := portalclient.OpenFile(ctx, conn, "Import", portalclient.FileFilter{Name: "WireGuard", Patterns: []string{"*.conf"}})
	if err != nil {
		t.Fatalf("OpenFile through the real frontend: %v", err)
	}
	if got != picked {
		t.Errorf("OpenFile = %q, want the file the chooser answered, %q", got, picked)
	}
	if title := chooser.opened.Load().Title; title != "Import" {
		t.Errorf("the chooser was asked for %q, want the client's title", title)
	}
	if rules := dbustest.MatchRules(t, conn); len(rules) != 0 {
		t.Errorf("the client left the match rules %q", rules)
	}
}
