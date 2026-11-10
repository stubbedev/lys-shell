package modes

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stubbedev/lys-desktop/opener/openertest"

	"github.com/stubbedev/lys-shell/internal/pango"
	"github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/launchertest"
)

func wantNoTerminalMessage(t *testing.T, a launcher.Action, names ...string) {
	t.Helper()
	_, err := launcher.DetectTerminal("")
	none, ok := errors.AsType[*launcher.NoTerminalError](err)
	if !ok {
		t.Fatalf("the test's environment has a terminal: %v", err)
	}
	reload, ok := a.(launcher.ActionReload)
	if !ok || reload.State.Message == nil {
		t.Fatalf("action = %#v, want the panel kept open with a message", a)
	}
	for _, want := range append(names, pango.Escape(none.Error())) {
		if !strings.Contains(*reload.State.Message, want) {
			t.Errorf("message %q does not say %q", *reload.State.Message, want)
		}
	}
}

func wantClosed(t *testing.T, a launcher.Action) {
	t.Helper()
	if _, ok := a.(launcher.ActionClose); !ok {
		t.Errorf("action = %#v, want the panel closed", a)
	}
}

func terminalApp(t *testing.T) *Drun {
	t.Helper()
	dir := t.TempDir()
	body := "[Desktop Entry]\nType=Application\nName=Tui\nExec=tui %f\nTerminal=true\n"
	if err := os.WriteFile(filepath.Join(dir, "tui.desktop"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CURRENT_DESKTOP", "")
	d := NewDrun(DefaultDrunConfig(), nil)
	d.dirs = []string{dir}
	return d
}

func TestRunInATerminalWithoutOneStaysAndSaysSo(t *testing.T) {
	launchertest.OnlyOnPath(t, "mytool")
	h, err := launcher.OpenHistoryAt(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()
	r := NewRun(DefaultRunConfig(), h)
	if st := r.Load(bg); len(st.Items) != 1 {
		t.Fatalf("items = %+v, want the one program on the path", st.Items)
	}
	a := r.Activate(bg, launcher.RowTarget(0), launcher.ActivateAlt{}, "")
	wantNoTerminalMessage(t, a, "mytool")
	if st := a.(launcher.ActionReload).State; len(st.Items) != 1 {
		t.Errorf("the list after the refusal = %+v, want it kept", st.Items)
	}
	if got, _ := h.Recent("run"); len(got) != 0 {
		t.Errorf("history = %v, want nothing recorded of a command that did not run", got)
	}
}

func TestRunInATerminalUsesTheConfiguredOne(t *testing.T) {
	desktop := openertest.New(t)
	term := desktop.Tool(t, "term")
	cfg := DefaultRunConfig()
	cfg.Terminal = "term"
	r := NewRun(cfg, nil)
	r.commands = []string{"mytool"}
	wantClosed(t, r.Activate(bg, launcher.RowTarget(0), launcher.ActivateAlt{}, ""))
	if got := term.Ran(t); got != "-e mytool|" {
		t.Errorf("the terminal ran with %q", got)
	}
}

func TestSSHWithoutATerminalStaysAndSaysSo(t *testing.T) {
	launchertest.OnlyOnPath(t)
	t.Setenv("HOME", t.TempDir())
	h, err := launcher.OpenHistoryAt(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()
	s := NewSSH(DefaultSSHConfig(), h)
	wantNoTerminalMessage(t, s.Activate(bg, launcher.NoRow, launcher.ActivateCustom{Text: "build.example"}, ""), "build.example")
	if got, _ := h.Recent("ssh"); len(got) != 0 {
		t.Errorf("history = %v, want nothing recorded of a host not connected to", got)
	}
}

func TestSSHConnectsInTheConfiguredTerminal(t *testing.T) {
	desktop := openertest.New(t)
	term := desktop.Tool(t, "term")
	cfg := DefaultSSHConfig()
	cfg.Terminal = "term"
	wantClosed(t, NewSSH(cfg, nil).Activate(bg, launcher.NoRow, launcher.ActivateCustom{Text: "build.example"}, ""))
	if got := term.Ran(t); got != "-e ssh build.example|" {
		t.Errorf("the terminal ran with %q", got)
	}
}

func TestDrunTerminalAppWithoutATerminalStaysAndSaysSo(t *testing.T) {
	launchertest.OnlyOnPath(t, "tui")
	d := terminalApp(t)
	if st := d.Load(bg); len(st.Items) != 1 {
		t.Fatalf("items = %+v, want the terminal app", st.Items)
	}
	a := d.Activate(bg, launcher.RowTarget(0), launcher.ActivateDefault{}, "")
	wantNoTerminalMessage(t, a, "tui.desktop")
	if st := a.(launcher.ActionReload).State; len(st.Items) != 1 || !st.MarkupRows {
		t.Errorf("the list after the refusal = %+v, want it kept", st)
	}
}

func TestDrunTerminalAppRunsInTheConfiguredTerminal(t *testing.T) {
	desktop := openertest.New(t)
	desktop.Tool(t, "tui")
	term := desktop.Tool(t, "term")
	d := terminalApp(t)
	d.cfg.Terminal = "term"
	if st := d.Load(bg); len(st.Items) != 1 {
		t.Fatalf("items = %+v, want the terminal app", st.Items)
	}
	wantClosed(t, d.Activate(bg, launcher.RowTarget(0), launcher.ActivateDefault{}, ""))
	if got := term.Ran(t); got != "-e tui|" {
		t.Errorf("the terminal ran with %q, want the Exec line without its field code", got)
	}
}

func TestFileBrowserTerminalHandlerWithoutATerminalStaysAndSaysSo(t *testing.T) {
	desktop := openertest.New(t)
	files := desktop.Tool(t, "files")
	desktop.Write(t, filepath.Join("data", "applications", "files.desktop"),
		"[Desktop Entry]\nType=Application\nName=files\nExec=files %u\nTerminal=true\nMimeType=inode/directory;\n")
	t.Setenv("PATH", filepath.Join(desktop.Root, "bin"))
	t.Setenv("TERMINAL", "")
	dir := filepath.Join(t.TempDir(), "a dir")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultFileBrowserConfig()
	cfg.Directory = filepath.Dir(dir)
	wantNoTerminalMessage(t, NewFileBrowser(cfg).open(dir), "files.desktop")
	if files.HasRun() {
		t.Error("the handler ran without a terminal")
	}
}
