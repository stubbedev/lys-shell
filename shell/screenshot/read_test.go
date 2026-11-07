package screenshot

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-lib/render"

	"github.com/stubbedev/lys-shell/shell/regionoverlay"
)

func qrImage(t testing.TB, payload string, size int) image.Image {
	t.Helper()
	m, err := qrcode.NewQRCodeWriter().Encode(payload, gozxing.BarcodeFormat_QR_CODE, size, size, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func onWhite(w, h int, img image.Image, at image.Point) *image.RGBA {
	out := solid(w, h, color.RGBA{255, 255, 255, 255})
	if img != nil {
		draw.Draw(out, image.Rectangle{Min: at, Max: at.Add(img.Bounds().Size())}, img, img.Bounds().Min, draw.Src)
	}
	return out
}

func TestDecodeQRReadsTheFixtureAndNothingElse(t *testing.T) {
	const payload = "https://example.org/lys?x=1"
	got, err := DecodeQR(onWhite(400, 300, qrImage(t, payload, 160), image.Pt(120, 70)))
	if err != nil || got != payload {
		t.Fatalf("DecodeQR = %q, %v; want %q", got, err, payload)
	}
	if _, err := DecodeQR(onWhite(400, 300, nil, image.Point{})); !errors.Is(err, ErrNoCode) {
		t.Fatalf("an empty region = %v, want ErrNoCode", err)
	}
}

func BenchmarkDecodeQR(b *testing.B) {
	img := onWhite(400, 300, qrImage(b, "https://example.org/lys", 160), image.Pt(120, 70))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := DecodeQR(img); err != nil {
			b.Fatal(err)
		}
	}
}

func TestIsURL(t *testing.T) {
	for s, want := range map[string]bool{
		"https://example.org/x": true, "http://a.b": true, "WIFI:S:home;T:WPA;P:pw;;": false,
		"mailto:a@b.c": false, "example.org": false, "": false, "javascript:alert(1)": false,
	} {
		if IsURL(s) != want {
			t.Errorf("IsURL(%q) = %v", s, !want)
		}
	}
}

func TestOCRLanguages(t *testing.T) {
	for _, c := range []struct {
		configured, locale, want []string
		fails                    bool
	}{
		{configured: []string{"deu", "eng"}, locale: []string{"fr_FR", "fr"}, want: []string{"deu", "eng"}},
		{locale: nil, want: []string{"eng"}},
		{locale: []string{"de_DE", "de"}, want: []string{"deu"}},
		{locale: []string{"zh_TW", "zh"}, want: []string{"chi_tra"}},
		{locale: []string{"zh_CN", "zh"}, want: []string{"chi_sim"}},
		{locale: []string{"tlh_QO", "tlh"}, fails: true},
	} {
		got, err := ocrLanguages(c.configured, c.locale)
		if c.fails {
			if err == nil || !strings.Contains(err.Error(), "screenshot.ocr-languages") {
				t.Errorf("%v: %v, %v; want an error naming the setting", c.locale, got, err)
			}
			continue
		}
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%v %v = %v, %v; want %v", c.configured, c.locale, got, err, c.want)
		}
	}
}

type readRecord struct {
	copied  []string
	toasts  []string
	opened  []string
	working int
}

func readHost(t *testing.T, frame *image.RGBA) (*Host, *readRecord, *[]Notice, *fakeRegion) {
	t.Helper()
	region := &fakeRegion{sel: regionoverlay.Selection{Output: "DP-1", Width: 100, Height: 50}, ok: true}
	h, notified, _ := testHost(t, region)
	out := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(out, 0o500); err != nil {
		t.Fatal(err)
	}
	cfg := h.Config()
	cfg.OutputDirectory = out
	cfg.OcrLanguages = []string{"eng"}
	h.Config = fixed(cfg)
	h.frozen = func(bool) ([]frozenOutput, error) { return []frozenOutput{offHeap(t, h, "DP-1", frame)}, nil }
	rec := &readRecord{}
	h.CopyText = func(s string) error { rec.copied = append(rec.copied, s); return nil }
	h.Toast = func(s, _ string) { rec.toasts = append(rec.toasts, s) }
	h.OpenURL = func(s string) { rec.opened = append(rec.opened, s) }
	t.Cleanup(func() {
		if entries, _ := os.ReadDir(out); len(entries) != 0 {
			t.Errorf("a reading wrote %v", entries)
		}
	})
	return h, rec, notified, region
}

func TestReadQRAnnouncesTheCodeAndCopiesOnlyWhenAsked(t *testing.T) {
	const payload = "https://example.org/qr"
	h, rec, notified, _ := readHost(t, onWhite(200, 100, qrImage(t, payload, 96), image.Pt(52, 2)))
	got, err := h.Read(portaldialogs.Caller{}, ReadQR)
	if err != nil || got != payload {
		t.Fatalf("Read = %q, %v", got, err)
	}
	if len(rec.copied) != 0 || len(*notified) != 1 {
		t.Fatalf("copied %v before asked, notified %d", rec.copied, len(*notified))
	}
	n := (*notified)[0]
	if !slices.Equal(n.Actions, []Action{ActionOpen, ActionCopy}) || n.Body != payload {
		t.Fatalf("notice %+v", n)
	}
	n.Respond(ActionOpen)
	n.Respond(ActionCopy)
	if !slices.Equal(rec.opened, []string{payload}) || !slices.Equal(rec.copied, []string{payload}) {
		t.Fatalf("opened %v copied %v", rec.opened, rec.copied)
	}

	h, _, notified, _ = readHost(t, onWhite(200, 100, qrImage(t, "WIFI:S:home;;", 96), image.Pt(52, 2)))
	if _, err := h.Read(portaldialogs.Caller{}, ReadQR); err != nil {
		t.Fatal(err)
	}
	if acts := (*notified)[0].Actions; !slices.Equal(acts, []Action{ActionCopy}) {
		t.Fatalf("a non-URL code offers %v, want Copy only", acts)
	}
}

func TestReadQRWithoutACodeCopiesNothing(t *testing.T) {
	h, rec, notified, _ := readHost(t, onWhite(200, 100, nil, image.Point{}))
	if _, err := h.Read(portaldialogs.Caller{}, ReadQR); !errors.Is(err, ErrNoCode) || err.Error() != "no code found" {
		t.Fatalf("Read = %v, want no code found", err)
	}
	if len(rec.copied)+len(*notified) != 0 {
		t.Fatalf("copied %v notified %v", rec.copied, *notified)
	}
}

func fakeTesseract(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	tools := map[string]string{}
	for _, name := range []string{"sh", "cat", "sleep"} {
		p, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		tools[name] = p
	}
	body := "#!" + tools["sh"] + "\necho \"$$ $*\" >> " + calls + "\n" + tools["cat"] + " > /dev/null\n" + strings.ReplaceAll(script, "sleep", tools["sleep"])
	if err := os.WriteFile(filepath.Join(dir, Tesseract), []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return calls
}

func spawns(t *testing.T, calls string) []string {
	t.Helper()
	data, err := os.ReadFile(calls)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestReadTextCopiesWhatTesseractReadOnceAndToastsTheFirstLine(t *testing.T) {
	calls := fakeTesseract(t, "printf 'Error 42\\nDisk full\\n\\f'\n")
	h, rec, _, _ := readHost(t, solid(200, 100, color.RGBA{255, 255, 255, 255}))
	got, err := h.Read(portaldialogs.Caller{}, ReadText)
	if err != nil || got != "Error 42\nDisk full" {
		t.Fatalf("Read = %q, %v", got, err)
	}
	if !slices.Equal(rec.copied, []string{got}) || !slices.Equal(rec.toasts, []string{"Error 42"}) {
		t.Fatalf("copied %q toasts %q", rec.copied, rec.toasts)
	}
	if s := spawns(t, calls); len(s) != 1 || !strings.HasSuffix(s[0], " stdin stdout -l eng") {
		t.Fatalf("tesseract ran %q, want once on stdin with -l eng", s)
	}
}

func TestReadTextWithoutTesseractFailsBeforeTheOverlay(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	h, rec, _, region := readHost(t, solid(200, 100, color.RGBA{255, 255, 255, 255}))
	_, err := h.Read(portaldialogs.Caller{}, ReadText)
	if !errors.Is(err, ErrNoTesseract) || !strings.Contains(err.Error(), "tesseract is not installed") {
		t.Fatalf("Read = %v, want the missing binary named", err)
	}
	if region.frames != nil || len(rec.copied) != 0 {
		t.Fatal("the overlay opened or something was copied without tesseract")
	}
}

func TestReadTextFindingNothingCopiesNothing(t *testing.T) {
	fakeTesseract(t, "printf '\\f'\n")
	h, rec, _, _ := readHost(t, solid(200, 100, color.RGBA{255, 255, 255, 255}))
	if _, err := h.Read(portaldialogs.Caller{}, ReadText); !errors.Is(err, ErrNoText) {
		t.Fatalf("Read = %v, want ErrNoText", err)
	}
	if len(rec.copied) != 0 {
		t.Fatalf("copied %v", rec.copied)
	}
}

func TestCancellingTheReadingKillsTesseract(t *testing.T) {
	calls := fakeTesseract(t, "exec sleep 30\n")
	h, rec, _, _ := readHost(t, solid(200, 100, color.RGBA{255, 255, 255, 255}))
	h.Working = func(cancel func()) func() {
		rec.working++
		go func() {
			for deadline := time.Now().Add(10 * time.Second); len(spawns(t, calls)) == 0; time.Sleep(10 * time.Millisecond) {
				if time.Now().After(deadline) {
					return
				}
			}
			cancel()
		}()
		return func() { rec.working-- }
	}
	start := time.Now()
	_, err := h.Read(portaldialogs.Caller{Handle: "/req/1"}, ReadText)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Read = %v, want cancelled", err)
	}
	if time.Since(start) > 20*time.Second {
		t.Fatal("cancelling waited for tesseract to finish")
	}
	s := spawns(t, calls)
	if len(s) != 1 || rec.working != 0 || len(rec.copied) != 0 {
		t.Fatalf("spawns %q, indicator %d, copied %v", s, rec.working, rec.copied)
	}
	pid, err := strconv.Atoi(strings.Fields(s[0])[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("tesseract %d still runs: %v", pid, err)
	}
}

func TestDismissStopsTesseract(t *testing.T) {
	calls := fakeTesseract(t, "exec sleep 30\n")
	h, _, _, _ := readHost(t, solid(200, 100, color.RGBA{255, 255, 255, 255}))
	go func() {
		for deadline := time.Now().Add(10 * time.Second); len(spawns(t, calls)) == 0 && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		}
		h.Dismiss("/req/2")
	}()
	if _, err := h.Read(portaldialogs.Caller{Handle: "/req/2"}, ReadText); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read = %v, want cancelled", err)
	}
}

func TestTesseractReadsAFixtureLine(t *testing.T) {
	if !OCRAvailable() {
		t.Skip("tesseract is not installed (the nix dev shell carries it)")
	}
	face, err := render.NewFixtureTypeface()
	if err != nil {
		t.Fatal(err)
	}
	w, h := 360, 60
	data := make([]byte, render.Stride(w)*h)
	cv := render.New(data, render.Stride(w), w, h)
	cv.FillRect(render.Rect{W: w, H: h}, render.RGB(255, 255, 255))
	face.Draw(cv, face.Shape("Disk quota exceeded", 28), 10, 40, render.RGB(0, 0, 0))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(data); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = data[i+2], data[i+1], data[i], data[i+3]
	}
	got, err := recognize(context.Background(), img, []string{"eng"})
	if err != nil || got != "Disk quota exceeded" {
		t.Fatalf("tesseract read %q, %v", got, err)
	}
}
