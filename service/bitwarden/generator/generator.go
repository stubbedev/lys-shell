package generator

import (
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/stubbedev/lys-lib/intconv"
)

// Charset is a class of characters a password draws from.
type Charset uint8

// The character classes.
const (
	Lowercase Charset = iota
	Uppercase
	Digits
	Symbols
	charsetCount
)

// Bounds of the rules.
const (
	MinLength = 5
	MaxLength = 128
	MinWords  = 3
	MaxWords  = 20
)

// Rule errors.
var (
	ErrLength    = errors.New("password length out of range")
	ErrWords     = errors.New("passphrase word count out of range")
	ErrNoCharset = errors.New("no character set selected")
	ErrCharset   = errors.New("unknown character set")
)

const ambiguous = "lIO01"

var alphabets = [charsetCount]string{
	Lowercase: "abcdefghijklmnopqrstuvwxyz",
	Uppercase: "ABCDEFGHIJKLMNOPQRSTUVWXYZ",
	Digits:    "0123456789",
	Symbols:   "!@#$%^&*",
}

//go:embed eff_large_wordlist.txt
var wordlist string

var words = strings.Split(strings.TrimSuffix(wordlist, "\n"), "\n")

// Alphabet is the characters c draws from, without the easily confused
// ones when avoidAmbiguous is set.
func (c Charset) Alphabet(avoidAmbiguous bool) string {
	if !avoidAmbiguous {
		return alphabets[c]
	}
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(ambiguous, r) {
			return -1
		}
		return r
	}, alphabets[c])
}

// Length is a password length within [MinLength, MaxLength]; the zero
// value is MinLength.
type Length struct{ overMin uint8 }

// NewLength validates a password length.
func NewLength(n int) (Length, error) {
	if n < MinLength || n > MaxLength {
		return Length{}, fmt.Errorf("%w: %d, valid %d-%d", ErrLength, n, MinLength, MaxLength)
	}
	return Length{overMin: uint8(n - MinLength)}, nil
}

// Int is the length in characters.
func (l Length) Int() int { return int(l.overMin) + MinLength }

// Words is a passphrase word count within [MinWords, MaxWords]; the
// zero value is MinWords.
type Words struct{ overMin uint8 }

// NewWords validates a passphrase word count.
func NewWords(n int) (Words, error) {
	if n < MinWords || n > MaxWords {
		return Words{}, fmt.Errorf("%w: %d, valid %d-%d", ErrWords, n, MinWords, MaxWords)
	}
	return Words{overMin: uint8(n - MinWords)}, nil
}

// Int is the number of words.
func (w Words) Int() int { return int(w.overMin) + MinWords }

// Charsets is a non-empty set of character classes; the zero value
// holds every class.
type Charsets struct{ excluded uint8 }

// NewCharsets is the set of cs, which must name at least one class.
func NewCharsets(cs ...Charset) (Charsets, error) {
	if len(cs) == 0 {
		return Charsets{}, ErrNoCharset
	}
	included := uint8(0)
	for _, c := range cs {
		if c >= charsetCount {
			return Charsets{}, fmt.Errorf("%w: %d", ErrCharset, c)
		}
		included |= 1 << c
	}
	return Charsets{excluded: ^included & (1<<charsetCount - 1)}, nil
}

// Has reports whether c is in the set.
func (s Charsets) Has(c Charset) bool { return c < charsetCount && s.excluded&(1<<c) == 0 }

// List is the set's classes in declaration order.
func (s Charsets) List() []Charset {
	out := make([]Charset, 0, charsetCount)
	for c := range charsetCount {
		if s.Has(c) {
			out = append(out, c)
		}
	}
	return out
}

// Password is the rules of a random-character password: every class
// in Charsets appears at least once.
type Password struct {
	Length         Length
	Charsets       Charsets
	AvoidAmbiguous bool
}

// Generate draws a password from random.
func (p Password) Generate(random io.Reader) (string, error) {
	classes := p.Charsets.List()
	alphabets := make([]string, len(classes))
	var pool strings.Builder
	for i, c := range classes {
		alphabets[i] = c.Alphabet(p.AvoidAmbiguous)
		pool.WriteString(alphabets[i])
	}
	all := pool.String()
	out := make([]byte, p.Length.Int())
	for i := range out {
		from := all
		if i < len(alphabets) {
			from = alphabets[i]
		}
		k, err := uniform(random, len(from))
		if err != nil {
			return "", err
		}
		out[i] = from[k]
	}
	for i := len(out) - 1; i > 0; i-- {
		j, err := uniform(random, i+1)
		if err != nil {
			return "", err
		}
		out[i], out[j] = out[j], out[i]
	}
	return string(out), nil
}

// Passphrase is the rules of a passphrase of words from the EFF large
// wordlist, joined by Separator, each capitalised when Capitalize is
// set, one of them followed by a digit when Number is set.
type Passphrase struct {
	Words      Words
	Separator  string
	Capitalize bool
	Number     bool
}

// Generate draws a passphrase from random.
func (p Passphrase) Generate(random io.Reader) (string, error) {
	picked := make([]string, p.Words.Int())
	for i := range picked {
		k, err := uniform(random, len(words))
		if err != nil {
			return "", err
		}
		picked[i] = words[k]
		if p.Capitalize {
			picked[i] = strings.ToUpper(picked[i][:1]) + picked[i][1:]
		}
	}
	if p.Number {
		at, err := uniform(random, len(picked))
		if err != nil {
			return "", err
		}
		digit, err := uniform(random, len(alphabets[Digits]))
		if err != nil {
			return "", err
		}
		picked[at] += alphabets[Digits][digit : digit+1]
	}
	return strings.Join(picked, p.Separator), nil
}

func uniform(random io.Reader, n int) (int, error) {
	bound := intconv.Clamp[uint32](n)
	limit := -bound % bound
	var buf [4]byte
	for {
		if _, err := io.ReadFull(random, buf[:]); err != nil {
			return 0, fmt.Errorf("generator: read randomness: %w", err)
		}
		if v := binary.LittleEndian.Uint32(buf[:]); v >= limit {
			return int(v % bound), nil
		}
	}
}
