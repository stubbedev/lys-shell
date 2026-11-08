package bar

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/fakeclock"
	"github.com/stubbedev/lys-desktop/i18n"
	"github.com/stubbedev/lys-desktop/service/mpris"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/service/lyrics"
)

func fakeLRCLIB(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

func syncedLRCLIB(lines int) string {
	var lrc strings.Builder
	for i := range lines {
		fmt.Fprintf(&lrc, "[%02d:%02d.00]line %d\\n", 2*i/60, 2*i%60, i)
	}
	return `{"instrumental":false,"plainLyrics":"x","syncedLyrics":"` + lrc.String() + `"}`
}

var lyricsPlayer = mpris.Player{
	BusName: "org.mpris.MediaPlayer2.vlc", Identity: "VLC", Title: "Song", Artist: "Band", Album: "Record",
	State: mpris.StatePaused, Length: 3 * time.Minute,
}

func lyricsDropdown(t *testing.T, enabled bool, server *httptest.Server, dir string) *mediaView {
	t.Helper()
	cfg := config.Defaults()
	cfg.Media.Lyrics = enabled
	ctx := newTestContext(t, cfg)
	ctx.Media = newFakeMedia(lyricsPlayer)
	ctx.Lyrics = lyrics.New(server.URL, dir)
	v := mediaDropdown(ctx).(*mediaView)
	t.Cleanup(func() {
		headlessLoop.Lock()
		defer headlessLoop.Unlock()
		v.dropdownClosed()
	})
	return v
}

func loopRead[T any](read func() T) T {
	headlessLoop.Lock()
	defer headlessLoop.Unlock()
	return read()
}

func waitLyricsPage(t *testing.T, v *mediaView, page string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for loopRead(func() string { return v.lyrics.Visible() }) != page {
		if time.Now().After(deadline) {
			t.Fatalf("the lyrics panel shows %q (%q), want %q", v.lyrics.Visible(), v.lyrics.status.Text(), page)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func currentRows(v *mediaView) []int {
	var out []int
	for i, row := range v.lyrics.rows {
		if row.HasClass("current") {
			out = append(out, i)
		}
	}
	return out
}

func TestSyncedLyricsFollowThePosition(t *testing.T) {
	server, requests := fakeLRCLIB(t, http.StatusOK, syncedLRCLIB(40))
	v := lyricsDropdown(t, true, server, t.TempDir())
	if !loopRead(func() bool { return widget.IsVisible(v.lyricsButton) }) {
		t.Fatal("the lyrics button is hidden with lyrics on")
	}
	loopRead(func() bool { v.lyricsButton.OnClick(); return true })
	waitLyricsPage(t, v, "lines")

	headlessLoop.Lock()
	defer headlessLoop.Unlock()
	if v.panel.Visible() != "lyrics" || !v.lyricsButton.HasClass("active") {
		t.Fatalf("the panel shows %q, want the lyrics", v.panel.Visible())
	}
	if len(v.lyrics.rows) != 40 || v.lyrics.rows[3].Text() != "line 3" {
		t.Fatalf("%d lyric rows", len(v.lyrics.rows))
	}
	v.Measure(widget.Column, 400)
	v.Arrange(render.Rect{W: 400, H: 500})

	v.setPosition(5 * time.Second)
	if got := currentRows(v); len(got) != 1 || got[0] != 2 {
		t.Fatalf("at 0:05 the current rows are %v, want line 2", got)
	}

	before := make([]bool, len(v.lyrics.rows))
	children := v.lyrics.lines.Children()
	for i, row := range v.lyrics.rows {
		before[i] = row.HasClass("current")
	}
	v.setPosition(61 * time.Second)
	restyled := 0
	for i, row := range v.lyrics.rows {
		if row.HasClass("current") != before[i] {
			restyled++
		}
	}
	if restyled != 2 {
		t.Errorf("a line change restyled %d rows, want the old and the new current line only", restyled)
	}
	after := v.lyrics.lines.Children()
	if len(after) != len(children) || after[0] != children[0] || after[39] != children[39] {
		t.Error("a line change rebuilt the lyric rows")
	}
	if got := currentRows(v); len(got) != 1 || got[0] != 30 {
		t.Errorf("at 1:01 the current rows are %v, want line 30", got)
	}
	v.Arrange(render.Rect{W: 400, H: 500})
	if _, y := v.lyrics.scroll.Offset(); y <= 0 {
		t.Errorf("the scroll stayed at %d with line 30 current, want it scrolled to the line", y)
	}

	v.refresh()
	v.refresh()
	if n := requests.Load(); n != 1 {
		t.Errorf("%d LRCLIB requests for one track, want 1", n)
	}
}

func TestMissingLyricsShowNoLyricsOnce(t *testing.T) {
	server, requests := fakeLRCLIB(t, http.StatusNotFound, `{"code":404}`)
	dir := t.TempDir()
	for range 2 {
		v := lyricsDropdown(t, true, server, dir)
		loopRead(func() bool { v.toggleLyrics(); return true })
		waitLyricsPage(t, v, "status")
		deadline := time.Now().Add(5 * time.Second)
		for loopRead(v.lyrics.status.Text) != i18n.T("dropdown-media-lyrics-none") {
			if time.Now().After(deadline) {
				t.Fatalf("status %q, want No lyrics", loopRead(v.lyrics.status.Text))
			}
			time.Sleep(2 * time.Millisecond)
		}
	}
	if n := requests.Load(); n != 1 {
		t.Errorf("%d requests for a track without lyrics, want the miss cached after 1", n)
	}
}

func TestLyricsOffAsksNothing(t *testing.T) {
	server, requests := fakeLRCLIB(t, http.StatusOK, syncedLRCLIB(3))
	v := lyricsDropdown(t, false, server, t.TempDir())
	headlessLoop.Lock()
	if widget.IsVisible(v.lyricsButton) {
		t.Error("the lyrics button shows with lyrics off")
	}
	v.toggleLyrics()
	v.refresh()
	v.setPosition(time.Second)
	if v.panel.Visible() != "art" || v.showLyrics {
		t.Errorf("with lyrics off the panel shows %q", v.panel.Visible())
	}
	headlessLoop.Unlock()
	if n := requests.Load(); n != 0 {
		t.Errorf("%d LRCLIB requests with lyrics off, want none", n)
	}
}

func TestPlayingLyricsAdvanceOnTheLineTime(t *testing.T) {
	server, _ := fakeLRCLIB(t, http.StatusOK, syncedLRCLIB(5))
	v := lyricsDropdown(t, true, server, t.TempDir())
	clock := fakeclock.New()
	loopRead(func() bool {
		v.lyrics.after = clock.AfterFunc
		v.current.State = mpris.StatePlaying
		v.toggleLyrics()
		return true
	})
	waitLyricsPage(t, v, "lines")
	loopRead(func() bool { v.setPosition(2500 * time.Millisecond); return true })
	if got := loopRead(func() []int { return currentRows(v) }); len(got) != 1 || got[0] != 1 {
		t.Fatalf("at 0:02.5 current %v, want line 1", got)
	}
	clock.Advance(1499 * time.Millisecond)
	if got := loopRead(func() []int { return currentRows(v) }); got[0] != 1 {
		t.Fatalf("before line 2 starts current %v", got)
	}
	clock.Advance(time.Millisecond)
	if got := loopRead(func() []int { return currentRows(v) }); len(got) != 1 || got[0] != 2 {
		t.Errorf("at 0:04 current %v, want line 2 without a position poll", got)
	}

	loopRead(func() bool {
		v.current.State = mpris.StatePaused
		v.setPosition(2500 * time.Millisecond)
		return true
	})
	clock.Advance(10 * time.Second)
	if got := loopRead(func() []int { return currentRows(v) }); got[0] != 1 {
		t.Errorf("paused lyrics moved on to %v", got)
	}
}
