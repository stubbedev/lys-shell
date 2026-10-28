package apptheme

import (
	"testing"

	"github.com/stubbedev/lys-shell/testhome"
)

// TestMain runs the package against a scratch home: building a theme
// or a config reads, and the shell scaffolds, the user's config dir.
func TestMain(m *testing.M) { testhome.Run(m) }
