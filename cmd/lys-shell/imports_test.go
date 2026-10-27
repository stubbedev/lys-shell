package main

import (
	"os/exec"
	"strings"
	"testing"
)

// lys-shell links no login screen: locking runs in `lys-login lock`, and
// the shell holds only the client that starts it. The greeter, the lock
// screen and the screen they share must never come back into its import
// graph, nor lys-secrets' keys.
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
	forbidden := []string{"login/screen", "login/greeter", "login/lock", "secrets/keyring", "secrets/service"}
	for _, dep := range deps {
		for _, f := range forbidden {
			if dep == module+f {
				t.Errorf("lys-shell imports %s", dep)
			}
		}
	}
	found := false
	for _, dep := range deps {
		found = found || dep == module+"login/lockclient"
	}
	if !found {
		t.Error("lys-shell does not import login/lockclient: the check above lists nothing")
	}
}
