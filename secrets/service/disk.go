package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stubbedev/lys-lib/atomicfile"
)

const (
	collectionExt = ".keyring"
	aliasesFile   = "aliases.json"
)

// disk is the service's directory: one file per collection, named by
// its id, and the alias table. Every write replaces its file atomically.
type disk struct{ dir string }

func (d disk) ensure() error {
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", d.dir, err)
	}
	return nil
}

func (d disk) ids() ([]string, error) {
	entries, err := os.ReadDir(d.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", d.dir, err)
	}
	var out []string
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), collectionExt); ok && e.Type().IsRegular() && validID(id) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out, nil
}

func (d disk) path(id string) string { return filepath.Join(d.dir, id+collectionExt) }

func (d disk) load(id string) ([]byte, error) {
	data, err := os.ReadFile(d.path(id))
	if err != nil {
		return nil, fmt.Errorf("read collection %s: %w", id, err)
	}
	return data, nil
}

func (d disk) save(id string, data []byte) error {
	if err := d.ensure(); err != nil {
		return err
	}
	return atomicfile.WriteFile(d.path(id), data, 0o600)
}

func (d disk) remove(id string) error {
	if err := os.Remove(d.path(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove collection %s: %w", id, err)
	}
	return nil
}

func (d disk) loadAliases() (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(d.dir, aliasesFile))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read aliases: %w", err)
	}
	aliases := map[string]string{}
	if err := json.Unmarshal(data, &aliases); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Join(d.dir, aliasesFile), err)
	}
	for alias, id := range aliases {
		if !validID(alias) || !validID(id) {
			return nil, fmt.Errorf("parse %s: invalid alias %q → %q", filepath.Join(d.dir, aliasesFile), alias, id)
		}
	}
	return aliases, nil
}

func (d disk) saveAliases(aliases map[string]string) error {
	if err := d.ensure(); err != nil {
		return err
	}
	data, err := json.Marshal(aliases)
	if err != nil {
		return fmt.Errorf("encode aliases: %w", err)
	}
	return atomicfile.WriteFile(filepath.Join(d.dir, aliasesFile), data, 0o600)
}

// validID is a collection id or alias: a non-empty object path element.
func validID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// idFor turns a label into a collection id: lowercase alphanumerics, the
// rest underscores, made unique against taken.
func idFor(label string, taken func(string) bool) string {
	var b strings.Builder
	for _, r := range strings.ToLower(label) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "_"):
			b.WriteByte('_')
		}
		if b.Len() >= 48 {
			break
		}
	}
	base := strings.Trim(b.String(), "_")
	if base == "" {
		base = "collection"
	}
	id := base
	for n := 2; taken(id); n++ {
		id = fmt.Sprintf("%s_%d", base, n)
	}
	return id
}
