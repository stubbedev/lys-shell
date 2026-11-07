package bar

import (
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-secrets/vault"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
)

func collectButtons(w widget.Widget, out []*widget.Button) []*widget.Button {
	if b, ok := w.(*widget.Button); ok {
		out = append(out, b)
	}
	if p, ok := w.(interface{ Children() []widget.Widget }); ok {
		for _, c := range p.Children() {
			out = collectButtons(c, out)
		}
	}
	return out
}

func TestEveryBitwardenButtonHasATooltip(t *testing.T) {
	v := unlockedVault()
	v.sends = []vault.Send{{ID: "s", Type: vault.SendText, Name: "Wi-Fi", Deletion: time.Now().Add(time.Hour)}}
	view, _ := bitwardenTestView(t, v, config.Defaults())
	onLoop(view.toggleSends)
	waitHeadless(t, "the Sends", func() bool { return rowCount(view.sendList) == 1 })
	onLoop(func() { view.setSending(false) })
	onLoop(view.toggleGenerator)
	onLoop(func() { view.setGenerating(false) })

	seen := map[*widget.Button]string{}
	for _, name := range []string{bwPageLogin, bwPageChallenge, bwPageUnlock, bwPageVault, bwPageReprompt, bwPageGenerator, bwPageSends} {
		for _, b := range onHeadlessLoop(func() []*widget.Button {
			view.pages.Show(name)
			return collectButtons(view, nil)
		}) {
			if b.TooltipText() == "" {
				t.Errorf("a button on the %s page has no tooltip", name)
			}
			seen[b] = name
		}
	}
	onLoop(view.toggleAPIKey)
	if tip := onHeadlessLoop(view.apiBtn.TooltipText); tip != i18n.T("dropdown-bitwarden-use-password-tooltip") {
		t.Errorf("the login switch in API key mode says %q", tip)
	}
	onLoop(view.toggleAPIKey)
	if tip := onHeadlessLoop(view.apiBtn.TooltipText); tip != i18n.T("dropdown-bitwarden-use-api-key-tooltip") {
		t.Errorf("the login switch in password mode says %q", tip)
	}

	const header, login, challenge, unlock, reprompt, generator, sends = 5, 2, 3, 1, 2, 1 + 2, 2 + 1
	rowCopies := 0
	for _, e := range v.entries {
		rowCopies += len(e.values)
	}
	if want := header + login + challenge + unlock + rowCopies + reprompt + generator + sends; len(seen) < want {
		t.Errorf("found %d buttons, want at least %d: the walk missed some", len(seen), want)
	}
}

func TestTheBitwardenBarButtonSaysWhatTheVaultIs(t *testing.T) {
	tips := []string{bitwardenTooltip(vault.StateLoggedOut), bitwardenTooltip(vault.StateLocked), bitwardenTooltip(vault.StateUnlocked)}
	for i, tip := range tips {
		if tip == "" {
			t.Errorf("state %d has no tooltip", i)
		}
	}
	if len(slices.Compact(slices.Sorted(slices.Values(tips)))) != len(tips) {
		t.Errorf("the states share a tooltip: %q", tips)
	}
}
