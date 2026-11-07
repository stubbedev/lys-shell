package bar

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/service/lyswm/lyswmtest"
)

const liveTimeout = 10 * time.Second

// countingToplevels counts the rectangles the windows are given.
type countingToplevels struct {
	appToplevels
	rects *int
}

func (s countingToplevels) windows() []taskWindow {
	out := s.appToplevels.windows()
	for i, w := range out {
		out[i] = countedWindow{w.(app.Toplevel), s.rects}
	}
	return out
}

type countedWindow struct {
	app.Toplevel
	rects *int
}

func (w countedWindow) SetRectangle(host app.Host, r render.Rect) {
	*w.rects++
	w.Toplevel.SetRectangle(host, r)
}

// liveApp runs a lys-ui application on the compositor the test points
// at, with build adding its surfaces on the loop before it runs.
func liveApp(t *testing.T, build func(a *app.Application, out *app.Output) error) *app.Application {
	t.Helper()
	sess, err := app.Connect()
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	a := app.NewApplication(sess)
	if err := build(a, sess.Outputs()[0]); err != nil {
		sess.Close()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = a.Run()
	}()
	t.Cleanup(func() {
		a.Invoke(a.Quit)
		select {
		case <-done:
		case <-time.After(liveTimeout):
			t.Error("the application did not quit")
		}
		sess.Close()
	})
	return a
}

// onLiveLoop runs fn on a's loop and waits for it.
func onLiveLoop[T any](a *app.Application, fn func() T) T {
	out := make(chan T, 1)
	a.Invoke(func() { out <- fn() })
	return <-out
}

// until polls cond, failing with what after the live timeout.
func until(t *testing.T, what string, cond func() error) {
	t.Helper()
	var err error
	for deadline := time.Now().Add(liveTimeout); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if err = cond(); err == nil {
			return
		}
	}
	t.Fatalf("no %s: %v", what, err)
}

// lysWMWindow is the lys-wm window with appID.
func lysWMWindow(appID string) (lyswm.Window, error) {
	windows, err := lyswm.Windows()
	if err != nil {
		return lyswm.Window{}, err
	}
	for _, w := range windows {
		if w.AppID == appID {
			return w, nil
		}
	}
	return lyswm.Window{}, fmt.Errorf("lys-wm lists no %s window in %+v", appID, windows)
}

// On a real lys-wm, a window minimized through lys-wm shows in the
// module under its app, with its entry as its minimize rectangle; a
// click brings it back focused, and a middle click on it minimized
// again closes it. The rectangles sent stay a handful however many
// layouts run.
func TestTheMinimizedWindowsModuleOnLysWM(t *testing.T) {
	lyswmtest.Headless(t)
	cfg := config.Defaults()
	rects := 0
	var (
		m     *minimizedWindowsModule
		hub   *taskHub
		layer *app.LayerWindow
	)
	shell := liveApp(t, func(a *app.Application, out *app.Output) error {
		if !a.ToplevelsAvailable() {
			return errors.New("lys-wm offers no foreign-toplevel list")
		}
		ctx := newTestContext(t, cfg)
		ctx.App, ctx.Connector = a, out.Name
		hub = newTaskHub(countingToplevels{appToplevels{a}, &rects}, a.Invoke)
		ctx.Tasks = hub
		module, err := newMinimizedWindows(ctx)
		if err != nil {
			return err
		}
		m = module.(*minimizedWindowsModule)
		layer, err = a.NewLayer(app.LayerConfig{Output: out, Layer: app.LayerTop, Anchor: app.AnchorTop, Namespace: "lys-shell-test-bar", Root: widget.NewBox(widget.Row, 0, 0).Append(widget.NewDrawingArea(1, 40), false).Append(m.Root(), false)})
		m.Attach(layer)
		return err
	})
	const probeID = "org.example.Probe"
	probe := startProbeProcess(t, probeID, "probe title")
	var id uint64
	until(t, "the probe on lys-wm", func() error {
		w, err := lysWMWindow(probeID)
		id = w.ID
		return err
	})
	if err := lyswm.Dispatch("minimize " + strconv.FormatUint(id, 10)); err != nil {
		t.Fatal(err)
	}
	until(t, "the minimized probe in the module, named its entry", func() error {
		return onLiveLoop(shell, func() error {
			if len(m.shown) != 1 || m.shown[0].AppID() != probeID {
				return fmt.Errorf("shown %d", len(m.shown))
			}
			e := m.entries[m.shown[0]]
			if e.TooltipText() != "probe title" {
				return fmt.Errorf("tooltip %q", e.TooltipText())
			}
			sent, ok := hub.windows.sent[m.shown[0]]
			if !ok || sent.host != app.Host(layer) || sent.rect != e.Bounds() || sent.rect.Empty() {
				return fmt.Errorf("rectangle %+v, entry %+v", sent, e.Bounds())
			}
			return nil
		})
	})
	shell.Invoke(func() { m.entries[m.shown[0]].ClickAt(widget.Point{}) })
	until(t, "the probe back and focused", func() error {
		w, err := lysWMWindow(probeID)
		if err == nil && (w.Minimized || !w.Focused) {
			err = fmt.Errorf("window %+v", w)
		}
		return err
	})
	until(t, "the module empty again", func() error {
		return onLiveLoop(shell, func() error {
			if len(m.shown) != 0 {
				return fmt.Errorf("shown %d", len(m.shown))
			}
			return nil
		})
	})
	if err := lyswm.Dispatch("minimize " + strconv.FormatUint(id, 10)); err != nil {
		t.Fatal(err)
	}
	until(t, "the probe minimized again", func() error {
		return onLiveLoop(shell, func() error {
			if len(m.shown) != 1 || m.entries[m.shown[0]].Bounds().Empty() {
				return fmt.Errorf("shown %d", len(m.shown))
			}
			return nil
		})
	})
	shell.Invoke(func() {
		b := m.entries[m.shown[0]].Bounds()
		r := &widget.Router{Root: m.root}
		p := widget.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}
		r.Move(p)
		r.Press(widget.BTNMiddle, p, 0)
	})
	until(t, "the probe closed", func() error {
		select {
		case <-probe:
			return nil
		default:
			return errors.New("still open")
		}
	})
	if sent := onLiveLoop(shell, func() int { return rects }); sent > 8 {
		t.Errorf("%d rectangles sent over two minimizes, want a handful", sent)
	}
}

// probeEnv names the app id a re-run of the test binary maps a window
// under, as TestProbeWindow; probeTitleEnv is its title.
const (
	probeEnv      = "LYS_SHELL_TEST_PROBE_APP_ID"
	probeTitleEnv = "LYS_SHELL_TEST_PROBE_TITLE"
)

// startProbeProcess maps a window under appID from a process of its
// own, as a real app is, and returns a channel closed when that process
// ends, which it does when the compositor closes the window.
func startProbeProcess(t *testing.T, appID, title string) <-chan struct{} {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProbeWindow$")
	socket := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), os.Getenv("WAYLAND_DISPLAY"))
	cmd.Env = append(os.Environ(), probeEnv+"="+appID, probeTitleEnv+"="+title, "WAYLAND_DISPLAY="+socket)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	return done
}

// TestProbeWindow is the probe startProbeProcess runs: one window until
// the compositor closes it. Run directly it does nothing.
func TestProbeWindow(t *testing.T) {
	appID := os.Getenv(probeEnv)
	if appID == "" {
		t.Skip("the probe window runs only under startProbeProcess")
	}
	sess, err := app.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	a := app.NewApplication(sess)
	if _, err := a.NewWindow(app.WindowConfig{Title: os.Getenv(probeTitleEnv), AppID: appID, Root: widget.NewDrawingArea(200, 200)}); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(); err != nil && !errors.Is(err, app.ErrClosed) {
		t.Fatal(err)
	}
}
