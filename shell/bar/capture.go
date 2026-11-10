package bar

import (
	"context"
	"errors"
	"image"
	"log"
	"slices"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/service/clipboard"
	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-lib/render"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/transfer"
	"github.com/stubbedev/lys-ui/widget"

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
	shots   *shotUI
	picker  *sharepicker.Picker
	release []func()
}

// newCaptureServices builds the overlays and the host on the application;
// serve puts the host and the share picker on the session bus, and a bus
// failure leaves the in-process builtin working.
func newCaptureServices(application *app.Application, outputs func() []*app.Output, current func() *config.Config, hypr *hyprland.Connection, clip *clipboard.Clipboard, sans render.Font, sheet *widget.Stylesheet) *captureServices {
	cfg := current()
	var font render.Font
	if face, err := app.FontWeighted(cfg.General.FontMono, overlayLabelPx, 700, false); err == nil {
		font = app.FontFallback(face)
	} else {
		log.Printf("screenshot: overlay font %q: %v", cfg.General.FontMono, err)
	}
	focus := screenshot.CompositorFocus{Hyprland: hypr}
	gate := &regionoverlay.Gate{}
	region := regionoverlay.New(application, gate, outputs, focus.Windows, font, sheet)
	monitors := func() []regionoverlay.Monitor {
		outs := outputs()
		mons := make([]regionoverlay.Monitor, len(outs))
		for i, o := range outs {
			mons[i] = regionoverlay.MonitorOf(o)
		}
		return mons
	}
	put := func(content transfer.Content) (func(), error) { return putOnClipboard(application, clip, content) }
	copyImage := func(img image.Image) (func(), error) {
		content, err := transfer.Image(img)
		if err != nil {
			return nil, err
		}
		return put(content)
	}
	s := &captureServices{host: &screenshot.Host{
		Config:   func() config.ScreenshotConfig { return current().Screenshot },
		Monitors: monitors,
		Gate:     gate,
		Region:   region,
		Picker:   colorpicker.New(application, gate, outputs, font, colorpicker.HistoryPath(), func() config.ColorFormat { return current().ColorPicker.Format }),
		Focus:    focus,
		Copy:     awaitLoop(application, copyImage),
		CopyText: copyTextOffLoop(application, clip),
		Forget: func(ids []render.ImageID) {
			application.Invoke(func() {
				for _, id := range ids {
					application.ForgetImage(id)
				}
			})
		},
	}}
	s.shots = newShotUI(application, outputs, current, focus.FocusedOutput, sans, put)
	s.shots.wire(s.host)
	windows := regionoverlay.NewWindowPick(application, gate, outputs, font, sheet)
	s.picker = sharepicker.New(application, sharepicker.Deps{Config: current, Font: sans, Sheet: sheet, Describe: describeApp}, gate, region, windows)
	return s
}

func (s *captureServices) serve(conn *dbus.Conn) (stop func()) {
	if conn == nil {
		log.Print("screenshot: no session bus")
		return func() {}
	}
	var releases []func()
	if release, err := screenshot.NewDaemon(s.host).Export(conn); err == nil {
		releases = append(releases, release)
	} else {
		log.Printf("screenshot: daemon: %v", err)
	}
	if release, err := sharepicker.NewDaemon(s.picker).Export(conn); err == nil {
		releases = append(releases, release)
	} else {
		log.Printf("share picker: daemon: %v", err)
	}
	return func() {
		for _, release := range slices.Backward(releases) {
			release()
		}
	}
}

// trigger is the `lys-shell screenshot ...` builtin: a fire-and-forget
// capture whose reply nobody awaits, like the Rust trigger's dropped
// oneshot. A press refused during an open overlay is only logged.
func (s *captureServices) trigger(mode, target string) {
	go func() {
		if _, err := s.host.Capture(portaldialogs.Caller{}, mode, target); err != nil {
			log.Printf("screenshot: %v", err)
		}
	}()
}

func (s *captureServices) read(kind screenshot.ReadKind) {
	go func() {
		if _, err := s.host.Read(portaldialogs.Caller{}, kind); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("screenshot: reading the screen: %v", err)
			if s.host.Toast != nil {
				s.host.Toast(err.Error(), "ld-scan-text-symbolic")
			}
		}
	}()
}

// pickColor lets the user pick a pixel, copies it in the format the
// picker showed, and hands the copied text to copied, on its own
// goroutine; cancelling hands over nothing.
func (s *captureServices) pickColor(copied func(text string)) {
	go deliverPickedColor(func() (string, bool, error) {
		return s.host.PickColorText(portaldialogs.Caller{}, colorpicker.Options{}, true)
	}, copied)
}

// deliverPickedColor hands over what pick picked and copied: cancelling
// hands over nothing, and a failure is logged rather than pretending a
// color.
func deliverPickedColor(pick func() (string, bool, error), copied func(text string)) {
	text, picked, err := pick()
	if err != nil {
		log.Printf("launcher: picking a color: %v", err)
		return
	}
	if picked {
		copied(text)
	}
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

// awaitLoop runs fn on the loop for any goroutine but the loop's, which
// would wait on itself forever, and returns fn's result.
func awaitLoop[T any](application *app.Application, fn func(T) (func(), error)) func(T) (func(), error) {
	return func(v T) (func(), error) {
		type result struct {
			retract func()
			err     error
		}
		done := make(chan result, 1)
		application.Invoke(func() {
			retract, err := fn(v)
			done <- result{retract, err}
		})
		r := <-done
		return r.retract, r.err
	}
}

// putOnClipboard copies content, through the clipboard history when the
// shell keeps one, which can take the copy back; the plain device
// cannot, so it returns no retract.
func putOnClipboard(application *app.Application, clip *clipboard.Clipboard, content transfer.Content) (func(), error) {
	if clip != nil {
		return clip.Put(content)
	}
	device := application.Clipboard()
	if device == nil {
		return nil, app.ErrClipboardUnavailable
	}
	return nil, device.Write(content)
}
