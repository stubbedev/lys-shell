package launcher

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/stubbedev/lys-shell/internal/spawn"
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

// DetectTerminal resolves the terminal emulator: the configured one,
// then $TERMINAL, then the first fallback on $PATH, else xterm.
func DetectTerminal(configured string) string {
	if t := strings.TrimSpace(configured); t != "" {
		return t
	}
	if t := strings.TrimSpace(os.Getenv("TERMINAL")); t != "" {
		return t
	}
	for _, candidate := range terminalFallbacks {
		if InPath(candidate) {
			return candidate
		}
	}
	return "xterm"
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
