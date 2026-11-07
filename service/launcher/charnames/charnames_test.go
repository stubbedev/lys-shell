package charnames

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/modes"
)

func shared(t *testing.T) *Table {
	t.Helper()
	tb, err := Shared()
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

func search(s *Searcher, query string, limit int) []Char {
	return s.Search(launcher.NewFuzzyQuery(query), query, limit)
}

func names(t *Table, found []Char) []string {
	out := make([]string, len(found))
	for i, c := range found {
		out[i] = string(c.Rune) + " " + string(t.AppendName(nil, c))
	}
	return out
}

func TestTheTableNamesEveryCharacterUnicodeDoes(t *testing.T) {
	tb := shared(t)
	if tb.Len() < 40000 {
		t.Fatalf("%d characters", tb.Len())
	}
	s := tb.NewSearcher()
	for query, want := range map[string]string{
		"U+2192": "→ Rightwards Arrow", "u+00b0": "° Degree Sign", "→": "→ Rightwards Arrow",
		"U+002D": "- Hyphen-Minus", "U+F900": "\uf900 Cjk Compatibility Ideograph-F900", "U+000A": "\n Line Feed",
		"U+1F44D": "👍 Thumbs Up Sign",
	} {
		got := names(tb, search(s, query, 5))
		if len(got) != 1 || got[0] != want {
			t.Errorf("%q = %q, want %q", query, got, want)
		}
	}
	for _, query := range []string{"U+E000", "U+D800", "U+110000", "€€"} {
		if got := search(s, query, 5); len(got) != 0 {
			t.Errorf("%q found %q", query, names(tb, got))
		}
	}
}

func TestSearchFindsByWordsAndAliases(t *testing.T) {
	tb := shared(t)
	s := tb.NewSearcher()
	for query, want := range map[string]rune{
		"right arrow": '→', "degree": '°', "hyphen minus": '-', "lf": '\n', "zero width joiner": '‍',
		"nbsp": ' ', "interrobang": '‽', "micro": 'µ', "one half": '½', "not equal": '≠',
	} {
		found := search(s, query, 4)
		hit := false
		for _, c := range found {
			hit = hit || c.Rune == want
		}
		if !hit {
			t.Errorf("%q: %q, want %c among them", query, names(tb, found), want)
		}
	}
	if got := search(s, "degree", 1); len(got) != 1 || got[0].Rune != '°' {
		t.Errorf("degree leads with %q", names(tb, got))
	}
	for _, query := range []string{"qqqzzz", "right qqqzzz", "flüssig", strings.Repeat("a ", 7)} {
		if got := search(s, query, 4); len(got) != 0 {
			t.Errorf("%q found %q", query, names(tb, got))
		}
	}
	if got := search(s, "", 3); len(got) != 3 || got[0].Rune != 0 || got[1].Rune != 1 {
		t.Errorf("the empty query lists %q", names(tb, got))
	}
	if got := search(s, "arrow", 0); len(got) != 0 {
		t.Errorf("a zero limit found %d", len(got))
	}
}

func TestGlyphsShowInvisibleCharactersVisibly(t *testing.T) {
	for _, tc := range []struct {
		c    Char
		want string
	}{
		{Char{Rune: '→', Kind: KindGraphic}, "→"},
		{Char{Rune: 0x301, Kind: KindCombining}, "◌́"},
		{Char{Rune: '\n', Kind: KindControl}, "␊"},
		{Char{Rune: 0x7f, Kind: KindControl}, "␡"},
		{Char{Rune: 0x200d, Kind: KindControl}, ""},
	} {
		if got := tc.c.Glyph(); got != tc.want {
			t.Errorf("%U = %q, want %q", tc.c.Rune, got, tc.want)
		}
	}
}

func TestSearchingEveryNameAllocatesNothing(t *testing.T) {
	s := shared(t).NewSearcher()
	for _, query := range []string{"letter", "a", "right arrow", "qqqzzz", "", "U+2192"} {
		q := launcher.NewFuzzyQuery(query)
		s.Search(q, query, 200)
		allocs := testing.AllocsPerRun(5, func() { s.Search(q, query, 200) })
		if allocs != 0 {
			t.Errorf("%q: %v allocations per search", query, allocs)
		}
	}
}

func TestAMalformedTableIsRejected(t *testing.T) {
	zr, err := gzip.NewReader(bytes.NewReader(embedded))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(data); err != nil {
		t.Fatalf("the embedded table: %v", err)
	}
	for name, bad := range map[string][]byte{
		"magic":     append([]byte("nope"), data[4:]...),
		"truncated": data[:len(data)-3],
		"trailing":  append(append([]byte(nil), data...), 0),
		"empty":     nil,
	} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

const measureEnv = "LYS_CHARNAMES_MEASURE"

func heapAfter(load func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	load()
	runtime.GC()
	runtime.ReadMemStats(&after)
	return after.HeapAlloc - before.HeapAlloc
}

func TestMeasureHelper(t *testing.T) {
	if os.Getenv(measureEnv) == "" {
		t.Skip("run by TestTheTableCostsLessThanTheEmojiTables")
	}
	var tb *Table
	var emoji []modes.Emoji
	chars := heapAfter(func() { tb, _ = Shared() })
	emojis := heapAfter(func() { emoji = modes.AvailableEmoji() })
	runtime.KeepAlive(tb)
	runtime.KeepAlive(emoji)
	os.Stdout.WriteString("chars=" + strconv.FormatUint(chars, 10) + " emoji=" + strconv.FormatUint(emojis, 10) + "\n")
}

func TestTheTableCostsLessThanTheEmojiTables(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestMeasureHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), measureEnv+"=1", "LANG=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	line := string(out)
	at := strings.Index(line, "chars=")
	if at < 0 {
		t.Fatalf("no measurement in %q", line)
	}
	var chars, emoji uint64
	for field := range strings.FieldsSeq(line[at:]) {
		k, v, _ := strings.Cut(field, "=")
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			continue
		}
		switch k {
		case "chars":
			chars = n
		case "emoji":
			emoji = n
		}
	}
	t.Logf("resident: character names %d bytes, emoji tables %d bytes", chars, emoji)
	if chars == 0 || emoji == 0 || chars >= emoji {
		t.Errorf("character names %d bytes, emoji tables %d: want less", chars, emoji)
	}
}
