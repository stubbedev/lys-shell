package styling

import (
	"regexp"
	"strings"
	"testing"
)

// The scale declares each role once per scale variable, smallest first,
// with body at 1rem.
func TestTypeScaleCSSDeclaresEveryRole(t *testing.T) {
	css := TypeScaleCSS()
	for _, r := range TextRoles() {
		for _, want := range []string{
			r.Token() + ": calc(",
			r.BarToken() + ": calc(",
		} {
			if strings.Count(css, want) != 1 {
				t.Errorf("TypeScaleCSS declares %q %d times, want once", want, strings.Count(css, want))
			}
		}
	}
	if !strings.Contains(css, "--text-body: calc(1rem * var(--global-scale));") || !strings.Contains(css, "--bar-text-body: calc(1rem * var(--bar-scale));") {
		t.Errorf("body is not 1rem at the scale:\n%s", css)
	}
	roles := TextRoles()
	for i := 1; i < len(roles); i++ {
		if roles[i].Ratio() <= roles[i-1].Ratio() {
			t.Errorf("%s (%v) is not larger than %s (%v)", roles[i].Token(), roles[i].Ratio(), roles[i-1].Token(), roles[i-1].Ratio())
		}
	}
	if got := Caption.Px(16, 2); got != 28 {
		t.Errorf("caption at 16px rem, scale 2 = %vpx, want 28", got)
	}
}

// fontSizeDecl is a font-size declaration's value.
var fontSizeDecl = regexp.MustCompile(`font-size:\s*([^;]+);`)

// offScaleSizes is every font-size value in css that is not one of the
// scale's tokens.
func offScaleSizes(css string) []string {
	allowed := map[string]bool{"var(--bar-btn-label-size)": true}
	for _, r := range TextRoles() {
		allowed["var("+r.Token()+")"] = true
		allowed["var("+r.BarToken()+")"] = true
	}
	var out []string
	for _, m := range fontSizeDecl.FindAllStringSubmatch(css, -1) {
		if v := strings.TrimSpace(m[1]); !allowed[v] {
			out = append(out, v)
		}
	}
	return out
}

// The built-in stylesheet sizes text only by the scale's tokens.
func TestStaticCSSSizesTextByTheScale(t *testing.T) {
	if off := offScaleSizes(StaticCSS); off != nil {
		t.Errorf("StaticCSS sizes text off the scale: %q", off)
	}
}

// A stray size, absolute or a token off the scale, is caught.
func TestOffScaleSizesCatchesAStraySize(t *testing.T) {
	off := offScaleSizes("label { font-size: 13px; }\nbox { font-size: var(--text-md); }\nrow { font-size: var(--text-body); }")
	if len(off) != 2 || off[0] != "13px" || off[1] != "var(--text-md)" {
		t.Errorf("off = %q, want the 13px and --text-md sizes", off)
	}
}
