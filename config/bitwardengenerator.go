package config

import (
	"fmt"

	"github.com/stubbedev/lys-shell/service/bitwarden/generator"
)

// PasswordCharset is a generator character class.
//
// A class of characters a generated password draws from.
type PasswordCharset string

// Password character classes.
const (
	// Lowercase letters, a-z.
	PasswordCharsetLowercase PasswordCharset = "lowercase"
	// Uppercase letters, A-Z.
	PasswordCharsetUppercase PasswordCharset = "uppercase"
	// Digits, 0-9.
	PasswordCharsetDigits PasswordCharset = "digits"
	// The symbols `!@#$%^&*`.
	PasswordCharsetSymbols PasswordCharset = "symbols"
)

var _ = registerEnum(PasswordCharsetLowercase, PasswordCharsetUppercase, PasswordCharsetDigits, PasswordCharsetSymbols)

var passwordCharsets = map[PasswordCharset]generator.Charset{
	PasswordCharsetLowercase: generator.Lowercase,
	PasswordCharsetUppercase: generator.Uppercase,
	PasswordCharsetDigits:    generator.Digits,
	PasswordCharsetSymbols:   generator.Symbols,
}

// PasswordCharsets is a password preset's classes, refused at load
// when empty.
//
// The character classes a generated password draws from, at least one
// of `lowercase`, `uppercase`, `digits` and `symbols` (`!@#$%^&*`).
// Every class listed appears in the password at least once.
type PasswordCharsets struct {
	listed []PasswordCharset
	set    generator.Charsets
}

// NewPasswordCharsets validates a list of character classes.
func NewPasswordCharsets(listed ...PasswordCharset) (PasswordCharsets, error) {
	cs := make([]generator.Charset, len(listed))
	for i, c := range listed {
		g, ok := passwordCharsets[c]
		if !ok {
			return PasswordCharsets{}, fmt.Errorf("%w: %q", generator.ErrCharset, c)
		}
		cs[i] = g
	}
	set, err := generator.NewCharsets(cs...)
	if err != nil {
		return PasswordCharsets{}, fmt.Errorf("%w, %s", err, expectedOneOf(enumVariants(typeOf[PasswordCharset]())))
	}
	return PasswordCharsets{listed: listed, set: set}, nil
}

func mustPasswordCharsets(listed ...PasswordCharset) PasswordCharsets {
	c, err := NewPasswordCharsets(listed...)
	if err != nil {
		panic(err)
	}
	return c
}

// Set is the validated classes.
func (c PasswordCharsets) Set() generator.Charsets { return c.set }

// UnmarshalConfig implements Unmarshaler.
func (c *PasswordCharsets) UnmarshalConfig(v any) error {
	var listed []PasswordCharset
	if err := decodeInto(valueOf(&listed), v); err != nil {
		return err
	}
	parsed, err := NewPasswordCharsets(listed...)
	if err != nil {
		return err
	}
	*c = parsed
	return nil
}

// MarshalConfig implements Marshaler.
func (c PasswordCharsets) MarshalConfig() any {
	out := make([]any, len(c.listed))
	for i, l := range c.listed {
		out[i] = string(l)
	}
	return out
}

func (PasswordCharsets) configSchema(g *schemaGen) Schema {
	return Schema{
		"description": typeDoc(typeOf[PasswordCharsets]()),
		"type":        "array",
		"items":       g.subschema(typeOf[PasswordCharset]()),
		"minItems":    1,
	}
}

// PasswordLength is a password preset's length, refused at load
// outside 5-128.
//
// A generated password's length in characters, 5-128.
type PasswordLength struct{ length generator.Length }

func mustPasswordLength(n int) PasswordLength {
	l, err := generator.NewLength(n)
	if err != nil {
		panic(err)
	}
	return PasswordLength{l}
}

// Length is the validated length.
func (l PasswordLength) Length() generator.Length { return l.length }

// UnmarshalConfig implements Unmarshaler.
func (l *PasswordLength) UnmarshalConfig(v any) error {
	n, err := decodeInteger[uint32](v)
	if err != nil {
		return err
	}
	parsed, err := generator.NewLength(int(n))
	if err != nil {
		return err
	}
	*l = PasswordLength{parsed}
	return nil
}

// MarshalConfig implements Marshaler.
func (l PasswordLength) MarshalConfig() any { return int64(l.length.Int()) }

func (PasswordLength) configSchema(*schemaGen) Schema {
	return uint32RangeSchema(typeOf[PasswordLength](), generator.MinLength, generator.MaxLength)
}

// PassphraseWords is a passphrase preset's word count, refused at load
// outside 3-20.
//
// A generated passphrase's number of words, 3-20.
type PassphraseWords struct{ words generator.Words }

func mustPassphraseWords(n int) PassphraseWords {
	w, err := generator.NewWords(n)
	if err != nil {
		panic(err)
	}
	return PassphraseWords{w}
}

// Words is the validated word count.
func (w PassphraseWords) Words() generator.Words { return w.words }

// UnmarshalConfig implements Unmarshaler.
func (w *PassphraseWords) UnmarshalConfig(v any) error {
	n, err := decodeInteger[uint32](v)
	if err != nil {
		return err
	}
	parsed, err := generator.NewWords(int(n))
	if err != nil {
		return err
	}
	*w = PassphraseWords{parsed}
	return nil
}

// MarshalConfig implements Marshaler.
func (w PassphraseWords) MarshalConfig() any { return int64(w.words.Int()) }

func (PassphraseWords) configSchema(*schemaGen) Schema {
	return uint32RangeSchema(typeOf[PassphraseWords](), generator.MinWords, generator.MaxWords)
}

// BitwardenPasswordPreset is one `modules.bitwarden.generator-passwords`
// entry.
//
// A random-character password the Bitwarden dropdown's generator makes
// and copies.
//
// ## Example
//
// ```yaml
// modules: {bitwarden: {generator-passwords: [{name: PIN-safe, length: 12, charsets: [uppercase, digits], avoid-ambiguous: true}]}}
// ```
type BitwardenPasswordPreset struct {
	// Name the generator lists the preset by.
	Name string `cfg:"name,required"`
	// Length in characters, 5-128.
	Length PasswordLength `cfg:"length,default"`
	// Character classes to draw from; each appears at least once.
	Charsets PasswordCharsets `cfg:"charsets,default"`
	// Leave out the easily confused characters `l`, `I`, `O`, `0` and `1`.
	AvoidAmbiguous bool `cfg:"avoid-ambiguous,default"`
}

// DefaultsBitwardenPasswordPreset returns the schema defaults.
func DefaultsBitwardenPasswordPreset() BitwardenPasswordPreset {
	return BitwardenPasswordPreset{
		Length:   mustPasswordLength(20),
		Charsets: mustPasswordCharsets(PasswordCharsetLowercase, PasswordCharsetUppercase, PasswordCharsetDigits, PasswordCharsetSymbols),
	}
}

func (p *BitwardenPasswordPreset) setDefaults() { *p = DefaultsBitwardenPasswordPreset() }

func (BitwardenPasswordPreset) noStructDefault() {}

// Rules are the preset's generator rules.
func (p BitwardenPasswordPreset) Rules() generator.Password {
	return generator.Password{Length: p.Length.Length(), Charsets: p.Charsets.Set(), AvoidAmbiguous: p.AvoidAmbiguous}
}

// BitwardenPassphrasePreset is one
// `modules.bitwarden.generator-passphrases` entry.
//
// A passphrase of words from the EFF large wordlist the Bitwarden
// dropdown's generator makes and copies.
//
// ## Example
//
// ```yaml
// modules: {bitwarden: {generator-passphrases: [{name: Spoken, words: 5, separator: " ", capitalize: false, number: false}]}}
// ```
type BitwardenPassphrasePreset struct {
	// Name the generator lists the preset by.
	Name string `cfg:"name,required"`
	// Number of words, 3-20.
	Words PassphraseWords `cfg:"words,default"`
	// Text between the words.
	Separator string `cfg:"separator,default"`
	// Capitalise the first letter of every word.
	Capitalize bool `cfg:"capitalize,default"`
	// Append a digit to one of the words.
	Number bool `cfg:"number,default"`
}

// DefaultsBitwardenPassphrasePreset returns the schema defaults.
func DefaultsBitwardenPassphrasePreset() BitwardenPassphrasePreset {
	return BitwardenPassphrasePreset{
		Words:      mustPassphraseWords(6),
		Separator:  "-",
		Capitalize: true,
		Number:     true,
	}
}

func (p *BitwardenPassphrasePreset) setDefaults() { *p = DefaultsBitwardenPassphrasePreset() }

func (BitwardenPassphrasePreset) noStructDefault() {}

// Rules are the preset's generator rules.
func (p BitwardenPassphrasePreset) Rules() generator.Passphrase {
	return generator.Passphrase{Words: p.Words.Words(), Separator: p.Separator, Capitalize: p.Capitalize, Number: p.Number}
}
