package bar

import (
	"errors"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/transfer"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/bitwarden"
	"github.com/stubbedev/lys-shell/service/bitwarden/generator"
	"github.com/stubbedev/lys-shell/service/clipboard"
	"github.com/stubbedev/lys-shell/styling"
)

func generatorTestView(t *testing.T, v *fakeVault, cfg *config.Config) (*bitwardenView, *[]copied) {
	t.Helper()
	view, copies := bitwardenTestView(t, v, cfg)
	view.random = rand.NewChaCha8([32]byte{9})
	return view, copies
}

func onLoop(fn func()) { onHeadlessLoop(func() struct{} { fn(); return struct{}{} }) }

func copiedText(t *testing.T, c copied) string {
	t.Helper()
	b, err := c.content.Bytes("text/plain")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBitwardenGeneratorCopiesASecret(t *testing.T) {
	cfg := config.Defaults()
	v := unlockedVault()
	view, copies := generatorTestView(t, v, cfg)
	onLoop(view.toggleGenerator)
	if page(view) != bwPageGenerator || rowCount(view.genList) != 2 {
		t.Fatalf("page %s, %d preset rows", page(view), rowCount(view.genList))
	}
	if got := view.dropdownFocus(); got != widget.Widget(view.genRows[0]) {
		t.Errorf("the generator focuses %T, want the first preset", got)
	}
	presets := bwPresets(cfg.Bitwarden)
	onLoop(func() { view.generate(presets[0]) })
	onLoop(func() { view.generate(presets[1]) })
	if len(*copies) != 2 {
		t.Fatalf("%d copies", len(*copies))
	}
	pw, phrase := (*copies)[0], (*copies)[1]
	for _, c := range []copied{pw, phrase} {
		if !clipboard.IsSensitive(c.content.Mimes) || c.ttl != 30*time.Second {
			t.Errorf("generated copy mimes %v, ttl %v, want a secret cleared after 30 s", c.content.Mimes, c.ttl)
		}
	}
	if got := copiedText(t, pw); len(got) != 20 || !strings.ContainsAny(got, "!@#$%^&*") || !strings.ContainsAny(got, "0123456789") {
		t.Errorf("password %q", got)
	}
	if got := strings.Split(copiedText(t, phrase), "-"); len(got) != 6 {
		t.Errorf("passphrase words %q", got)
	}
	want := i18n.T("dropdown-bitwarden-copied-clears", i18n.Str("field", i18n.T("dropdown-bitwarden-field-passphrase")), i18n.Str("seconds", "30"))
	if view.genStatus.Text() != want || view.genStatus.HasClass("error") {
		t.Errorf("status %q, want %q", view.genStatus.Text(), want)
	}
	if v.touches != 0 {
		t.Errorf("generating counted as vault use %d times", v.touches)
	}
	cfg.Bitwarden.ClipboardClear = 0
	onLoop(func() { view.generate(presets[0]) })
	if got := (*copies)[2]; got.ttl != 0 || !clipboard.IsSensitive(got.content.Mimes) || copiedText(t, got) == copiedText(t, pw) {
		t.Errorf("clipboard-clear 0: ttl %v, mimes %v, same password %v", got.ttl, got.content.Mimes, copiedText(t, got) == copiedText(t, pw))
	}
}

func TestBitwardenGeneratorWorksWhileLocked(t *testing.T) {
	cfg := config.Defaults()
	v := &fakeVault{status: bitwarden.Status{State: bitwarden.StateLocked}}
	view, copies := generatorTestView(t, v, cfg)
	view.unlockPw.entry.SetText("hunter")
	onLoop(view.toggleGenerator)
	onLoop(view.apply)
	if page(view) != bwPageGenerator {
		t.Fatalf("a locked vault's update left the generator for %s", page(view))
	}
	onLoop(func() { view.generate(bwPresets(cfg.Bitwarden)[0]) })
	if len(*copies) != 1 {
		t.Fatalf("%d copies while locked", len(*copies))
	}
	onLoop(view.toggleGenerator)
	if page(view) != bwPageUnlock || view.unlockPw.entry.Text() != "" || view.genStatus.Visible() {
		t.Errorf("leaving: page %s, unlock box %q, status shown %v", page(view), view.unlockPw.entry.Text(), view.genStatus.Visible())
	}
}

func TestBitwardenGeneratorDropsAPendingReprompt(t *testing.T) {
	cfg := config.Defaults()
	v := unlockedVault()
	view, copies := generatorTestView(t, v, cfg)
	onLoop(func() { view.copyField(v.items[1], bwPassword) })
	view.repromptPw.entry.SetText("hunt")
	onLoop(view.toggleGenerator)
	if view.copying != nil || view.repromptPw.entry.Text() != "" || page(view) != bwPageGenerator {
		t.Fatalf("opening the generator kept the reprompt: %+v, box %q, page %s", view.copying, view.repromptPw.entry.Text(), page(view))
	}
	onLoop(view.toggleGenerator)
	if page(view) != bwPageVault || len(*copies) != 0 {
		t.Errorf("leaving: page %s, %d copies", page(view), len(*copies))
	}
}

func TestBitwardenGeneratorClosesWithThePopover(t *testing.T) {
	cfg := config.Defaults()
	view, _ := generatorTestView(t, unlockedVault(), cfg)
	onLoop(view.toggleGenerator)
	onLoop(func() { view.generate(bwPresets(cfg.Bitwarden)[0]) })
	onLoop(view.dropdownHidden)
	if view.generating || page(view) != bwPageVault || view.genStatus.Visible() {
		t.Errorf("after hiding: generating %v, page %s, status %q", view.generating, page(view), view.genStatus.Text())
	}
}

func TestBitwardenGeneratorWithoutPresets(t *testing.T) {
	cfg := config.Defaults()
	cfg.Bitwarden.GeneratorPasswords = nil
	cfg.Bitwarden.GeneratorPassphrases = nil
	view, copies := generatorTestView(t, unlockedVault(), cfg)
	onLoop(view.toggleGenerator)
	if rowCount(view.genList) != 0 || onHeadlessLoop(view.genSt.Visible) != "empty" || view.dropdownFocus() != nil || len(*copies) != 0 {
		t.Errorf("no presets: %d rows, shows %s, focus %T, %d copies", rowCount(view.genList), onHeadlessLoop(view.genSt.Visible), view.dropdownFocus(), len(*copies))
	}
	cfg.Bitwarden.GeneratorPassphrases = config.DefaultsBitwarden().GeneratorPassphrases
	onLoop(func() { view.toggleGenerator(); view.toggleGenerator() })
	if rowCount(view.genList) != 1 || onHeadlessLoop(view.genSt.Visible) != "presets" {
		t.Errorf("a preset added while closed: %d rows, shows %s", rowCount(view.genList), onHeadlessLoop(view.genSt.Visible))
	}
}

type brokenRandom struct{}

var errNoRandom = errors.New("no randomness")

func (brokenRandom) Read([]byte) (int, error) { return 0, errNoRandom }

func TestBitwardenGeneratorFailuresCopyNothing(t *testing.T) {
	cfg := config.Defaults()
	view, copies := generatorTestView(t, unlockedVault(), cfg)
	view.random = brokenRandom{}
	onLoop(view.toggleGenerator)
	onLoop(func() { view.generate(bwPresets(cfg.Bitwarden)[0]) })
	if len(*copies) != 0 || !view.genStatus.HasClass("error") || !strings.Contains(view.genStatus.Text(), errNoRandom.Error()) {
		t.Errorf("randomness failure: %d copies, status %q", len(*copies), view.genStatus.Text())
	}
	view.random = rand.NewChaCha8([32]byte{1})
	clipErr := errors.New("no clipboard")
	view.host.copy = func(transfer.Content, time.Duration) error { return clipErr }
	onLoop(func() { view.generate(bwPresets(cfg.Bitwarden)[1]) })
	if !view.genStatus.HasClass("error") || view.genStatus.Text() != clipErr.Error() {
		t.Errorf("clipboard failure: status %q", view.genStatus.Text())
	}
}

func TestBitwardenPresetRows(t *testing.T) {
	cfg := config.DefaultsBitwarden()
	pin := config.DefaultsBitwardenPasswordPreset()
	pin.Name = "PIN-safe"
	var err error
	if pin.Charsets, err = config.NewPasswordCharsets(config.PasswordCharsetDigits, config.PasswordCharsetUppercase); err != nil {
		t.Fatal(err)
	}
	cfg.GeneratorPasswords = append(cfg.GeneratorPasswords, pin)
	got := bwPresets(cfg)
	if len(got) != 3 {
		t.Fatalf("%d presets", len(got))
	}
	wantCaption := i18n.T("dropdown-bitwarden-generator-password-caption", i18n.Int("length", 20), i18n.Str("charsets", "A-Z 0-9"))
	if got[1].name != "PIN-safe" || got[1].caption != wantCaption || got[1].rules != pin.Rules() {
		t.Errorf("PIN-safe row %+v, want caption %q", got[1], wantCaption)
	}
	if !strings.Contains(got[0].caption, "a-z A-Z 0-9 !@#") || got[0].field != i18n.T("dropdown-bitwarden-field-password") {
		t.Errorf("password row %+v", got[0])
	}
	phrase, ok := got[2].rules.(generator.Passphrase)
	if !ok || got[2].name != "Passphrase" || phrase.Words.Int() != 6 || got[2].field != i18n.T("dropdown-bitwarden-field-passphrase") {
		t.Errorf("passphrase row %+v", got[2])
	}
	for c := range generator.Symbols + 1 {
		if bwCharsetTokens[c] == "" {
			t.Errorf("class %d has no caption token", c)
		}
	}
}

func TestBitwardenGeneratorIconsAreBundled(t *testing.T) {
	names := []string{"ld-shuffle-symbolic", "ld-copy-symbolic"}
	for _, p := range bwPresets(config.DefaultsBitwarden()) {
		names = append(names, p.icon)
	}
	for _, name := range names {
		assertBundledIcon(t, name)
	}
}

func TestBitwardenGeneratorNeedsTheHost(t *testing.T) {
	cfg := config.Defaults()
	style := computeStyle(cfg, styling.Default())
	broken := bitwardenDropdown(ModuleContext{Config: cfg, Font: testFont(t), Style: &style}).(*bitwardenView)
	if broken.genBtn.Visible() {
		t.Error("the generator is offered without a vault host")
	}
	view, _ := generatorTestView(t, &fakeVault{}, cfg)
	if !view.genBtn.Visible() {
		t.Error("the generator is not offered while logged out")
	}
}
