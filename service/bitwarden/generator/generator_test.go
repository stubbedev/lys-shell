package generator

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"unicode"
)

func seeded(seed byte) *rand.ChaCha8 { return rand.NewChaCha8([32]byte{seed}) }

func mustCharsets(t *testing.T, cs ...Charset) Charsets {
	t.Helper()
	s, err := NewCharsets(cs...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustLength(t *testing.T, n int) Length {
	t.Helper()
	l, err := NewLength(n)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func mustWords(t *testing.T, n int) Words {
	t.Helper()
	w, err := NewWords(n)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestWordlistIsTheEFFLargeList(t *testing.T) {
	if len(words) != 7776 {
		t.Fatalf("%d words, want 7776", len(words))
	}
	seen := make(map[string]bool, len(words))
	for _, w := range words {
		if seen[w] {
			t.Errorf("%q twice", w)
		}
		seen[w] = true
		if w == "" || strings.Trim(w, "abcdefghijklmnopqrstuvwxyz-") != "" || w[0] == '-' {
			t.Errorf("word %q is not lowercase ASCII starting with a letter", w)
		}
	}
	if words[0] != "abacus" || words[len(words)-1] != "zoom" {
		t.Errorf("first %q, last %q", words[0], words[len(words)-1])
	}
}

func TestLengthBounds(t *testing.T) {
	for _, n := range []int{MinLength, 20, MaxLength} {
		l, err := NewLength(n)
		if err != nil || l.Int() != n {
			t.Errorf("NewLength(%d) = %d, %v", n, l.Int(), err)
		}
	}
	for _, n := range []int{-1, 0, MinLength - 1, MaxLength + 1} {
		if _, err := NewLength(n); !errors.Is(err, ErrLength) {
			t.Errorf("NewLength(%d) error %v, want ErrLength", n, err)
		}
	}
	if got := (Length{}).Int(); got != MinLength {
		t.Errorf("zero Length is %d, want %d", got, MinLength)
	}
	if _, err := NewLength(0); err == nil || err.Error() != "password length out of range: 0, valid 5-128" {
		t.Errorf("length 0 error %q", err)
	}
}

func TestWordsBounds(t *testing.T) {
	for _, n := range []int{MinWords, 6, MaxWords} {
		w, err := NewWords(n)
		if err != nil || w.Int() != n {
			t.Errorf("NewWords(%d) = %d, %v", n, w.Int(), err)
		}
	}
	for _, n := range []int{0, MinWords - 1, MaxWords + 1} {
		if _, err := NewWords(n); !errors.Is(err, ErrWords) {
			t.Errorf("NewWords(%d) error %v, want ErrWords", n, err)
		}
	}
	if got := (Words{}).Int(); got != MinWords {
		t.Errorf("zero Words is %d, want %d", got, MinWords)
	}
}

func TestCharsets(t *testing.T) {
	s := mustCharsets(t, Symbols, Lowercase, Symbols)
	if !slices.Equal(s.List(), []Charset{Lowercase, Symbols}) || s.Has(Digits) || s.Has(Uppercase) {
		t.Errorf("set of symbols, lowercase, symbols lists %v", s.List())
	}
	if got := (Charsets{}).List(); !slices.Equal(got, []Charset{Lowercase, Uppercase, Digits, Symbols}) {
		t.Errorf("zero Charsets lists %v, want every class", got)
	}
	if _, err := NewCharsets(); !errors.Is(err, ErrNoCharset) {
		t.Errorf("empty set error %v, want ErrNoCharset", err)
	}
	if _, err := NewCharsets(Lowercase, charsetCount); !errors.Is(err, ErrCharset) {
		t.Errorf("unknown class error %v, want ErrCharset", err)
	}
	if s.Has(charsetCount) {
		t.Error("an unknown class is in the set")
	}
}

func TestAlphabetAvoidsAmbiguousCharacters(t *testing.T) {
	want := [charsetCount]string{
		Lowercase: "abcdefghijkmnopqrstuvwxyz",
		Uppercase: "ABCDEFGHJKLMNPQRSTUVWXYZ",
		Digits:    "23456789",
		Symbols:   "!@#$%^&*",
	}
	for c := range charsetCount {
		if got := c.Alphabet(false); got != alphabets[c] || strings.ContainsAny(alphabets[c], ambiguous) == (c == Symbols) {
			t.Errorf("%d: full alphabet %q", c, got)
		}
		if got := c.Alphabet(true); got != want[c] {
			t.Errorf("%d: unambiguous alphabet %q, want %q", c, got, want[c])
		}
	}
}

func classOf(t *testing.T, b byte) Charset {
	t.Helper()
	for c := range charsetCount {
		if strings.IndexByte(alphabets[c], b) >= 0 {
			return c
		}
	}
	t.Fatalf("%q is in no alphabet", b)
	return 0
}

func TestPasswordHoldsEveryEnabledClassAndNoOther(t *testing.T) {
	const samples = 300
	random := seeded(1)
	for mask := 1; mask < 1<<charsetCount; mask++ {
		var enabled []Charset
		for c := range charsetCount {
			if mask&(1<<c) != 0 {
				enabled = append(enabled, c)
			}
		}
		for _, avoid := range []bool{false, true} {
			p := Password{Length: mustLength(t, MinLength), Charsets: mustCharsets(t, enabled...), AvoidAmbiguous: avoid}
			for range samples {
				pw, err := p.Generate(random)
				if err != nil {
					t.Fatal(err)
				}
				if len(pw) != MinLength {
					t.Fatalf("%q is not %d long", pw, MinLength)
				}
				found := map[Charset]bool{}
				for i := range len(pw) {
					c := classOf(t, pw[i])
					if !p.Charsets.Has(c) {
						t.Fatalf("classes %v: %q holds %q of a disabled class", enabled, pw, pw[i])
					}
					if avoid && strings.IndexByte(ambiguous, pw[i]) >= 0 {
						t.Fatalf("avoiding ambiguous: %q holds %q", pw, pw[i])
					}
					found[c] = true
				}
				for _, c := range enabled {
					if !found[c] {
						t.Fatalf("classes %v: %q lacks class %d", enabled, pw, c)
					}
				}
			}
		}
	}
}

func TestPasswordDrawsTheWholeAlphabetEvenly(t *testing.T) {
	p := Password{Length: mustLength(t, MaxLength), Charsets: mustCharsets(t, Lowercase, Uppercase, Digits, Symbols)}
	random := seeded(2)
	counts := map[rune]int{}
	total := 0
	for range 200 {
		pw, err := p.Generate(random)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range pw {
			counts[r]++
			total++
		}
	}
	pool := strings.Join(alphabets[:], "")
	if len(counts) != len(pool) {
		t.Fatalf("%d distinct characters drawn, want all %d", len(counts), len(pool))
	}
	mean := float64(total) / float64(len(pool))
	for _, r := range pool {
		if got := float64(counts[r]); got < 0.8*mean || got > 1.3*mean {
			t.Errorf("%q drawn %v times, mean %.0f", r, got, mean)
		}
	}
}

func TestPasswordIsDeterministicInItsRandomness(t *testing.T) {
	p := Password{Length: mustLength(t, 32), Charsets: mustCharsets(t, Lowercase, Digits)}
	a, errA := p.Generate(seeded(3))
	b, errB := p.Generate(seeded(3))
	c, errC := p.Generate(seeded(4))
	if errA != nil || errB != nil || errC != nil {
		t.Fatal(errA, errB, errC)
	}
	if a != b || a == c {
		t.Errorf("same seed %q and %q, other seed %q", a, b, c)
	}
}

func TestPassphraseShape(t *testing.T) {
	inList := make(map[string]bool, len(words))
	for _, w := range words {
		inList[w] = true
	}
	random := seeded(5)
	for _, p := range []Passphrase{
		{Words: mustWords(t, 6), Separator: "-", Capitalize: true, Number: true},
		{Words: mustWords(t, MinWords), Separator: " "},
		{Words: mustWords(t, MaxWords), Separator: ".", Number: true},
		{Words: mustWords(t, 4), Separator: "_", Capitalize: true},
	} {
		for range 200 {
			phrase, err := p.Generate(random)
			if err != nil {
				t.Fatal(err)
			}
			parts := strings.Split(phrase, p.Separator)
			if len(parts) != p.Words.Int() {
				t.Fatalf("%q splits into %d words, want %d", phrase, len(parts), p.Words.Int())
			}
			digits := 0
			for _, part := range parts {
				word := strings.TrimRightFunc(part, unicode.IsDigit)
				digits += len(part) - len(word)
				if p.Capitalize != unicode.IsUpper(rune(word[0])) {
					t.Fatalf("%q: capitalised %v, word %q", phrase, p.Capitalize, word)
				}
				if !inList[strings.ToLower(word[:1])+word[1:]] {
					t.Fatalf("%q: %q is not in the wordlist", phrase, word)
				}
			}
			if want := map[bool]int{true: 1, false: 0}[p.Number]; digits != want {
				t.Fatalf("%q holds %d digits, want %d", phrase, digits, want)
			}
		}
	}
}

func TestPassphraseDrawsAcrossTheWordlist(t *testing.T) {
	p := Passphrase{Words: mustWords(t, MaxWords), Separator: " "}
	random := seeded(6)
	seen := map[string]bool{}
	for range 2000 {
		phrase, err := p.Generate(random)
		if err != nil {
			t.Fatal(err)
		}
		for w := range strings.SplitSeq(phrase, " ") {
			seen[w] = true
		}
	}
	if len(seen) < len(words)*99/100 {
		t.Errorf("40000 draws saw %d of %d words", len(seen), len(words))
	}
}

func TestPassphraseNumberLandsOnEveryWord(t *testing.T) {
	p := Passphrase{Words: mustWords(t, MinWords), Separator: "-", Number: true}
	random := seeded(7)
	positions := map[int]bool{}
	digits := map[byte]bool{}
	for range 500 {
		phrase, err := p.Generate(random)
		if err != nil {
			t.Fatal(err)
		}
		for i, part := range strings.Split(phrase, "-") {
			if last := part[len(part)-1]; last >= '0' && last <= '9' {
				positions[i] = true
				digits[last] = true
			}
		}
	}
	if len(positions) != MinWords || len(digits) != 10 {
		t.Errorf("the number landed on words %v with digits %v", positions, digits)
	}
}

func TestUniformRejectsTheBiasedRange(t *testing.T) {
	var zero, five [4]byte
	five[0] = 5
	r := bytes.NewReader(append(zero[:], five[:]...))
	got, err := uniform(r, 3)
	if err != nil || got != 2 {
		t.Errorf("uniform over 0 then 5 mod 3 = %d, %v; want 0 rejected and 2", got, err)
	}
	if r.Len() != 0 {
		t.Errorf("%d bytes left unread", r.Len())
	}
	got, err = uniform(bytes.NewReader(five[:]), 7)
	if err != nil || got != 5 {
		t.Errorf("uniform over 5 mod 7 = %d, %v", got, err)
	}
}

type failingReader struct{}

var errNoEntropy = errors.New("no entropy")

func (failingReader) Read([]byte) (int, error) { return 0, errNoEntropy }

func TestGenerateFailsWithTheRandomnessError(t *testing.T) {
	pw, err := Password{Charsets: mustCharsets(t, Digits)}.Generate(failingReader{})
	if !errors.Is(err, errNoEntropy) || pw != "" {
		t.Errorf("password %q, error %v", pw, err)
	}
	phrase, err := Passphrase{Separator: "-"}.Generate(bytes.NewReader(make([]byte, 6)))
	if err == nil || phrase != "" {
		t.Errorf("short randomness: passphrase %q, error %v", phrase, err)
	}
}
