package main

import (
	"github.com/stubbedev/lys-shell/internal/cli"
	"github.com/stubbedev/lys-shell/internal/lockcli"
)

// lockCommand is app.rs's Lock.
func lockCommand() *cli.Command { return lockcli.Command("lock") }
