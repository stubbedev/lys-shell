package search

import (
	"context"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/clipboard"
	"github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/charnames"
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
	return Spec{Section: SectionClipboard, MinQuery: 3, Weight: -16}
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
		// Preview folds the lines into one, so the title is what is searched.
		entries[i] = Entry{
			ID: "clipboard:" + strconv.FormatUint(e.ID, 10), Title: e.Preview(clipboardPreviewChars), Icon: clipboardIcon(e), Data: clipboardData{id: e.ID, kind: e.Kind(), paths: e.Paths(), pinned: e.Pinned},
		}
		if e.Pinned {
			entries[i].Hint = i18n.T("launcher-clipboard-pinned")
		}
		if c.previews {
			entries[i].Preview = modes.EntryPreview(c.history, e)
		}
	}
	return entries
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
	id     uint64
	kind   clipboard.Kind
	paths  []string
	pinned bool
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
	if c.history.CanPin() {
		actions = append(actions, c.pinAction(d))
	}
	return append(actions,
		Action{Title: i18n.T("launcher-action-forget"), Icon: "ld-trash-2-symbolic", Key: ActionKeyDelete, Run: func(context.Context) Outcome {
			c.history.Forget(d.id)
			return Reload{}
		}},
		Action{Title: i18n.T("launcher-action-clear-unpinned"), Icon: "ld-trash-2-symbolic", Run: func(context.Context) Outcome {
			c.history.ClearUnpinned()
			return Reload{}
		}},
	)
}

// pinAction pins the entry, or unpins a pinned one; a refused pin (the
// pinned budget is full) is said in the footer.
func (c *Clipboard) pinAction(d clipboardData) Action {
	if d.pinned {
		return Action{Title: i18n.T("launcher-action-unpin"), Icon: "ld-x-symbolic", Run: func(context.Context) Outcome {
			if err := c.history.Unpin(d.id); err != nil {
				return Stay{Message: err.Error()}
			}
			return Reload{}
		}}
	}
	return Action{Title: i18n.T("launcher-action-pin"), Icon: "ld-bookmark-symbolic", Run: func(context.Context) Outcome {
		if err := c.history.Pin(d.id); err != nil {
			return Stay{Message: err.Error()}
		}
		return Reload{}
	}}
}

// Emoji offers the emoji table.
type Emoji struct {
	load    func() []modes.Emoji
	tone    modes.SkinTone
	setTone func(modes.SkinTone)
	flat    bool
}

// NewEmoji offers load's emoji (modes.AvailableEmoji) in skin tone tone.
// Typing a tone picked from an emoji's actions hands it to setTone, which
// keeps it as the new default. Load decompresses a table, so it runs only
// on the provider's goroutine.
func NewEmoji(load func() []modes.Emoji, tone modes.SkinTone, setTone func(modes.SkinTone)) *Emoji {
	return &Emoji{load: load, tone: tone, setTone: setTone}
}

// Spec implements Provider.
func (e *Emoji) Spec() Spec {
	if e.flat {
		return Spec{Section: SectionNone}
	}
	return Spec{Section: SectionEmoji, MinQuery: 2, Weight: -24}
}

// Load implements Provider.
func (e *Emoji) Load(_ context.Context, publish func([]Entry)) {
	table := e.load()
	entries := make([]Entry, len(table))
	for i, em := range table {
		id := "emoji:" + em.Glyph
		entries[i] = Entry{ID: id, Learn: id, Title: em.Name, Keywords: strings.Join(em.Keywords, " "), Glyph: em.WithTone(e.tone), Data: em}
	}
	publish(entries)
}

var skinTones = [...]struct {
	name config.LauncherSkinTone
	key  string
}{
	modes.ToneNone:        {config.SkinToneNone, ""},
	modes.ToneLight:       {config.SkinToneLight, "launcher-skin-tone-light"},
	modes.ToneMediumLight: {config.SkinToneMediumLight, "launcher-skin-tone-medium-light"},
	modes.ToneMedium:      {config.SkinToneMedium, "launcher-skin-tone-medium"},
	modes.ToneMediumDark:  {config.SkinToneMediumDark, "launcher-skin-tone-medium-dark"},
	modes.ToneDark:        {config.SkinToneDark, "launcher-skin-tone-dark"},
}

// SkinToneOf is the config's skin tone as emoji take it.
func SkinToneOf(name config.LauncherSkinTone) modes.SkinTone {
	for tone, t := range skinTones {
		if t.name == name {
			return modes.SkinTone(tone)
		}
	}
	panic("launcher: skin tone " + string(name) + " has no emoji modifier")
}

// SkinToneName is tone as the config spells it.
func SkinToneName(tone modes.SkinTone) config.LauncherSkinTone { return skinTones[tone].name }

// Actions implements Provider: type and copy in the current tone, then,
// for an emoji that takes one, each of the five tones.
func (e *Emoji) Actions(entry Entry) []Action {
	em, ok := entry.Data.(modes.Emoji)
	if !ok {
		return nil
	}
	glyph := em.WithTone(e.tone)
	actions := []Action{
		{Title: i18n.T("launcher-action-type"), Icon: "ld-keyboard-symbolic", Run: func(context.Context) Outcome { return Type{Text: glyph} }},
		copyAction(i18n.T("launcher-action-copy"), glyph),
		copyAction(i18n.T("launcher-action-copy-name"), em.Name),
	}
	if !em.Toneable() {
		return actions
	}
	for _, tone := range modes.SkinTones {
		variant := em.WithTone(tone)
		actions = append(actions, Action{
			Title: i18n.T("launcher-action-type-skin-tone", i18n.Str("emoji", variant), i18n.Str("tone", i18n.T(skinTones[tone].key))),
			Icon:  "ld-smile-symbolic",
			Run: func(context.Context) Outcome {
				if e.setTone != nil {
					e.setTone(tone)
				}
				return Type{Text: variant}
			},
		})
	}
	return actions
}

// EmojiLevel lists every emoji, as the Emoji Picker row opens it.
func EmojiLevel(load func() []modes.Emoji, tone modes.SkinTone, setTone func(modes.SkinTone)) *Level {
	return &Level{Title: i18n.T("launcher-picker-emoji"), Placeholder: i18n.T("launcher-placeholder-filter"), Providers: []Provider{&Emoji{load: load, tone: tone, setTone: setTone, flat: true}}}
}

// CharactersLevel searches every named character, as the Unicode
// Characters row and `-show unicode` open it.
func CharactersLevel(table func() (*charnames.Table, error)) *Level {
	return &Level{Title: i18n.T("launcher-picker-characters"), Placeholder: i18n.T("launcher-placeholder-characters"), Providers: []Provider{&Characters{table: table, flat: true}}}
}

// Pickers are root rows that open a whole list of their own: the full
// clipboard history, every emoji, the vault, every bookmark, the most
// visited pages, the running processes.
type Pickers struct {
	items []picker
}

type picker struct {
	id, title, keywords, icon string
	level                     func() *Level
}

// PickerLevels are the levels of the pickers with nothing to configure;
// a nil one leaves its row out.
type PickerLevels struct {
	Emoji, Characters, Vault, Bookmarks, Visited, Processes func() *Level
}

// NewPickers builds the rows for what is available. A nil history leaves
// its row out, as does a nil level.
func NewPickers(history modes.ClipboardHistory, levels PickerLevels, previews bool) *Pickers {
	p := &Pickers{}
	if history != nil {
		title := i18n.T("launcher-picker-clipboard")
		p.items = append(p.items, picker{"clipboard", title, "cliphist paste history", "ld-clipboard-symbolic", func() *Level {
			return &Level{Title: title, Placeholder: i18n.T("launcher-placeholder-filter"), Providers: []Provider{&Clipboard{history: history, flat: true, previews: previews}}}
		}})
	}
	if levels.Emoji != nil {
		p.items = append(p.items, picker{"emoji", i18n.T("launcher-picker-emoji"), "emoji smiley picker symbols", "ld-smile-symbolic", levels.Emoji})
	}
	if levels.Characters != nil {
		p.items = append(p.items, picker{"characters", i18n.T("launcher-picker-characters"), "unicode character map symbols arrows maths glyphs", "ld-a-large-small-symbolic", levels.Characters})
	}
	if levels.Vault != nil {
		p.items = append(p.items, picker{"vault", i18n.T("launcher-picker-vault"), "bitwarden bw password vault secrets", "ld-shield-check-symbolic", levels.Vault})
	}
	if levels.Bookmarks != nil {
		p.items = append(p.items, picker{"bookmarks", i18n.T("launcher-picker-bookmarks"), "browser firefox chrome favorites", "ld-bookmark-symbolic", levels.Bookmarks})
	}
	if levels.Visited != nil {
		p.items = append(p.items, picker{"visited", i18n.T("launcher-picker-visited"), "browser firefox chrome history frequent top sites", "ld-history-symbolic", levels.Visited})
	}
	if levels.Processes != nil {
		p.items = append(p.items, picker{"processes", i18n.T("launcher-picker-processes"), "end task terminate force quit stop", "ld-activity-symbolic", levels.Processes})
	}
	return p
}

// Spec implements Provider.
func (*Pickers) Spec() Spec { return Spec{Section: SectionCommands, MinQuery: 2} }

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
