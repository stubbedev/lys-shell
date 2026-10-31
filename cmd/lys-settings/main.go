// Command lys-settings is the settings GUI (crates/lys-settings):
// every change a runtime override, saved to runtime.yaml as it lands.
package main

import (
	"os"

	"github.com/stubbedev/lys-shell/internal/cli"
	"github.com/stubbedev/lys-shell/internal/version"
	"github.com/stubbedev/lys-shell/shell/settings"
)

func main() {
	os.Exit(run(os.Args[1:], cli.StdOutput()))
}

// run is main without the process exit: the arguments are parsed
// before the window connects to the session, so -h, --help and
// --version print and exit, and anything else is a usage error.
func run(args []string, out cli.Output) int {
	return cli.Run(&cli.Command{
		Name:    "lys-settings",
		About:   "Configure the lys-shell desktop shell",
		Version: version.Version,
		Run:     func(*cli.Matches) error { return settings.Run() },
	}, args, out)
}
