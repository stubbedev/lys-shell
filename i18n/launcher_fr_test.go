package i18n

import (
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"

	settingslocales "github.com/stubbedev/lys-shell/i18n/locales/settings"
	shelllocales "github.com/stubbedev/lys-shell/i18n/locales/shell"
)

var ftlPlaceholder = regexp.MustCompile(`\{"[^"]*"\}|\{ *\$[a-z-]+ *\}`)

func launcherKeys(t *testing.T, fsys fs.FS, locale string) map[string]string {
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
		for k, line := range ftlKeys(string(data)) {
			if strings.Contains(k, "launcher") {
				keys[k] = line
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func ftlKeys(src string) map[string]string {
	byID := map[string]string{}
	var message string
	for line := range strings.SplitSeq(src, "\n") {
		trimmed := strings.TrimSpace(line)
		id, _, found := strings.Cut(trimmed, "=")
		if !found || strings.HasPrefix(trimmed, "#") {
			continue
		}
		id = strings.TrimSpace(id)
		if strings.HasPrefix(id, ".") {
			byID[message+id] = line
			continue
		}
		message = id
		byID[id] = line
	}
	keys := map[string]string{}
	for _, e := range parseResource(src).entries {
		keys[e.id] = byID[e.id]
		for _, a := range e.attrs {
			keys[e.id+"."+a.id] = byID[e.id+"."+a.id]
		}
	}
	return keys
}

func untranslated(en, fr map[string]string) []string {
	var problems []string
	for k, line := range en {
		have, ok := fr[k]
		if !ok {
			problems = append(problems, k+": no translation")
			continue
		}
		want := ftlPlaceholder.FindAllString(line, -1)
		got := ftlPlaceholder.FindAllString(have, -1)
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(want, got) {
			problems = append(problems, k+": placeholders "+strings.Join(got, " ")+", want "+strings.Join(want, " "))
		}
	}
	slices.Sort(problems)
	return problems
}

func TestEveryLauncherStringHasAFrenchTranslation(t *testing.T) {
	for name, fsys := range map[string]fs.FS{"shell": shelllocales.FS, "settings": settingslocales.FS} {
		en, fr := launcherKeys(t, fsys, "en-US"), launcherKeys(t, fsys, "fr")
		if len(en) == 0 {
			t.Fatalf("%s: no launcher keys in en-US", name)
		}
		for _, problem := range untranslated(en, fr) {
			t.Errorf("%s: %s", name, problem)
		}
	}
}

func TestAMissingOrDamagedTranslationIsReported(t *testing.T) {
	en := ftlKeys("a = one {\"{cmd}\"}\n    .description = two\nb = three\n")
	fr := ftlKeys("a = un\nc = quatre\n")
	got := untranslated(en, fr)
	want := []string{
		`a.description: no translation`,
		`a: placeholders , want {"{cmd}"}`,
		`b: no translation`,
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
