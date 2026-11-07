package search

import (
	"context"

	"github.com/stubbedev/lys-shell/internal/pango"
	"github.com/stubbedev/lys-shell/service/launcher"
)

// ModeLevel hosts a rofi mode as a level, so what already works as a
// mode (the vault, with its unlock prompt and field picker) keeps one
// implementation. Its reloads swap the level; its masked prompts mask
// the field.
func ModeLevel(mode launcher.Mode, title string) *Level {
	ctx := context.Background()
	return modeStateLevel(mode, mode.Load(ctx), title)
}

func modeStateLevel(mode launcher.Mode, st launcher.ModeState, title string) *Level {
	level := &Level{Title: title, Mask: st.Password}
	if st.Message != nil {
		level.Message = *st.Message
	}
	level.Providers = []Provider{&modeProvider{mode: mode, title: title, items: st.Items, markup: st.MarkupRows}}
	if launcher.AllowsCustom(mode) && !st.NoCustom {
		level.Submit = func(ctx context.Context, text string) Outcome {
			return modeOutcome(mode, title, mode.Activate(ctx, launcher.NoRow, launcher.ActivateCustom{Text: text}, text))
		}
	}
	return level
}

// modeOutcome maps a mode's answer onto the panel's.
func modeOutcome(mode launcher.Mode, title string, a launcher.Action) Outcome {
	switch v := a.(type) {
	case launcher.ActionClose, launcher.ActionExit:
		return Close{}
	case launcher.ActionCopy:
		return Copy{Text: v.Text}
	case launcher.ActionType:
		return Type{Text: v.Text}
	case launcher.ActionSetInput:
		return SetQuery{Text: v.Text}
	case launcher.ActionReload:
		return Replace{Level: modeStateLevel(mode, v.State, title)}
	}
	return Stay{}
}

// modeProvider offers a mode state's rows.
type modeProvider struct {
	mode   launcher.Mode
	title  string
	items  []launcher.Item
	markup bool
}

func (*modeProvider) Spec() Spec { return Spec{Section: SectionNone} }

func (*modeProvider) Immediate() {}

func (p *modeProvider) Load(_ context.Context, publish func([]Entry)) {
	entries := make([]Entry, len(p.items))
	for i, it := range p.items {
		title := it.Display
		if p.markup || it.Flags.Has(launcher.FlagMarkup) {
			if plain, ok := pango.PlainText(it.Display); ok {
				title = plain
			}
		}
		entries[i] = Entry{ID: it.MatchText + "\x00" + title, Title: title, Keywords: it.MatchText, Icon: it.Icon, Data: uint32(i)}
		if previewer, ok := p.mode.(launcher.Previewer); ok {
			entries[i].Preview, _ = previewer.RowPreview(uint32(i))
		}
	}
	publish(entries)
}

func (p *modeProvider) Actions(e Entry) []Action {
	i, ok := e.Data.(uint32)
	if !ok {
		return nil
	}
	return []Action{
		{Title: e.Title, Icon: "ld-copy-symbolic", Run: func(ctx context.Context) Outcome {
			return modeOutcome(p.mode, p.title, p.mode.Activate(ctx, launcher.RowTarget(i), launcher.ActivateDefault{}, ""))
		}},
		{Title: p.title, Icon: "ld-list-symbolic", Run: func(ctx context.Context) Outcome {
			return modeOutcome(p.mode, p.title, p.mode.Activate(ctx, launcher.RowTarget(i), launcher.ActivateAlt{}, ""))
		}},
	}
}
