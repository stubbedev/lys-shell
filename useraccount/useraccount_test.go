package useraccount

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"
)

// The AccountsService icon wins over ~/.face; an unreadable or missing
// file is skipped; none at all is "".
func TestFindAvatarOrder(t *testing.T) {
	dir := t.TempDir()
	service := filepath.Join(dir, "icons", "ada")
	home := filepath.Join(dir, "home")
	face := filepath.Join(home, ".face")
	prev := avatarCandidates
	avatarCandidates = func(string, string) []string { return []string{service, face} }
	t.Cleanup(func() { avatarCandidates = prev })
	if got := FindAvatar("ada", home); got != "" {
		t.Errorf("no avatar = %q", got)
	}
	for _, p := range []string{face, service} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("png"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := FindAvatar("ada", home); got != service {
		t.Errorf("avatar = %q, want the AccountsService icon", got)
	}
	if err := os.Remove(service); err != nil {
		t.Fatal(err)
	}
	if got := FindAvatar("ada", home); got != face {
		t.Errorf("avatar = %q, want ~/.face", got)
	}
}

// The account comes from the passwd entry for the uid: an empty or
// spoofed $USER does not change it.
func TestCurrentIgnoresTheEnvironment(t *testing.T) {
	want, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		t.Skipf("uid not in the passwd file: %v", err)
	}
	for _, env := range []string{"", "mallory"} {
		t.Setenv("USER", env)
		acct, err := Current()
		if err != nil || acct.Name != want.Username || acct.Home != want.HomeDir {
			t.Errorf("$USER=%q: %+v, %v; want %s", env, acct, err, want.Username)
		}
		if acct.RealName == "" {
			t.Error("no real name, not even the login name")
		}
	}
}
