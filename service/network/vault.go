package network

import (
	"slices"

	"github.com/stubbedev/lys-shell/service/network/secrets"
)

// Vault is a password store the secret agent answers from before it
// asks the user.
type Vault interface {
	// Entry is the entry stored for the connection profile named name;
	// false when there is none, or the store is locked.
	Entry(name string) (VaultEntry, bool)
}

// VaultEntry is one stored credential: a login's username and password,
// and extra fields by name.
type VaultEntry struct {
	Username, Password string
	Fields             map[string]string
}

// fromVault answers fields for the profile named name from the vault,
// every one of them or none.
func (o *agentObject) fromVault(name string, fields []secrets.Field) (map[string]string, bool) {
	if o.vault == nil {
		return nil, false
	}
	entry, ok := o.vault.Entry(name)
	if !ok {
		return nil, false
	}
	return vaultValues(entry, fields)
}

// vaultValues fills each field from the entry: a field of the key's own
// name first, else the login's username or password for the keys those
// answer. A key the entry has nothing for leaves the whole request to
// the user.
func vaultValues(e VaultEntry, fields []secrets.Field) (map[string]string, bool) {
	values := make(map[string]string, len(fields))
	for _, f := range fields {
		v := e.Fields[f.Key]
		switch {
		case v != "":
		case slices.Contains(usernameKeys, f.Key):
			v = e.Username
		case slices.Contains(passwordKeys, f.Key):
			v = e.Password
		}
		if v == "" {
			return nil, false
		}
		values[f.Key] = v
	}
	return values, true
}
