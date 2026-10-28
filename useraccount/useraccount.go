// Package useraccount describes a user account the way the desktop
// shows it: login name, real name, home, and avatar.
package useraccount

import (
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

// accountsServiceIcons is where AccountsService (and gdm and sddm)
// keep avatars, one file per login name.
const accountsServiceIcons = "/var/lib/AccountsService/icons"

// Account is one user account.
type Account struct {
	// Name is the login name; RealName the GECOS full name, else Name.
	Name, RealName string
	Home           string
	// Avatar is a readable avatar path, "" when none.
	Avatar string
}

// Current is the account the process runs as, from its passwd entry by
// uid, so an empty or spoofed $USER changes nothing. An account the
// passwd file does not list (userdb, LDAP) falls back to what the
// environment says, with a warning.
func Current() (Account, error) {
	u, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		log.Printf("useraccount: uid %d is not in the passwd file (%v); using the environment's account", os.Getuid(), err)
		if u, err = user.Current(); err != nil {
			return Account{}, fmt.Errorf("look up the current user: %w", err)
		}
	}
	real, _, _ := strings.Cut(u.Name, ",")
	real = strings.TrimSpace(real)
	if real == "" {
		real = u.Username
	}
	return Account{Name: u.Username, RealName: real, Home: u.HomeDir, Avatar: FindAvatar(u.Username, u.HomeDir)}, nil
}

// FindAvatar is the first readable avatar for a login: the
// AccountsService icon, then ~/.face.
func FindAvatar(name, home string) string {
	for _, candidate := range avatarCandidates(name, home) {
		if f, err := os.Open(candidate); err == nil { //nolint:gosec // avatar lookup
			_ = f.Close()
			return candidate
		}
	}
	return ""
}

// avatarCandidates is where an avatar may be, in order; a seam for
// tests, which cannot write the AccountsService directory.
var avatarCandidates = func(name, home string) []string {
	return []string{filepath.Join(accountsServiceIcons, name), filepath.Join(home, ".face")}
}
