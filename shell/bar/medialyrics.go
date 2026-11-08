package bar

import (
	"context"
	"log"
	"time"

	"github.com/stubbedev/lys-desktop/i18n"
	"github.com/stubbedev/lys-desktop/service/mpris"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/service/lyrics"
)

const lyricsLookupTimeout = 15 * time.Second

type lyricsPanel struct {
	ctx  ModuleContext
	font render.Font
	px   float64

	*widget.Stack
	status *widget.Label
	scroll *widget.Scroll
	lines  *widget.Box
	rows   []*widget.Label

	track   lyrics.Track
	asked   bool
	gen     uint64
	shown   lyrics.Lyrics
	current int
	after   func(time.Duration, func()) (stop func() bool)
	stop    func() bool
}

func newLyricsPanel(ctx ModuleContext, font render.Font, px float64) *lyricsPanel {
	l := &lyricsPanel{ctx: ctx, font: font, px: px, current: -1, after: afterFunc}
	l.Stack = widget.NewStack()
	l.AddClass("media-lyrics")
	l.status = widget.NewLabel(font, px, "", 0)
	l.status.AddClass("media-lyrics-status", css.DimLabel)
	l.status.SetAlignment(render.AlignCenter)
	l.status.SetWrap(true)
	status := widget.NewBox(widget.Column, 0, 0)
	status.AppendAligned(l.status, true, widget.AlignCenter)
	l.lines = widget.NewBox(widget.Column, widget.Current().Spacing/2, 0)
	l.lines.AddClass("media-lyrics-lines")
	l.scroll = dropdownScroll(l.lines, "media-lyrics-scroll")
	l.Add("status", status)
	l.Add("lines", l.scroll)
	return l
}

func lyricsTrack(p mpris.Player) lyrics.Track {
	return lyrics.Track{Artist: p.Artist, Title: p.Title, Album: p.Album, Length: p.Length}
}

func (l *lyricsPanel) follow(p mpris.Player) {
	track := lyricsTrack(p)
	if l.asked && track == l.track {
		return
	}
	l.stopTimer()
	l.track, l.asked = track, true
	l.gen++
	gen := l.gen
	l.setStatus(i18n.T("dropdown-media-lyrics-loading"))
	if l.ctx.Lyrics == nil {
		l.setStatus(i18n.T("dropdown-media-lyrics-unavailable"))
		return
	}
	svc := l.ctx.Lyrics
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), lyricsLookupTimeout)
		defer cancel()
		found, err := svc.Get(ctx, track)
		if err != nil {
			log.Printf("media: %v", err)
		}
		l.ctx.Invoke(func() { l.show(gen, found, err) })
	}()
}

func (l *lyricsPanel) show(gen uint64, found lyrics.Lyrics, err error) {
	if gen != l.gen {
		return
	}
	switch {
	case err != nil:
		l.setStatus(i18n.T("dropdown-media-lyrics-unavailable"))
		return
	case !found.Found():
		l.setStatus(i18n.T("dropdown-media-lyrics-none"))
		return
	}
	l.shown, l.current = found, -1
	l.lines.Clear()
	l.rows = l.rows[:0]
	for _, line := range found.Lines {
		row := widget.NewLabel(l.font, l.px, line.Text, 0)
		row.AddClass("media-lyrics-line")
		row.SetWrap(true)
		row.SetAlignment(render.AlignCenter)
		l.rows = append(l.rows, row)
		l.lines.Append(row, false)
	}
	setClass(l.lines, "synced", found.Synced)
	l.scroll.SetOffset(0, 0)
	l.Show("lines")
}

func (l *lyricsPanel) setStatus(text string) {
	l.shown, l.current = lyrics.Lyrics{}, -1
	l.status.SetText(text)
	l.Show("status")
}

func (l *lyricsPanel) position(pos time.Duration, playing bool) {
	l.stopTimer()
	if !l.shown.Synced {
		return
	}
	at := l.shown.Current(pos)
	l.highlight(at)
	if !playing || at+1 >= len(l.shown.Lines) {
		return
	}
	gen := l.gen
	next := at + 1
	l.stop = l.after(l.shown.Lines[next].At-pos, func() {
		l.ctx.Invoke(func() {
			if gen == l.gen {
				l.highlight(next)
			}
		})
	})
}

func (l *lyricsPanel) highlight(at int) {
	if at == l.current {
		return
	}
	if l.current >= 0 {
		l.rows[l.current].RemoveClass("current")
	}
	l.current = at
	if at < 0 {
		return
	}
	l.rows[at].AddClass("current")
	l.reveal(l.rows[at])
}

func (l *lyricsPanel) reveal(row *widget.Label) {
	view, content, line := l.scroll.Bounds(), l.lines.Bounds(), row.Bounds()
	if view.H == 0 {
		return
	}
	l.scroll.SetOffset(0, line.Y-content.Y-(view.H-line.H)/2)
}

func (l *lyricsPanel) stopTimer() {
	if l.stop != nil {
		l.stop()
		l.stop = nil
	}
}

func (l *lyricsPanel) reset() {
	l.stopTimer()
	l.gen++
	l.asked = false
}

func afterFunc(d time.Duration, fn func()) func() bool { return time.AfterFunc(d, fn).Stop }
