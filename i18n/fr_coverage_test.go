package i18n

import (
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	greeterlocales "github.com/stubbedev/lys-shell/i18n/locales/greeter"
	settingslocales "github.com/stubbedev/lys-shell/i18n/locales/settings"
	shelllocales "github.com/stubbedev/lys-shell/i18n/locales/shell"
)

var ftlPlaceholder = regexp.MustCompile(`\{"[^"]*[^"\s][^"]*"\}|\{ *[$-]?[a-zA-Z][a-zA-Z0-9_.-]* *(->)? *\}?`)

func catalogueKeys(t *testing.T, fsys fs.FS, locale string) map[string]string {
	t.Helper()
	keys := map[string]string{}
	err := fs.WalkDir(fsys, locale, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".ftl") {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		maps.Copy(keys, ftlKeys(string(data)))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

var (
	ftlMessage   = regexp.MustCompile(`^-?[a-zA-Z][a-zA-Z0-9_-]* *=`)
	ftlAttribute = regexp.MustCompile(`^\s+\.[a-zA-Z][a-zA-Z0-9_-]* *=`)
)

func ftlKeys(src string) map[string]string {
	text := map[string]string{}
	var message, current string
	for line := range strings.SplitSeq(src, "\n") {
		id, _, _ := strings.Cut(line, "=")
		id = strings.TrimSpace(id)
		switch {
		case strings.HasPrefix(line, "#"):
			current = ""
			continue
		case ftlMessage.MatchString(line):
			message, current = id, id
		case ftlAttribute.MatchString(line):
			current = message + id
		}
		if current != "" {
			text[current] += line + "\n"
		}
	}
	keys := map[string]string{}
	for _, e := range parseResource(src).entries {
		keys[e.id] = text[e.id]
		for _, a := range e.attrs {
			keys[e.id+"."+a.id] = text[e.id+"."+a.id]
		}
	}
	return keys
}

func placeholders(text string) []string {
	found := ftlPlaceholder.FindAllString(text, -1)
	for i, p := range found {
		found[i] = strings.Join(strings.Fields(p), "")
	}
	slices.Sort(found)
	return slices.Compact(found)
}

func untranslated(en, fr map[string]string) []string {
	var problems []string
	for k, text := range en {
		have, ok := fr[k]
		if !ok {
			problems = append(problems, k+": no translation")
			continue
		}
		want, got := placeholders(text), placeholders(have)
		if !slices.Equal(want, got) {
			problems = append(problems, k+": placeholders "+strings.Join(got, " ")+", want "+strings.Join(want, " "))
		}
	}
	slices.Sort(problems)
	return problems
}

func TestEveryStringHasAFrenchTranslation(t *testing.T) {
	for name, fsys := range map[string]fs.FS{"shell": shelllocales.FS, "settings": settingslocales.FS, "greeter": greeterlocales.FS} {
		en, fr := catalogueKeys(t, fsys, "en-US"), catalogueKeys(t, fsys, "fr")
		if len(en) == 0 {
			t.Fatalf("%s: no keys in en-US", name)
		}
		for _, problem := range untranslated(en, fr) {
			t.Errorf("%s: %s", name, problem)
		}
	}
}

func TestAMissingOrDamagedTranslationIsReported(t *testing.T) {
	en := ftlKeys("a = one {\"{cmd}\"}\n    .description = two\nb = three\nc = { $n ->\n    [one] one\n   *[other] { $n } and { $m }\n}\n")
	fr := ftlKeys("a = un\nc = { $n ->\n    [one] un\n   *[other] { $n }\n}\nd = quatre\n")
	got := untranslated(en, fr)
	want := []string{
		`a.description: no translation`,
		`a: placeholders , want {"{cmd}"}`,
		`b: no translation`,
		`c: placeholders {$n-> {$n}, want {$m} {$n-> {$n}`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestTheFrenchLauncherSettingsResolve(t *testing.T) {
	l, err := Select(trees["lys-shell-i18n"], []LangID{MustLangID("fr-FR")})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := l.Get("settings-page-launcher"), "Lanceur"; got != want {
		t.Errorf("settings-page-launcher = %q, want %q", got, want)
	}
	if got, want := l.Attr("settings-launcher-window-format", "description"), `{w}`; !strings.Contains(got, want) {
		t.Errorf("the window format's description %q lost %s", got, want)
	}
	shell, err := Select(trees["lys-shell-core"], []LangID{MustLangID("fr-FR")})
	if err != nil {
		t.Fatal(err)
	}
	failed := shell.Get("launcher-failed", Str("what", "Alacritty"), Str("error", "x"))
	if strings.Contains(failed, "de Alacritty") {
		t.Errorf("launcher-failed does not elide before a vowel: %q", failed)
	}
}
