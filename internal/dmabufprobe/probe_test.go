package dmabufprobe

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/dmabuf"
)

// A probe takes the modifiers the compositor imports one at a time,
// in its order: linear first, as a render target where the device
// renders it and a plain buffer where it only exports it, then the ones
// the device renders, then the ones it only exports; modifiers the
// device has neither way, and a compositor naming none, leave nothing.
func TestLayoutsKeepTheCompositorsModifiers(t *testing.T) {
	const tiledA, tiledB, tiledC, foreign = 0x300000000606015, 0x300000000606014, 0x300000000606012, 0x100000000000001
	linear := dmabuf.ModifierLinear
	for name, c := range map[string]struct {
		compositor, rendered, exported []uint64
		want                           []Layout
	}{
		"linear only exported, tilings rendered": {
			[]uint64{tiledB, linear, foreign, tiledA},
			[]uint64{tiledA, tiledB},
			[]uint64{tiledA, tiledB, linear},
			[]Layout{{Modifier: linear}, {Modifier: tiledB, Render: true}, {Modifier: tiledA, Render: true}},
		},
		"linear rendered": {
			[]uint64{linear, tiledA},
			[]uint64{linear, tiledA},
			[]uint64{linear, tiledA},
			[]Layout{{Modifier: linear, Render: true}, {Modifier: tiledA, Render: true}},
		},
		"a tiling only exported": {
			[]uint64{tiledC, tiledA},
			[]uint64{tiledA},
			[]uint64{tiledA, tiledC},
			[]Layout{{Modifier: tiledA, Render: true}, {Modifier: tiledC}},
		},
		"the compositor takes no linear": {
			[]uint64{tiledA},
			[]uint64{linear, tiledA},
			[]uint64{linear, tiledA},
			[]Layout{{Modifier: tiledA, Render: true}},
		},
		"nothing in common":         {[]uint64{foreign}, []uint64{tiledA}, []uint64{tiledA, linear}, nil},
		"the compositor names none": {nil, []uint64{tiledA}, []uint64{tiledA}, nil},
	} {
		if got := Layouts(c.compositor, c.rendered, c.exported); !slices.Equal(got, c.want) {
			t.Errorf("%s: Layouts = %+v, want %+v", name, got, c.want)
		}
	}
}

// First returns the first layout try accepts, without trying the rest;
// declining every one names each modifier's reason, and no layouts at
// all is ErrNoLayout rather than a zero layout.
func TestFirstStopsAtTheFirstAccepted(t *testing.T) {
	layouts := []Layout{{Modifier: 1}, {Modifier: 2, Render: true}, {Modifier: 3}}
	var tried []uint64
	v, l, err := First(layouts, func(l Layout) (string, error) {
		tried = append(tried, l.Modifier)
		if l.Modifier == 2 {
			return "two", nil
		}
		return "", errors.New("refused")
	})
	if err != nil || v != "two" || l != layouts[1] || !slices.Equal(tried, []uint64{1, 2}) {
		t.Errorf("First = %q %+v %v after trying %v, want two at modifier 2 after 1 and 2", v, l, err, tried)
	}
	_, _, err = First(layouts, func(l Layout) (int, error) { return 0, errors.New("refused") })
	if err == nil || !strings.Contains(err.Error(), "modifier 0x1: refused") || !strings.Contains(err.Error(), "modifier 0x3: refused") {
		t.Errorf("all declined: %v, want every modifier's reason", err)
	}
	if _, _, err := First(nil, func(Layout) (int, error) { return 1, nil }); !errors.Is(err, ErrNoLayout) {
		t.Errorf("no layouts: %v, want ErrNoLayout", err)
	}
}
