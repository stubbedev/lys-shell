package osd

import (
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/service/mpris"
)

func track(title string, requested bool) mpris.TrackChange {
	return mpris.TrackChange{Player: mpris.Player{Title: title, Artist: "Band"}, Requested: requested}
}

func TestOsdMediaFiltersTrackChanges(t *testing.T) {
	for _, tc := range []struct {
		mode      config.OsdMedia
		requested bool
		want      bool
	}{
		{config.OsdMediaRequested, true, true},
		{config.OsdMediaRequested, false, false},
		{config.OsdMediaAlways, false, true},
		{config.OsdMediaAlways, true, true},
		{config.OsdMediaNever, true, false},
		{config.OsdMediaNever, false, false},
	} {
		cfg := quietConfig()
		cfg.Osd.Media = tc.mode
		h := readyHarness(t, cfg)
		h.osd.Show(NowPlaying(track("Song", tc.requested)))
		if got := len(h.opened) == 1; got != tc.want {
			t.Errorf("osd.media %s, requested %v: shown %v, want %v", tc.mode, tc.requested, got, tc.want)
		}
	}
	cfg := quietConfig()
	cfg.Osd.Media = config.OsdMediaNever
	h := readyHarness(t, cfg)
	h.osd.Show(Slider("Speakers", "", 40, false))
	if len(h.opened) != 1 {
		t.Error("osd.media never also silenced the volume slider")
	}
}

func TestNowPlayingShowsTitleArtistAndArt(t *testing.T) {
	h := readyHarness(t, quietConfig())
	h.osd.Show(NowPlaying(track("Song", true)))
	f := h.osd.face
	if !f.mediaHeader.Visible() || f.sliderHeader.Visible() || f.toggleHeader.Visible() || f.bar.Visible() {
		t.Fatal("a track change did not show the media header alone")
	}
	if f.mediaTitle.Text() != "Song" || f.mediaArtist.Text() != "Band" {
		t.Errorf("media header reads %q / %q", f.mediaTitle.Text(), f.mediaArtist.Text())
	}
	if len(f.art.Children()) != 1 {
		t.Error("a track without art shows no placeholder")
	}

	h.osd.Show(NowPlaying(mpris.TrackChange{Player: mpris.Player{Title: "Next", ArtURL: "file:///nonexistent/cover.png"}, Requested: true}))
	if f.mediaArtist.Visible() {
		t.Error("an empty artist line stayed visible")
	}
	if len(f.art.Children()) != 1 {
		t.Error("the art box does not hold exactly the cover")
	}
	h.osd.Show(Slider("Speakers", "", 40, false))
	if f.mediaHeader.Visible() || !f.sliderHeader.Visible() {
		t.Error("a slider after a track change kept the media header")
	}
}
