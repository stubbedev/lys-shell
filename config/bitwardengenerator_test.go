package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/stubbedev/lys-shell/service/bitwarden/generator"
)

func loadBitwarden(t *testing.T, body string) (*Config, error) {
	t.Helper()
	return LoadFile(writeFile(t, t.TempDir(), "config.yaml", "modules:\n  bitwarden:\n"+body))
}

func TestGeneratorPresetsLoad(t *testing.T) {
	cfg, err := loadBitwarden(t, `    generator-passwords:
      - {name: PIN-safe, length: 12, charsets: [digits, uppercase, digits], avoid-ambiguous: true}
      - {name: Bare}
    generator-passphrases:
      - {name: Spoken, words: 20, separator: " ", capitalize: false, number: false}
      - {name: Bare}
`)
	if err != nil {
		t.Fatalf("valid presets refused: %v", err)
	}
	bw := cfg.Bitwarden
	if len(bw.GeneratorPasswords) != 2 || len(bw.GeneratorPassphrases) != 2 {
		t.Fatalf("presets %+v %+v", bw.GeneratorPasswords, bw.GeneratorPassphrases)
	}
	pin := bw.GeneratorPasswords[0].Rules()
	if bw.GeneratorPasswords[0].Name != "PIN-safe" || pin.Length.Int() != 12 || !pin.AvoidAmbiguous ||
		!slices.Equal(pin.Charsets.List(), []generator.Charset{generator.Uppercase, generator.Digits}) {
		t.Errorf("PIN-safe rules %+v, charsets %v", pin, pin.Charsets.List())
	}
	if got := bw.GeneratorPasswords[1].Rules(); got != DefaultsBitwardenPasswordPreset().Rules() {
		t.Errorf("a bare password preset is %+v, want the defaults", got)
	}
	spoken := bw.GeneratorPassphrases[0].Rules()
	if spoken != (generator.Passphrase{Words: spoken.Words, Separator: " "}) || spoken.Words.Int() != 20 {
		t.Errorf("Spoken rules %+v", spoken)
	}
	bare := bw.GeneratorPassphrases[1].Rules()
	if bare.Words.Int() != 6 || bare.Separator != "-" || !bare.Capitalize || !bare.Number {
		t.Errorf("a bare passphrase preset is %+v, want 6 capitalised words with a number, by -", bare)
	}
}

func TestGeneratorDefaultPresets(t *testing.T) {
	bw := DefaultsBitwarden()
	if len(bw.GeneratorPasswords) != 1 || bw.GeneratorPasswords[0].Name != "Password" || len(bw.GeneratorPasswords[0].Rules().Charsets.List()) != 4 {
		t.Errorf("default password presets %+v", bw.GeneratorPasswords)
	}
	if len(bw.GeneratorPassphrases) != 1 || bw.GeneratorPassphrases[0].Name != "Passphrase" {
		t.Errorf("default passphrase presets %+v", bw.GeneratorPassphrases)
	}
	if got := Encode(bw.GeneratorPasswords[0]).(map[string]any)["charsets"]; !slices.Equal(got.([]any), []any{"lowercase", "uppercase", "digits", "symbols"}) {
		t.Errorf("charsets encode as %v", got)
	}
}

func TestGeneratorPresetsRefuseBadValues(t *testing.T) {
	for _, c := range []struct{ name, body, field, err string }{
		{
			"length 0", "    generator-passwords: [{name: x, length: 0}]\n",
			"modules.bitwarden.generator-passwords.length", "password length out of range: 0, valid 5-128",
		},
		{
			"length above", "    generator-passwords: [{name: x, length: 129}]\n",
			"modules.bitwarden.generator-passwords.length", "password length out of range: 129, valid 5-128",
		},
		{
			"no charset", "    generator-passwords: [{name: x, charsets: []}]\n",
			"modules.bitwarden.generator-passwords.charsets", "no character set selected, expected one of `lowercase`, `uppercase`, `digits`, `symbols`",
		},
		{
			"unknown charset", "    generator-passwords: [{name: x, charsets: [emoji]}]\n",
			"modules.bitwarden.generator-passwords.charsets", "unknown variant `emoji`",
		},
		{
			"no name", "    generator-passwords: [{length: 12}]\n",
			"modules.bitwarden.generator-passwords", "missing field `name`",
		},
		{
			"too few words", "    generator-passphrases: [{name: x, words: 2}]\n",
			"modules.bitwarden.generator-passphrases.words", "passphrase word count out of range: 2, valid 3-20",
		},
		{
			"too many words", "    generator-passphrases: [{name: x, words: 21}]\n",
			"modules.bitwarden.generator-passphrases.words", "passphrase word count out of range: 21, valid 3-20",
		},
		{
			"fractional words", "    generator-passphrases: [{name: x, words: 4.5}]\n",
			"modules.bitwarden.generator-passphrases.words", "invalid type: floating point `4.5`, expected u32",
		},
		{
			"negative length", "    generator-passwords: [{name: x, length: -5}]\n",
			"modules.bitwarden.generator-passwords.length", "invalid value: integer `-5`, expected u32",
		},
	} {
		cfg, err := loadBitwarden(t, c.body)
		if err == nil || !strings.Contains(err.Error(), "Field: "+c.field+"\n") || !strings.Contains(err.Error(), "Error: "+c.err) {
			t.Errorf("%s: err = %v, want %q under %s", c.name, err, c.err, c.field)
			continue
		}
		if len(cfg.Bitwarden.GeneratorPasswords) != 1 || len(cfg.Bitwarden.GeneratorPassphrases) != 1 {
			t.Errorf("%s: kept %+v %+v, want the defaults", c.name, cfg.Bitwarden.GeneratorPasswords, cfg.Bitwarden.GeneratorPassphrases)
		}
	}
}

func TestNewPasswordCharsets(t *testing.T) {
	if _, err := NewPasswordCharsets("emoji"); err == nil || !strings.Contains(err.Error(), `"emoji"`) {
		t.Errorf("an unknown class: %v", err)
	}
	c, err := NewPasswordCharsets(PasswordCharsetSymbols)
	if err != nil || !c.Set().Has(generator.Symbols) || c.Set().Has(generator.Lowercase) {
		t.Errorf("symbols only: %v, %v", c.Set().List(), err)
	}
	for _, v := range enumVariants(typeOf[PasswordCharset]()) {
		if _, ok := passwordCharsets[PasswordCharset(v)]; !ok {
			t.Errorf("variant %s maps to no generator class", v)
		}
	}
}

func TestGeneratorBoundsAreSpins(t *testing.T) {
	for name, c := range map[string]struct {
		meta   FieldMeta
		lo, hi float64
	}{
		"length": {MetaOf[PasswordLength](), generator.MinLength, generator.MaxLength},
		"words":  {MetaOf[PassphraseWords](), generator.MinWords, generator.MaxWords},
	} {
		if c.meta.Kind != FieldInt || c.meta.Min != c.lo || c.meta.Max != c.hi {
			t.Errorf("%s meta %+v, want an int in %v-%v", name, c.meta, c.lo, c.hi)
		}
	}
}
