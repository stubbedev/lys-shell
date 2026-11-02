package search

import (
	"context"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/clipboard"
	"github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/modes"
)

// clipboardPreviewChars is how much of a text entry a row searches and
// its pane shows.
const clipboardPreviewChars = 4096

// Clipboard offers the clipboard history. Accepting a row puts it back
// under the mime it arrived with.
type Clipboard struct {
	history  modes.ClipboardHistory
	flat     bool
	previews bool
}

// NewClipboard builds the root's section of history; nil (a compositor
// without data-control) offers nothing.
func NewClipboard(history modes.ClipboardHistory, previews bool) *Clipboard {
	return &Clipboard{history: history, previews: previews}
}

// Spec implements Provider.
func (c *Clipboard) Spec() Spec {
	if c.flat {
		return Spec{Section: SectionNone}
	}
	return Spec{Section: SectionClipboard, Limit: 3, MinQuery: 3, Weight: -16}
}

// Load implements Provider.
func (c *Clipboard) Load(_ context.Context, publish func([]Entry)) {
	if c.history == nil {
		return
	}
	publish(c.read())
}

func (c *Clipboard) read() []Entry {
	snap := c.history.Entries()
	defer snap.Release()
	all := snap.Entries()
	entries := make([]Entry, len(all))
	for i, e := range all {
		preview := e.Preview(clipboardPreviewChars)
		title, _, _ := strings.Cut(preview, "\n")
		entries[i] = Entry{
			ID: "clipboard:" + strconv.FormatUint(e.ID, 10), Title: title, Keywords: preview, Icon: clipboardIcon(e), Data: clipboardData{id: e.ID, kind: e.Kind(), paths: e.Paths()},
		}
		if c.previews {
			entries[i].Preview = c.preview(e, preview)
		}
	}
	return entries
}

func (c *Clipboard) preview(e clipboard.Entry, text string) Preview {
	switch e.Kind() {
	case clipboard.KindImage:
		id := e.ID
		return Preview{Load: func() ([]byte, error) { return c.imageBytes(id) }}
	case clipboard.KindFiles:
		if paths := e.Paths(); len(paths) == 1 {
			return Preview{Path: paths[0]}
		}
	}
	return Preview{Text: text}
}

// imageBytes copies one entry's bytes out while a snapshot holds them.
func (c *Clipboard) imageBytes(id uint64) ([]byte, error) {
	snap := c.history.Entries()
	defer snap.Release()
	for _, e := range snap.Entries() {
		if e.ID == id {
			return e.Bytes(), nil
		}
	}
	return nil, clipboard.ErrEntryGone
}

func clipboardIcon(e clipboard.Entry) launcher.Icon {
	switch e.Kind() {
	case clipboard.KindText:
		return launcher.IconName("ld-file-text-symbolic")
	case clipboard.KindFiles:
		return launcher.IconName("ld-folder-symbolic")
	case clipboard.KindImage:
		return launcher.IconName("ld-image-symbolic")
	}
	return launcher.IconName("ld-layers-symbolic")
}

type clipboardData struct {
	id    uint64
	kind  clipboard.Kind
	paths []string
}

// Actions implements Provider.
func (c *Clipboard) Actions(e Entry) []Action {
	d, ok := e.Data.(clipboardData)
	if !ok || c.history == nil {
		return nil
	}
	actions := []Action{{Title: i18n.T("launcher-action-copy"), Icon: "ld-copy-symbolic", Run: func(context.Context) Outcome {
		if !c.history.Copy(d.id) {
			return Stay{Message: i18n.T("launcher-clipboard-gone")}
		}
		return Close{}
	}}}
	if len(d.paths) > 0 {
		actions = append(actions, copyAction(i18n.T("launcher-action-copy-path"), strings.Join(d.paths, "\n")))
	}
	return append(actions,
		Action{Title: i18n.T("launcher-action-forget"), Icon: "ld-trash-2-symbolic", Run: func(context.Context) Outcome {
			c.history.Forget(d.id)
			return Reload{}
		}},
		Action{Title: i18n.T("launcher-action-clear-history"), Icon: "ld-trash-2-symbolic", Run: func(context.Context) Outcome {
			c.history.Clear()
			return Reload{}
		}},
	)
}

// Emoji offers the emoji table.
type Emoji struct {
	load func() []modes.Emoji
	flat bool
}

// NewEmoji offers load's emoji (modes.AvailableEmoji). It decompresses a
// table, so it runs only on the provider's goroutine.
func NewEmoji(load func() []modes.Emoji) *Emoji { return &Emoji{load: load} }

// Spec implements Provider.
func (e *Emoji) Spec() Spec {
	if e.flat {
		return Spec{Section: SectionNone}
	}
	return Spec{Section: SectionEmoji, Limit: 4, MinQuery: 2, Weight: -24}
}

// Load implements Provider.
func (e *Emoji) Load(_ context.Context, publish func([]Entry)) {
	table := e.load()
	entries := make([]Entry, len(table))
	for i, em := range table {
		id := "emoji:" + em.Glyph
		entries[i] = Entry{ID: id, Learn: id, Title: em.Name, Keywords: strings.Join(em.Keywords, " "), Glyph: em.Glyph, Data: em}
	}
	publish(entries)
}

// Actions implements Provider.
func (*Emoji) Actions(e Entry) []Action {
	em, ok := e.Data.(modes.Emoji)
	if !ok {
		return nil
	}
	return []Action{
		copyAction(i18n.T("launcher-action-copy"), em.Glyph),
		copyAction(i18n.T("launcher-action-copy-name"), em.Name),
	}
}

// Pickers are root rows that open a whole list of their own: the full
// clipboard history, every emoji, the vault.
type Pickers struct {
	items []picker
}

type picker struct {
	id, title, keywords, icon string
	level                     func() *Level
}

// NewPickers builds the rows for what is available. A nil history or
// vault leaves its row out.
func NewPickers(history modes.ClipboardHistory, emoji func() []modes.Emoji, vault func() *Level, previews bool) *Pickers {
	p := &Pickers{}
	if history != nil {
		title := i18n.T("launcher-picker-clipboard")
		p.items = append(p.items, picker{"clipboard", title, "cliphist paste history", "ld-clipboard-symbolic", func() *Level {
			return &Level{Title: title, Placeholder: i18n.T("launcher-placeholder-filter"), Providers: []Provider{&Clipboard{history: history, flat: true, previews: previews}}}
		}})
	}
	if emoji != nil {
		title := i18n.T("launcher-picker-emoji")
		p.items = append(p.items, picker{"emoji", title, "emoji smiley picker symbols", "ld-smile-symbolic", func() *Level {
			return &Level{Title: title, Placeholder: i18n.T("launcher-placeholder-filter"), Providers: []Provider{&Emoji{load: emoji, flat: true}}}
		}})
	}
	if vault != nil {
		p.items = append(p.items, picker{"vault", i18n.T("launcher-picker-vault"), "bitwarden bw password vault secrets", "ld-shield-check-symbolic", vault})
	}
	return p
}

// Spec implements Provider.
func (*Pickers) Spec() Spec { return Spec{Section: SectionCommands, MinQuery: 2, Limit: 5} }

// Load implements Provider.
func (p *Pickers) Load(_ context.Context, publish func([]Entry)) {
	entries := make([]Entry, len(p.items))
	for i, it := range p.items {
		id := "picker:" + it.id
		entries[i] = Entry{ID: id, Learn: id, Title: it.title, Keywords: it.keywords, Icon: launcher.IconName(it.icon), Data: it}
	}
	publish(entries)
}

// Actions implements Provider.
func (*Pickers) Actions(e Entry) []Action {
	it, ok := e.Data.(picker)
	if !ok {
		return nil
	}
	return []Action{{Title: i18n.T("launcher-action-open"), Icon: it.icon, Run: func(context.Context) Outcome {
		return Push{Level: it.level()}
	}}}
}
