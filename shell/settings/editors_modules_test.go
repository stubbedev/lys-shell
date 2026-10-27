package settings

import (
	"slices"
	"testing"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
)

func typeNumber(n *number, text string) {
	n.SelectAll()
	n.Insert(text)
	n.KeyAction(widget.KeyEnter, 0)
}

func TestPasswordPresetCards(t *testing.T) {
	k := testKit(t, "")
	path := "modules.bitwarden.generator-passwords"
	c := fieldControl(k, field(path, passwordPresetList)).(*cardList)
	if len(c.cards) != 1 {
		t.Fatalf("%d cards for the default preset", len(c.cards))
	}
	c.local = append(c.local, c.spec.blank())
	c.commit()
	c.rebuild()
	if got, _ := k.store.value(path).([]any); len(got) != 1 {
		t.Fatalf("a nameless preset was stored: %v", got)
	}
	c.cards[1].identity.(*liveText).Insert("PIN")
	got, _ := k.store.value(path).([]any)
	if len(got) != 2 {
		t.Fatalf("named preset stored %v", got)
	}
	added := got[1].(map[string]any)
	if added["name"] != "PIN" || added["length"] != int64(20) || added["avoid-ambiguous"] != false ||
		!slices.Equal(added["charsets"].([]any), []any{"lowercase", "uppercase", "digits", "symbols"}) {
		t.Errorf("a new preset is %v, want the defaults under its name", added)
	}
	typeNumber(c.cards[1].controls[0].(*number), "12")
	charsets := c.cards[1].controls[1].(*listEditor)
	charsets.remove(charsets.items[0])
	c.cards[1].controls[2].(*toggle).SetOn(true)
	cfg := k.store.svc.Config()
	if len(cfg.Bitwarden.GeneratorPasswords) != 2 {
		t.Fatalf("config presets %+v", cfg.Bitwarden.GeneratorPasswords)
	}
	rules := cfg.Bitwarden.GeneratorPasswords[1].Rules()
	if rules.Length.Int() != 12 || !rules.AvoidAmbiguous || len(rules.Charsets.List()) != 3 {
		t.Errorf("edited preset rules %+v %v", rules, rules.Charsets.List())
	}
}

func TestPasswordPresetCardsKeepTheLastCharset(t *testing.T) {
	k := testKit(t, "")
	path := "modules.bitwarden.generator-passwords"
	c := fieldControl(k, field(path, passwordPresetList)).(*cardList)
	charsets := c.cards[0].controls[1].(*listEditor)
	for len(charsets.items) > 0 {
		charsets.remove(charsets.items[0])
	}
	got := k.store.svc.Config().Bitwarden.GeneratorPasswords
	if len(got) != 1 || !slices.Equal(config.Encode(got[0].Charsets).([]any), []any{"symbols"}) {
		t.Errorf("removing every class stored %+v, want the empty list refused and the last class kept", got)
	}
}

func TestPassphrasePresetCards(t *testing.T) {
	k := testKit(t, "")
	path := "modules.bitwarden.generator-passphrases"
	c := fieldControl(k, field(path, passphrasePresetList)).(*cardList)
	typeNumber(c.cards[0].controls[0].(*number), "4")
	sep := c.cards[0].controls[1].(*liveText)
	sep.SetText("")
	sep.Insert(".")
	c.cards[0].controls[2].(*toggle).SetOn(false)
	c.cards[0].controls[3].(*toggle).SetOn(false)
	got := k.store.svc.Config().Bitwarden.GeneratorPassphrases
	if len(got) != 1 {
		t.Fatalf("config presets %+v", got)
	}
	if r := got[0].Rules(); r.Words.Int() != 4 || r.Separator != "." || r.Capitalize || r.Number || got[0].Name != config.DefaultsBitwarden().GeneratorPassphrases[0].Name {
		t.Errorf("edited preset %+v rules %+v", got[0], r)
	}
	typeNumber(c.cards[0].controls[0].(*number), "99")
	if r := k.store.svc.Config().Bitwarden.GeneratorPassphrases[0].Rules(); r.Words.Int() != 20 {
		t.Errorf("the words spin went past its bound: %d", r.Words.Int())
	}
}
