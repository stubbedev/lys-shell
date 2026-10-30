package network

import (
	"context"
	"maps"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/service/network/secrets"
)

// fakeVault holds entries by connection name.
type fakeVault map[string]VaultEntry

func (v fakeVault) Entry(name string) (VaultEntry, bool) {
	e, ok := v[name]
	return e, ok
}

// Saveable holds for a name the vault has nothing for.
func (v fakeVault) Saveable(name string) bool {
	_, ok := v[name]
	return !ok
}

// closedVault is a vault that answers nothing and takes nothing: locked,
// or with no network folder configured.
type closedVault struct{}

func (closedVault) Entry(string) (VaultEntry, bool) { return VaultEntry{}, false }
func (closedVault) Saveable(string) bool            { return false }

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

// offerShown waits for the agent's save offer.
func offerShown(t *testing.T, a *Agent) SaveOffer {
	t.Helper()
	var o SaveOffer
	waitFor(t, "a save offer", func() bool {
		var ok bool
		o, ok = a.SaveOffer()
		return ok
	})
	return o
}

// noOffer checks no offer goes up while the agent watches a few polls.
func noOffer(t *testing.T, a *Agent, what string) {
	t.Helper()
	time.Sleep(3 * connectPoll)
	if o, ok := a.SaveOffer(); ok {
		t.Errorf("%s: offered %+v", what, o)
	}
}

// A password the user typed is offered to the vault once its profile
// connected with it, and dismissing takes the offer down.
func TestAgentOffersATypedSignInOnceConnected(t *testing.T) {
	fx := serveFixtureWith(t, RequestBudget, fakeVault{})
	pending := fx.ask(wifiConnection("cafe", "Cafe", []byte("cafe")), 1, "802-11-wireless-security")
	fx.shown(t)
	fx.agent.Submit(map[string]string{"psk": "espresso"})
	await(t, pending)
	noOffer(t, fx.agent, "before the profile connected")
	fx.states.set("cafe", ActiveActivating)
	noOffer(t, fx.agent, "while it activates")
	fx.states.set("cafe", ActiveActivated)
	if o := offerShown(t, fx.agent); o.UUID != "cafe" || o.Name != "Cafe" || o.Entry.Password != "espresso" || o.Entry.Username != "" || o.Entry.Fields != nil {
		t.Errorf("offer = %+v", o)
	}
	fx.agent.DismissSave("other")
	if _, ok := fx.agent.SaveOffer(); !ok {
		t.Error("dismissing another profile took the offer down")
	}
	fx.agent.DismissSave("cafe")
	if _, ok := fx.agent.SaveOffer(); ok {
		t.Error("the offer outlived its dismissal")
	}
}

// Nothing is offered that the vault could not keep or give back: an
// answer with more than a login holds, a vault that cannot take it, a
// password NM asked for again, and secrets the vault itself answered.
func TestAgentOffersOnlyWhatTheVaultCanKeep(t *testing.T) {
	typed := func(fx *agentFixture, conn ConnectionDict, setting string, values map[string]string, hints ...string) {
		t.Helper()
		pending := fx.ask(conn, 1, setting, hints...)
		fx.shown(t)
		fx.agent.Submit(values)
		await(t, pending)
	}

	fx := serveFixtureWith(t, RequestBudget, fakeVault{})
	fx.states.set("office", ActiveActivated)
	typed(fx, formVPNConnection("office"), "vpn", map[string]string{"password": "pw", "pin": "1234"}, "password", "pin")
	noOffer(t, fx.agent, "a password with a PIN")

	closed := serveFixtureWith(t, RequestBudget, closedVault{})
	closed.states.set("cafe", ActiveActivated)
	typed(closed, wifiConnection("cafe", "Cafe", []byte("cafe")), "802-11-wireless-security", map[string]string{"psk": "espresso"})
	noOffer(t, closed.agent, "a vault that takes nothing")

	again := serveFixtureWith(t, RequestBudget, fakeVault{})
	typed(again, wifiConnection("cafe", "Cafe", []byte("cafe")), "802-11-wireless-security", map[string]string{"psk": "wrong"})
	pending := make(chan answer, 1)
	go func() {
		reply, err := again.nm.getSecrets(context.Background(), again.name, wifiConnection("cafe", "Cafe", []byte("cafe")), settingPath(1), "802-11-wireless-security", nil, flagAllowInteraction|flagRequestNew)
		pending <- answer{reply, err}
	}()
	again.shown(t)
	again.states.set("cafe", ActiveActivated)
	noOffer(t, again.agent, "a password NM asked for again")
	again.agent.Cancel()
	await(t, pending)

	known := serveFixtureWith(t, RequestBudget, fakeVault{"Home": {Password: "kept"}})
	known.states.set("home", ActiveActivated)
	await(t, known.ask(wifiConnection("home", "Home", []byte("home")), 1, "802-11-wireless-security"))
	noOffer(t, known.agent, "secrets the vault answered")
}
