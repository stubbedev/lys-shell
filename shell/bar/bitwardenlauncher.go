package bar

import (
	"context"
	"errors"
	"time"

	"github.com/stubbedev/lys-secrets/vault"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/pango"
	engine "github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/shell/launcher"
)

// vaultPhase is what the vault mode lists.
type vaultPhase int

const (
	// vaultItems lists the open vault's items.
	vaultItems vaultPhase = iota
	// vaultFields lists one item's fields.
	vaultFields
	// vaultUnlock asks for the master password to open the vault.
	vaultUnlock
	// vaultReprompt asks for the master password before copying a
	// protected item's secret.
	vaultReprompt
	// vaultUnusable says why there is nothing to list.
	vaultUnusable
)

// vaultMode is the launcher's vault search: the items of the shell's
// one vault, Enter copying an item's primary field and Shift+Enter
// listing its fields to pick one. A locked vault asks for the master
// password in the masked input; a protected item asks again before a
// secret is copied. Copies go through the bar's host, marked and
// cleared as the dropdown's are.
type vaultMode struct {
	host *bitwardenHost
	// invoke runs fn on the UI loop, which owns the clipboard.
	invoke func(fn func())
	now    func() time.Time

	phase  vaultPhase
	reason string
	// items are the item rows in row order; item, fields and field the
	// item and field a fields list or reprompt is about.
	items []vault.Item
	item  vault.Item
	field vault.Field
}

func newVaultMode(host *bitwardenHost, invoke func(func()), now func() time.Time) *vaultMode {
	return &vaultMode{host: host, invoke: invoke, now: now}
}

// Name is "vault".
func (*vaultMode) Name() string { return launcher.VaultModeName }

// AllowsCustom is true only while the input is a password.
func (m *vaultMode) AllowsCustom() bool { return m.phase == vaultUnlock || m.phase == vaultReprompt }

// Load lists what the vault's state allows.
func (m *vaultMode) Load(context.Context) engine.ModeState {
	v, err := m.host.vault()
	if err != nil {
		return m.unusable(i18n.T("dropdown-bitwarden-unavailable") + ": " + bwErrorText(err))
	}
	return m.fresh(v, "")
}

// fresh is the state the vault's lock state calls for, with message
// shown over it.
func (m *vaultMode) fresh(v bwVault, message string) engine.ModeState {
	switch v.Status().State {
	case vault.StateLoggedOut:
		return m.unusable(i18n.T("launcher-bitwarden-logged-out"))
	case vault.StateLocked:
		if message == "" {
			message = i18n.T("launcher-bitwarden-locked")
		}
		return m.password(vaultUnlock, message)
	}
	return m.list(v.Items(), message)
}

func (m *vaultMode) unusable(reason string) engine.ModeState {
	m.phase, m.reason, m.items = vaultUnusable, reason, nil
	return engine.ModeState{Prompt: launcher.VaultModeName, Message: &m.reason, NoCustom: true}
}

// password is a masked prompt for the master password.
func (m *vaultMode) password(phase vaultPhase, message string) engine.ModeState {
	m.phase, m.reason, m.items = phase, message, nil
	return engine.ModeState{Prompt: i18n.T("dropdown-bitwarden-master-password"), Message: &m.reason, Password: true}
}

// list is the item rows: the name over its caption, matched on both.
func (m *vaultMode) list(items []vault.Item, message string) engine.ModeState {
	m.phase, m.items = vaultItems, items
	rows := make([]engine.Item, len(items))
	for i, it := range items {
		rows[i] = vaultRow(it.Name, bwItemCaption(it), bwItemIcon(it.Type))
	}
	if message == "" {
		message = i18n.T("launcher-bitwarden-hint")
	}
	m.reason = message
	return engine.ModeState{Items: rows, Prompt: launcher.VaultModeName, Message: &m.reason, MarkupRows: true, NoCustom: true}
}

// fieldList is one item's field rows.
func (m *vaultMode) fieldList(it vault.Item) engine.ModeState {
	m.phase, m.item = vaultFields, it
	rows := make([]engine.Item, len(it.Fields))
	for i, f := range it.Fields {
		rows[i] = vaultRow(i18n.T("dropdown-bitwarden-copy-field", i18n.Str("field", bwFieldName(f))), "", bwFieldIcon(f))
	}
	m.reason = it.Name
	return engine.ModeState{Items: rows, Prompt: launcher.VaultModeName, Message: &m.reason, MarkupRows: true, NoCustom: true}
}

// vaultRow is a row of name with a dim caption, matched on both.
func vaultRow(name, caption, icon string) engine.Item {
	display := pango.Escape(name)
	if caption != "" {
		display += " <span weight='light' size='small'>" + pango.Escape(caption) + "</span>"
	}
	return engine.Item{Display: display, MatchText: name + " " + caption, Icon: engine.IconName(icon)}
}

// Activate unlocks, copies, or lists an item's fields.
func (m *vaultMode) Activate(ctx context.Context, target engine.Target, kind engine.ActivateKind, input string) engine.Action {
	v, err := m.host.vault()
	if err != nil {
		return engine.ActionReload{State: m.unusable(bwErrorText(err))}
	}
	switch m.phase {
	case vaultUnlock:
		if input == "" {
			return engine.ActionNothing{}
		}
		if err := v.Unlock(ctx, input); err != nil {
			return engine.ActionReload{State: m.password(vaultUnlock, bwErrorText(err))}
		}
		return engine.ActionReload{State: m.fresh(v, "")}
	case vaultReprompt:
		if input == "" {
			return engine.ActionNothing{}
		}
		return m.copy(ctx, v, m.item, m.field, input)
	case vaultItems:
		i, ok := target.Row()
		if !ok || int(i) >= len(m.items) {
			return engine.ActionNothing{}
		}
		it := m.items[i]
		if _, alt := kind.(engine.ActivateAlt); alt {
			return engine.ActionReload{State: m.fieldList(it)}
		}
		if len(it.Fields) == 0 {
			return engine.ActionNothing{}
		}
		return m.pick(ctx, v, it, it.Fields[0])
	case vaultFields:
		i, ok := target.Row()
		if !ok || int(i) >= len(m.item.Fields) {
			return engine.ActionNothing{}
		}
		return m.pick(ctx, v, m.item, m.item.Fields[i])
	}
	return engine.ActionNothing{}
}

// pick copies f, asking for the master password first when the item
// is protected by the reprompt and f is a secret.
func (m *vaultMode) pick(ctx context.Context, v bwVault, it vault.Item, f vault.Field) engine.Action {
	if it.Reprompt && f.Secret() {
		m.item, m.field = it, f
		return engine.ActionReload{State: m.password(vaultReprompt, i18n.T("dropdown-bitwarden-reprompt-desc", i18n.Str("field", bwFieldName(f)), i18n.Str("item", it.Name)))}
	}
	return m.copy(ctx, v, it, f, "")
}

// copy reads the field from the vault, with the master password for a
// protected item, puts it on the clipboard on the UI loop and closes. A
// wrong master password asks again; another failure stays open on the
// items, saying why.
func (m *vaultMode) copy(ctx context.Context, v bwVault, it vault.Item, f vault.Field, password string) engine.Action {
	value, err := v.Field(ctx, it.ID, f, password)
	if errors.Is(err, vault.ErrWrongPassword) {
		return engine.ActionReload{State: m.password(vaultReprompt, bwErrorText(err))}
	}
	if err != nil {
		return engine.ActionReload{State: m.fresh(v, bwErrorText(err))}
	}
	done := make(chan error, 1)
	m.invoke(func() {
		_, err := m.host.copyValue(f, value, m.now())
		done <- err
	})
	select {
	case <-ctx.Done():
		return engine.ActionNothing{}
	case err := <-done:
		if err != nil {
			return engine.ActionReload{State: m.fresh(v, bwErrorText(err))}
		}
		return engine.ActionClose{}
	}
}
