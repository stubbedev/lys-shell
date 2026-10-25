package bar

import (
	"context"
	"errors"
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
	"github.com/stubbedev/lys-shell/internal/xdg"
	"github.com/stubbedev/lys-shell/service/bitwarden"
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
}

// newBitwardenHost is the host over the session clipboard, the vault
// opening in the state directory.
func newBitwardenHost(life context.Context, application *app.Application, current func() *config.Config) *bitwardenHost {
	h := &bitwardenHost{life: life, config: func() config.BitwardenConfig { return current().Bitwarden }}
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
		go h.keep(svc)
		return svc, nil
	})
	return h
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
	if err := v.Sync(h.life); err != nil {
		log.Printf("bitwarden: periodic sync: %v", err)
	}
	return now
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
	label *widget.Label
	icon  *widget.Icon
	root  widget.Widget
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
	m := &bitwardenModule{ctx: ctx, vault: v, label: widget.NewLabel(ctx.Font, ctx.Style.labelPx, "", ctx.Style.fg)}
	m.icon = moduleIcon(ctx, ctx.Config.Bitwarden.Icons()[config.BitwardenLocked])
	m.root = assembleModule(ctx, m.icon, m.label)
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
	m.label.SetText(text)
	if m.icon == nil {
		return
	}
	state := config.BitwardenLocked
	if st.State == bitwarden.StateUnlocked {
		state = config.BitwardenUnlocked
	}
	m.icon.SetThemeName(cfg.Icons()[state].Name)
}

func (m *bitwardenModule) Root() widget.Widget { return m.root }
