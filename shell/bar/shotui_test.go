package bar

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/transfer"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/keytest"
	"github.com/stubbedev/lys-shell/service/clipboard"
	"github.com/stubbedev/lys-shell/service/clipboard/historybudget"
	"github.com/stubbedev/lys-shell/shell/screenshot"
)

func TestEditorSaveWritesAndCopiesWithoutStallingTheLoop(t *testing.T) {
	cfg := config.Defaults()
	ctx := styledContext(t, cfg)
	a := apptest.Run(t)
	history, err := clipboard.DefaultHistory(historybudget.DefaultBudget(), historybudget.DefaultPinnedBudget())
	if err != nil {
		t.Fatal(err)
	}
	var clip *clipboard.Clipboard
	if err := apptest.On(a, func() error {
		a.SetClipboard(app.NewClipboard(a.Session()))
		var err error
		clip, err = clipboard.Start(a, history, clipboard.Stores{})
		return err
	}); err != nil {
		t.Fatalf("the shell's clipboard on sway: %v", err)
	}
	t.Cleanup(func() { apptest.On(a, func() bool { clip.Close(); return true }) })
	svc := apptest.On(a, func() *captureServices {
		return newCaptureServices(a, a.Session().Outputs, func() *config.Config { return cfg }, nil, clip, ctx.Font, ctx.Theme.Sheet)
	})
	t.Cleanup(func() { apptest.On(a, func() bool { svc.close(); return true }) })

	path := filepath.Join(t.TempDir(), "shot.png")
	capture := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for i := range capture.Pix {
		capture.Pix[i] = 0xff
	}
	apptest.On(a, func() bool {
		shot := svc.shots.hold(screenshot.Saved{Path: path, Image: capture})
		svc.shots.edit(shot)
		svc.shots.release(shot)
		return true
	})
	keys := keytest.Dial(t, a)
	keytest.AwaitFocus(t, a)
	keys.Tap(keytest.Return)

	apptest.Until(t, a, "the editor closed and let the capture go", func() bool { return len(svc.shots.holders) == 0 })
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Enter in the editor wrote nothing: %v", err)
	}
	saved := decodePNG(t, data)
	if saved.Bounds() != capture.Bounds() || saved.RGBAAt(10, 10) != (color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("saved a %v image with %v at (10,10), want the unmarked %v capture", saved.Bounds(), saved.RGBAAt(10, 10), capture.Bounds())
	}
	snap := apptest.On(a, clip.Entries)
	defer snap.Release()
	if entries := snap.Entries(); len(entries) != 1 || entries[0].Mime != transfer.MimePNG {
		t.Fatalf("the clipboard history holds %v, want the edited image as image/png", entries)
	}
}
