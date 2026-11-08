package i18nids_test

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stubbedev/lys-desktop/i18n"
	shelllocales "github.com/stubbedev/lys-desktop/i18n/locales/shell"
)

// goUsage is what the repo's Go code asks of the shell domain: the ids
// passed literally to i18n.T/Attr, and every string literal in a file
// that imports this package (ids kept in tables, like the weather
// conditions).
type goUsage struct {
	calls    map[string]bool
	literals map[string]bool
}

func scanGoUsage(t *testing.T) goUsage {
	t.Helper()
	use := goUsage{calls: map[string]bool{}, literals: map[string]bool{}}
	root := "../.."
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == root {
				return nil
			}
			// The go tool's own rule: dot and underscore directories
			// (.git, the direnv cache in .direnv, agent
			// worktrees in .claude) and testdata hold no package code.
			name := d.Name()
			switch {
			case strings.HasPrefix(name, "."), strings.HasPrefix(name, "_"),
				name == "testdata", name == "target", name == "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		file, err := goparser.ParseFile(token.NewFileSet(), p, nil, 0)
		if err != nil {
			return err
		}
		imports := false
		for _, imp := range file.Imports {
			if imp.Path.Value == `"github.com/stubbedev/lys-desktop/i18n"` {
				imports = true
			}
		}
		if !imports {
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BasicLit:
				if s, err := strconv.Unquote(x.Value); err == nil && x.Kind == token.STRING {
					use.literals[s] = true
				}
			case *ast.CallExpr:
				sel, ok := x.Fun.(*ast.SelectorExpr)
				if !ok || len(x.Args) == 0 {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "i18n" || (sel.Sel.Name != "T" && sel.Sel.Name != "Attr") {
					return true
				}
				if lit, ok := x.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if id, err := strconv.Unquote(lit.Value); err == nil {
						use.calls[id] = true
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return use
}

// locale loads one locale alone: under the fallback's name, so that
// Has answers for that locale and not through en-US.
func locale(t *testing.T, lang string) *i18n.Loader {
	t.Helper()
	tree := fstest.MapFS{}
	err := fs.WalkDir(shelllocales.FS, lang, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(shelllocales.FS, p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(lang, p)
		if err != nil {
			return err
		}
		tree[path.Join(i18n.Fallback.String(), rel)] = &fstest.MapFile{Data: data}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	l, err := i18n.Select(i18n.NewAssets(tree), []i18n.LangID{i18n.Fallback})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// Every shell message id the Go code uses resolves in en-US and in
// fr, so a missing translation fails here instead of rendering the
// "No localization" marker.
func TestGoMessageIDsResolveInEveryLocale(t *testing.T) {
	use := scanGoUsage(t)
	if len(use.calls) == 0 {
		t.Fatal("found no i18n.T call sites; the scanner is broken")
	}
	en, fr := locale(t, "en-US"), locale(t, "fr")
	ids := map[string]bool{}
	for id := range use.calls {
		ids[id] = true
		if !en.Has(id) {
			t.Errorf("en-US: message %q (called from Go) missing", id)
		}
	}
	for lit := range use.literals {
		if en.Has(lit) {
			ids[lit] = true
		}
	}
	for id := range ids {
		if !fr.Has(id) {
			t.Errorf("fr: message %q missing", id)
		}
	}
	// The table-held weather ids are found through the literal scan.
	if !ids["weather-light-rain"] {
		t.Error("the literal scan missed the weather condition table")
	}
}
