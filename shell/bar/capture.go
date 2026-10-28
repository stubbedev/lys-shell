package bar

import (
	"image"
	"log"
	"slices"

	"github.com/stubbedev/lys-lib/render"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/transfer"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/service/clipboard"
	"github.com/stubbedev/lys-shell/service/hyprland"
	"github.com/stubbedev/lys-shell/service/recorder"
	"github.com/stubbedev/lys-shell/shell/colorpicker"
	"github.com/stubbedev/lys-shell/shell/regionoverlay"
	"github.com/stubbedev/lys-shell/shell/screenshot"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// overlayLabelPx is the region overlay's size-label face size; the
// color picker shapes its readout from the same face.
const overlayLabelPx = 14

// captureServices is the shell's capture side: the region overlay, the
// color picker, the screenshot host behind dev.stubbe.lys.Screenshot1, and
// the share picker behind dev.stubbe.lys.SharePicker1
// (bootstrap's screenshot and share picker service starts).
type captureServices struct {
	host    *screenshot.Host
	release []func()
}

// startCapture builds the overlays and the host on the application and
// serves the host on the session bus. A bus failure leaves the
// in-process builtin working.
func startCapture(application *app.Application, outputs func() []*app.Output, current func() *config.Config, hypr *hyprland.Connection, clip *clipboard.Clipboard, sans render.Font, sheet *widget.Stylesheet) *captureServices {
	cfg := current()
	var font render.Font
	if face, err := app.FontWeighted(cfg.General.FontMono, overlayLabelPx, 700, false); err == nil {
		font = app.FontFallback(face)
	} else {
		log.Printf("screenshot: overlay font %q: %v", cfg.General.FontMono, err)
	}
	focus := screenshot.CompositorFocus{Hyprland: hypr}
	region := regionoverlay.New(application, outputs, focus.Windows, font, sheet)
	monitors := func() []regionoverlay.Monitor {
		outs := outputs()
		mons := make([]regionoverlay.Monitor, len(outs))
		for i, o := range outs {
			mons[i] = regionoverlay.MonitorOf(o)
		}
		return mons
	}
	s := &captureServices{host: &screenshot.Host{
		Config:   func() config.ScreenshotConfig { return current().Screenshot },
		Monitors: monitors,
		Region:   region,
		Picker:   colorpicker.New(application, outputs, font, colorpicker.HistoryPath()),
		Focus:    focus,
		Copy:     clipboardCopier(application, clip),
		Forget: func(ids []render.ImageID) {
			application.Invoke(func() {
				for _, id := range ids {
					application.ForgetImage(id)
				}
			})
		},
	}}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		log.Printf("screenshot: session bus: %v", err)
		return s
	}
	s.release = append(s.release, func() { _ = conn.Close() })
	if release, err := screenshot.NewDaemon(s.host).Export(conn); err == nil {
		s.release = append(s.release, release)
	} else {
		log.Printf("screenshot: daemon: %v", err)
	}
	picker := sharepicker.New(application, sharepicker.Deps{Config: current, Font: sans, Sheet: sheet, Describe: describeApp}, region)
	if release, err := sharepicker.NewDaemon(picker).Export(conn); err == nil {
		s.release = append(s.release, release)
	} else {
		log.Printf("share picker: daemon: %v", err)
	}
	return s
}

// trigger is the `lys-shell screenshot ...` builtin: a fire-and-forget
// capture whose reply nobody awaits, like the Rust trigger's dropped
// oneshot.
func (s *captureServices) trigger(mode, target string) {
	go func() {
		if _, err := s.host.Capture(portalreq.Caller{}, mode, target); err != nil {
			log.Printf("screenshot: %v", err)
		}
	}()
}

// close drops the bus name and connection.
func (s *captureServices) close() {
	for _, release := range slices.Backward(s.release) {
		release()
	}
}

// logicalOutputSize reads an output's logical size (xdg-output) on the
// loop, for a recorder measuring the region it was given.
func logicalOutputSize(application *app.Application, outputs func() []*app.Output) recorder.LogicalSize {
	type found struct {
		size image.Point
		ok   bool
	}
	return func(name string) (image.Point, bool) {
		reply := make(chan found, 1)
		application.Invoke(func() {
			for _, o := range outputs() {
				if o.Name == name {
					reply <- found{image.Pt(int(o.LogicalW), int(o.LogicalH)), true}
					return
				}
			}
			reply <- found{}
		})
		f := <-reply
		return f.size, f.ok
	}
}

func clipboardCopier(application *app.Application, clip *clipboard.Clipboard) func(image.Image) error {
	return func(img image.Image) error {
		content, err := transfer.Image(img)
		if err != nil {
			return err
		}
		errc := make(chan error, 1)
		application.Invoke(func() { errc <- putOnClipboard(application, clip, content) })
		return <-errc
	}
}

func putOnClipboard(application *app.Application, clip *clipboard.Clipboard, content transfer.Content) error {
	if clip != nil {
		return clip.Put(content)
	}
	device := application.Clipboard()
	if device == nil {
		return app.ErrClipboardUnavailable
	}
	return device.Write(content)
}
