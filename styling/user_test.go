package styling

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureLog redirects the standard logger for the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(flags)
	})
	return &buf
}

func writeStyles(t *testing.T, files map[string]string) string {
	t.Helper()
	config := t.TempDir()
	for name, text := range files {
		path := filepath.Join(config, "styles", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return config
}

func TestTryUserCSSIsTheFileVerbatim(t *testing.T) {
	const css = ".bar {\n  color: #ff0000;\n}\n.bar:hover { opacity: 0.5; }\n"
	config := writeStyles(t, map[string]string{"index.css": css, "other.css": ".never { a: b; }"})
	got, err := TryUserCSS(config)
	if err != nil || got != css {
		t.Errorf("got (%q, %v), want (%q, nil): only index.css loads, untouched", got, err, css)
	}
}

func TestTryUserCSSAbsentEntryIsNoOverrides(t *testing.T) {
	for name, config := range map[string]string{
		"no styles dir":   t.TempDir(),
		"no entry in dir": writeStyles(t, map[string]string{"partial.css": ".x { a: b; }"}),
	} {
		got, err := TryUserCSS(config)
		if got != "" || err != nil {
			t.Errorf("%s: got (%q, %v), want (\"\", nil)", name, got, err)
		}
	}
}

// A styles/index.scss left from before the CSS cut is an error naming
// the file to move its rules to, even beside an index.css, so its
// rules never stop applying unnoticed.
func TestTryUserCSSRejectsTheLegacySCSSEntry(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"alone":          {"index.scss": ".a { .b { c: d; } }"},
		"beside the css": {"index.scss": ".a { .b { c: d; } }", "index.css": ".x { y: z; }"},
	} {
		config := writeStyles(t, files)
		got, err := TryUserCSS(config)
		if got != "" || err == nil ||
			!strings.Contains(err.Error(), filepath.Join(config, "styles", "index.scss")+" is SCSS") ||
			!strings.Contains(err.Error(), filepath.Join(config, "styles", "index.css")) {
			t.Errorf("%s: got (%q, %v), want the legacy error naming both files", name, got, err)
		}
	}
}

func TestTryUserCSSSurfacesReadErrors(t *testing.T) {
	config := t.TempDir()
	if err := os.MkdirAll(filepath.Join(config, "styles", "index.css"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := TryUserCSS(config); got != "" || err == nil || !strings.Contains(err.Error(), "read ") {
		t.Errorf("an unreadable index.css = (%q, %v), want a read error", got, err)
	}
}

func TestUserCSSLogsAndDropsAFailure(t *testing.T) {
	logs := captureLog(t)
	config := writeStyles(t, map[string]string{"index.scss": "$x: 1;"})
	if got := UserCSS(config); got != "" {
		t.Errorf("UserCSS of a failing stylesheet = %q, want \"\"", got)
	}
	if !strings.Contains(logs.String(), "styling: user styles: ") || !strings.Contains(logs.String(), "is SCSS") {
		t.Errorf("log = %q, want the failure", logs.String())
	}
}

func TestUserCSSReturnsTheCSSSilently(t *testing.T) {
	logs := captureLog(t)
	config := writeStyles(t, map[string]string{"index.css": ".a { b: c; }"})
	if got := UserCSS(config); got != ".a { b: c; }" {
		t.Errorf("UserCSS = %q", got)
	}
	if logs.Len() != 0 {
		t.Errorf("a clean read logged %q", logs.String())
	}
}

func TestUserStylesDir(t *testing.T) {
	config := writeStyles(t, map[string]string{"index.css": ""})
	if dir, ok := UserStylesDir(config); !ok || dir != filepath.Join(config, "styles") {
		t.Errorf("present: got (%q, %v)", dir, ok)
	}
	if dir, ok := UserStylesDir(t.TempDir()); ok || dir != "" {
		t.Errorf("absent: got (%q, %v), want (\"\", false)", dir, ok)
	}
	notDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(notDir, "styles"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := UserStylesDir(notDir); ok {
		t.Error("a file named styles is not a styles directory")
	}
}

func TestEnsureUserStylesScaffoldCreatesTheTemplate(t *testing.T) {
	config := filepath.Join(t.TempDir(), "lys-shell")
	EnsureUserStylesScaffold(config)
	got, err := os.ReadFile(filepath.Join(config, "styles", "index.css"))
	if err != nil {
		t.Fatal(err)
	}
	const want = "/* Custom lys-shell styles. Anything here overrides the built-in styling. */\n"
	if string(got) != want {
		t.Errorf("template = %q, want %q", got, want)
	}
	if css, err := TryUserCSS(config); css != want || err != nil {
		t.Errorf("the template loads as (%q, %v)", css, err)
	}
}

func TestEnsureUserStylesScaffoldNeverOverwrites(t *testing.T) {
	config := writeStyles(t, map[string]string{"index.css": ".mine { a: b; }"})
	EnsureUserStylesScaffold(config)
	EnsureUserStylesScaffold(config)
	got, err := os.ReadFile(filepath.Join(config, "styles", "index.css"))
	if err != nil || string(got) != ".mine { a: b; }" {
		t.Errorf("index.css = (%q, %v), want the user's file untouched", got, err)
	}

	// An existing, empty styles dir only gains the entry file.
	bare := t.TempDir()
	if err := os.Mkdir(filepath.Join(bare, "styles"), 0o755); err != nil {
		t.Fatal(err)
	}
	EnsureUserStylesScaffold(bare)
	if got, err := os.ReadFile(filepath.Join(bare, "styles", "index.css")); err != nil || string(got) != userStylesTemplate {
		t.Errorf("bare dir: index.css = (%q, %v)", got, err)
	}
}

func TestEnsureUserStylesScaffoldLogsFailures(t *testing.T) {
	logs := captureLog(t)
	config := t.TempDir()
	// styles cannot be created where a file already sits.
	if err := os.WriteFile(filepath.Join(config, "styles"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	EnsureUserStylesScaffold(config)
	if !strings.Contains(logs.String(), "cannot create user styles directory") {
		t.Errorf("log = %q, want the directory failure", logs.String())
	}

	logs.Reset()
	// The entry cannot be written where a directory sits.
	config = t.TempDir()
	if err := os.MkdirAll(filepath.Join(config, "styles", "index.css", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	EnsureUserStylesScaffold(config)
	if logs.Len() != 0 {
		t.Errorf("an existing index.css path (even a directory) is left alone, but logged %q", logs.String())
	}
}
