package filechooser

import (
	"os"
	"testing"

	"github.com/stubbedev/lys-desktop/testhome"
)

// TestMain runs the package against a scratch home: building a theme
// or a config reads, and the shell scaffolds, the user's config dir.
// Run with fakeThumbnailerArg first, the test binary is a thumbnailer
// instead.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == fakeThumbnailerArg {
		os.Exit(fakeThumbnailer(os.Args[2:]))
	}
	testhome.Run(m)
}

// testReader lists folders for the tests, as the host's reader does.
var testReader = newDirReader()
