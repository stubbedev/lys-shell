package search

import (
	"github.com/stubbedev/lys-desktop/i18n"

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
	Open func(target string) error
	// Terminal is the terminal emulator command ("" autodetects).
	Terminal string
}

// RealEnv spawns and opens for real.
func RealEnv(terminal string) Env {
	return Env{
		Spawn:      launcher.RunArgv,
		SpawnShell: launcher.RunShell,
		Open:       func(target string) error { return launcher.Open(target, terminal, "") },
		Terminal:   terminal,
	}
}

func failed(what string, err error) Outcome {
	return Stay{Message: i18n.T("launcher-failed", i18n.Str("what", what), i18n.Str("error", err.Error()))}
}

func (env Env) open(what, target string) Outcome {
	if err := env.Open(target); err != nil {
		return failed(what, err)
	}
	return Close{}
}

// inTerminal runs argv inside the terminal and closes the panel; with no
// terminal to run it in the panel stays and says why what failed.
func (env Env) inTerminal(what string, argv ...string) Outcome {
	in, err := launcher.InTerminal(env.Terminal, argv...)
	if err != nil {
		return failed(what, err)
	}
	env.Spawn(in)
	return Close{}
}
