package launcher

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stubbedev/lys-desktop/opener/openertest"

	"github.com/stubbedev/lys-shell/service/launcher/launchertest"
)

func TestDetectTerminalTakesTheConfiguredOneThenTheEnvironmentThenThePath(t *testing.T) {
	launchertest.OnlyOnPath(t, "konsole", "kitty")
	if got, err := DetectTerminal(""); err != nil || got != "kitty" {
		t.Errorf("on the path = %q, %v; want kitty, the first of the known terminals installed", got, err)
	}
	t.Setenv("TERMINAL", " myterm ")
	if got, err := DetectTerminal(""); err != nil || got != "myterm" {
		t.Errorf("with $TERMINAL = %q, %v; want myterm", got, err)
	}
	if got, err := DetectTerminal("configured --flag"); err != nil || got != "configured --flag" {
		t.Errorf("configured = %q, %v; want the configured command", got, err)
	}
}

func TestNoTerminalIsAnErrorAndNeverXterm(t *testing.T) {
	launchertest.OnlyOnPath(t, "not-a-terminal")
	got, err := DetectTerminal(" ")
	none, ok := errors.AsType[*NoTerminalError](err)
	if !ok || got != "" {
		t.Fatalf("with no terminal = %q, %v; want a NoTerminalError and no command", got, err)
	}
	if !slices.Equal(none.Candidates, terminalFallbacks) {
		t.Errorf("candidates = %q, want the terminals looked for", none.Candidates)
	}
	for _, want := range []string{"launcher.terminal", "$TERMINAL", "foot", "xterm", "$PATH"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not name %s", err, want)
		}
	}
	launchertest.OnlyOnPath(t, "xterm")
	if got, err := DetectTerminal(""); err != nil || got != "xterm" {
		t.Errorf("with xterm installed = %q, %v; want xterm", got, err)
	}
}

func TestInTerminalWrapsTheCommandOrSaysThereIsNoTerminal(t *testing.T) {
	launchertest.OnlyOnPath(t)
	got, err := InTerminal("term", "sh", "-c", "echo hi")
	if err != nil || !slices.Equal(got, []string{"term", "-e", "sh", "-c", "echo hi"}) {
		t.Errorf("in a configured terminal = %q, %v", got, err)
	}
	got, err = InTerminal("", "sh", "-c", "echo hi")
	if _, ok := errors.AsType[*NoTerminalError](err); !ok || got != nil {
		t.Errorf("with no terminal = %q, %v; want a NoTerminalError and nothing to run", got, err)
	}
}

func TestRenderInTerminalLooksForATerminalOnlyWhenTheTemplateNamesOne(t *testing.T) {
	launchertest.OnlyOnPath(t)
	values := map[string]string{"cmd": "htop"}
	if got, err := RenderInTerminal("mine -e {cmd}", "", values); err != nil || got != "mine -e htop" {
		t.Errorf("a template with its own terminal = %q, %v; want it rendered without one found", got, err)
	}
	got, err := RenderInTerminal("{terminal} -e {cmd}", "", values)
	if _, ok := errors.AsType[*NoTerminalError](err); !ok || got != "" {
		t.Errorf("a template naming the terminal with none = %q, %v; want a NoTerminalError and no command", got, err)
	}
	if got, err := RenderInTerminal("{terminal} -e {cmd}", "term", values); err != nil || got != "term -e htop" {
		t.Errorf("with a configured terminal = %q, %v", got, err)
	}
}

func terminalDesktop(t *testing.T) (viewer, browser, term openertest.Tool) {
	t.Helper()
	desktop := openertest.New(t)
	viewer = desktop.Tool(t, "viewer")
	desktop.Write(t, filepath.Join("data", "applications", "viewer.desktop"),
		"[Desktop Entry]\nType=Application\nName=viewer\nExec=viewer %u\nTerminal=true\nMimeType=x-scheme-handler/gopher;\n")
	browser = desktop.Handler(t, "browser", "x-scheme-handler/https")
	term = desktop.Tool(t, "term")
	t.Setenv("PATH", filepath.Join(desktop.Root, "bin"))
	t.Setenv("TERMINAL", "")
	return viewer, browser, term
}

func TestOpenRefusesATerminalHandlerWithoutATerminal(t *testing.T) {
	viewer, browser, term := terminalDesktop(t)
	err := Open("gopher://host/page", "", "")
	if _, ok := errors.AsType[*NoTerminalError](err); !ok {
		t.Fatalf("a terminal handler with no terminal = %v, want a NoTerminalError", err)
	}
	for _, want := range []string{"viewer.desktop", "gopher://host/page"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not name %s", err, want)
		}
	}
	if viewer.HasRun() || term.HasRun() {
		t.Error("the handler ran without a terminal")
	}
	if err := Open("https://example.com/", "", "tok"); err != nil {
		t.Fatalf("a handler that needs no terminal, with none installed = %v", err)
	}
	if got := browser.Ran(t); got != "https://example.com/|tok" {
		t.Errorf("the browser ran with %q", got)
	}
}

func TestOpenRunsATerminalHandlerInTheConfiguredTerminal(t *testing.T) {
	viewer, _, term := terminalDesktop(t)
	if err := Open("gopher://host/page", "term", ""); err != nil {
		t.Fatal(err)
	}
	if got := term.Ran(t); got != "-e viewer gopher://host/page|" {
		t.Errorf("the terminal ran with %q, want -e and the handler's command", got)
	}
	if viewer.HasRun() {
		t.Error("the handler ran outside the terminal")
	}
}
