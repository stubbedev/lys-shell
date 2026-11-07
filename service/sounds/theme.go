// Package sounds plays notification sounds: names resolve through the
// freedesktop sound theme, files decode once (Ogg Vorbis and WAV) into
// the PCM the audio server's sample cache keeps, and a notification's
// sound plays from that cache through the session's PipeWire.
package sounds

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FallbackTheme is the theme every lookup ends in.
const FallbackTheme = "freedesktop"

// extensions are the file types a sound theme ships, in the spec's order.
var extensions = [...]string{".oga", ".ogg", ".wav"}

// Theme resolves sound names in a sound theme and those it inherits, over
// the XDG data directories.
type Theme struct {
	name string
	dirs []string
	fsys fs.StatFS
}

// NewTheme looks names up in theme ("" is the fallback theme) under the
// sounds directory of each of dataDirs, most important first.
func NewTheme(theme string, dataDirs []string) Theme {
	if theme == "" {
		theme = FallbackTheme
	}
	return Theme{name: theme, dirs: dataDirs, fsys: osFS{}}
}

// ErrNoSound is a name no theme in the chain has a file for.
var ErrNoSound = errors.New("no sound theme file for this name")

// Lookup is the file for name in the theme, the themes it inherits and the
// fallback theme, in that order; only when none has the name does a
// shorter one (message-new-instant, then message-new, then message) count.
func (t Theme) Lookup(name string) (string, error) {
	chain := t.chain(map[string]bool{}, t.name)
	for n := name; n != ""; n = shorter(n) {
		for _, theme := range chain {
			if path, ok := t.find(theme, n); ok {
				return path, nil
			}
		}
	}
	return "", ErrNoSound
}

func shorter(name string) string {
	i := strings.LastIndexByte(name, '-')
	if i <= 0 {
		return ""
	}
	return name[:i]
}

func (t Theme) chain(seen map[string]bool, theme string) []string {
	if seen[theme] {
		return nil
	}
	seen[theme] = true
	out := []string{theme}
	for _, parent := range t.inherits(theme) {
		out = append(out, t.chain(seen, parent)...)
	}
	if theme == t.name && !seen[FallbackTheme] {
		out = append(out, t.chain(seen, FallbackTheme)...)
	}
	return out
}

func (t Theme) find(theme, name string) (string, bool) {
	for _, dir := range t.dirs {
		for _, sub := range t.subdirs(dir, theme) {
			for _, ext := range extensions {
				path := filepath.Join(dir, "sounds", theme, sub, name+ext)
				if info, err := t.fsys.Stat(path); err == nil && info.Mode().IsRegular() {
					return path, true
				}
			}
		}
	}
	return "", false
}

func (t Theme) subdirs(dir, theme string) []string {
	index, ok := t.index(dir, theme)
	if !ok || len(index.directories) == 0 {
		return []string{"stereo", ""}
	}
	return append(index.directories, "")
}

func (t Theme) inherits(theme string) []string {
	for _, dir := range t.dirs {
		if index, ok := t.index(dir, theme); ok {
			return index.inherits
		}
	}
	return nil
}

type themeIndex struct {
	inherits    []string
	directories []string
}

func (t Theme) index(dir, theme string) (themeIndex, bool) {
	f, err := t.fsys.Open(filepath.Join(dir, "sounds", theme, "index.theme"))
	if err != nil {
		return themeIndex{}, false
	}
	defer func() { _ = f.Close() }()
	return parseIndex(bufio.NewScanner(f)), true
}

func parseIndex(s *bufio.Scanner) themeIndex {
	var (
		idx     themeIndex
		group   string
		dirs    []string
		profile = map[string]string{}
	)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		if line[0] == '[' && line[len(line)-1] == ']' {
			group = line[1 : len(line)-1]
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch {
		case group == "Sound Theme" && key == "Inherits":
			idx.inherits = splitList(value)
		case group == "Sound Theme" && key == "Directories":
			dirs = splitList(value)
		case key == "OutputProfile":
			profile[group] = value
		}
	}
	for _, d := range dirs {
		if p := profile[d]; p == "" || p == "stereo" {
			idx.directories = append(idx.directories, d)
		}
	}
	return idx
}

func splitList(value string) []string {
	var out []string
	for item := range strings.SplitSeq(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

type osFS struct{}

func (osFS) Open(name string) (fs.File, error)     { return os.Open(name) } //nolint:gosec // sound theme files under the XDG data dirs
func (osFS) Stat(name string) (fs.FileInfo, error) { return os.Stat(name) }
