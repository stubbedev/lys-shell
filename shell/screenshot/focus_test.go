package screenshot

import (
	"image"
	"slices"
	"testing"

	"github.com/stubbedev/lys-shell/service/hyprland"
	"github.com/stubbedev/lys-shell/service/sway"
)

func hyprClient(ws, focus int, floating bool, at, size [2]int) hyprland.Client {
	c := hyprland.Client{Floating: floating, At: at, Size: size, Mapped: true, FocusHistoryID: focus}
	c.Workspace.ID = ws
	return c
}

// Hyprland's windows on screen come topmost first: a shown special
// workspace's, then floating ones, then tiled, each by focus history;
// unmapped, hidden, sizeless and off-screen workspaces' are left out.
func TestHyprlandWindows(t *testing.T) {
	var mon hyprland.Monitor
	mon.ActiveWS.ID, mon.SpecialWS.ID = 1, -98
	tiledOld := hyprClient(1, 4, false, [2]int{0, 0}, [2]int{100, 100})
	tiledNew := hyprClient(1, 1, false, [2]int{100, 0}, [2]int{100, 100})
	floating := hyprClient(1, 3, true, [2]int{50, 50}, [2]int{20, 20})
	special := hyprClient(-98, 9, false, [2]int{10, 10}, [2]int{300, 300})
	unmapped := hyprClient(1, 0, true, [2]int{0, 0}, [2]int{5, 5})
	unmapped.Mapped = false
	hidden := hyprClient(1, 0, true, [2]int{0, 0}, [2]int{5, 5})
	hidden.Hidden = true
	elsewhere := hyprClient(2, 0, true, [2]int{0, 0}, [2]int{5, 5})
	sizeless := hyprClient(1, 0, true, [2]int{0, 0}, [2]int{0, 5})
	got := hyprlandWindows([]hyprland.Monitor{mon}, []hyprland.Client{tiledOld, unmapped, tiledNew, hidden, floating, elsewhere, special, sizeless})
	want := []image.Rectangle{
		image.Rect(10, 10, 310, 310),
		image.Rect(50, 50, 70, 70),
		image.Rect(100, 0, 200, 100),
		image.Rect(0, 0, 100, 100),
	}
	if !slices.Equal(got, want) {
		t.Errorf("hyprland windows =\n%v\nwant\n%v", got, want)
	}
}

// Sway's visible windows come floating first, each kind latest in the
// tree first; hidden and empty ones are left out.
func TestSwayWindows(t *testing.T) {
	wins := []sway.Window{
		{Visible: true, Rect: image.Rect(0, 0, 100, 100)},
		{Visible: true, Floating: true, Rect: image.Rect(10, 10, 20, 20)},
		{Visible: false, Rect: image.Rect(0, 0, 5, 5)},
		{Visible: true, Rect: image.Rect(100, 0, 200, 100)},
		{Visible: true, Rect: image.Rectangle{}},
	}
	want := []image.Rectangle{image.Rect(10, 10, 20, 20), image.Rect(100, 0, 200, 100), image.Rect(0, 0, 100, 100)}
	if got := swayWindows(wins); !slices.Equal(got, want) {
		t.Errorf("sway windows = %v, want %v", got, want)
	}
}
