// Command unicodegen builds the launcher's Unicode character name table
// from the Unicode Character Database: every character UnicodeData.txt
// names, with its Unicode 1.0 name and the NameAliases.txt aliases as
// extra words to find it by.
//
//	go run ./internal/tools/unicodegen /path/to/ucd service/launcher/charnames/names.bin.gz
//
// The table is written gzip-compressed in the format charnames reads.
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-lib/intconv"

	"github.com/stubbedev/lys-shell/service/launcher/charnames"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: unicodegen <ucd-dir> <out.bin.gz>")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "unicodegen:", err)
		os.Exit(1)
	}
}

type char struct {
	cp      rune
	kind    charnames.Kind
	name    string
	aliases []string
}

func run(ucd, out string) error {
	chars, err := readUnicodeData(filepath.Join(ucd, "UnicodeData.txt"))
	if err != nil {
		return err
	}
	if err := readAliases(filepath.Join(ucd, "NameAliases.txt"), chars); err != nil {
		return err
	}
	list := make([]*char, 0, len(chars))
	for _, c := range chars {
		if c.name != "" {
			list = append(list, c)
		}
	}
	slices.SortFunc(list, func(a, b *char) int { return int(a.cp - b.cp) })
	data, err := encode(list)
	if err != nil {
		return err
	}
	f, err := os.Create(out) //nolint:gosec // a path the developer names
	if err != nil {
		return err
	}
	zw, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		_ = f.Close()
		return err
	}
	if _, err := zw.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Printf("wrote %d characters, %d bytes uncompressed\n", len(list), len(data))
	return nil
}

func readUnicodeData(path string) (map[rune]*char, error) {
	f, err := os.Open(path) //nolint:gosec // a path the developer names
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	chars := map[rune]*char{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ";")
		if len(fields) < 15 {
			return nil, fmt.Errorf("%s: short line %q", path, sc.Text())
		}
		cp, err := strconv.ParseUint(fields[0], 16, 21)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		c := &char{cp: intconv.Must[rune](cp), kind: kindOf(fields[2])}
		if !strings.HasPrefix(fields[1], "<") {
			c.name = fields[1]
		}
		if fields[10] != "" && fields[10] != c.name {
			c.aliases = append(c.aliases, fields[10])
		}
		if c.name == "" && c.kind != charnames.KindControl {
			continue
		}
		chars[c.cp] = c
	}
	return chars, sc.Err()
}

func kindOf(category string) charnames.Kind {
	switch category {
	case "Mn", "Mc", "Me":
		return charnames.KindCombining
	case "Cc", "Cf", "Zl", "Zp":
		return charnames.KindControl
	}
	return charnames.KindGraphic
}

func readAliases(path string, chars map[rune]*char) error {
	f, err := os.Open(path) //nolint:gosec // a path the developer names
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		fields := strings.Split(line, ";")
		if len(fields) != 3 {
			return fmt.Errorf("%s: bad line %q", path, line)
		}
		cp, err := strconv.ParseUint(fields[0], 16, 21)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		c, ok := chars[intconv.Must[rune](cp)]
		if !ok {
			continue
		}
		switch fields[2] {
		case "correction", "control":
			if c.name == "" {
				c.name = fields[1]
				continue
			}
			c.aliases = append(c.aliases, fields[1])
		case "alternate", "abbreviation":
			c.aliases = append(c.aliases, fields[1])
		}
	}
	return sc.Err()
}

type prepared struct {
	name, alias []string
	hexTail     bool
}

func prepare(c *char) prepared {
	name := strings.Fields(c.name)
	tail := "-" + fmt.Sprintf("%04X", c.cp)
	last := name[len(name)-1]
	p := prepared{}
	if len(last) > len(tail) && strings.HasSuffix(last, tail) {
		name[len(name)-1] = strings.TrimSuffix(last, tail)
		p.hexTail = true
	}
	p.name = name
	for _, a := range c.aliases {
		p.alias = append(p.alias, strings.Fields(a)...)
	}
	return p
}

type pair struct{ a, b string }

func mergePhrases(prep []prepared) {
	for range maxMerges {
		counts := map[pair]int{}
		for _, p := range prep {
			for i := 1; i < len(p.name); i++ {
				counts[pair{p.name[i-1], p.name[i]}]++
			}
		}
		best, n := pair{}, 0
		for pr, c := range counts {
			if c > n || c == n && (pr.a < best.a || pr.a == best.a && pr.b < best.b) {
				best, n = pr, c
			}
		}
		if n < minPhraseUses {
			return
		}
		joined := best.a + " " + best.b
		for i := range prep {
			name := prep[i].name
			out := name[:0]
			for j := 0; j < len(name); j++ {
				if j+1 < len(name) && name[j] == best.a && name[j+1] == best.b {
					out = append(out, joined)
					j++
					continue
				}
				out = append(out, name[j])
			}
			prep[i].name = out
		}
	}
}

const (
	maxMerges     = 3000
	minPhraseUses = 6
)

func encode(list []*char) ([]byte, error) {
	prep := make([]prepared, len(list))
	for i, c := range list {
		prep[i] = prepare(c)
	}
	mergePhrases(prep)
	freq := map[string]int{}
	for _, p := range prep {
		for _, w := range p.name {
			freq[w]++
		}
		for _, w := range p.alias {
			freq[w]++
		}
	}
	words := make([]string, 0, len(freq))
	for w := range freq {
		if len(w) > 255 {
			return nil, fmt.Errorf("word %q too long", w)
		}
		words = append(words, w)
	}
	if len(words) >= 1<<14 {
		return nil, errors.New("too many distinct words")
	}
	slices.SortFunc(words, func(a, b string) int {
		if freq[a] != freq[b] {
			return freq[b] - freq[a]
		}
		return strings.Compare(a, b)
	})
	ids := make(map[string]uint64, len(words))
	for i, w := range words {
		ids[w] = uint64(i) + charnames.FirstWord
	}
	buf := []byte(charnames.Magic)
	buf = binary.AppendUvarint(buf, uint64(len(words)))
	for _, w := range words {
		buf = append(buf, intconv.Must[byte](len(w)))
		buf = append(buf, w...)
	}
	buf = binary.AppendUvarint(buf, uint64(len(list)))
	prev := rune(-1)
	for i, c := range list {
		p := prep[i]
		codes := len(p.name)
		if p.hexTail {
			codes++
		}
		if codes > charnames.MaxNameCodes || len(p.alias) > 255 {
			return nil, fmt.Errorf("U+%04X: %d name and %d alias codes", c.cp, codes, len(p.alias))
		}
		head := byte(codes) | byte(c.kind)<<4
		gap := c.cp - prev - 1
		prev = c.cp
		if len(p.alias) > 0 {
			head |= charnames.HasAliases
		}
		if gap > 0 {
			head |= charnames.HasGap
		}
		buf = append(buf, head)
		if gap > 0 {
			buf = binary.AppendUvarint(buf, uint64(gap))
		}
		if len(p.alias) > 0 {
			buf = append(buf, intconv.Must[byte](len(p.alias)))
		}
		for _, w := range p.name {
			buf = binary.AppendUvarint(buf, ids[w])
		}
		if p.hexTail {
			buf = binary.AppendUvarint(buf, charnames.HexTail)
		}
		for _, w := range p.alias {
			buf = binary.AppendUvarint(buf, ids[w])
		}
	}
	return buf, nil
}
