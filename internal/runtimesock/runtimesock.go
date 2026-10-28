// Package runtimesock places lys-shell's sockets in its runtime
// directory, $XDG_RUNTIME_DIR/lys-shell.
package runtimesock

import (
	"fmt"
	"path/filepath"

	"github.com/stubbedev/lys-lib/xdg"

	"github.com/stubbedev/lys-shell/internal/version"
)

// Path is the socket name in env's lys-shell runtime directory. Without
// an absolute XDG_RUNTIME_DIR it is an error naming the variable: the
// spec defines no fallback, and a socket in a shared directory such as
// /tmp is any local user's to squat or connect to.
func Path(env xdg.Env, name string) (string, error) {
	dir, ok := env.RuntimeDir(version.Name)
	if !ok {
		return "", fmt.Errorf("%s socket: XDG_RUNTIME_DIR is %q, not an absolute path", name, env("XDG_RUNTIME_DIR"))
	}
	return filepath.Join(dir, name+".sock"), nil
}
