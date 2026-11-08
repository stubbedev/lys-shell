package bar

import (
	"image"
	"log"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/i18n"
	"github.com/stubbedev/lys-desktop/styling"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/transfer"
	"github.com/stubbedev/lys-ui/widget"

	timeclock "github.com/stubbedev/lys-shell/internal/clock"
	"github.com/stubbedev/lys-shell/shell/keys"
	"github.com/stubbedev/lys-shell/shell/markup"
	"github.com/stubbedev/lys-shell/shell/screenshot"
	"github.com/stubbedev/lys-shell/shell/shotthumb"
)

const toolbarPx = 14

type shotUI struct {
	app     *app.Application
	outputs func() []*app.Output
	current func() *config.Config
	focused func() (string, bool)
	font    render.Font
	copy    func(transfer.Content) (func(), error)

	thumb   *shotthumb.Thumbnail
	holders map[render.ImageID]int
	saved   map[render.ImageID]screenshot.Saved
}

// newShotUI shows saved captures; copyOnLoop puts content on the
// clipboard and is called on the loop.
func newShotUI(application *app.Application, outputs func() []*app.Output, current func() *config.Config, focused func() (string, bool), font render.Font, copyOnLoop func(transfer.Content) (func(), error)) *shotUI {
	u := &shotUI{
		app: application, outputs: outputs, current: current, focused: focused, font: font, copy: copyOnLoop,
		holders: map[render.ImageID]int{}, saved: map[render.ImageID]screenshot.Saved{},
	}
	u.thumb = shotthumb.New(shotthumb.Deps{
		Open: func(lc app.LayerConfig) (shotthumb.Window, error) {
			w, err := application.NewLayer(lc)
			if err != nil {
				return nil, err
			}
			return w, nil
		},
		KeySym: application.KeySym,
		Invoke: application.Invoke,
		Clock:  timeclock.Wall{},
		Edit:   u.edit,
		Delete: u.discard,
		Done:   u.release,
	})
	return u
}

func (u *shotUI) wire(h *screenshot.Host) {
	h.Present = u.present
	h.Edit = func(s screenshot.Saved) {
		u.app.Invoke(func() {
			shot := u.hold(s)
			u.edit(shot)
			u.release(shot)
		})
	}
	h.OpenFolder = func(dir string) { u.app.Invoke(func() { u.app.OpenPath(dir) }) }
	h.OpenURL = func(uri string) { u.app.Invoke(func() { u.app.OpenURL(uri) }) }
	h.Working = u.working
}

func (u *shotUI) present(s screenshot.Saved) {
	name, _ := u.focused()
	u.app.Invoke(func() {
		shot := u.hold(s)
		var out *app.Output
		for _, o := range u.outputs() {
			if o.Name == name {
				out = o
			}
		}
		if err := u.thumb.Show(shot, out, u.current().Screenshot); err != nil {
			log.Printf("screenshot: thumbnail: %v", err)
		}
	})
}

func (u *shotUI) hold(s screenshot.Saved) shotthumb.Shot {
	shot := shotthumb.Shot{Path: s.Path, Image: render.NewStillImage(s.Image)}
	u.holders[shot.Image.ID] = 1
	u.saved[shot.Image.ID] = s
	return shot
}

func (u *shotUI) release(shot shotthumb.Shot) {
	id := shot.Image.ID
	if u.holders[id]--; u.holders[id] > 0 {
		return
	}
	delete(u.holders, id)
	delete(u.saved, id)
	u.app.ForgetImage(id)
}

func (u *shotUI) discard(shot shotthumb.Shot) {
	s, ok := u.saved[shot.Image.ID]
	if !ok {
		s = screenshot.Saved{Path: shot.Path}
	}
	go func() {
		if err := s.Discard(); err != nil {
			log.Printf("screenshot: %v", err)
		}
	}()
}

func (u *shotUI) edit(shot shotthumb.Shot) {
	u.holders[shot.Image.ID]++
	_, err := markup.Open(markup.Deps{
		App:     u.app,
		Font:    u.font,
		Px:      toolbarPx,
		Palette: markupPalette(u.current().Styling),
		Save:    u.writer(true),
		SaveAs:  u.writer(false),
		Closed:  func() { u.release(shot) },
	}, shot.Image, shot.Path)
	if err != nil {
		log.Printf("screenshot: markup editor: %v", err)
		u.release(shot)
	}
}

// writer encodes and writes an edited image off the loop and, when
// copies, puts it on the clipboard back on the loop. A failed copy is
// logged: the image is saved all the same.
func (u *shotUI) writer(copies bool) markup.Writer {
	return func(img *image.RGBA, path string, done func(error)) {
		go func() {
			err := screenshot.WritePNG(img, path)
			var content transfer.Content
			var encodeErr error
			if err == nil && copies {
				content, encodeErr = transfer.Image(img)
			}
			u.app.Invoke(func() {
				if err == nil && copies {
					u.copyEdited(content, encodeErr)
				}
				done(err)
			})
		}()
	}
}

func (u *shotUI) copyEdited(content transfer.Content, encodeErr error) {
	err := encodeErr
	if err == nil {
		_, err = u.copy(content)
	}
	if err != nil {
		log.Printf("screenshot: copying the edited image: %v", err)
	}
}

func markupPalette(s config.StylingConfig) []render.Color {
	p, err := styling.ConfigPalette(s)
	if err != nil {
		log.Printf("screenshot: markup palette: %v", err)
		p = styling.Default()
	}
	return []render.Color{p.Red, p.Yellow, p.Green, p.Blue, p.Primary, p.Fg, p.Bg}
}

func (u *shotUI) working(cancel func()) func() {
	opened := make(chan *app.LayerWindow, 1)
	u.app.Invoke(func() {
		label := widget.NewLabel(u.font, toolbarPx, i18n.T("screenshot-reading-text"), 0)
		box := widget.NewBox(widget.Row, 0, 12)
		box.SetElement("item")
		box.Append(label, false)
		w, err := u.app.NewLayer(app.LayerConfig{
			Layer:     app.LayerOverlay,
			Anchor:    app.AnchorTop,
			Margin:    app.Margins{Top: 48},
			Keyboard:  app.KeyboardExclusive,
			Namespace: "lys-shell-screenshot-reading",
			Root:      box,
			OnKey:     keys.OnEscape(u.app.KeySym, cancel),
		})
		if err != nil {
			log.Printf("screenshot: reading indicator: %v", err)
		}
		opened <- w
	})
	return func() {
		u.app.Invoke(func() {
			if w := <-opened; w != nil {
				w.Close()
			}
		})
	}
}
