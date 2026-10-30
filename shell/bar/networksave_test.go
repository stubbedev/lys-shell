package bar

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stubbedev/lys-secrets/vault"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/network"
)

// savedLogins records what the offer card kept, failing with err.
type savedLogins struct {
	mu    sync.Mutex
	err   error
	saved []string
}

func (s *savedLogins) save(_ context.Context, name string, e network.VaultEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.saved = append(s.saved, name+" "+e.Username+" "+e.Password)
	return nil
}

func (s *savedLogins) list() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.saved...)
}

// The agent's offer shows as a card naming the connection; Save keeps
// the sign-in in the vault and takes the offer down.
func TestNetworkSaveOfferKeepsTheSignIn(t *testing.T) {
	agent := &fakeAgent{offer: &network.SaveOffer{UUID: "u1", Name: "Office", Entry: network.VaultEntry{Username: "ann", Password: "pw"}}}
	logins := &savedLogins{}
	v, _, _ := newNetTestView(t, network.Snapshot{}, netDeps{agent: agent, saveLogin: logins.save})
	f := v.saveOffer
	if !f.Visible() || itemText(f.head, "title") != "Office" {
		t.Fatalf("card visible %v title %q", f.Visible(), itemText(f.head, "title"))
	}
	onLoop(f.save)
	waitHeadless(t, "the save", func() bool { return !f.saving && !f.Visible() })
	if got := logins.list(); len(got) != 1 || got[0] != "Office ann pw" {
		t.Errorf("saved %q", got)
	}
	if _, ok := agent.SaveOffer(); ok {
		t.Error("the offer outlived its save")
	}
}

// Not now takes the offer down without saving; a vault refusing the
// save keeps the card up with why; without a vault there is no card.
func TestNetworkSaveOfferRefusals(t *testing.T) {
	agent := &fakeAgent{offer: &network.SaveOffer{UUID: "u1", Name: "Office", Entry: network.VaultEntry{Password: "pw"}}}
	logins := &savedLogins{err: vault.ErrItemExists}
	v, _, _ := newNetTestView(t, network.Snapshot{}, netDeps{agent: agent, saveLogin: logins.save})
	f := v.saveOffer
	onLoop(f.save)
	waitHeadless(t, "the refused save", func() bool { return !f.saving })
	if !f.Visible() || !f.errText.Visible() || !errors.Is(logins.err, vault.ErrItemExists) {
		t.Errorf("a refused save: card %v, error shown %v", f.Visible(), f.errText.Visible())
	}
	if _, ok := agent.SaveOffer(); !ok {
		t.Error("a refused save dropped the offer")
	}
	onLoop(f.dismiss)
	if f.Visible() || len(logins.list()) != 0 {
		t.Errorf("not now: card %v, saved %q", f.Visible(), logins.list())
	}
	if _, ok := agent.SaveOffer(); ok {
		t.Error("not now kept the offer")
	}

	agent.offer = &network.SaveOffer{UUID: "u2", Name: "Lab"}
	bare, _, _ := newNetTestView(t, network.Snapshot{}, netDeps{agent: agent})
	if bare.saveOffer.Visible() {
		t.Error("an offer shows without a vault to keep it")
	}
}

// The network vault saves into the configured folder, named like the
// connection, and tells a connection the folder has no login for: none
// without a folder, a locked vault, or one already there.
func TestBitwardenNetworkVaultSaves(t *testing.T) {
	cfg := config.Defaults()
	cfg.Bitwarden.NetworkFolder = "Networks"
	host := &hostFake{status: vault.Status{State: vault.StateUnlocked}, items: []vault.Item{{ID: "1", Type: vault.ItemLogin, Name: "Home"}}}
	nv := bitwardenNetworkVault{
		folder: func() string { return cfg.Bitwarden.NetworkFolder },
		find: func(_ context.Context, name string, scope vault.FolderScope) (vault.Item, bool, error) {
			return host.FindLogin(name, scope)
		},
		create: host.CreateLogin,
	}
	if !nv.Saveable("Office") || nv.Saveable("Home") {
		t.Errorf("saveable: Office %v, Home %v; want only Office", nv.Saveable("Office"), nv.Saveable("Home"))
	}
	if err := nv.Save(context.Background(), "Office", network.VaultEntry{Username: "ann", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	if c := host.created; len(c) != 1 || c[0].Folder != "Networks" || c[0].Name != "Office" || c[0].Username != "ann" || c[0].Password != "pw" || c[0].URIs != nil {
		t.Errorf("created %+v", host.created)
	}
	host.Lock()
	if nv.Saveable("Office") {
		t.Error("a locked vault is saveable")
	}
	cfg.Bitwarden.NetworkFolder = ""
	if nv.Saveable("Office") || nv.Save(context.Background(), "Office", network.VaultEntry{Password: "pw"}) == nil {
		t.Error("saving without a network folder")
	}
}
