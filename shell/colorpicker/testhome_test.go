package colorpicker

import (
	"testing"

	"github.com/stubbedev/lys-lib/testhome"
)

func TestMain(m *testing.M) { testhome.Run(m) }

func TestTheTestsReachNoHostSession(t *testing.T) { testhome.RequireNoHostSession(t) }
