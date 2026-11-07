package modes

import (
	"bytes"
	"compress/gzip"
	"context"
	"embed"
	"io"
	"log"
	"os"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/stubbedev/lys-shell/internal/gvariant"
	"github.com/stubbedev/lys-shell/service/launcher"
)

// emojiData holds GTK's emoji tables, extracted from libgtk-4 by
// internal/tools/emojigen. The Rust mode reads the same GResource
// entries (/org/gtk/libgtk/emoji/<lang>.data) from the loaded library;
// libgtk-4 itself carries only the English table, so English is what
// both shells find. The GVariant shape is a(aussasasu): per emoji the
// codepoints (0 stands for a variation selector or skin tone), the
// English name, the localized name, the English keywords, the
// localized keywords, and the group.
//
//go:embed emojidata/*.data.gz
var emojiData embed.FS

// emojiDataType is the tables' GVariant type.
const emojiDataType = "a(aussasasu)"

// Emoji is one emoji as the mode needs it.
type Emoji struct {
	// Glyph is the emoji itself, with no skin tone.
	Glyph string
	// Name is what to call it, localized where there is a translation.
	Name string
	// Keywords are everything the matcher sees beyond the name.
	Keywords []string
	// toned is the glyph with U+1F3FB where a skin tone goes, "" for an
	// emoji that takes none.
	toned string
}

// SkinTone is the Fitzpatrick modifier person and hand emoji take.
type SkinTone uint8

// The skin tones: none (the default yellow), then light to dark.
const (
	ToneNone SkinTone = iota
	ToneLight
	ToneMediumLight
	ToneMedium
	ToneMediumDark
	ToneDark
)

// SkinTones are the five tones an emoji can take, light to dark.
var SkinTones = [...]SkinTone{ToneLight, ToneMediumLight, ToneMedium, ToneMediumDark, ToneDark}

const (
	tonePlaceholder   = 0x1f3fb
	variationSelector = 0xfe0f
	firstEmojiBlock   = 0x1f000
)

// Toneable reports an emoji that takes a skin tone.
func (e Emoji) Toneable() bool { return e.toned != "" }

// WithTone is the glyph in tone t, the plain glyph for an emoji that takes
// none.
func (e Emoji) WithTone(t SkinTone) string {
	if e.toned == "" {
		return e.Glyph
	}
	return applyTone(e.toned, t)
}

// applyTone fills the tone placeholders of template. With no tone a
// placeholder after a character older than the emoji blocks becomes U+FE0F,
// which keeps ☝ an emoji rather than a text symbol, and goes otherwise.
func applyTone(template string, t SkinTone) string {
	var b strings.Builder
	b.Grow(len(template))
	var prev rune
	for _, r := range template {
		if r != tonePlaceholder {
			b.WriteRune(r)
			prev = r
			continue
		}
		switch {
		case t != ToneNone:
			b.WriteRune(tonePlaceholder - 1 + rune(t))
		case prev < firstEmojiBlock:
			b.WriteRune(variationSelector)
		}
	}
	return b.String()
}

// templateFromCodes assembles the glyph from GTK's code points. GTK marks
// where a skin tone goes with U+1F3FB, kept here as the placeholder, and a
// variation selector with 0, which becomes U+FE0F so the emoji renders as
// emoji rather than monochrome text. Values that are not characters (a
// surrogate, past U+10FFFF) are dropped.
func templateFromCodes(codes []uint32) string {
	var b strings.Builder
	for _, c := range codes {
		if c == 0 {
			c = variationSelector
		}
		r := rune(c)
		if c > utf8.MaxRune || !utf8.ValidRune(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// GlyphFromCodes is the glyph GTK's code points make, with no skin tone.
func GlyphFromCodes(codes []uint32) string { return applyTone(templateFromCodes(codes), ToneNone) }

// rowToEmoji turns one table row into an Emoji. Both languages' names
// and keywords are matchable; the localized name is the one shown.
func rowToEmoji(codes []uint32, nameEn, name string, keywordsEn, keywords []string) (Emoji, bool) {
	template := templateFromCodes(codes)
	if template == "" {
		return Emoji{}, false
	}
	e := Emoji{Glyph: applyTone(template, ToneNone)}
	if strings.ContainsRune(template, tonePlaceholder) {
		e.toned = template
	}
	shown := name
	if shown == "" {
		shown = nameEn
	}
	if shown == "" {
		return Emoji{}, false
	}
	var all []string
	// Compared against what is shown: an untranslated locale shows the
	// English name, which must not also be a keyword.
	if nameEn != "" && nameEn != shown {
		all = append(all, nameEn)
	}
	all = append(all, keywords...)
	for _, k := range keywordsEn {
		if !slices.Contains(keywords, k) {
			all = append(all, k)
		}
	}
	e.Name, e.Keywords = shown, all
	return e, true
}

// languagesFor is the tag widening GTK's chooser does: fr_CA.UTF-8 asks
// for fr-ca, then fr, then English; C and POSIX name no language.
func languagesFor(locale string) []string {
	tag := locale
	if i := strings.IndexAny(tag, ".@"); i >= 0 {
		tag = tag[:i]
	}
	tag = strings.ToLower(strings.ReplaceAll(tag, "_", "-"))
	var tags []string
	if tag != "" && tag != "c" && tag != "posix" {
		tags = append(tags, tag)
		if base, _, ok := strings.Cut(tag, "-"); ok {
			tags = append(tags, base)
		}
	}
	if !slices.Contains(tags, "en") {
		tags = append(tags, "en")
	}
	return tags
}

func currentLanguages() []string {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v, ok := os.LookupEnv(key); ok {
			return languagesFor(v)
		}
	}
	return languagesFor("")
}

// parseEmojiTable reads every row of one table.
func parseEmojiTable(data []byte) ([]Emoji, error) {
	v, err := gvariant.New(emojiDataType, data)
	if err != nil {
		return nil, err
	}
	rows, err := v.Elements()
	if err != nil {
		return nil, err
	}
	out := make([]Emoji, 0, len(rows))
	for _, row := range rows {
		f, err := row.Fields()
		if err != nil {
			return nil, err
		}
		codes, err := f[0].Uint32s()
		if err != nil {
			continue
		}
		nameEn, _ := f[1].Str()
		name, _ := f[2].Str()
		keywordsEn, _ := f[3].Strs()
		keywords, _ := f[4].Strs()
		if e, ok := rowToEmoji(codes, nameEn, name, keywordsEn, keywords); ok {
			out = append(out, e)
		}
	}
	return out, nil
}

var (
	emojiOnce  sync.Once
	emojiTable []Emoji
)

// AvailableEmoji reads the table for the current locale, widening to
// English. It is parsed once per process.
func AvailableEmoji() []Emoji {
	emojiOnce.Do(func() {
		for _, lang := range currentLanguages() {
			f, err := emojiData.Open("emojidata/" + lang + ".data.gz")
			if err != nil {
				continue
			}
			table, err := readGzip(f)
			_ = f.Close()
			if err != nil {
				log.Printf("launcher: emoji table %s: %v", lang, err)
				continue
			}
			if emojis, err := parseEmojiTable(table); err == nil && len(emojis) > 0 {
				emojiTable = emojis
				return
			}
		}
		log.Printf("launcher: no emoji data found; the emoji mode has nothing to show")
	})
	return emojiTable
}

func readGzip(r io.Reader) ([]byte, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, zr); err != nil { //nolint:gosec // an embedded table of known size
		return nil, err
	}
	return buf.Bytes(), nil
}

// emojiItem is the row for one emoji: the glyph and name shown, the
// keywords invisible but matchable, and the glyph alone as its info so
// an accept enters the emoji and not its name.
func emojiItem(e Emoji, tone SkinTone) launcher.Item {
	glyph := e.WithTone(tone)
	return launcher.Item{
		Display:   glyph + "  " + e.Name,
		MatchText: e.Name + " " + strings.Join(e.Keywords, " "),
		Info:      &glyph,
	}
}

// EmojiMode picks an emoji by name; accept enters it in the focused window.
type EmojiMode struct {
	emojis []Emoji
	tone   SkinTone
}

// NewEmoji reads the table (once per process); emoji that take a skin tone
// show and enter it in tone.
func NewEmoji(tone SkinTone) *EmojiMode { return &EmojiMode{emojis: AvailableEmoji(), tone: tone} }

// Name is "emoji".
func (*EmojiMode) Name() string { return "emoji" }

// Load lists every emoji.
func (m *EmojiMode) Load(context.Context) launcher.ModeState {
	items := make([]launcher.Item, len(m.emojis))
	for i, e := range m.emojis {
		items[i] = emojiItem(e, m.tone)
	}
	return launcher.ModeState{Items: items, Prompt: "emoji"}
}

// Activate enters the glyph.
func (m *EmojiMode) Activate(_ context.Context, target launcher.Target, _ launcher.ActivateKind, _ string) launcher.Action {
	i, ok := target.Row()
	if !ok || int(i) >= len(m.emojis) {
		return launcher.ActionNothing{}
	}
	return launcher.ActionType{Text: m.emojis[i].WithTone(m.tone)}
}

// AllowsCustom is false: typed text matching no emoji is not an emoji.
func (*EmojiMode) AllowsCustom() bool { return false }
