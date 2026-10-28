package main

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// lys-shell links no login screen: locking runs in `lys-login lock`, and
// the shell holds only the client that starts it. The greeter, the lock
// screen and the screen they share must never come back into its import
// graph, nor lys-secrets' keys (its internals, or the test server that
// serves them).
func TestShellLinksNoLoginScreen(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go tool is needed to list the import graph: %v", err)
	}
	out, err := exec.Command(gobin, "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	deps := strings.Fields(string(out))
	const module = "github.com/stubbedev/lys-shell/"
	forbidden := []string{module + "login/screen", module + "login/greeter", module + "login/lock", "github.com/stubbedev/lys-secrets/secretstest"}
	const secretsInternal = "github.com/stubbedev/lys-secrets/internal/"
	for _, dep := range deps {
		if slices.Contains(forbidden, dep) || strings.HasPrefix(dep, secretsInternal) {
			t.Errorf("lys-shell imports %s", dep)
		}
	}
	found := slices.Contains(deps, module+"shell/lockclient")
	if !found {
		t.Error("lys-shell does not import shell/lockclient: the check above lists nothing")
	}
}
