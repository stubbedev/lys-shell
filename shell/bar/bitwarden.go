package bar

import (
	"context"
	"errors"
	"io"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-secrets/vault"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/transfer"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/desktopnotify"
	"github.com/stubbedev/lys-shell/internal/feed"
	"github.com/stubbedev/lys-shell/service/clipboard"
	"github.com/stubbedev/lys-shell/service/network"
)

// vaultCallTimeout bounds a call the shell makes on its own: locking
// with the session, answering the network agent.
const vaultCallTimeout = 5 * time.Second

// bwVault is what the module, the dropdown and the launcher use of
// lys-secrets' vault: its status and listing as last read, a tick after
// each change of them, and the vault's operations. *vaultMirror
// implements it.
type bwVault interface {
	Status() vault.Status
	Items() []vault.Item
	Subscribe() (<-chan struct{}, func())
	Login(ctx context.Context, r vault.LoginRequest) error
	SendLoginEmail(ctx context.Context, r vault.LoginRequest) error
	LoginAPIKey(ctx context.Context, r vault.APIKeyRequest) error
	Unlock(ctx context.Context, password string) error
	Sync(ctx context.Context) error
	Lock(ctx context.Context) error
	Logout(ctx context.Context) error
	Field(ctx context.Context, id string, f vault.Field, password string) (vault.Value, error)
	Sends(ctx context.Context) ([]vault.Send, error)
	CreateTextSend(ctx context.Context, s vault.NewTextSend) (vault.CreatedSend, error)
	DeleteSend(ctx context.Context, id vault.SendID) error
}

// vaultMirror keeps the vault's status and listing as lys-secrets last
// reported them, so the UI reads them without a bus call, and reads
// them again after every operation and every Changed signal.
type vaultMirror struct {
	client *vault.Client
	ticks  *feed.Tick

	// refreshing serializes the reads, so an older answer never lands
	// after a newer one.
	refreshing sync.Mutex

	mu     sync.Mutex
	status vault.Status
	items  []vault.Item
}

func newVaultMirror(client *vault.Client) *vaultMirror {
	return &vaultMirror{client: client, ticks: feed.NewTick()}
}

// refresh reads the status, and the listing of an open vault. A vault
// that does not answer reads as logged out, its error the reason.
func (m *vaultMirror) refresh(ctx context.Context) {
	m.refreshing.Lock()
	defer m.refreshing.Unlock()
	st, err := m.client.Status(ctx)
	var items []vault.Item
	switch {
	case err != nil:
		st = vault.Status{State: vault.StateLoggedOut, Err: err}
	case st.State == vault.StateUnlocked:
		if items, err = m.client.Items(ctx); err != nil && !errors.Is(err, vault.ErrLocked) {
			st.Err = err
		}
	}
	m.mu.Lock()
	m.status, m.items = st, items
	m.mu.Unlock()
	feed.Notify(m.ticks)
}

func (m *vaultMirror) Status() vault.Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *vaultMirror) Items() []vault.Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.items
}

func (m *vaultMirror) Subscribe() (<-chan struct{}, func()) { return m.ticks.Subscribe() }

// then reads the vault again after op, returning op's error.
func (m *vaultMirror) then(ctx context.Context, err error) error {
	m.refresh(ctx)
	return err
}

func (m *vaultMirror) Login(ctx context.Context, r vault.LoginRequest) error {
	return m.then(ctx, m.client.Login(ctx, r))
}

func (m *vaultMirror) SendLoginEmail(ctx context.Context, r vault.LoginRequest) error {
	return m.client.SendLoginEmail(ctx, r)
}

func (m *vaultMirror) LoginAPIKey(ctx context.Context, r vault.APIKeyRequest) error {
	return m.then(ctx, m.client.LoginAPIKey(ctx, r))
}

func (m *vaultMirror) Unlock(ctx context.Context, password string) error {
	return m.then(ctx, m.client.Unlock(ctx, password))
}

func (m *vaultMirror) Sync(ctx context.Context) error { return m.then(ctx, m.client.Sync(ctx)) }

func (m *vaultMirror) Lock(ctx context.Context) error { return m.then(ctx, m.client.Lock(ctx)) }

func (m *vaultMirror) Logout(ctx context.Context) error { return m.then(ctx, m.client.Logout(ctx)) }

func (m *vaultMirror) Field(ctx context.Context, id string, f vault.Field, password string) (vault.Value, error) {
	return m.client.Field(ctx, id, f, password)
}

func (m *vaultMirror) Sends(ctx context.Context) ([]vault.Send, error) { return m.client.Sends(ctx) }

func (m *vaultMirror) CreateTextSend(ctx context.Context, s vault.NewTextSend) (vault.CreatedSend, error) {
	return m.client.CreateTextSend(ctx, s)
}

func (m *vaultMirror) DeleteSend(ctx context.Context, id vault.SendID) error {
	return m.client.DeleteSend(ctx, id)
}

// bitwardenHost is the shell's one view of the vault, shared by every
// bar's module, the dropdown, the launcher mode and the network agent.
// The client connects on first use and the mirror follows the vault
// from its first use until the shell exits, so a layout without the
// module never starts lys-secrets.
type bitwardenHost struct {
	life   context.Context
	config func() config.BitwardenConfig
	// copy puts content on the clipboard, cleared after ttl unless
	// replaced (0 keeps it); loop goroutine only.
	copy func(content transfer.Content, ttl time.Duration) error
	// client is the vault's client on the session bus.
	client func() (*vault.Client, error)
	// vault is the mirror, following the vault once opened.
	vault func() (bwVault, error)
	// notify posts a desktop notification, for what happens while the
	// dropdown is closed: a failing periodic sync, a forced logout.
	notify func(summary, body string)
}

// newBitwardenHost is the host over the session clipboard and the
// session bus.
func newBitwardenHost(life context.Context, application *app.Application, current func() *config.Config) *bitwardenHost {
	h := &bitwardenHost{
		life: life, config: func() config.BitwardenConfig { return current().Bitwarden },
		notify: func(summary, body string) {
			desktopnotify.Notify(i18n.T("dropdown-bitwarden-title"), summary, body, "ld-shield-check-symbolic")
		},
	}
	h.copy = func(content transfer.Content, ttl time.Duration) error {
		clip := application.Clipboard()
		if clip == nil {
			return app.ErrClipboardUnavailable
		}
		if ttl == 0 {
			return clip.Write(content)
		}
		return clip.WriteExpiring(content, ttl)
	}
	h.client = sync.OnceValues(func() (*vault.Client, error) {
		conn, err := dbus.ConnectSessionBus()
		if err != nil {
			return nil, err
		}
		return vault.NewClient(conn), nil
	})
	h.vault = sync.OnceValues(func() (bwVault, error) {
		c, err := h.client()
		if err != nil {
			return nil, err
		}
		return h.follow(newVaultMirror(c))
	})
	return h
}

// follow keeps m current until the shell exits, reporting the vault's
// periodic sync failures.
func (h *bitwardenHost) follow(m *vaultMirror) (bwVault, error) {
	err := m.client.Watch(h.life, vault.Watcher{
		Changed:    func(vault.Status) { m.refresh(h.life) },
		SyncFailed: h.reportSync,
	})
	if err != nil {
		return nil, err
	}
	go m.refresh(h.life)
	return m, nil
}

// copyValue puts field f's value on the clipboard as of now: a secret
// marked and cleared after clipboard-clear, or when the value expires
// if that is sooner (a verification code's end); the rest as plain
// text. It returns the clear delay (0 for none). Loop goroutine only.
func (h *bitwardenHost) copyValue(f vault.Field, v vault.Value, now time.Time) (time.Duration, error) {
	var expires time.Duration
	if !v.Expires.IsZero() {
		expires = max(v.Expires.Sub(now), time.Second)
	}
	return h.copyText(v.Text, f.Secret(), expires)
}

func (h *bitwardenHost) copyGenerated(rules bwRules, random io.Reader) (time.Duration, error) {
	value, err := rules.Generate(random)
	if err != nil {
		return 0, err
	}
	return h.copyText(value, true, 0)
}

// copyText copies value; a secret clears after clipboard-clear, or at
// expires when that comes sooner.
func (h *bitwardenHost) copyText(value string, secret bool, expires time.Duration) (time.Duration, error) {
	content, ttl := transfer.Text(value), time.Duration(0)
	if secret {
		content = clipboard.SecretContent(value)
		ttl = time.Duration(h.config().ClipboardClear) * time.Second
		if expires > 0 && (ttl == 0 || expires < ttl) {
			ttl = expires
		}
	}
	if err := h.copy(content, ttl); err != nil {
		return 0, err
	}
	return ttl, nil
}

// lockVault locks lys-secrets' vault with the session, off the loop.
func (h *bitwardenHost) lockVault() {
	go func() {
		ctx, cancel := context.WithTimeout(h.life, vaultCallTimeout)
		defer cancel()
		if err := h.lock(ctx); err != nil {
			log.Printf("bitwarden: lock with the session: %v", err)
		}
	}()
}

// lock locks lys-secrets' vault whether or not this shell ever opened
// it; a vault that is not running is left alone.
func (h *bitwardenHost) lock(ctx context.Context) error {
	c, err := h.client()
	if err != nil {
		return err
	}
	return c.Lock(ctx)
}

// reportSync notifies a periodic sync failing, which lys-secrets
// reports once per run of failures, and a forced logout, which the
// closed dropdown would not show.
func (h *bitwardenHost) reportSync(err error) {
	log.Printf("bitwarden: periodic sync: %v", err)
	if errors.Is(err, vault.ErrLoginRequired) {
		h.notify(i18n.T("dropdown-bitwarden-notify-logged-out"), i18n.T("dropdown-bitwarden-notify-logged-out-body"))
		return
	}
	h.notify(i18n.T("dropdown-bitwarden-notify-sync-failed"), bwErrorText(err))
}

// bitwardenLabel is the label: {{ count }} is the open vault's item
// count, empty while locked.
func bitwardenLabel(format string, st vault.Status) string {
	count := ""
	if st.State == vault.StateUnlocked {
		count = strconv.Itoa(st.Items)
	}
	return strings.ReplaceAll(format, "{{ count }}", count)
}

// bitwardenModule is the bar button: the lock state's icon and the
// item count.
type bitwardenModule struct {
	ctx   ModuleContext
	vault bwVault
	btn   *barButton
	icon  *widget.Icon
}

var errBitwardenNoHost = errors.New("bitwarden: requires the shared vault host")

func newBitwarden(ctx ModuleContext) (Module, error) {
	if ctx.Bitwarden == nil {
		return nil, errBitwardenNoHost
	}
	v, err := ctx.Bitwarden.vault()
	if err != nil {
		return nil, err
	}
	m := &bitwardenModule{ctx: ctx, vault: v}
	m.icon = moduleIcon(ctx, ctx.Config.Bitwarden.Icons()[config.BitwardenLocked])
	m.btn = newBarButton(ctx, m.icon, "")
	m.render()
	ticks, stop := v.Subscribe()
	follow(ctx, ticks, stop, func(struct{}) { m.render() })
	return m, nil
}

// render applies the vault's status to the label and icon.
func (m *bitwardenModule) render() {
	cfg := m.ctx.Config.Bitwarden
	st := m.vault.Status()
	text := ""
	if cfg.LabelShow {
		text = bitwardenLabel(cfg.Format, st)
	}
	m.btn.SetLabel(text)
	if m.icon == nil {
		return
	}
	state := config.BitwardenLocked
	if st.State == vault.StateUnlocked {
		state = config.BitwardenUnlocked
	}
	m.icon.SetThemeName(cfg.Icons()[state].Name)
}

func (m *bitwardenModule) Root() widget.Widget { return m.btn }

// bitwardenNetworkVault answers the NetworkManager secret agent from
// the vault's network folder (lys-secrets' NetworkLogin); no folder
// configured, a vault that is not open or does not answer, answers
// nothing.
type bitwardenNetworkVault struct {
	folder func() string
	lookup func(ctx context.Context, folder, name string) (vault.NetworkLogin, bool, error)
	find   func(ctx context.Context, name string, scope vault.FolderScope) (vault.Item, bool, error)
	create func(ctx context.Context, l vault.NewLogin) (vault.ItemID, error)
}

// networkVault is the host's vault for the network agent.
func (h *bitwardenHost) networkVault() bitwardenNetworkVault {
	return bitwardenNetworkVault{
		folder: func() string { return h.config().NetworkFolder },
		lookup: func(ctx context.Context, folder, name string) (vault.NetworkLogin, bool, error) {
			c, err := h.client()
			if err != nil {
				return vault.NetworkLogin{}, false, err
			}
			return c.NetworkLogin(ctx, folder, name)
		},
		find: func(ctx context.Context, name string, scope vault.FolderScope) (vault.Item, bool, error) {
			c, err := h.client()
			if err != nil {
				return vault.Item{}, false, err
			}
			return c.FindLogin(ctx, name, scope)
		},
		create: func(ctx context.Context, l vault.NewLogin) (vault.ItemID, error) {
			c, err := h.client()
			if err != nil {
				return "", err
			}
			return c.CreateLogin(ctx, l)
		},
	}
}

// Entry implements network.Vault.
func (v bitwardenNetworkVault) Entry(name string) (network.VaultEntry, bool) {
	folder := v.folder()
	if folder == "" {
		return network.VaultEntry{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), vaultCallTimeout)
	defer cancel()
	l, ok, err := v.lookup(ctx, folder, name)
	if err != nil {
		log.Printf("bitwarden: the network login for %s: %v", name, err)
		return network.VaultEntry{}, false
	}
	if !ok {
		return network.VaultEntry{}, false
	}
	return network.VaultEntry{Username: l.Username, Password: l.Password, Fields: l.Fields}, true
}

// Saveable implements network.Vault: with a network folder configured
// and the vault open, a profile the folder has no login for.
func (v bitwardenNetworkVault) Saveable(name string) bool {
	folder := v.folder()
	if folder == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), vaultCallTimeout)
	defer cancel()
	_, found, err := v.find(ctx, name, vault.InFolder(folder))
	if err != nil {
		if !errors.Is(err, vault.ErrLocked) {
			log.Printf("bitwarden: look up the network login for %s: %v", name, err)
		}
		return false
	}
	return !found
}

// Save keeps the profile's sign-in as a login named like it in the
// network folder, where the agent finds it next time.
func (v bitwardenNetworkVault) Save(ctx context.Context, name string, e network.VaultEntry) error {
	folder := v.folder()
	if folder == "" {
		return errBitwardenNoNetworkFolder
	}
	_, err := v.create(ctx, vault.NewLogin{Folder: folder, Name: name, Username: e.Username, Password: e.Password})
	return err
}

var errBitwardenNoNetworkFolder = errors.New("bitwarden: no network folder is configured")
