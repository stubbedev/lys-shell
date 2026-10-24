// Command lys-settings is the settings GUI (crates/lys-settings):
// every change a runtime override, saved to runtime.toml as it lands.
package main

import (
	"fmt"
	"os"

	"github.com/stubbedev/lys-shell/shell/settings"
)

func main() {
	if err := settings.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
