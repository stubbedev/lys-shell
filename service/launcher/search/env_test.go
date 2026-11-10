package search

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stubbedev/lys-desktop/desktopentry"
	"github.com/stubbedev/lys-desktop/i18n"

	"github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/launchertest"
)

func runAction(t *testing.T, actions []Action, title string) Outcome {
	t.Helper()
	i := slices.IndexFunc(actions, func(a Action) bool { return a.Title == title })
	if i < 0 {
		t.Fatalf("no action titled %q among %d", title, len(actions))
	}
	return actions[i].Run(context.Background())
}

func wantNoTerminalNotice(t *testing.T, o Outcome, what string, ran *spawned) {
	t.Helper()
	_, err := launcher.DetectTerminal("")
	none, ok := errors.AsType[*launcher.NoTerminalError](err)
	if !ok {
		t.Fatalf("the test's environment has a terminal: %v", err)
	}
	stay, ok := o.(Stay)
	if !ok || !strings.Contains(stay.Message, what) || !strings.Contains(stay.Message, none.Error()) {
		t.Errorf("outcome = %#v, want the panel kept open saying %s has no terminal to run in", o, what)
	}
	if len(ran.argv) != 0 {
		t.Errorf("spawned %q with no terminal to run it in", ran.argv)
	}
}

func TestATerminalActionWithoutATerminalStaysAndSaysSo(t *testing.T) {
	launchertest.OnlyOnPath(t, "tui")
	inTerminal := i18n.T("launcher-action-run-in-terminal")

	ran := &spawned{}
	shell := NewShell(ran.env())
	wantNoTerminalNotice(t, runAction(t, shell.Actions(shell.Answer("> htop -d 5")[0]), inTerminal), "htop -d 5", ran)

	entry := filepath.Join(t.TempDir(), "tui.desktop")
	if err := os.WriteFile(entry, []byte("[Desktop Entry]\nType=Application\nName=Tui\nExec=tui %f\nTerminal=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, ok := desktopentry.LoadApp("tui.desktop", entry)
	if !ok {
		t.Fatal("the terminal app did not load")
	}
	apps := NewApps(ran.env(), nil)
	wantNoTerminalNotice(t, runAction(t, apps.Actions(Entry{Data: appData{app: &app}}), i18n.T("launcher-action-open")), "Tui", ran)

	files := NewFiles(ran.env(), FilesConfig{})
	dir := t.TempDir()
	here := i18n.T("launcher-action-open-terminal-here")
	wantNoTerminalNotice(t, runAction(t, files.Actions(Entry{Data: &fileData{path: dir, dir: true}}), here), here, ran)

	scripts := NewScripts(ran.env(), t.TempDir())
	wantNoTerminalNotice(t, scripts.run(context.Background(), Script{Path: "/x/top", Title: "Top", Output: ScriptTerminal}, nil), "Top", ran)
}

func TestATerminalActionRunsInTheConfiguredTerminal(t *testing.T) {
	launchertest.OnlyOnPath(t)
	ran := &spawned{}
	env := ran.env()
	env.Terminal = "term"
	shell := NewShell(env)
	o := runAction(t, shell.Actions(shell.Answer("> htop -d 5")[0]), i18n.T("launcher-action-run-in-terminal"))
	if _, closed := o.(Close); !closed {
		t.Errorf("outcome = %#v, want the panel closed", o)
	}
	if want := [][]string{{"term", "-e", "sh", "-c", "htop -d 5"}}; !slices.EqualFunc(ran.argv, want, slices.Equal) {
		t.Errorf("spawned %q, want %q", ran.argv, want)
	}
}

func TestAFailedOpenStaysAndSaysWhy(t *testing.T) {
	refused := &launcher.NoTerminalError{Candidates: []string{"foot"}}
	var opened []string
	env := Env{Open: func(target string) error {
		opened = append(opened, target)
		if strings.HasPrefix(target, "gopher:") {
			return refused
		}
		return nil
	}}
	open := i18n.T("launcher-action-open")
	web := NewWeb(env, "", "https://search.example/?q={query}")
	o := runAction(t, web.Actions(Entry{Data: "gopher://host/page"}), open)
	if stay, ok := o.(Stay); !ok || !strings.Contains(stay.Message, refused.Error()) {
		t.Errorf("a refused open = %#v, want the panel kept open with the reason", o)
	}
	o = runAction(t, web.Actions(Entry{Data: "https://example.com/"}), open)
	if _, closed := o.(Close); !closed {
		t.Errorf("an open that worked = %#v, want the panel closed", o)
	}
	if want := []string{"gopher://host/page", "https://example.com/"}; !slices.Equal(opened, want) {
		t.Errorf("opened %q, want %q", opened, want)
	}
}
