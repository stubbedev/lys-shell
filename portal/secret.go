package portal

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/secrets/client"
)

// secretLen is the master secret's length, as the reference backends.
const secretLen = 64

// secret is org.freedesktop.impl.portal.Secret: a stable per-app
// master secret libsecret derives the app's keyring key from, generated
// on first use and kept in the user's Secret Service (lys-secrets or
// gnome-keyring) under the dev.stubbe.lys.PortalSecret schema, encrypted
// at rest there. It must never change, or the app's keyring becomes
// undecryptable: one is created only when the Secret Service has none
// and no earlier plain file holds one; such a file, from before the
// secrets moved into the keyring, is migrated once and removed. Without
// a Secret Service the request fails rather than store the secret in
// the clear. The frontend's token option carries nothing this backend
// issued, so it is not read.
type secret struct {
	conn *dbus.Conn
	// mu makes lookup-or-create one step, so concurrent first uses
	// agree on one secret.
	mu *sync.Mutex
}

// The attributes a portal master secret is stored under.
const portalSecretSchema = "dev.stubbe.lys.PortalSecret" //nolint:gosec // a schema name, not a credential

func secretAttrs(appID string) map[string]string {
	return map[string]string{"xdg:schema": portalSecretSchema, "app_id": appID}
}

func secretIface(conn *dbus.Conn) dbusx.Interface {
	return dbusx.Interface{
		Name:       "org.freedesktop.impl.portal.Secret",
		Methods:    secret{conn: conn, mu: &sync.Mutex{}},
		Properties: dbusx.Getters{"version": func() any { return uint32(1) }},
	}
}

// RetrieveSecret writes the app's secret to fd. A host app (no app id)
// has no identity to key a secret by and is refused.
func (s secret) RetrieveSecret(handle dbus.ObjectPath, appID string, fd dbus.UnixFD, _ Vardict) (uint32, Vardict, *dbus.Error) {
	f, err := dbusx.File(fd, "secret")
	if err != nil {
		return ResponseOther, Vardict{}, dbusx.InvalidArgs(err.Error())
	}
	defer func() { _ = f.Close() }()
	c, ok := beginOr(s.conn, handle, appID, "", nil)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	if appID == "" {
		warnf("secret: refusing a master secret to an app without an app id")
		return ResponseOther, Vardict{}, nil
	}
	value, err := s.loadOrCreate(c.ctx, appID)
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

// loadOrCreate returns the app's stored secret, migrating a plain file
// left from before, or generating one when there is neither.
func (s secret) loadOrCreate(ctx context.Context, appID string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keyring, err := client.Open(ctx, s.conn)
	if err != nil {
		return nil, err
	}
	defer keyring.Close()
	attrs := secretAttrs(appID)
	value, found, err := keyring.Lookup(ctx, attrs)
	if err != nil {
		return nil, err
	}
	if found {
		if len(value) != secretLen {
			clear(value)
			return nil, fmt.Errorf("the stored secret holds %d bytes, want %d; refusing to replace it", len(value), secretLen)
		}
		return value, nil
	}
	legacy, err := secretPath(appID)
	if err != nil {
		return nil, err
	}
	value, err = readSecret(legacy)
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
	if err := keyring.Store(ctx, "Portal master secret for "+appID, attrs, value); err != nil {
		clear(value)
		return nil, err
	}
	if migrating {
		if err := os.Remove(legacy); err != nil {
			warnf("secret: migrated %s into the keyring but could not remove it: %v", legacy, err)
		}
	}
	return value, nil
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
