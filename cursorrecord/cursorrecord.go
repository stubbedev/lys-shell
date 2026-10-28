// Package cursorrecord is the record of the cursor a lys-shell session
// shows, left in the user's state directory for the login greeter: it
// runs before anyone logs in, so it cannot ask a session, and shows the
// last user's cursor from this record instead.
package cursorrecord

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-lib/atomicfile"
	"github.com/stubbedev/lys-lib/xdg"

	"github.com/stubbedev/lys-shell/internal/version"
)

// fileName is the record's name in lys-shell's state directory.
const fileName = "greeter-cursor"

// Cursor is a cursor theme and size; either may be unknown.
type Cursor struct {
	// Theme is the xcursor theme, "" when unknown.
	Theme string
	// Size is the nominal size in pixels, 0 when unknown.
	Size uint32
}

// Session is the cursor a session with environment env shows:
// XCURSOR_THEME, and XCURSOR_SIZE else HYPRCURSOR_SIZE, which the
// compositor exported. False when env names neither.
func Session(env xdg.Env) (Cursor, bool) {
	c := Cursor{Theme: env("XCURSOR_THEME")}
	size := env("XCURSOR_SIZE")
	if size == "" {
		size = env("HYPRCURSOR_SIZE")
	}
	c.Size = parseSize(size)
	return c, c != Cursor{}
}

// Record writes the cursor of the session with environment env
// (Session) to its lys-shell state directory, replacing the record
// whole. An environment naming no cursor writes nothing, so a session
// without one never clobbers a good record; written reports whether it
// wrote.
func Record(env xdg.Env) (written bool, err error) {
	c, ok := Session(env)
	if !ok {
		return false, nil
	}
	dir, ok := env.StateDir(version.Name)
	if !ok {
		return false, errors.New("cursor record: no state directory: neither XDG_STATE_HOME nor HOME is an absolute path")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // the greeter reads the record
		return false, fmt.Errorf("cursor record: %w", err)
	}
	if err := atomicfile.WriteFile(filepath.Join(dir, fileName), []byte(format(c)), 0o644); err != nil {
		return false, fmt.Errorf("cursor record: %w", err)
	}
	return true, nil
}

// Read reads the record a session of the user whose home is home left
// in the state directory under it: the greeter knows the user's home,
// not their environment.
func Read(home string) (Cursor, error) {
	dir, ok := xdg.Env(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}).StateDir(version.Name)
	if !ok {
		return Cursor{}, fmt.Errorf("cursor record: home %q is not an absolute path", home)
	}
	data, err := os.ReadFile(filepath.Join(dir, fileName)) //nolint:gosec // the record in the user's state directory
	if err != nil {
		return Cursor{}, fmt.Errorf("cursor record: %w", err)
	}
	return parse(string(data)), nil
}

// format is the record's text: a theme= and a size= line, each only
// when known.
func format(c Cursor) string {
	var b strings.Builder
	if c.Theme != "" {
		b.WriteString("theme=" + c.Theme + "\n")
	}
	if c.Size != 0 {
		b.WriteString("size=" + strconv.FormatUint(uint64(c.Size), 10) + "\n")
	}
	return b.String()
}

// parse reads a record; lines it does not know are skipped.
func parse(text string) Cursor {
	var c Cursor
	for line := range strings.SplitSeq(text, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "theme":
			c.Theme = value
		case "size":
			c.Size = parseSize(value)
		}
	}
	return c
}

// parseSize is a positive size in pixels, 0 for anything else.
func parseSize(s string) uint32 {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 32)
	if err != nil {
		return 0
	}
	return uint32(n)
}
