package search

import (
	"github.com/stubbedev/lys-shell/service/launcher"
)

// Env is how providers reach out of the process: a seam, so a test's
// launches and opens land in a list instead of on the desktop.
type Env struct {
	// Spawn runs an argv detached; nothing waits for it.
	Spawn func(argv []string)
	// SpawnShell runs a shell line detached.
	SpawnShell func(line string)
	// Terminal is the terminal emulator command ("" autodetects).
	Terminal string
}

// RealEnv spawns for real.
func RealEnv(terminal string) Env {
	return Env{Spawn: launcher.RunArgv, SpawnShell: launcher.RunShell, Terminal: terminal}
}

// openURI hands a path or URL to the desktop's default handler.
func (env Env) openURI(uri string) { env.Spawn([]string{"xdg-open", uri}) }

// inTerminal runs argv inside the terminal.
func (env Env) inTerminal(argv ...string) {
	env.Spawn(append([]string{launcher.DetectTerminal(env.Terminal), "-e"}, argv...))
}
