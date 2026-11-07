package search

import (
	"context"
	"image"
	"testing"

	"github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/modes"
)

type picturedMode struct {
	scriptedMode
	pictured []bool
}

func (m *picturedMode) Load(context.Context) launcher.ModeState {
	items := make([]launcher.Item, len(m.pictured))
	for i := range items {
		items[i] = launcher.NewItem(string(rune('a' + i)))
	}
	return launcher.ModeState{Items: items}
}

func (m *picturedMode) RowPreview(index uint32) (launcher.Preview, bool) {
	if !m.pictured[index] {
		return launcher.Preview{}, false
	}
	return launcher.Preview{Image: func(context.Context) (image.Image, error) {
		return image.NewRGBA(image.Rect(0, 0, int(index)+1, 1)), nil
	}}, true
}

func modeEntries(t *testing.T, mode launcher.Mode) []Entry {
	t.Helper()
	var got []Entry
	ModeLevel(mode, "mode").Providers[0].Load(context.Background(), func(e []Entry) { got = e })
	return got
}

func TestAModeThatPicturesItsRowsGivesThemAnImagePreview(t *testing.T) {
	got := modeEntries(t, &picturedMode{pictured: []bool{true, false, true}})
	if len(got) != 3 {
		t.Fatalf("entries %+v", got)
	}
	if got[1].Preview.Any() {
		t.Error("a row the mode does not picture has a preview")
	}
	for _, i := range []int{0, 2} {
		content, err := launcher.LoadPreview(context.Background(), got[i].Preview, nil)
		if err != nil || content.Image == nil || content.Image.Bounds().Dx() != i+1 {
			t.Errorf("row %d previewed %+v, %v; want its own picture", i, content, err)
		}
	}
}

func TestAModeWithoutPicturesHasNoPreviews(t *testing.T) {
	for _, e := range modeEntries(t, &scriptedMode{}) {
		if e.Preview.Any() {
			t.Errorf("%q has a preview", e.Title)
		}
	}
}

func TestEveryWindowRowCanBePreviewed(t *testing.T) {
	w := NewWindows(&fakeWindows{list: []modes.WindowInfo{{ID: "2", Title: "there", Class: "Firefox"}}})
	var got []Entry
	w.Load(context.Background(), func(e []Entry) { got = e })
	if len(got) != 1 || got[0].Preview.Image == nil {
		t.Fatalf("window entries %+v, want a captured preview", got)
	}
}
