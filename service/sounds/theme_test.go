package sounds

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func themeTree(t *testing.T) (user, system string) {
	t.Helper()
	user, system = t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(system, "sounds/freedesktop/index.theme"), "[Sound Theme]\nName=Default\nDirectories=stereo\n\n[stereo]\nOutputProfile=stereo\n")
	writeFile(t, filepath.Join(system, "sounds/freedesktop/stereo/message-new-instant.oga"), "x")
	writeFile(t, filepath.Join(system, "sounds/freedesktop/stereo/message.oga"), "x")
	writeFile(t, filepath.Join(system, "sounds/freedesktop/stereo/bell.oga"), "x")
	writeFile(t, filepath.Join(system, "sounds/ocean/index.theme"), "[Sound Theme]\nInherits=calm\nDirectories=stereo,5.1\n\n[stereo]\nOutputProfile=stereo\n\n[5.1]\nOutputProfile=5.1\n")
	writeFile(t, filepath.Join(system, "sounds/ocean/5.1/bell.oga"), "x")
	writeFile(t, filepath.Join(system, "sounds/calm/stereo/complete.wav"), "x")
	writeFile(t, filepath.Join(user, "sounds/ocean/stereo/message-new-email.ogg"), "x")
	return user, system
}

func TestNamesResolveThroughTheThemeChain(t *testing.T) {
	user, system := themeTree(t)
	theme := NewTheme("ocean", []string{user, system})
	for name, want := range map[string]string{
		"message-new-email":   filepath.Join(user, "sounds/ocean/stereo/message-new-email.ogg"),
		"complete":            filepath.Join(system, "sounds/calm/stereo/complete.wav"),
		"message-new-instant": filepath.Join(system, "sounds/freedesktop/stereo/message-new-instant.oga"),
		"message-new-email-x": filepath.Join(user, "sounds/ocean/stereo/message-new-email.ogg"),
		"message-other":       filepath.Join(system, "sounds/freedesktop/stereo/message.oga"),
		"bell":                filepath.Join(system, "sounds/freedesktop/stereo/bell.oga"),
	} {
		got, err := theme.Lookup(name)
		if err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	if _, err := theme.Lookup("nothing-like-it"); !errors.Is(err, ErrNoSound) {
		t.Errorf("an unknown name: %v, want ErrNoSound", err)
	}
	if got, err := NewTheme("", []string{system}).Lookup("bell"); err != nil || got != filepath.Join(system, "sounds/freedesktop/stereo/bell.oga") {
		t.Errorf("no theme: %q, %v", got, err)
	}
}
