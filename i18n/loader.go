package i18n

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

// Arg is one named Fluent argument, built with Str, Int, or Float.
type Arg struct {
	name  string
	value value
}

// Str is a string argument.
func Str(name, v string) Arg { return Arg{name: name, value: stringValue(v)} }

// Int is a numeric argument; it formats and selects plural variants
// like a Rust integer passed to fl!.
func Int(name string, v int64) Arg {
	return Arg{name: name, value: numberValue(codeNumber(float64(v)))}
}

// Float is a numeric argument with Rust f64 formatting.
func Float(name string, v float64) Arg { return Arg{name: name, value: numberValue(codeNumber(v))} }

func argMap(args []Arg) map[string]value {
	if len(args) == 0 {
		return nil
	}
	m := make(map[string]value, len(args))
	for _, a := range args {
		m[a.name] = a.value
	}
	return m
}

// Loader is i18n-embed's FluentLanguageLoader over one embedded locale
// tree: the negotiated languages' bundles in preference order, the
// en-US fallback last.
type Loader struct {
	languages []LangID
	bundles   []*bundle
}

// Fallback is the fallback language both lys-shell domains declare in
// their i18n.toml.
var Fallback = MustLangID("en-US")

// Assets is an embedded locale tree: top-level locale directories
// holding `_*.ftl` partials at any depth.
type Assets struct {
	fsys fs.FS
	// file, when set, is the one FTL file each locale holds (a crate
	// whose locales are plain files, as lys-greeter's are, loaded by
	// i18n-embed under the crate's name) instead of `_*.ftl` partials.
	file string
	// more are further trees of partials (With).
	more []fs.FS
}

// NewAssets wraps a locale tree of `_*.ftl` partials.
func NewAssets(fsys fs.FS) Assets { return Assets{fsys: fsys} }

// With adds another locale tree of partials after these: a locale's
// source is both trees' partials, for a program that adds its own
// messages to a domain it does not own.
func (a Assets) With(fsys fs.FS) Assets {
	a.more = append(slices.Clone(a.more), fsys)
	return a
}

// trees is every locale tree, in the order their partials concatenate.
func (a Assets) trees() []fs.FS { return append([]fs.FS{a.fsys}, a.more...) }

// NewFileAssets wraps a locale tree holding one file per locale,
// <locale>/<file>.
func NewFileAssets(fsys fs.FS, file string) Assets { return Assets{fsys: fsys, file: file} }

// Available lists the locales that carry at least one partial, sorted
// like rust-embed's file list, with the fallback first when missing
// (i18n-embed's available_languages).
func (a Assets) Available() ([]LangID, error) {
	var names []string
	for _, tree := range a.trees() {
		dirs, err := fs.ReadDir(tree, ".")
		if err != nil {
			return nil, err
		}
		for _, d := range dirs {
			if d.IsDir() && !slices.Contains(names, d.Name()) {
				names = append(names, d.Name())
			}
		}
	}
	slices.Sort(names)
	var ids []LangID
	for _, name := range names {
		parts, err := a.partials(name)
		if err != nil {
			return nil, err
		}
		if len(parts) == 0 {
			continue
		}
		id, err := ParseLangID(name)
		if err != nil {
			return nil, fmt.Errorf("i18n: locale dir %q: %w", name, err)
		}
		ids = append(ids, id)
	}
	if !slices.ContainsFunc(ids, Fallback.Equal) {
		ids = slices.Insert(ids, 0, Fallback)
	}
	return ids, nil
}

// partials lists a locale's `_*.ftl` files in the order the Rust build
// scripts concatenate them: PathBuf order, compared component-wise.
func (a Assets) partials(locale string) ([]partial, error) {
	if a.file != "" {
		p := locale + "/" + a.file
		if _, err := fs.Stat(a.fsys, p); err != nil {
			return nil, nil // a locale without the file offers nothing
		}
		return []partial{{a.fsys, p}}, nil
	}
	var all []partial
	for _, tree := range a.trees() {
		files, err := treePartials(tree, locale)
		if err != nil {
			return nil, err
		}
		all = append(all, files...)
	}
	return all, nil
}

// partial is one FTL file and the tree holding it.
type partial struct {
	tree fs.FS
	path string
}

// treePartials is one tree's partials of a locale, sorted; a tree
// without the locale has none.
func treePartials(tree fs.FS, locale string) ([]partial, error) {
	if _, err := fs.Stat(tree, locale); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var files []partial
	err := fs.WalkDir(tree, locale, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if !d.IsDir() && strings.HasPrefix(name, "_") && strings.HasSuffix(name, ".ftl") {
			files = append(files, partial{tree, p})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(files, func(x, y partial) int {
		return slices.Compare(strings.Split(x.path, "/"), strings.Split(y.path, "/"))
	})
	return files, nil
}

// Source is a locale's combined FTL, exactly as wayle-shell-core's and
// wayle-i18n's build.rs write it (each partial followed by a newline),
// with CRLF folded to LF as i18n-embed does before parsing.
func (a Assets) Source(locale string) (string, error) {
	files, err := a.partials(locale)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, f := range files {
		data, err := fs.ReadFile(f.tree, f.path)
		if err != nil {
			return "", err
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return strings.ReplaceAll(b.String(), "\r\n", "\n"), nil
}

// Select negotiates the requested languages against the available
// ones (i18n_embed::select: fluent-langneg filtering, en-US default)
// and loads the result, fallback last. It is an error only when the
// fallback itself has no partials.
func Select(assets Assets, requested []LangID) (*Loader, error) {
	available, err := assets.Available()
	if err != nil {
		return nil, err
	}
	supported := negotiateLanguages(requested, available, Fallback, filtering)
	load := slices.Clone(supported)
	if !slices.ContainsFunc(load, Fallback.Equal) {
		load = append(load, Fallback)
	}
	l := &Loader{languages: supported}
	for _, lang := range load {
		src, err := assets.Source(lang.String())
		if err != nil {
			return nil, err
		}
		if src == "" && lang.Equal(Fallback) {
			return nil, fmt.Errorf("i18n: fallback language %s has no FTL partials", Fallback)
		}
		l.bundles = append(l.bundles, newBundle(lang, parseResource(src)))
	}
	return l, nil
}

// Languages are the negotiated languages in preference order.
func (l *Loader) Languages() []LangID { return slices.Clone(l.languages) }

// Get formats a message's value from the first bundle that has one,
// or returns i18n-embed's `No localization for id: "<id>"` marker.
func (l *Loader) Get(id string, args ...Arg) string {
	for _, b := range l.bundles {
		if msg := b.messages[id]; msg != nil && msg.value != nil {
			return b.format(msg.value, argMap(args))
		}
	}
	return `No localization for id: "` + id + `"`
}

// Attr formats a message attribute from the first bundle that has it,
// or returns i18n-embed's missing-attribute marker.
func (l *Loader) Attr(id, attr string, args ...Arg) string {
	for _, b := range l.bundles {
		if msg := b.messages[id]; msg != nil {
			if p := msg.attribute(attr); p != nil {
				return b.format(p, argMap(args))
			}
		}
	}
	return `No localization for message id: "` + id + `" and attribute id: "` + attr + `"`
}

// Has reports whether any loaded bundle defines the message with a
// value.
func (l *Loader) Has(id string) bool {
	for _, b := range l.bundles {
		if msg := b.messages[id]; msg != nil && msg.value != nil {
			return true
		}
	}
	return false
}

// HasAttr reports whether any loaded bundle defines the message's
// attribute: an optional attribute is shown only where it exists.
func (l *Loader) HasAttr(id, attr string) bool {
	for _, b := range l.bundles {
		if msg := b.messages[id]; msg != nil && msg.attribute(attr) != nil {
			return true
		}
	}
	return false
}
