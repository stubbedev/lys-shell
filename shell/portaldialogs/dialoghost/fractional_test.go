package dialoghost

import (
	"testing"

	"github.com/stubbedev/lys-desktop/shell/treetest"
	"github.com/stubbedev/lys-ui/widget"
)

func TestADialogFileIconPaintsExactlyAtAFractionalScale(t *testing.T) {
	for _, sc := range treetest.FractionalScales {
		t.Run(sc.Name, func(t *testing.T) {
			path := treetest.GreenSVG(t)
			center := treetest.FractionalFirstPaint(t, sc.Num, func(post func(func())) *widget.Icon {
				h := &Dialogs{d: Deps{Invoke: post}}
				return h.icon(path, 16)
			})
			treetest.ExpectGreen(t, sc.Name, center)
		})
	}
}
