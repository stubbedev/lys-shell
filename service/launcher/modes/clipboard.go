package modes

import (
	"context"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/clipboard"
	"github.com/stubbedev/lys-shell/service/launcher"
)

// ClipboardHistory is what the clipboard mode needs from the session's
// clipboard service; *clipboard.Clipboard implements it.
type ClipboardHistory interface {
	Entries() *clipboard.Snapshot
	Forget(id uint64) bool
	ClearUnpinned()
	Copy(id uint64) bool
	CanPin() bool
	Pin(id uint64) error
	Unpin(id uint64) error
}

// Row icons, one per clipboard.Kind (clipboard.rs).
const (
	iconText  = "ld-file-text-symbolic"
	iconFiles = "ld-folder-symbolic"
	iconImage = "ld-image-symbolic"
	iconOther = "ld-layers-symbolic"
)

// previewChars is how much of an entry a row shows.
const previewChars = 160

// kb-custom-1 forgets the selected entry, kb-custom-2 every entry that
// is not pinned: a clipboard history is exactly where a password ends up
// when a password manager forgets to mark it. kb-custom-3 pins or unpins
// the selected entry.
const (
	forgetOne uint8 = 1
	forgetAll uint8 = 2
	togglePin uint8 = 3
)

// Clipboard lists the session's clipboard history (clipboard.rs): the
// cliphist | rofi -dmenu | wl-copy pipeline without a second daemon.
// Accepting a row puts it back under the mime it arrived with, so a
// copied file goes back as a file and an image as an image.
type Clipboard struct {
	history ClipboardHistory
	// ids are the entry ids in row order, so a row index resolves to
	// the entry it was drawn for even after something new arrives;
	// pinned says which of them are pinned.
	ids    []uint64
	pinned []bool
}

// NewClipboard builds the mode; a nil history is a compositor without
// data-control, which the mode says rather than looking empty.
func NewClipboard(history ClipboardHistory) *Clipboard { return &Clipboard{history: history} }

// Name is "clipboard".
func (*Clipboard) Name() string { return "clipboard" }

func (c *Clipboard) state() launcher.ModeState {
	if c.history == nil {
		c.ids, c.pinned = nil, nil
		msg := "No clipboard history: the compositor does not support wlr-data-control."
		return launcher.ModeState{Prompt: "clipboard", Message: &msg, NoCustom: true, UseHotKeys: true}
	}
	snap := c.history.Entries()
	defer snap.Release()
	entries := snap.Entries()
	c.ids = make([]uint64, len(entries))
	c.pinned = make([]bool, len(entries))
	items := make([]launcher.Item, len(entries))
	for i, e := range entries {
		c.ids[i] = e.ID
		preview := e.Preview(previewChars)
		info := strconv.FormatUint(e.ID, 10)
		items[i] = launcher.Item{Display: preview, MatchText: preview, Info: &info, Icon: iconFor(e)}
		if e.Pinned {
			items[i].Flags = launcher.FlagActive
		}
		c.pinned[i] = e.Pinned
	}
	// Typed text matching nothing must not land on the clipboard: this
	// mode restores, it does not compose.
	return launcher.ModeState{Items: items, Prompt: "clipboard", NoCustom: true, UseHotKeys: true}
}

// idAt is the entry id a row was drawn for.
func (c *Clipboard) idAt(target launcher.Target) (uint64, bool) {
	i, ok := target.Row()
	if !ok || int(i) >= len(c.ids) {
		return 0, false
	}
	return c.ids[i], true
}

// Load lists the history, most recent first.
func (c *Clipboard) Load(context.Context) launcher.ModeState { return c.state() }

// Activate restores the entry; kb-custom-1/2 forget one or every
// unpinned one, kb-custom-3 pins or unpins it.
func (c *Clipboard) Activate(_ context.Context, target launcher.Target, kind launcher.ActivateKind, _ string) launcher.Action {
	id, ok := c.idAt(target)
	if c.history == nil || !ok {
		return launcher.ActionNothing{}
	}
	if k, ok := kind.(launcher.ActivateKbCustom); ok {
		switch k.N {
		case forgetOne:
			c.history.Forget(id)
			return launcher.ActionReload{State: c.state()}
		case forgetAll:
			c.history.ClearUnpinned()
			return launcher.ActionReload{State: c.state()}
		case togglePin:
			if err := c.togglePin(target, id); err != nil {
				msg := err.Error()
				state := c.state()
				state.Message = &msg
				return launcher.ActionReload{State: state}
			}
			return launcher.ActionReload{State: c.state()}
		}
	}
	// Every other accept restores, the alternate one included: there
	// is no second thing to do with a clipboard entry.
	c.history.Copy(id)
	return launcher.ActionClose{}
}

// AllowsCustom is false.
func (*Clipboard) AllowsCustom() bool { return false }

// iconFor is a row's icon: one copied file is pictured by a thumbnail
// of itself; everything else, a multi-file copy included, gets a name.
func iconFor(e clipboard.Entry) launcher.Icon {
	var name string
	switch e.Kind() {
	case clipboard.KindText:
		name = iconText
	case clipboard.KindFiles:
		name = iconFiles
	case clipboard.KindImage:
		name = iconImage
	default:
		name = iconOther
	}
	if e.Kind() == clipboard.KindFiles {
		if paths := e.Paths(); len(paths) == 1 {
			return launcher.IconThumbnail{Path: paths[0], Fallback: name}
		}
	}
	return launcher.IconName(name)
}

// RowPreview is the row's entry in full, read from the history only once
// the row is selected; false once the entry has left the history.
func (c *Clipboard) RowPreview(index uint32) (launcher.Preview, bool) {
	id, ok := c.idAt(launcher.RowTarget(index))
	if c.history == nil || !ok {
		return launcher.Preview{}, false
	}
	snap := c.history.Entries()
	defer snap.Release()
	for _, e := range snap.Entries() {
		if e.ID == id {
			return EntryPreview(c.history, e), true
		}
	}
	return launcher.Preview{}, false
}

// EntryPreview is a clipboard entry's side pane: text whole, an image
// decoded with its type and size, one copied file as that file, several
// as their paths, and anything else its type and size. Bytes are copied
// out of the history only when the row is selected.
func EntryPreview(history ClipboardHistory, e clipboard.Entry) launcher.Preview {
	id := e.ID
	load := func() ([]byte, error) { return entryBytes(history, id) }
	facts := func() []launcher.PreviewFact {
		return []launcher.PreviewFact{
			{Label: i18n.T("launcher-preview-type"), Value: e.Mime},
			{Label: i18n.T("launcher-preview-size"), Value: clipboard.HumanSize(e.Size())},
		}
	}
	switch e.Kind() {
	case clipboard.KindText:
		return launcher.Preview{Load: load, LoadsText: true}
	case clipboard.KindImage:
		return launcher.Preview{Load: load, Facts: facts()}
	case clipboard.KindFiles:
		paths := e.Paths()
		if len(paths) == 1 {
			return launcher.Preview{Path: paths[0]}
		}
		return launcher.Preview{Text: strings.Join(paths, "\n")}
	}
	return launcher.Preview{Facts: facts()}
}

// entryBytes copies one entry's bytes out while a snapshot holds them.
func entryBytes(history ClipboardHistory, id uint64) ([]byte, error) {
	snap := history.Entries()
	defer snap.Release()
	for _, e := range snap.Entries() {
		if e.ID == id {
			return e.Bytes(), nil
		}
	}
	return nil, clipboard.ErrEntryGone
}

// togglePin pins the row's entry, or unpins a pinned one.
func (c *Clipboard) togglePin(target launcher.Target, id uint64) error {
	i, _ := target.Row()
	if c.pinned[i] {
		return c.history.Unpin(id)
	}
	return c.history.Pin(id)
}
