package notifyui

import (
	"testing"

	"github.com/stubbedev/lys-desktop/shell/treetest"
	"github.com/stubbedev/lys-ui/widget"
)

func TestANotificationFileIconPaintsExactlyAtAFractionalScale(t *testing.T) {
	for _, sc := range treetest.FractionalScales {
		t.Run(sc.Name, func(t *testing.T) {
			path := treetest.GreenSVG(t)
			center := treetest.FractionalFirstPaint(t, sc.Num, func(post func(func())) *widget.Icon {
				return NewIcon(Icon{File: path}, 16, 0, post, nil)
			})
			treetest.ExpectGreen(t, sc.Name, center)
		})
	}
}
