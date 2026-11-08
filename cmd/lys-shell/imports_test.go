package main

import (
	"bytes"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// lys-shell links no login screen: locking runs in `lys-login lock`, and
// the shell holds only the client that starts it. lys-login must never
// come into its import graph (lys-login imports lys-shell, so that would
// be a cycle), nor lys-secrets' keys (its internals, or the test server
// that serves them).
var realHome, realCacheHome = os.Getenv("HOME"), os.Getenv("XDG_CACHE_HOME")

func TestShellLinksNoLoginScreen(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go tool is needed to list the import graph: %v", err)
	}
	list := exec.Command(gobin, "list", "-deps", ".")
	list.Env = append(os.Environ(), "HOME="+realHome, "XDG_CACHE_HOME="+realCacheHome)
	var stderr bytes.Buffer
	list.Stderr = &stderr
	out, err := list.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v: %s", err, stderr.Bytes())
	}
	deps := strings.Fields(string(out))
	forbidden := []string{"github.com/stubbedev/lys-login/", "github.com/stubbedev/lys-secrets/internal/", "github.com/stubbedev/lys-secrets/secretstest"}
	for _, dep := range deps {
		if slices.ContainsFunc(forbidden, func(prefix string) bool { return strings.HasPrefix(dep, prefix) }) {
			t.Errorf("lys-shell imports %s", dep)
		}
	}
	found := slices.Contains(deps, "github.com/stubbedev/lys-desktop/shell/lockclient")
	if !found {
		t.Error("lys-shell does not import shell/lockclient: the check above lists nothing")
	}
}
