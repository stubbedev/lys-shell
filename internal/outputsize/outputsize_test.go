package outputsize

import (
	"image"
	"slices"
	"testing"

	"github.com/stubbedev/lys-ui/app"
)

func TestDevice(t *testing.T) {
	for name, tc := range map[string]struct {
		out  app.Output
		want image.Point
	}{
		"normal":       {app.Output{ModeW: 3840, ModeH: 2160, Scale: 2}, image.Pt(3840, 2160)},
		"turned 90":    {app.Output{ModeW: 1920, ModeH: 1080, Transform: 1}, image.Pt(1080, 1920)},
		"flipped 270":  {app.Output{ModeW: 1920, ModeH: 1080, Transform: 7}, image.Pt(1080, 1920)},
		"upside down":  {app.Output{ModeW: 1920, ModeH: 1080, Transform: 2}, image.Pt(1920, 1080)},
		"flipped only": {app.Output{ModeW: 1920, ModeH: 1080, Transform: 4}, image.Pt(1920, 1080)},
	} {
		if got, ok := Device(&tc.out); !ok || got != tc.want {
			t.Errorf("%s: %v %v, want %v", name, got, ok, tc.want)
		}
	}
	for _, out := range []app.Output{{}, {ModeW: 1920}, {ModeH: 1080, Transform: 1}} {
		if got, ok := Device(&out); ok {
			t.Errorf("an output without a mode (%+v) has size %v", out, got)
		}
	}
}

func TestDevices(t *testing.T) {
	outputs := []*app.Output{
		{ModeW: 1920, ModeH: 1080, Transform: 1},
		{},
		{ModeW: 3840, ModeH: 2160},
	}
	got := Devices(outputs)
	if want := []image.Point{{1080, 1920}, {3840, 2160}}; !slices.Equal(got, want) {
		t.Errorf("Devices = %v, want %v: the turned output turned, the modeless one left out", got, want)
	}
	if got := Devices(nil); len(got) != 0 {
		t.Errorf("no outputs: %v", got)
	}
}
