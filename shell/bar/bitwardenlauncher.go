package bar

import (
	"context"
	"time"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/pango"
	"github.com/stubbedev/lys-shell/service/bitwarden"
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
	items  []bitwarden.Item
	item   bitwarden.Item
	fields []bwField
	field  bwField
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
func (m *vaultMode) fresh(v vault, message string) engine.ModeState {
	switch v.Status().State {
	case bitwarden.StateLoggedOut:
		return m.unusable(i18n.T("launcher-bitwarden-logged-out"))
	case bitwarden.StateLocked:
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
func (m *vaultMode) list(items []bitwarden.Item, message string) engine.ModeState {
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
func (m *vaultMode) fieldList(it bitwarden.Item) engine.ModeState {
	m.phase, m.item, m.fields = vaultFields, it, bwFields(it)
	rows := make([]engine.Item, len(m.fields))
	for i, f := range m.fields {
		rows[i] = vaultRow(i18n.T("dropdown-bitwarden-copy-field", i18n.Str("field", f.name())), "", f.icon())
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
		if err := v.VerifyPassword(input); err != nil {
			return engine.ActionReload{State: m.password(vaultReprompt, bwErrorText(err))}
		}
		return m.copy(ctx, v, m.item, m.field)
	case vaultItems:
		i, ok := target.Row()
		if !ok || int(i) >= len(m.items) {
			return engine.ActionNothing{}
		}
		it := m.items[i]
		if _, alt := kind.(engine.ActivateAlt); alt {
			return engine.ActionReload{State: m.fieldList(it)}
		}
		fields := bwFields(it)
		if len(fields) == 0 {
			return engine.ActionNothing{}
		}
		return m.pick(ctx, v, it, fields[0])
	case vaultFields:
		i, ok := target.Row()
		if !ok || int(i) >= len(m.fields) {
			return engine.ActionNothing{}
		}
		return m.pick(ctx, v, m.item, m.fields[i])
	}
	return engine.ActionNothing{}
}

// pick copies f, asking for the master password first when the item
// is protected by the reprompt and f is a secret.
func (m *vaultMode) pick(ctx context.Context, v vault, it bitwarden.Item, f bwField) engine.Action {
	if it.Reprompt && f.secret() {
		m.item, m.field = it, f
		return engine.ActionReload{State: m.password(vaultReprompt, i18n.T("dropdown-bitwarden-reprompt-desc", i18n.Str("field", f.name()), i18n.Str("item", it.Name)))}
	}
	return m.copy(ctx, v, it, f)
}

// copy puts the field on the clipboard on the UI loop and closes; a
// failure stays open on the items, saying why.
func (m *vaultMode) copy(ctx context.Context, v vault, it bitwarden.Item, f bwField) engine.Action {
	done := make(chan error, 1)
	m.invoke(func() {
		_, err := m.host.copyField(v, it, f, m.now())
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
