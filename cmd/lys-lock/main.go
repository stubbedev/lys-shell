// Command lys-lock is a single-token `lys-shell lock` (lys-lock.rs):
// the same dispatch as its own executable, for contexts that exec one
// token without splitting on whitespace - a systemd ExecStart=, an idle
// daemon, a compositor spawn - so locking needs no wrapper script.
package main

import (
	"os"

	"github.com/stubbedev/lys-shell/internal/cli"
	"github.com/stubbedev/lys-shell/internal/lockcli"
	"github.com/stubbedev/lys-shell/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], cli.StdOutput()))
}

// run is main without the process exit.
func run(args []string, out cli.Output) int {
	root := lockcli.Command("lys-lock")
	root.Version = version.Version
	return cli.Run(root, args, out)
}
