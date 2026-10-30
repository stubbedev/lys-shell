package portal

import (
	"bytes"
	"context"
	"encoding/base64"
	"sync"
	"testing"

	"github.com/stubbedev/lys-secrets/vault"
)

// portalVault is lys-secrets' vault for the Secret portal's tests: its
// logins by name, and the calls that reached it.
type portalVault struct {
	mu      sync.Mutex
	state   vault.State
	logins  map[string]vault.NewLogin
	created []vault.NewLogin
	calls   int
}

func (h *portalVault) touch() {
	h.mu.Lock()
	h.calls++
	h.mu.Unlock()
}

func (h *portalVault) Status() vault.Status {
	h.touch()
	h.mu.Lock()
	defer h.mu.Unlock()
	return vault.Status{State: h.state}
}

func (h *portalVault) Items() ([]vault.Item, error) { return nil, nil }

func (h *portalVault) Field(id string, f vault.Field, _ string) (vault.Value, error) {
	h.touch()
	h.mu.Lock()
	defer h.mu.Unlock()
	l, ok := h.logins[id]
	if !ok || f != vault.FieldPassword {
		return vault.Value{}, vault.ErrNoSuchItem
	}
	return vault.Value{Text: l.Password}, nil
}

func (h *portalVault) Login(context.Context, vault.LoginRequest) error          { return nil }
func (h *portalVault) SendLoginEmail(context.Context, vault.LoginRequest) error { return nil }
func (h *portalVault) LoginAPIKey(context.Context, vault.APIKeyRequest) error   { return nil }
func (h *portalVault) Unlock(context.Context, string) error                     { return nil }
func (h *portalVault) Lock()                                                    {}
func (h *portalVault) Logout() error                                            { return nil }
func (h *portalVault) Sync(context.Context) error                               { return nil }
func (h *portalVault) NetworkLogin(string, string) (vault.NetworkLogin, bool) {
	return vault.NetworkLogin{}, false
}
func (h *portalVault) Sends() ([]vault.Send, error) { return nil, nil }
func (h *portalVault) CreateTextSend(context.Context, vault.NewTextSend) (vault.CreatedSend, error) {
	return vault.CreatedSend{}, nil
}
func (h *portalVault) DeleteSend(context.Context, vault.SendID) error { return nil }

func (h *portalVault) CreateLogin(_ context.Context, l vault.NewLogin) (vault.ItemID, error) {
	h.touch()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state != vault.StateUnlocked {
		return "", vault.ErrLocked
	}
	h.created = append(h.created, l)
	h.logins[l.Name] = l
	return vault.ItemID(l.Name), nil
}

// FindLogin finds a login by its name in the folder asked for.
func (h *portalVault) FindLogin(name string, scope vault.FolderScope) (vault.Item, bool, error) {
	h.touch()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state != vault.StateUnlocked {
		return vault.Item{}, false, vault.ErrLocked
	}
	l, ok := h.logins[name]
	folder, _ := scope.Folder()
	if !ok || l.Folder != folder {
		return vault.Item{}, false, nil
	}
	return vault.Item{ID: name, Type: vault.ItemLogin, Name: name, Folder: folder, Fields: []vault.Field{vault.FieldPassword}}, true, nil
}

func (h *portalVault) set(fn func(h *portalVault)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fn(h)
}

func (h *portalVault) snapshot() (created []vault.NewLogin, calls int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]vault.NewLogin(nil), h.created...), h.calls
}

// vaultRig is a rig with a keyring, the dialogs answering yes or no,
// lys-secrets' vault in the state given, and portal-folder "Apps".
func vaultRig(t *testing.T, state vault.State, yes bool) (*rig, *portalVault, *fakeDialogs) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	r := newRig(t, nil)
	withKeyring(t, r)
	dialogs := serveDialogs(t, r, yes)
	host := &portalVault{state: state, logins: map[string]vault.NewLogin{}}
	release, err := vault.NewDaemon(context.Background(), host).Export(r.bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	if err := r.cfg.SetByPath("modules.bitwarden.portal-folder", "Apps"); err != nil {
		t.Fatal(err)
	}
	return r, host, dialogs
}

// keep puts value in the vault as app's secret.
func (h *portalVault) keep(appID string, value []byte) {
	h.set(func(h *portalVault) {
		h.logins[secretLabel(appID)] = vault.NewLogin{Folder: "Apps", Name: secretLabel(appID), Username: appID, Password: base64.StdEncoding.EncodeToString(value)}
	})
}

// A new app's secret goes into the vault folder once allowed, named
// like the keyring's item, and comes back the same from both; the
// second use asks nothing.
func TestSecretIsKeptInTheVaultOnceAllowed(t *testing.T) {
	r, host, dialogs := vaultRig(t, vault.StateUnlocked, true)
	code, first := r.retrieveSecret(t, "org.app")
	if code != ResponseSuccess || len(first) != secretLen {
		t.Fatalf("first = %d, %d bytes", code, len(first))
	}
	created, _ := host.snapshot()
	if len(created) != 1 {
		t.Fatalf("created %d logins", len(created))
	}
	l := created[0]
	if l.Folder != "Apps" || l.Name != secretLabel("org.app") || l.Username != "org.app" || l.Password != base64.StdEncoding.EncodeToString(first) {
		t.Errorf("created %+v", l)
	}
	if v, found := stored(t, r, "org.app"); !found || !bytes.Equal(v, first) {
		t.Error("the keyring has no copy")
	}
	dialogs.asked.Store(nil)
	if _, again := r.retrieveSecret(t, "org.app"); !bytes.Equal(again, first) {
		t.Error("the secret changed")
	}
	if created, _ := host.snapshot(); len(created) != 1 || dialogs.asked.Load() != nil {
		t.Errorf("the second use created %d logins, asked %v", len(created), dialogs.asked.Load())
	}
}

// A secret the vault already holds (from another computer) is the
// app's here too, copied into the keyring, without asking; a keyring
// secret the vault lacks goes in unchanged.
func TestSecretComesFromTheVault(t *testing.T) {
	r, host, dialogs := vaultRig(t, vault.StateUnlocked, true)
	kept := bytes.Repeat([]byte{3}, secretLen)
	host.keep("org.app", kept)
	if code, got := r.retrieveSecret(t, "org.app"); code != ResponseSuccess || !bytes.Equal(got, kept) {
		t.Fatalf("got %d, %x; want the vault's", code, got)
	}
	if v, found := stored(t, r, "org.app"); !found || !bytes.Equal(v, kept) {
		t.Error("the vault's secret was not copied into the keyring")
	}
	if dialogs.asked.Load() != nil {
		t.Errorf("asked %v for a secret the vault had", dialogs.asked.Load())
	}

	if err := r.cfg.SetByPath("modules.bitwarden.portal-folder", ""); err != nil {
		t.Fatal(err)
	}
	_, local := r.retrieveSecret(t, "org.local")
	if err := r.cfg.SetByPath("modules.bitwarden.portal-folder", "Apps"); err != nil {
		t.Fatal(err)
	}
	if code, got := r.retrieveSecret(t, "org.local"); code != ResponseSuccess || !bytes.Equal(got, local) {
		t.Fatalf("the keyring's secret changed once the vault kept secrets: %d", code)
	}
	created, _ := host.snapshot()
	if len(created) != 1 || created[0].Password != base64.StdEncoding.EncodeToString(local) {
		t.Errorf("created %+v, want the keyring's secret", created)
	}
}

// A refusal keeps the secret on this computer and is not asked again; a
// vault holding another secret than the keyring never replaces it.
func TestSecretStaysLocalWhenRefused(t *testing.T) {
	r, host, dialogs := vaultRig(t, vault.StateUnlocked, false)
	code, first := r.retrieveSecret(t, "org.app")
	if code != ResponseSuccess || len(first) != secretLen {
		t.Fatalf("refused = %d, %d bytes", code, len(first))
	}
	if created, _ := host.snapshot(); len(created) != 0 {
		t.Errorf("a refusal created %+v", created)
	}
	dialogs.asked.Store(nil)
	if _, again := r.retrieveSecret(t, "org.app"); !bytes.Equal(again, first) || dialogs.asked.Load() != nil {
		t.Errorf("after a refusal: same %v, asked %v", bytes.Equal(again, first), dialogs.asked.Load())
	}
	host.keep("org.app", bytes.Repeat([]byte{9}, secretLen))
	if _, got := r.retrieveSecret(t, "org.app"); !bytes.Equal(got, first) {
		t.Error("the vault's other secret replaced the keyring's")
	}
}

// While the vault is closed an app the keyring knows gets its secret;
// one it does not is refused, as the vault may hold its secret; and
// without a folder configured the vault is never asked.
func TestSecretWithTheVaultClosed(t *testing.T) {
	r, host, _ := vaultRig(t, vault.StateLocked, true)
	if code, got := r.retrieveSecret(t, "org.new"); code != ResponseOther || len(got) != 0 {
		t.Errorf("a new app with the vault locked = %d, %d bytes; want other and nothing", code, len(got))
	}
	if _, found := stored(t, r, "org.new"); found {
		t.Error("a secret was made up while the vault was locked")
	}
	if err := r.cfg.SetByPath("modules.bitwarden.portal-folder", ""); err != nil {
		t.Fatal(err)
	}
	_, before := host.snapshot()
	code, local := r.retrieveSecret(t, "org.new")
	if _, after := host.snapshot(); code != ResponseSuccess || after != before {
		t.Errorf("without a folder: %d, %d vault calls", code, after-before)
	}
	if err := r.cfg.SetByPath("modules.bitwarden.portal-folder", "Apps"); err != nil {
		t.Fatal(err)
	}
	if code, got := r.retrieveSecret(t, "org.new"); code != ResponseSuccess || !bytes.Equal(got, local) {
		t.Errorf("a known app with the vault locked = %d, same %v", code, bytes.Equal(got, local))
	}
}

// A vault entry that is not a secret is refused, not handed out.
func TestSecretRefusesABrokenVaultEntry(t *testing.T) {
	r, host, _ := vaultRig(t, vault.StateUnlocked, true)
	host.set(func(h *portalVault) {
		h.logins[secretLabel("org.app")] = vault.NewLogin{Folder: "Apps", Name: secretLabel("org.app"), Password: "not base64!"}
	})
	if code, got := r.retrieveSecret(t, "org.app"); code != ResponseOther || len(got) != 0 {
		t.Errorf("a broken entry = %d, %d bytes", code, len(got))
	}
	if _, found := stored(t, r, "org.app"); found {
		t.Error("a secret was stored over the broken entry")
	}
}
