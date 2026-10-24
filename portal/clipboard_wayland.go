package portal

import (
	"errors"
	"os"

	"github.com/stubbedev/lys-ui/app"
)

// waylandClipboard is the seat's data-control device on the portal's
// Wayland loop.
type waylandClipboard struct {
	loop *waylandLoop
	dev  *app.DataControl
}

// waylandClipboardStarter binds the device on loop; changed runs on
// every clipboard selection change.
func waylandClipboardStarter(loop *waylandLoop) func(func(selectionChange)) (clipDevice, error) {
	return func(changed func(selectionChange)) (clipDevice, error) {
		w := &waylandClipboard{loop: loop}
		err := loop.do(func(a *app.Application) error {
			dev, err := a.DataControl()
			if err != nil {
				return err
			}
			// Registered before the loop next dispatches, so the current
			// selection's announcement is not missed.
			// Run on the loop, in order: a claim's echo must not overtake
			// the selection it replaced.
			dev.OnSelection(func(sel app.Selection, offer *app.SelectionOffer) {
				if sel != app.SelectionClipboard {
					return
				}
				var change selectionChange
				if offer != nil {
					change = selectionChange{mimes: offer.Mimes(), own: offer.Own()}
				}
				changed(change)
			})
			w.dev = dev
			return nil
		})
		if err != nil {
			return nil, err
		}
		return w, nil
	}
}

func (w *waylandClipboard) own(mimes []string, pass func(string, *os.File)) error {
	return w.loop.do(func(*app.Application) error {
		_, err := w.dev.SetSelection(app.SelectionClipboard, app.SelectionSource{Mimes: mimes, Pass: pass})
		return err
	})
}

func (w *waylandClipboard) clear() error {
	return w.loop.do(func(*app.Application) error {
		if offer := w.dev.Offer(app.SelectionClipboard); offer == nil || !offer.Own() {
			return nil
		}
		return w.dev.ClearSelection(app.SelectionClipboard)
	})
}

func (w *waylandClipboard) read(mime string) (*os.File, error) {
	var f *os.File
	err := w.loop.do(func(*app.Application) error {
		offer := w.dev.Offer(app.SelectionClipboard)
		if offer == nil {
			return errors.New("no selection")
		}
		var err error
		f, err = offer.Receive(mime)
		return err
	})
	return f, err
}
