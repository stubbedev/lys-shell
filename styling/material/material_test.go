package material

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type golden struct {
	Roles  []string `json:"roles"`
	Images map[string]struct {
		Quantized [][2]any `json:"quantized"`
		Ranked    []string `json:"ranked"`
	} `json:"images"`
	Schemes []struct {
		Seed     string  `json:"seed"`
		Variant  string  `json:"variant"`
		Dark     bool    `json:"dark"`
		Contrast float64 `json:"contrast"`
		Colors   string  `json:"colors"`
	} `json:"schemes"`
}

func loadGolden(t *testing.T) golden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func loadPixels(t *testing.T, name string) []ARGB {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".argb"))
	if err != nil {
		t.Fatal(err)
	}
	pixels := make([]ARGB, len(data)/4)
	for i := range pixels {
		pixels[i] = ARGB(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return pixels
}

func hex6(c ARGB) string { return fmt.Sprintf("%06x", uint32(c)&0xffffff) }

func parseHex6(t *testing.T, s string) ARGB {
	t.Helper()
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		t.Fatal(err)
	}
	return ARGB(0xff000000 | uint32(v))
}

func TestQuantizeAndScoreMatchTheReference(t *testing.T) {
	g := loadGolden(t)
	if len(g.Images) == 0 {
		t.Fatal("golden has no images")
	}
	for name, want := range g.Images {
		t.Run(name, func(t *testing.T) {
			got := Quantize(loadPixels(t, name), 128)
			if len(got) != len(want.Quantized) {
				t.Fatalf("quantized %d colors, reference %d", len(got), len(want.Quantized))
			}
			for i, p := range got {
				wantColor, wantCount := want.Quantized[i][0].(string), int(want.Quantized[i][1].(float64))
				if hex6(p.Color) != wantColor || p.Count != wantCount {
					t.Fatalf("cluster %d = %s x%d, reference %s x%d", i, hex6(p.Color), p.Count, wantColor, wantCount)
				}
			}
			ranked := Score(got)
			gotRanked := make([]string, len(ranked))
			for i, c := range ranked {
				gotRanked[i] = hex6(c)
			}
			if strings.Join(gotRanked, " ") != strings.Join(want.Ranked, " ") {
				t.Fatalf("ranked %v, reference %v", gotRanked, want.Ranked)
			}
		})
	}
}

func TestSchemesMatchTheReference(t *testing.T) {
	g := loadGolden(t)
	if len(g.Roles) != int(RoleCount) {
		t.Fatalf("golden has %d roles, the package %d", len(g.Roles), RoleCount)
	}
	for i, name := range g.Roles {
		if Role(i).String() != name {
			t.Fatalf("role %d is %q, golden %q", i, Role(i), name)
		}
	}
	variants := map[string]Variant{}
	for v := range FruitSalad + 1 {
		variants[strings.ToUpper(strings.ReplaceAll(v.String(), "-", "_"))] = v
	}
	failures := 0
	for _, c := range g.Schemes {
		v, ok := variants[c.Variant]
		if !ok {
			t.Fatalf("unknown variant %q", c.Variant)
		}
		s := NewScheme(parseHex6(t, c.Seed), v, c.Dark, c.Contrast)
		for i, want := range strings.Fields(c.Colors) {
			if got := hex6(s.Color(Role(i))); got != want {
				failures++
				if failures <= 20 {
					t.Errorf("seed %s %s dark=%t contrast=%v: %s = %s, reference %s", c.Seed, c.Variant, c.Dark, c.Contrast, Role(i), got, want)
				}
			}
		}
	}
	if failures > 0 {
		t.Fatalf("%d role colors differ from the reference", failures)
	}
}
