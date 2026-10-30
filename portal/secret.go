package portal

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/stubbedev/lys-lib/atomicfile"
	"github.com/stubbedev/lys-lib/dbusx"
	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/xdg"
	"github.com/stubbedev/lys-secrets/client"
	"github.com/stubbedev/lys-secrets/vault"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/version"
)

// secretLen is the master secret's length, as the reference backends.
const secretLen = 64

// secret is org.freedesktop.impl.portal.Secret: a stable per-app
// master secret libsecret derives the app's keyring key from, generated
// on first use and kept in the user's Secret Service (lys-secrets or
// gnome-keyring) under the dev.stubbe.lys.PortalSecret schema, encrypted
// at rest there. It must never change, or the app's keyring becomes
// undecryptable: one is created only when neither the Secret Service,
// nor the vault, nor an earlier plain file holds one; such a file, from
// before the secrets moved into the keyring, is migrated once and
// removed. Without a Secret Service the request fails rather than store
// the secret in the clear. The frontend's token option carries nothing
// this backend issued, so it is not read.
//
// With modules.bitwarden.portal-folder set, lys-secrets' vault keeps
// the secrets too, as logins named like the keyring's items, so they
// survive a reinstall and follow the user to other computers. An app's
// secret goes into the vault on its first use with the vault open,
// once the user allowed it (a refusal is remembered); one the vault
// already holds comes from there into the keyring. While the vault is
// closed an app the keyring knows still gets its secret, and one it
// does not is refused rather than handed a new secret the vault may
// already have another of.
type secret struct {
	conn *dbus.Conn
	// mu makes lookup-or-create one step, so concurrent first uses
	// agree on one secret.
	mu *sync.Mutex
	// folder is the vault folder the secrets are kept in, "" for none.
	folder func() string
	// vault is lys-secrets' vault.
	vault secretVault
	// declined is the file of apps whose secret the user kept out of
	// the vault, "" when there is no state directory.
	declined string
}

// secretVault is what the portal uses of lys-secrets' vault
// (*vault.Client).
type secretVault interface {
	Status(ctx context.Context) (vault.Status, error)
	FindLogin(ctx context.Context, name string, scope vault.FolderScope) (vault.Item, bool, error)
	Field(ctx context.Context, id string, f vault.Field, password string) (vault.Value, error)
	CreateLogin(ctx context.Context, l vault.NewLogin) (vault.ItemID, error)
}

// The attributes a portal master secret is stored under.
const portalSecretSchema = "dev.stubbe.lys.PortalSecret" //nolint:gosec // a schema name, not a credential

func secretAttrs(appID string) map[string]string {
	return map[string]string{"xdg:schema": portalSecretSchema, "app_id": appID}
}

// secretLabel names an app's secret in the keyring and the vault.
func secretLabel(appID string) string { return "Portal master secret for " + appID }

// errVaultClosed refuses a secret the keyring does not have while the
// vault, which may hold it, is not open.
var errVaultClosed = errors.New("the Bitwarden vault keeping portal secrets is not open; unlock it and try again")

func secretIface(conn *dbus.Conn, cfg *config.Service) dbusx.Interface {
	return dbusx.Interface{
		Name: "org.freedesktop.impl.portal.Secret",
		Methods: secret{
			conn: conn, mu: &sync.Mutex{},
			folder:   func() string { return cfg.Config().Bitwarden.PortalFolder },
			vault:    vault.NewClient(conn),
			declined: secretDeclinedPath(),
		},
		Properties: dbusx.Getters{"version": func() any { return uint32(1) }},
	}
}

// secretDeclinedPath is the file of apps kept out of the vault, under
// lys-shell's state directory; "" when none resolves.
func secretDeclinedPath() string {
	dir, ok := xdg.OS.StateDir(version.Name)
	if !ok {
		return ""
	}
	return filepath.Join(dir, "portal-secret-vault.json")
}

// RetrieveSecret writes the app's secret to fd. A host app (no app id)
// has no identity to key a secret by and is refused.
func (s secret) RetrieveSecret(handle dbus.ObjectPath, appID string, fd dbus.UnixFD, _ Vardict) (uint32, Vardict, *dbus.Error) {
	f, err := dbusx.File(fd, "secret")
	if err != nil {
		return ResponseOther, Vardict{}, dbusx.InvalidArgs(err.Error())
	}
	defer func() { _ = f.Close() }()
	dialogs := portaldialogs.NewClient(s.conn)
	c, ok := beginOr(s.conn, handle, appID, "", dialogs.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	if appID == "" {
		warnf("secret: refusing a master secret to an app without an app id")
		return ResponseOther, Vardict{}, nil
	}
	consent := func() (bool, error) {
		granted, _, err := dialogs.Access(c.ctx, c.caller, portaldialogs.AccessRequest{
			Title:      "Keep this app's key in Bitwarden?",
			Subtitle:   appID,
			Body:       "The key encrypts the passwords this app saves. In your Bitwarden vault it survives reinstalling and is the same on every computer you use the vault on. A copy stays in this computer's keyring either way.",
			GrantLabel: "Keep in the vault", DenyLabel: "Only on this computer",
			Icon: "ld-shield-check-symbolic",
		})
		return granted, err
	}
	value, err := s.loadOrCreate(c.ctx, appID, consent)
	if err != nil {
		warnf("secret: could not obtain the master secret for %s: %v", appID, err)
		return ResponseOther, Vardict{}, nil
	}
	defer clear(value)
	if _, err := f.Write(value); err != nil {
		warnf("secret: writing to fd failed for %s: %v", appID, err)
		return ResponseOther, Vardict{}, nil
	}
	return ResponseSuccess, Vardict{}, nil
}

// loadOrCreate returns the app's secret: the keyring's, else the
// vault's (copied into the keyring), else a plain file's left from
// before (migrated), else a new one. With the vault open, a secret it
// lacks is offered to it through consent.
func (s secret) loadOrCreate(ctx context.Context, appID string, consent func() (bool, error)) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keyring, err := client.Open(ctx, s.conn)
	if err != nil {
		return nil, err
	}
	defer keyring.Close()
	attrs := secretAttrs(appID)
	local, found, err := keyring.Lookup(ctx, attrs)
	if err != nil {
		return nil, err
	}
	if found && len(local) != secretLen {
		clear(local)
		return nil, fmt.Errorf("the stored secret holds %d bytes, want %d; refusing to replace it", len(local), secretLen)
	}
	kept, open, vaultErr := s.fromVault(ctx, appID)
	switch {
	case found:
		if kept != nil && !bytes.Equal(kept, local) {
			warnf("secret: the vault holds another secret for %s than this computer's keyring; keeping the keyring's", appID)
		}
		if vaultErr != nil && !errors.Is(vaultErr, errVaultClosed) {
			warnf("secret: %s in the vault: %v", appID, vaultErr)
		}
		if open && kept == nil {
			s.provision(ctx, appID, local, consent)
		}
		clear(kept)
		return local, nil
	case kept != nil:
		if err := keyring.Store(ctx, secretLabel(appID), attrs, kept); err != nil {
			clear(kept)
			return nil, err
		}
		return kept, nil
	case vaultErr != nil:
		return nil, vaultErr
	}
	legacy, err := secretPath(appID)
	if err != nil {
		return nil, err
	}
	value, err := readSecret(legacy)
	migrating := err == nil
	switch {
	case migrating:
	case errors.Is(err, fs.ErrNotExist):
		value = make([]byte, secretLen)
		if _, err := rand.Read(value); err != nil {
			return nil, fmt.Errorf("generate: %w", err)
		}
	default:
		return nil, err
	}
	if err := keyring.Store(ctx, secretLabel(appID), attrs, value); err != nil {
		clear(value)
		return nil, err
	}
	if migrating {
		if err := os.Remove(legacy); err != nil {
			warnf("secret: migrated %s into the keyring but could not remove it: %v", legacy, err)
		}
	}
	if open {
		s.provision(ctx, appID, value, consent)
	}
	return value, nil
}

// fromVault reads the app's secret from the vault folder: nil and open
// when the folder has none. Without a folder configured, or with the
// vault unreachable (logged), there is no vault (not open, no error);
// a vault that is not open, or that fails to answer, is an error.
func (s secret) fromVault(ctx context.Context, appID string) (value []byte, open bool, err error) {
	folder := s.folder()
	if folder == "" || s.vault == nil {
		return nil, false, nil
	}
	st, err := s.vault.Status(ctx)
	if err != nil {
		warnf("secret: the vault is unreachable, keeping %s in the keyring only: %v", appID, err)
		return nil, false, nil
	}
	if st.State != vault.StateUnlocked {
		return nil, false, errVaultClosed
	}
	item, found, err := s.vault.FindLogin(ctx, secretLabel(appID), vault.InFolder(folder))
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, true, nil
	}
	v, err := s.vault.Field(ctx, item.ID, vault.FieldPassword, "")
	if err != nil {
		return nil, false, err
	}
	value, err = base64.StdEncoding.DecodeString(v.Text)
	if err != nil || len(value) != secretLen {
		clear(value)
		return nil, false, fmt.Errorf("the vault's secret for %s is not %d base64 bytes; refusing it", appID, secretLen)
	}
	return value, true, nil
}

// provision puts the app's secret into the vault folder once the user
// allows it; a refusal is remembered, and a failure only logged: the
// keyring keeps the secret either way.
func (s secret) provision(ctx context.Context, appID string, value []byte, consent func() (bool, error)) {
	if s.isDeclined(appID) {
		return
	}
	granted, err := consent()
	if err != nil {
		warnf("secret: asking to keep %s's secret in the vault: %v", appID, err)
		return
	}
	if !granted {
		if err := s.decline(appID); err != nil {
			warnf("secret: remember that %s stays out of the vault: %v", appID, err)
		}
		return
	}
	_, err = s.vault.CreateLogin(ctx, vault.NewLogin{
		Folder: s.folder(), Name: secretLabel(appID), Username: appID,
		Password: base64.StdEncoding.EncodeToString(value),
	})
	if err != nil {
		warnf("secret: keep %s's secret in the vault: %v", appID, err)
	}
}

// secretDeclines is the declined file's content.
type secretDeclines struct {
	Declined []string `json:"declined"`
}

func (s secret) declines() secretDeclines {
	var d secretDeclines
	if s.declined == "" {
		return d
	}
	data, err := os.ReadFile(s.declined)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			warnf("secret: %v", err)
		}
		return d
	}
	if err := json.Unmarshal(data, &d); err != nil {
		warnf("secret: %s: %v", s.declined, err)
	}
	return d
}

func (s secret) isDeclined(appID string) bool { return slices.Contains(s.declines().Declined, appID) }

// decline remembers that the app's secret stays out of the vault.
func (s secret) decline(appID string) error {
	if s.declined == "" {
		return errors.New("no state directory to remember it in")
	}
	d := s.declines()
	d.Declined = append(d.Declined, appID)
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.declined), 0o700); err != nil {
		return err
	}
	return atomicfile.WriteFile(s.declined, data, 0o600)
}

// readSecret reads a secret file left from before the secrets moved
// into the keyring, which must be exactly secretLen bytes.
func readSecret(path string) ([]byte, error) {
	value, err := os.ReadFile(path) //nolint:gosec // a sanitized name under the data dir
	if err != nil {
		return nil, err
	}
	if len(value) != secretLen {
		return nil, fmt.Errorf("%s holds %d bytes, want %d; refusing to replace it", path, len(value), secretLen)
	}
	return value, nil
}

// secretPath is where the app's secret file was kept before the
// keyring: under $XDG_DATA_HOME (when absolute) or ~/.local/share.
func secretPath(appID string) (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(base) {
		home, ok := os.LookupEnv("HOME")
		if !ok {
			return "", errors.New("neither XDG_DATA_HOME nor HOME is set")
		}
		base = filepath.Join(home, ".local/share")
	}
	return filepath.Join(base, "lys-shell/portal/secrets", sanitizeAppID(appID)), nil
}

// sanitizeAppID maps an app id to one safe path component: anything
// but ASCII alphanumerics and .-_ becomes _, and a leading . (or an
// empty id) is prefixed with _, so it is never ".", ".." or hidden.
func sanitizeAppID(appID string) string {
	var b strings.Builder
	b.WriteByte('_')
	for _, r := range appID {
		if r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(".-_", r)) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 1 && out[1] != '.' {
		return out[1:]
	}
	return out
}
