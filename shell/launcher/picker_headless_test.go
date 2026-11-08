package launcher

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/imagedecode"
	"github.com/stubbedev/lys-desktop/shell/apptheme"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/keytest"
	"github.com/stubbedev/lys-shell/internal/launcheripc"
	engine "github.com/stubbedev/lys-shell/service/launcher"
)

// headlessWalls makes real thumbnails of real files; what it was told is
// read on the loop.
type headlessWalls struct {
	images   []string
	thumbs   engine.ImageThumbnails
	mu       sync.Mutex
	decodes  int
	previews []string
	ended    int
}

func (w *headlessWalls) Images() ([]string, error)   { return w.images, nil }
func (w *headlessWalls) Shown(string) string         { return w.images[0] }
func (w *headlessWalls) Preview(p, _ string) error   { w.previews = append(w.previews, p); return nil }
func (w *headlessWalls) EndPreview()                 { w.ended++ }
func (w *headlessWalls) Commit(string, string) error { return nil }
func (w *headlessWalls) LoadThumbnail(path string) (image.Image, error) {
	img, decoded, err := w.thumbs.Load(path)
	if decoded {
		w.mu.Lock()
		w.decodes++
		w.mu.Unlock()
	}
	return img, err
}

func (w *headlessWalls) decoded() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.decodes
}

// On a real compositor the picker draws its grid with real thumbnails,
// previews where the arrow keys leave the selection, and Esc ends the
// preview; a second picker reads every thumbnail from the cache.
func TestHeadlessPickerPreviewsAndReadsItsThumbnailsBack(t *testing.T) {
	dir := t.TempDir()
	var images []string
	for i := range 6 {
		path := filepath.Join(dir, "wall"+string(rune('a'+i))+".png")
		img := image.NewRGBA(image.Rect(0, 0, 640, 360))
		for p := range img.Pix {
			img.Pix[p] = uint8(p*(i+1)) | 0x0f
		}
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		f.Close()
		images = append(images, path)
	}
	walls := &headlessWalls{images: images, thumbs: engine.ImageThumbnails{Dir: filepath.Join(dir, "large"), Decode: imagedecode.Open}}
	cfg := config.Defaults()
	sheet := apptheme.New(cfg).Sheet
	a := apptest.Run(t)
	font, err := app.Font("sans", 14)
	if err != nil {
		t.Fatal(err)
	}
	var layers []*app.LayerWindow
	s := New(Deps{
		Invoke: a.Invoke,
		After: func(d time.Duration, fn func()) func() {
			tm := time.AfterFunc(d, func() { a.Invoke(fn) })
			return func() { tm.Stop() }
		},
		Config: func() *config.Config { return cfg },
		Open: func(c app.LayerConfig) (Window, error) {
			w, err := a.NewLayer(c)
			layers = append(layers, w)
			return w, err
		},
		Font:          font,
		Sheet:         sheet,
		Wallpapers:    walls,
		FocusedOutput: func() (string, bool) { return "HEADLESS-1", true },
	})
	open := func(id uint64) <-chan launcheripc.ServerFrame {
		mode := WallpaperMode
		sess, reply := launcheripc.NewSession(id, launcheripc.SessionOptions{Mode: &mode}, false, nil)
		s.Open(sess)
		return reply
	}
	reply := open(1)
	kb := keytest.Dial(t, a)
	keytest.AwaitFocus(t, a)
	apptest.Until(t, a, "every thumbnail", func() bool { return s.picker != nil && len(s.picker.cache.byKey) == len(images) })
	layer := apptest.On(a, func() *app.LayerWindow { return layers[len(layers)-1] })
	apptest.Drawn(t, a, layer.HostSurface(), func() { s.picker.frame.root.Invalidate() })
	if got := walls.decoded(); got != len(images) {
		t.Errorf("the first picker decoded %d images, want %d", got, len(images))
	}
	kb.Tap(keytest.Right)
	apptest.Until(t, a, "the preview", func() bool { return slices.Equal(walls.previews, images[1:2]) })
	kb.Tap(keytest.Escape)
	if f := apptest.Answer(t, "the reply", reply); f.Type != launcheripc.FrameCancelled {
		t.Errorf("escape answered %+v", f)
	}
	if ended := apptest.On(a, func() int { return walls.ended }); ended != 1 {
		t.Errorf("the preview ended %d times, want 1", ended)
	}

	reply = open(2)
	keytest.AwaitFocus(t, a)
	apptest.Until(t, a, "every thumbnail again", func() bool { return s.picker != nil && len(s.picker.cache.byKey) == len(images) })
	if got := walls.decoded(); got != len(images) {
		t.Errorf("the second picker decoded %d more images, want 0", got-len(images))
	}
	kb.Tap(keytest.Escape)
	apptest.Answer(t, "the second reply", reply)
}
