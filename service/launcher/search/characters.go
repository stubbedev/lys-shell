package search

import (
	"context"
	"fmt"
	"log"

	"github.com/stubbedev/lys-desktop/i18n"

	"github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/charnames"
)

var iconInvisible launcher.Icon = launcher.IconName("ld-hash-symbolic")

// Characters finds any character Unicode names. Its forty thousand names
// are never published as rows: it searches charnames' compact table itself
// and builds rows only for what shows.
type Characters struct {
	table    func() (*charnames.Table, error)
	flat     bool
	loaded   *charnames.Table
	searcher *charnames.Searcher
	failed   bool
	name     []byte
}

// NewCharacters searches table's names (charnames.Shared), read on the
// first search.
func NewCharacters(table func() (*charnames.Table, error)) *Characters {
	return &Characters{table: table}
}

// Spec implements Provider.
func (c *Characters) Spec() Spec {
	if c.flat {
		return Spec{Section: SectionNone}
	}
	return Spec{Section: SectionCharacters, MinQuery: 3, Weight: -24}
}

// Load implements Provider: the table is read on the first search, not
// when the panel opens.
func (*Characters) Load(context.Context, func([]Entry)) {}

// Search implements Searcher.
func (c *Characters) Search(q *launcher.FuzzyQuery, query string, limit int) []Found {
	if c.searcher == nil {
		if c.failed {
			return nil
		}
		t, err := c.table()
		if err != nil {
			log.Printf("launcher: %v", err)
			c.failed = true
			return nil
		}
		c.loaded, c.searcher = t, t.NewSearcher()
	}
	chars := c.searcher.Search(q, query, limit)
	if len(chars) == 0 {
		return nil
	}
	found := make([]Found, len(chars))
	for i, ch := range chars {
		c.name = c.loaded.AppendName(c.name[:0], ch)
		title := string(c.name)
		e := Entry{
			ID: "char:" + codePoint(ch.Rune), Title: title, Subtitle: codePoint(ch.Rune),
			Glyph: ch.Glyph(), Data: character{r: ch.Rune, name: title},
		}
		if e.Glyph == "" {
			e.Icon = iconInvisible
		}
		found[i] = Found{Entry: e, Score: ch.Score, Title: launcher.Haystack(title)}
	}
	return found
}

func codePoint(r rune) string { return fmt.Sprintf("U+%04X", r) }

type character struct {
	r    rune
	name string
}

// Actions implements Provider.
func (*Characters) Actions(e Entry) []Action {
	ch, ok := e.Data.(character)
	if !ok {
		return nil
	}
	text := string(ch.r)
	return []Action{
		{Title: i18n.T("launcher-action-type"), Icon: "ld-keyboard-symbolic", Run: func(context.Context) Outcome { return Type{Text: text} }},
		copyAction(i18n.T("launcher-action-copy"), text),
		copyAction(i18n.T("launcher-action-copy-code-point"), codePoint(ch.r)),
		copyAction(i18n.T("launcher-action-copy-html-entity"), fmt.Sprintf("&#x%X;", ch.r)),
		copyAction(i18n.T("launcher-action-copy-name"), ch.name),
	}
}
