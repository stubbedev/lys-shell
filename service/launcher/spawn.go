package launcher

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/stubbedev/lys-desktop/desktopentry"
	"github.com/stubbedev/lys-desktop/opener"
	"github.com/stubbedev/lys-desktop/spawn"
)

// RunShell spawns `sh -c command`, detached (spawn.rs run_shell).
// Failures are logged, not returned: the surface has already closed by
// the time a child could fail. A blank command runs nothing.
func RunShell(command string) {
	if strings.TrimSpace(command) == "" {
		return
	}
	if err := spawn.Detached(exec.Command("sh", "-c", command), strings.Fields(command)[0]); err != nil { //nolint:gosec // the user's own launcher command
		log.Printf("launcher: spawn %q failed: %v", command, err)
	}
}

// RunArgv spawns an argv directly, no shell, detached (run_argv).
func RunArgv(argv []string) {
	if len(argv) == 0 {
		return
	}
	if err := spawn.Detached(exec.Command(argv[0], argv[1:]...), argv[0]); err != nil { //nolint:gosec // an argv the user configured
		log.Printf("launcher: spawn %q failed: %v", argv, err)
	}
}

// terminalFallbacks are tried in order when neither the config nor
// $TERMINAL names a terminal.
var terminalFallbacks = []string{
	"foot", "kitty", "alacritty", "wezterm", "ghostty", "gnome-terminal", "konsole", "xterm",
}

// NoTerminalError is no terminal emulator to run a command in: the
// launcher's terminal setting names none, $TERMINAL is unset, and none of
// Candidates is on $PATH.
type NoTerminalError struct {
	Candidates []string
}

func (e *NoTerminalError) Error() string {
	return "launcher: no terminal emulator: launcher.terminal names none, $TERMINAL is unset, and none of " +
		strings.Join(e.Candidates, ", ") + " is on $PATH"
}

// DetectTerminal resolves the terminal emulator: the configured one,
// then $TERMINAL, then the first fallback on $PATH; a *NoTerminalError
// when there is none.
func DetectTerminal(configured string) (string, error) {
	if t := strings.TrimSpace(configured); t != "" {
		return t, nil
	}
	if t := strings.TrimSpace(os.Getenv("TERMINAL")); t != "" {
		return t, nil
	}
	for _, candidate := range terminalFallbacks {
		if InPath(candidate) {
			return candidate, nil
		}
	}
	return "", &NoTerminalError{Candidates: terminalFallbacks}
}

// InTerminal is argv run inside the terminal DetectTerminal resolves
// from configured, as `<terminal> -e <argv>`.
func InTerminal(configured string, argv ...string) ([]string, error) {
	terminal, err := DetectTerminal(configured)
	if err != nil {
		return nil, err
	}
	return append([]string{terminal, "-e"}, argv...), nil
}

// RenderInTerminal renders template with values, and with {terminal} as
// DetectTerminal resolves it from configured. The terminal is resolved
// only when the template names it: a template that names a terminal of
// its own needs none found.
func RenderInTerminal(template, configured string, values map[string]string) (string, error) {
	var missing error
	rendered := Render(template, func(key string) (string, bool) {
		if key != "terminal" {
			return Values(values)(key)
		}
		terminal, err := DetectTerminal(configured)
		if err != nil {
			missing = err
			return "", false
		}
		return terminal, true
	})
	if missing != nil {
		return "", missing
	}
	return rendered, nil
}

// InPath reports whether program is a regular file in some $PATH
// directory.
func InPath(program string) bool {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if st, err := os.Stat(filepath.Join(dir, program)); err == nil && st.Mode().IsRegular() { //nolint:gosec // a PATH lookup
			return true
		}
	}
	return false
}

// Open opens target, an absolute path or a URI, with the user's default
// app for its mimetype, handing the app token to raise its window ("" hands
// none). A Terminal=true app runs inside terminal as DetectTerminal
// resolves it, which is asked only for such an app: with no terminal the
// error wraps its *NoTerminalError.
func Open(target, terminal, token string) error {
	return opener.Open(target, desktopentry.LaunchContext{
		ActivationToken: token,
		Terminal:        func() (string, error) { return DetectTerminal(terminal) },
	})
}
