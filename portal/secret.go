package portal

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
)

// secretLen is the master secret's length, as the reference backends.
const secretLen = 64

// secret is org.freedesktop.impl.portal.Secret: a stable per-app
// master secret libsecret derives the app's keyring key from, generated
// on first use and kept in plain bytes at
// $XDG_DATA_HOME/lys-shell/portal/secrets/<app_id> (0600, readable by
// the user alone, like the rest of their keyring files). It must never
// change, or the keyring becomes undecryptable: only a missing file is
// ever created, never one that fails to read. The frontend's token
// option carries nothing this backend issued, so it is not read.
type secret struct{ conn *dbus.Conn }

func secretIface(conn *dbus.Conn) dbusx.Interface {
	return dbusx.Interface{
		Name:       "org.freedesktop.impl.portal.Secret",
		Methods:    secret{conn},
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
	value, err := loadOrCreateSecret(appID)
	if err != nil {
		warnf("secret: could not obtain the master secret for %s: %v", appID, err)
		return ResponseOther, Vardict{}, nil
	}
	if _, err := f.Write(value); err != nil {
		warnf("secret: writing to fd failed for %s: %v", appID, err)
		return ResponseOther, Vardict{}, nil
	}
	return ResponseSuccess, Vardict{}, nil
}

// loadOrCreateSecret reads the stored secret, or generates and stores
// one when there is no file. A file that cannot be read, or holds
// anything but secretLen bytes, is an error and stays as it is.
func loadOrCreateSecret(appID string) ([]byte, error) {
	path, err := secretPath(appID)
	if err != nil {
		return nil, err
	}
	if value, err := readSecret(path); !errors.Is(err, fs.ErrNotExist) {
		return value, err
	}
	value := make([]byte, secretLen)
	if _, err := rand.Read(value); err != nil {
		return nil, fmt.Errorf("generate: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	won, err := publishSecret(path, value)
	if err != nil {
		return nil, err
	}
	if !won {
		return readSecret(path)
	}
	return value, nil
}

// readSecret reads a stored secret, which must be exactly secretLen
// bytes.
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

// publishSecret writes value to a temporary file beside path, syncs it,
// and links it into place, which fails rather than replace a secret
// another call published first: won reports whether this value is the
// stored one. Nothing partial is ever visible at path.
func publishSecret(path string, value []byte) (won bool, err error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp.*")
	if err != nil {
		return false, fmt.Errorf("temp for %s: %w", path, err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(value); err != nil {
		_ = f.Close()
		return false, fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return false, fmt.Errorf("sync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return false, fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Link(tmp, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("link %s: %w", path, err)
	}
	return true, nil
}

// secretPath is the app's file under $XDG_DATA_HOME (when absolute) or
// ~/.local/share.
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
