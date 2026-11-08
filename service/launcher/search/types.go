// Package search is the launcher's search: one ranked list across
// providers (apps, windows, files, answers, ...), frecency that learns,
// levels you enter and leave, and the actions of a selected row. Pure
// logic and effects behind small interfaces, no widgets; the panel that
// shows it is shell/launcher's. Everything here runs on the session's
// engine goroutine or a provider's own, never on the UI loop.
package search

import (
	"context"
	"sync"

	"github.com/stubbedev/lys-desktop/i18n"

	"github.com/stubbedev/lys-shell/service/launcher"
)

// Section is the heading a row sits under.
type Section uint8

// The sections. Their order breaks ties between sections of equal rank.
const (
	SectionAnswer Section = iota
	// SectionRecent is what an empty query shows: what gets used.
	SectionRecent
	SectionApps
	SectionWindows
	SectionCommands
	SectionLinks
	SectionBrowser
	SectionSettings
	SectionFiles
	SectionClipboard
	SectionEmoji
	SectionCharacters
	SectionVault
	SectionWeb
	// SectionNone is a flat list: an actions panel or a picker.
	SectionNone
	sectionCount
)

var sectionKeys = [sectionCount]string{
	SectionAnswer: "launcher-section-answer", SectionRecent: "launcher-section-recent", SectionApps: "launcher-section-apps",
	SectionWindows: "launcher-section-windows", SectionCommands: "launcher-section-commands",
	SectionLinks: "launcher-section-links", SectionBrowser: "launcher-section-browser", SectionSettings: "launcher-section-settings",
	SectionFiles: "launcher-section-files", SectionClipboard: "launcher-section-clipboard",
	SectionEmoji: "launcher-section-emoji", SectionCharacters: "launcher-section-characters", SectionVault: "launcher-section-vault",
	SectionWeb: "launcher-section-web",
}

// sectionCaps bound what a section shows, whichever providers fill it; a
// section without one is bound only by flatRowCap.
var sectionCaps = [sectionCount]int{
	SectionApps: 6, SectionWindows: 5, SectionCommands: 5, SectionLinks: 6, SectionBrowser: 6,
	SectionSettings: 4, SectionFiles: 6, SectionClipboard: 3, SectionEmoji: 4, SectionCharacters: 4,
}

// rowCap is how many rows the section shows.
func (s Section) rowCap() int {
	if c := sectionCaps[s]; c > 0 {
		return c
	}
	return flatRowCap
}

// Title is the section's heading, "" for a flat list.
func (s Section) Title() string {
	if s == SectionNone {
		return ""
	}
	sectionTitlesOnce.Do(func() {
		for i, key := range sectionKeys {
			if key != "" {
				sectionTitles[i] = i18n.T(key)
			}
		}
	})
	return sectionTitles[s]
}

// The headings, read from the locale once: every keystroke shows them.
var (
	sectionTitlesOnce sync.Once
	sectionTitles     [sectionCount]string
)

// Entry is one row a provider offers.
type Entry struct {
	// ID identifies the row across queries, so the selection can follow
	// it. Unique within its provider.
	ID string
	// Learn is the frecency key; "" never learns. Rows that are the same
	// thing to the user share one (every window of an application).
	Learn string
	// Title is the row's main text, matched and highlighted.
	Title string
	// Subtitle is the dim text beside it, matched.
	Subtitle string
	// Keywords are matched and never shown.
	Keywords string
	// Icon leads the row; Glyph leads it instead when set (an emoji).
	Icon  launcher.Icon
	Glyph string
	// Completion is what Tab puts in the search field ("" opens the
	// actions panel instead).
	Completion string
	// Hint is the dim text at the row's end, as an action's key.
	Hint string
	// Rank is added to the row's score: a recent file over one merely
	// indexed.
	Rank int
	// Preview is the row's side pane.
	Preview launcher.Preview
	// Data is the provider's own, for Actions.
	Data any
}

// Action is one thing to do with a row. Run happens on the engine
// goroutine: it may wait for the world, and the panel stays live.
type Action struct {
	// Title names it in the panel and the footer.
	Title string
	// Icon leads its row in the actions panel.
	Icon string
	// Key runs it straight from the list, without the actions panel.
	Key ActionKey
	// Run does it.
	Run func(ctx context.Context) Outcome
}

// ActionKey is a key that runs a row's action from the list itself.
type ActionKey uint8

const (
	// ActionKeyNone is an action reached through the actions panel only.
	ActionKeyNone ActionKey = iota
	// ActionKeyDelete is Ctrl+Backspace: remove what the row stands for.
	ActionKeyDelete
)

// Label is the key as the actions panel shows it, "" for none.
func (k ActionKey) Label() string {
	if k == ActionKeyDelete {
		return "Ctrl+Backspace"
	}
	return ""
}

// Outcome is what the panel does after an action ran.
type Outcome interface{ isOutcome() }

type (
	// Close ends the session: the action did its work.
	Close struct{}
	// Cancel ends the session with nothing done (Esc at the root).
	Cancel struct{}
	// CloseThen ends the session and runs Run on the UI loop once the panel
	// has left the screen.
	CloseThen struct{ Run func() }
	// Copy puts Text on the clipboard and ends the session.
	Copy struct{ Text string }
	// Type ends the session and enters Text in the window it leaves focused.
	Type struct{ Text string }
	// SetQuery replaces the search field's text.
	SetQuery struct{ Text string }
	// Push enters a level; Esc comes back.
	Push struct{ Level *Level }
	// Replace swaps the current level for another.
	Replace struct{ Level *Level }
	// Stay leaves everything as it is, Message shown when not empty (a
	// failure said in the footer rather than swallowed).
	Stay struct{ Message string }
	// Reload loads the current level's providers again.
	Reload struct{}
)

func (Close) isOutcome()     {}
func (CloseThen) isOutcome() {}
func (Cancel) isOutcome()    {}
func (Copy) isOutcome()      {}
func (Type) isOutcome()      {}
func (SetQuery) isOutcome()  {}
func (Push) isOutcome()      {}
func (Replace) isOutcome()   {}
func (Stay) isOutcome()      {}
func (Reload) isOutcome()    {}

// Spec says how a provider's rows take part in a list.
type Spec struct {
	Section Section
	// MinQuery is the query length, in characters, below which its rows
	// stay out: a one-letter query should not list every emoji.
	MinQuery int
	// Weight is added to its rows' score: a good app beats an equally
	// good match in a file name.
	Weight int
}

// Provider offers rows. Load runs once per level on its own goroutine:
// it reads the disk or a socket and calls publish with the rows it has,
// as often as it learns more, each call replacing the last; it returns
// when done or ctx ends. Actions lists a row's actions, the first the
// default; it must not wait for anything, as the panel calls it when the
// selection moves.
type Provider interface {
	Spec() Spec
	Load(ctx context.Context, publish func([]Entry))
	Actions(e Entry) []Action
}

// Immediate marks a provider whose rows are already in memory: its Load
// runs as the level opens, so the level's first frame has them and the
// panel does not change height a moment later.
type Immediate interface{ Immediate() }

// Answerer is a provider whose rows answer the query rather than being
// searched through (a calculation, a web search). Answer runs on every
// query on the engine goroutine, so it must be quick.
type Answerer interface {
	Answer(query string) []Entry
}

// Searcher is a provider with too many rows to publish (every character
// Unicode names): it matches the query against a compact table of its own,
// on the engine goroutine, and returns at most limit rows with their
// scores, best first, building only those. An empty query lists its first
// rows.
type Searcher interface {
	Search(q *launcher.FuzzyQuery, query string, limit int) []Found
}

// Found is a row a Searcher built, its score, and its title as the matcher
// reads it, for highlights.
type Found struct {
	Entry Entry
	Score int32
	Title []rune
}

// Level is one screen of the panel: the search field and what it
// searches. Esc goes back through levels, then closes.
type Level struct {
	// Title is the breadcrumb beside the field; "" at the root.
	Title string
	// Placeholder is the empty field's text.
	Placeholder string
	// Message is shown above the rows.
	Message string
	// Mask shows the field as dots: the input is a secret.
	Mask bool
	// Providers are searched.
	Providers []Provider
	// Submit is Enter with no row selected: the typed text is the
	// answer (a script's argument, a master password). Nil: nothing.
	Submit func(ctx context.Context, text string) Outcome
	// SubmitTitle names Submit in the footer.
	SubmitTitle string
	// Query is what the field starts with.
	Query string
}

// The icons rows of more than one provider share.
var (
	iconCalc     launcher.Icon = launcher.IconName("ld-calculator-symbolic")
	iconWeb      launcher.Icon = launcher.IconName("ld-globe-symbolic")
	iconTerminal launcher.Icon = launcher.IconName("ld-terminal-symbolic")
	iconFolder   launcher.Icon = launcher.IconName("ld-folder-symbolic")
)
