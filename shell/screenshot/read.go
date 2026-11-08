package screenshot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	"github.com/stubbedev/lys-desktop/desktopentry"
	"github.com/stubbedev/lys-lib/portaldialogs"

	"github.com/stubbedev/lys-shell/service/sharepreview"
)

// ReadKind is what Read looks for in a region of the screen.
type ReadKind string

// What Read can read.
const (
	ReadText ReadKind = "text"
	ReadQR   ReadKind = "qr"
)

// ParseReadKind is the kind a CLI or D-Bus argument names.
func ParseReadKind(s string) (ReadKind, error) {
	switch k := ReadKind(s); k {
	case ReadText, ReadQR:
		return k, nil
	}
	return "", fmt.Errorf("unknown screen reading %q: want %s or %s", s, ReadText, ReadQR)
}

// Tesseract is the OCR engine's binary.
const Tesseract = "tesseract"

const (
	ocrTimeout   = 30 * time.Second
	upscaleBelow = 1000
)

var (
	// ErrNoTesseract answers a text reading on a system without tesseract.
	ErrNoTesseract = fmt.Errorf("%s is not installed, so text cannot be read from the screen: %w", Tesseract, exec.ErrNotFound)
	// ErrNoCode answers a QR reading of a region that holds no code.
	ErrNoCode = errors.New("no code found")
	// ErrNoText answers a text reading of a region tesseract found no text in.
	ErrNoText = errors.New("no text found")
)

// OCRAvailable reports whether tesseract is on PATH.
func OCRAvailable() bool {
	_, err := exec.LookPath(Tesseract)
	return err == nil
}

// Read lets the user drag a region and reads it: its text through
// tesseract, or the QR code in it. Nothing is saved. Text lands on the
// clipboard and in a toast; a code is announced with buttons to copy it
// or, for a URL, open it. "" is a cancelled selection. A text reading
// without tesseract fails with ErrNoTesseract before anything opens, and
// Dismiss with the caller's handle stops a running tesseract.
func (h *Host) Read(c portaldialogs.Caller, kind ReadKind) (string, error) {
	var langs []string
	if kind == ReadText {
		if !OCRAvailable() {
			return "", ErrNoTesseract
		}
		var err error
		if langs, err = ocrLanguages(h.Config().OcrLanguages, desktopentry.Languages()); err != nil {
			return "", err
		}
	}
	img, ok, err := h.captureRegion(c, false)
	if err != nil || !ok {
		return "", err
	}
	switch kind {
	case ReadText:
		return h.readText(c, img, langs)
	case ReadQR:
		return h.readCode(img)
	}
	return "", fmt.Errorf("unknown screen reading %q", kind)
}

func (h *Host) readText(c portaldialogs.Caller, img *image.RGBA, langs []string) (string, error) {
	ctx, done := h.dismissable(c.Handle)
	defer done()
	if h.Working != nil {
		finished := h.Working(done)
		defer finished()
	}
	text, err := recognize(ctx, img, langs)
	if err != nil {
		return "", err
	}
	if err := h.CopyText(text); err != nil {
		return "", fmt.Errorf("cannot copy the text: %w", err)
	}
	first, _, _ := strings.Cut(text, "\n")
	h.Toast(first, "ld-scan-text-symbolic")
	return text, nil
}

func (h *Host) readCode(img image.Image) (string, error) {
	payload, err := DecodeQR(img)
	if err != nil {
		return "", err
	}
	actions := []Action{ActionCopy}
	if IsURL(payload) {
		actions = []Action{ActionOpen, ActionCopy}
	}
	h.announce(Notice{
		Summary: "QR code",
		Body:    payload,
		Icon:    "ld-scan-qr-code-symbolic",
		Actions: actions,
		Respond: func(a Action) {
			switch a {
			case ActionCopy:
				if err := h.CopyText(payload); err != nil {
					h.Toast("Not copied: "+err.Error(), "ld-scan-qr-code-symbolic")
				}
			case ActionOpen:
				h.OpenURL(payload)
			}
		},
	})
	return payload, nil
}

// IsURL reports whether a decoded payload is a link to open.
func IsURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}

// DecodeQR is the payload of the QR code in img, ErrNoCode when it holds none.
func DecodeQR(img image.Image) (string, error) {
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", fmt.Errorf("cannot read the region for a QR code: %w", err)
	}
	res, err := qrcode.NewQRCodeReader().Decode(bmp, map[gozxing.DecodeHintType]any{gozxing.DecodeHintType_TRY_HARDER: true})
	if _, missing := errors.AsType[gozxing.NotFoundException](err); missing {
		return "", ErrNoCode
	}
	if err != nil {
		return "", fmt.Errorf("cannot decode the QR code: %w", err)
	}
	return res.GetText(), nil
}

func recognize(ctx context.Context, img *image.RGBA, langs []string) (string, error) {
	bin, err := exec.LookPath(Tesseract)
	if err != nil {
		return "", ErrNoTesseract
	}
	var in bytes.Buffer
	if err := encodePNG(&in, ocrImage(img)); err != nil {
		return "", fmt.Errorf("cannot hand the region to %s: %w", Tesseract, err)
	}
	ctx, cancel := context.WithTimeout(ctx, ocrTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "stdin", "stdout", "-l", strings.Join(langs, "+")) //nolint:gosec // tesseract from PATH, languages from the config
	cmd.Stdin = &in
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "", fmt.Errorf("%s did not finish within %s", Tesseract, ocrTimeout)
	case ctx.Err() != nil:
		return "", context.Canceled
	case err != nil:
		return "", fmt.Errorf("%s -l %s: %w: %s", Tesseract, strings.Join(langs, "+"), err, strings.TrimSpace(stderr.String()))
	}
	text := strings.TrimSpace(strings.ReplaceAll(out.String(), "\f", ""))
	if text == "" {
		return "", ErrNoText
	}
	return text, nil
}

func ocrImage(img *image.RGBA) *image.RGBA {
	b := img.Bounds()
	if max(b.Dx(), b.Dy()) >= upscaleBelow {
		return img
	}
	return sharepreview.Resize(img, 2*b.Dx(), 2*b.Dy())
}

var tesseractLanguages = map[string]string{
	"ar": "ara", "bg": "bul", "ca": "cat", "cs": "ces", "da": "dan", "de": "deu", "el": "ell",
	"en": "eng", "es": "spa", "et": "est", "fa": "fas", "fi": "fin", "fr": "fra", "he": "heb",
	"hi": "hin", "hr": "hrv", "hu": "hun", "id": "ind", "is": "isl", "it": "ita", "ja": "jpn",
	"ko": "kor", "lt": "lit", "lv": "lav", "nb": "nor", "nl": "nld", "nn": "nor", "no": "nor",
	"pl": "pol", "pt": "por", "ro": "ron", "ru": "rus", "sk": "slk", "sl": "slv", "sr": "srp",
	"sv": "swe", "th": "tha", "tr": "tur", "uk": "ukr", "vi": "vie",
	"zh_CN": "chi_sim", "zh_SG": "chi_sim", "zh_TW": "chi_tra", "zh_HK": "chi_tra", "zh": "chi_sim",
}

func ocrLanguages(configured, locale []string) ([]string, error) {
	if len(configured) > 0 {
		return configured, nil
	}
	if len(locale) == 0 {
		return []string{"eng"}, nil
	}
	for _, l := range locale {
		if code, ok := tesseractLanguages[l]; ok {
			return []string{code}, nil
		}
	}
	return nil, fmt.Errorf("the session language %s has no known tesseract language: set screenshot.ocr-languages", locale[len(locale)-1])
}
