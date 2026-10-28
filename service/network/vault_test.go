package network

import (
	"context"
	"maps"
	"testing"

	"github.com/stubbedev/lys-shell/service/network/secrets"
)

// fakeVault holds entries by connection name.
type fakeVault map[string]VaultEntry

func (v fakeVault) Entry(name string) (VaultEntry, bool) {
	e, ok := v[name]
	return e, ok
}

func TestVaultValues(t *testing.T) {
	entry := VaultEntry{Username: "ann", Password: "hunter22", Fields: map[string]string{"pin": "1234", "password": "field wins"}}
	fields := func(keys ...string) []secrets.Field {
		out := make([]secrets.Field, len(keys))
		for i, k := range keys {
			out[i] = secrets.Field{Key: k}
		}
		return out
	}
	got, ok := vaultValues(entry, fields("username", "password", "pin"))
	if want := map[string]string{"username": "ann", "password": "field wins", "pin": "1234"}; !ok || !maps.Equal(got, want) {
		t.Errorf("values = %v %v, want %v", got, ok, want)
	}
	if got, ok := vaultValues(VaultEntry{Password: "p"}, fields("psk")); !ok || got["psk"] != "p" {
		t.Errorf("a PSK from the login's password: %v %v", got, ok)
	}
	for _, missing := range [][]secrets.Field{fields("psk", "domain"), fields("user"), fields("private-key")} {
		if got, ok := vaultValues(VaultEntry{Password: "p"}, missing); ok {
			t.Errorf("%+v answered %v from an entry without them all", missing, got)
		}
	}
}

// A connection the vault has a login for is answered without a prompt,
// even with nobody watching; one it has none for gets the prompt.
func TestAgentAnswersFromTheVaultFirst(t *testing.T) {
	fx := serveFixtureWith(t, RequestBudget, fakeVault{"Home": {Password: "from the vault"}})
	fx.agent.ExpectUnattended("home")
	a := await(t, fx.ask(wifiConnection("home", "Home", []byte("home")), 1, "802-11-wireless-security"))
	if a.err != nil || variantString(a.reply["802-11-wireless-security"]["psk"]) != "from the vault" {
		t.Fatalf("reply = %v %v, want the vault's password as the psk", a.reply, a.err)
	}
	if _, ok := fx.agent.Request(); ok {
		t.Error("a prompt went up for secrets the vault had")
	}

	pending := fx.ask(wifiConnection("cafe", "Cafe", []byte("cafe")), 2, "802-11-wireless-security")
	if req := fx.shown(t); req.UUID != "cafe" {
		t.Errorf("request = %+v", req)
	}
	fx.agent.Cancel()
	await(t, pending)
}

// Secrets NetworkManager rejected are asked for again, not answered
// from the vault a second time.
func TestAgentAsksWhenTheVaultsSecretsWereRejected(t *testing.T) {
	fx := serveFixtureWith(t, RequestBudget, fakeVault{"Home": {Password: "stale"}})
	pending := make(chan answer, 1)
	go func() {
		reply, err := fx.nm.getSecrets(context.Background(), fx.name, wifiConnection("home", "Home", []byte("home")), settingPath(1), "802-11-wireless-security", nil, flagAllowInteraction|flagRequestNew)
		pending <- answer{reply, err}
	}()
	if req := fx.shown(t); req.UUID != "home" {
		t.Errorf("request = %+v", req)
	}
	fx.agent.Submit(map[string]string{"psk": "typed"})
	if a := await(t, pending); variantString(a.reply["802-11-wireless-security"]["psk"]) != "typed" {
		t.Errorf("reply = %v %v, want what the user typed", a.reply, a.err)
	}
}
