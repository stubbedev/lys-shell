package resources_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stubbedev/lys-shell/resources"
)

// iconName is a full theme icon name as the shell writes one.
var iconName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*-symbolic$`)

// brandIcons are the bundled logos that are no Lucide glyph: Simple
// Icons' brands, and the distro logos Simple Icons lacks.
var brandIcons = []string{"cm-cachyos-symbolic", "cm-nobara-symbolic"}

// bundledIcons are the icon names resources.Icons carries.
func bundledIcons(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := fs.ReadDir(resources.Icons, resources.IconsDir)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		out[strings.TrimSuffix(e.Name(), path.Ext(e.Name()))] = true
	}
	return out
}

// sourceIconNames maps each icon name a Go string literal in src spells
// to where it appears.
func sourceIconNames(fset *token.FileSet, file *ast.File, into map[string]string) {
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err == nil && iconName.MatchString(s) {
			into[s] = fset.Position(lit.Pos()).String()
		}
		return true
	})
}

// moduleIconNames is sourceIconNames over every non-test Go file of the
// module, vendored third-party code aside.
func moduleIconNames(t *testing.T, root string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	names := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "third_party", "testdata", ".git", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		sourceIconNames(fset, file, names)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// unbundled lists, sorted, each name in names the bundle lacks, with
// where it appears.
func unbundled(names map[string]string, bundled map[string]bool) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(names)) {
		if !bundled[name] {
			out = append(out, name+" at "+names[name])
		}
	}
	return out
}

// Every icon name the shell's code spells, config defaults included,
// is one lys-shell bundles: none points at a set it no longer ships or
// at a desktop theme that may not be installed.
func TestEveryIconTheCodeNamesIsBundled(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	names := moduleIconNames(t, root)
	if len(names) < 100 {
		t.Fatalf("found only %d icon names in the module; the scan is broken", len(names))
	}
	for _, miss := range unbundled(names, bundledIcons(t)) {
		t.Errorf("not bundled: %s", miss)
	}
}

// The check reports a name from a dropped set or a desktop theme, and
// passes a bundled one.
func TestUnbundledReportsWhatTheBundleLacks(t *testing.T) {
	src := "package p\nvar a, b, c = \"tb-minus-symbolic\", \"folder-symbolic\", \"ld-minus-symbolic\"\n"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	sourceIconNames(fset, file, names)
	got := unbundled(names, bundledIcons(t))
	want := []string{"folder-symbolic at p.go:2:36", "tb-minus-symbolic at p.go:2:15"}
	if !slices.Equal(got, want) {
		t.Errorf("unbundled = %q, want %q", got, want)
	}
}

// The bundle is Lucide's UI glyphs and brand logos only: Lucide has no
// brands, so those are Simple Icons' and the distro logos it lacks.
func TestBundledIconsAreLucideOrBrands(t *testing.T) {
	for _, name := range slices.Sorted(maps.Keys(bundledIcons(t))) {
		if strings.HasPrefix(name, "ld-") || strings.HasPrefix(name, "si-") || slices.Contains(brandIcons, name) {
			continue
		}
		t.Errorf("%s is neither Lucide nor a brand logo", name)
	}
}

// Each icon set the bundle draws from ships its licence beside it, and
// no licence outlives its set.
func TestBundledLicencesMatchTheSets(t *testing.T) {
	entries, err := os.ReadDir("icons")
	if err != nil {
		t.Fatal(err)
	}
	var licences []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-LICENSE") {
			licences = append(licences, e.Name())
		}
	}
	if want := []string{"LUCIDE-LICENSE", "SIMPLE-ICONS-LICENSE"}; !slices.Equal(licences, want) {
		t.Errorf("licences = %v, want %v", licences, want)
	}
}
