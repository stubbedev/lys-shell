package bar

import (
	"context"
	"errors"
	"io"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/transfer"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/desktopnotify"
	"github.com/stubbedev/lys-shell/internal/xdg"
	"github.com/stubbedev/lys-shell/service/bitwarden"
	"github.com/stubbedev/lys-shell/service/clipboard"
)

// bitwardenDevice is the name the account's device list shows.
const bitwardenDevice = "lys-shell"

// bitwardenTick is how often the upkeep checks the idle lock and the
// sync interval, both configured in minutes.
const bitwardenTick = 30 * time.Second

// vault is what the module and the dropdown use of the Bitwarden
// service; *bitwarden.Service implements it.
type vault interface {
	Status() bitwarden.Status
	Items() []bitwarden.Item
	Subscribe() (<-chan struct{}, func())
	Login(ctx context.Context, req bitwarden.LoginRequest) error
	SendLoginEmail(ctx context.Context, req bitwarden.LoginRequest) error
	LoginAPIKey(ctx context.Context, req bitwarden.APIKeyRequest) error
	Unlock(ctx context.Context, password string) error
	Sync(ctx context.Context) error
	Lock()
	Logout() error
	VerifyPassword(password string) error
	Touch()
	LockIdle(idle time.Duration) bool
}

// bitwardenHost is the shell's one vault, shared by every bar's module
// and the dropdown. The service opens on first use, so a layout without
// the module never creates its state directory; opening starts the
// upkeep, which runs until the shell exits.
type bitwardenHost struct {
	life   context.Context
	config func() config.BitwardenConfig
	// copy puts content on the clipboard, cleared after ttl unless
	// replaced (0 keeps it); loop goroutine only.
	copy  func(content transfer.Content, ttl time.Duration) error
	vault func() (vault, error)
	// notify posts a desktop notification, for what happens while the
	// dropdown is closed: a failing periodic sync, a forced logout.
	notify func(summary, body string)

	mu     sync.Mutex
	opened vault
	// syncFailing holds while periodic syncs keep failing, so only the
	// first failure of a run notifies; upkeep goroutine only.
	syncFailing bool
}

// newBitwardenHost is the host over the session clipboard, the vault
// opening in the state directory.
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
	h.vault = sync.OnceValues(func() (vault, error) {
		dir, ok := xdg.StateDir()
		if !ok {
			return nil, errors.New("bitwarden: no state directory: neither XDG_STATE_HOME nor HOME is set")
		}
		svc, err := bitwarden.Open(filepath.Join(dir, "bitwarden"), bitwardenDevice)
		if err != nil {
			return nil, err
		}
		h.mu.Lock()
		h.opened = svc
		h.mu.Unlock()
		go h.keep(svc)
		return svc, nil
	})
	return h
}

// copyField puts item it's field f on the clipboard, as its value at now: a
// secret marked and cleared after clipboard-clear, the rest as plain
// text. The copy counts as use of v. It returns the clear delay (0 for
// none). Loop goroutine only.
func (h *bitwardenHost) copyField(v vault, it bitwarden.Item, f bwField, now time.Time) (time.Duration, error) {
	value, err := f.value(it, now)
	if err != nil {
		return 0, err
	}
	ttl, err := h.copyText(value, f.secret(), f.expiry(it, now))
	if err != nil {
		return 0, err
	}
	v.Touch()
	return ttl, nil
}

func (h *bitwardenHost) copyGenerated(rules bwRules, random io.Reader) (time.Duration, error) {
	value, err := rules.Generate(random)
	if err != nil {
		return 0, err
	}
	return h.copyText(value, true, 0)
}

// copyText copies value; a secret clears after clipboard-clear, or at
// expires when that comes sooner (a verification code's end).
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

// lockIfOpen locks the vault with the session; a vault never opened
// stays unopened.
func (h *bitwardenHost) lockIfOpen() {
	h.mu.Lock()
	v := h.opened
	h.mu.Unlock()
	if v != nil {
		v.Lock()
	}
}

// keep runs the upkeep every tick until the shell exits.
func (h *bitwardenHost) keep(v vault) {
	t := time.NewTicker(bitwardenTick)
	defer t.Stop()
	var lastSync time.Time
	for {
		select {
		case <-h.life.Done():
			return
		case now := <-t.C:
			lastSync = h.upkeep(v, now, lastSync)
		}
	}
}

// upkeep locks the vault once unused for lock-after and syncs it every
// sync-interval, counted from the last sync or the last attempt, so a
// server that is down is retried once an interval, not every tick. It
// returns the attempt time.
func (h *bitwardenHost) upkeep(v vault, now, lastAttempt time.Time) time.Time {
	cfg := h.config()
	if cfg.LockAfter > 0 {
		v.LockIdle(minutes(cfg.LockAfter))
	}
	st := v.Status()
	if !syncDue(st, cfg.SyncInterval, lastAttempt, now) {
		return lastAttempt
	}
	h.reportSync(v.Sync(h.life))
	return now
}

// reportSync notifies the first failure of a run of periodic syncs and
// a forced logout, which the closed dropdown would not show.
func (h *bitwardenHost) reportSync(err error) {
	switch {
	case err == nil:
		h.syncFailing = false
	case errors.Is(err, bitwarden.ErrLoginRequired):
		log.Printf("bitwarden: periodic sync: %v", err)
		h.syncFailing = false
		h.notify(i18n.T("dropdown-bitwarden-notify-logged-out"), i18n.T("dropdown-bitwarden-notify-logged-out-body"))
	default:
		log.Printf("bitwarden: periodic sync: %v", err)
		if !h.syncFailing {
			h.syncFailing = true
			h.notify(i18n.T("dropdown-bitwarden-notify-sync-failed"), bwErrorText(err))
		}
	}
}

// syncDue reports whether an open vault is due its periodic sync.
func syncDue(st bitwarden.Status, every uint32, lastAttempt, now time.Time) bool {
	if every == 0 || st.State != bitwarden.StateUnlocked || st.Syncing {
		return false
	}
	last := st.LastSync
	if lastAttempt.After(last) {
		last = lastAttempt
	}
	return !now.Before(last.Add(minutes(every)))
}

func minutes(n uint32) time.Duration { return time.Duration(n) * time.Minute }

// bitwardenLabel is the label: {{ count }} is the open vault's item
// count, empty while locked.
func bitwardenLabel(format string, st bitwarden.Status) string {
	count := ""
	if st.State == bitwarden.StateUnlocked {
		count = strconv.Itoa(st.Items)
	}
	return strings.ReplaceAll(format, "{{ count }}", count)
}

// bitwardenModule is the bar button: the lock state's icon and the
// item count.
type bitwardenModule struct {
	ctx   ModuleContext
	vault vault
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
	if st.State == bitwarden.StateUnlocked {
		state = config.BitwardenUnlocked
	}
	m.icon.SetThemeName(cfg.Icons()[state].Name)
}

func (m *bitwardenModule) Root() widget.Widget { return m.btn }
