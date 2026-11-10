package search

import (
	"log"

	"github.com/stubbedev/lys-shell/service/launcher"
)

// Env is how providers reach out of the process: a seam, so a test's
// launches and opens land in a list instead of on the desktop.
type Env struct {
	// Spawn runs an argv detached; nothing waits for it.
	Spawn func(argv []string)
	// SpawnShell runs a shell line detached.
	SpawnShell func(line string)
	// Open hands an absolute path or a URI to the user's default app.
	Open func(target string)
	// Terminal is the terminal emulator command ("" autodetects).
	Terminal string
}

// RealEnv spawns and opens for real, a failed open logged.
func RealEnv(terminal string) Env {
	return Env{
		Spawn:      launcher.RunArgv,
		SpawnShell: launcher.RunShell,
		Open: func(target string) {
			if err := launcher.Open(target, terminal, ""); err != nil {
				log.Printf("launcher: %v", err)
			}
		},
		Terminal: terminal,
	}
}

// inTerminal runs argv inside the terminal.
func (env Env) inTerminal(argv ...string) {
	env.Spawn(append([]string{launcher.DetectTerminal(env.Terminal), "-e"}, argv...))
}
