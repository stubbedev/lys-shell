package search

import (
	"context"
	"strings"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/launcher"
)

// Links offers the configured quick links and snippets. A link whose URL
// takes {query} completes to its keyword: "gh rust" answers with the
// filled URL.
type Links struct {
	env      Env
	links    []config.LauncherQuicklink
	snippets []config.LauncherSnippet
	previews bool
}

// NewLinks offers links and snippets.
func NewLinks(env Env, links []config.LauncherQuicklink, snippets []config.LauncherSnippet, previews bool) *Links {
	return &Links{env: env, links: links, snippets: snippets, previews: previews}
}

// Spec implements Provider.
func (*Links) Spec() Spec { return Spec{Section: SectionLinks, Weight: 8} }

func linkIcon(raw string) launcher.Icon {
	if raw == "" {
		return launcher.IconName("ld-link-symbolic")
	}
	return appIcon(raw, "ld-link-symbolic")
}

// Load implements Provider.
func (l *Links) Load(_ context.Context, publish func([]Entry)) {
	entries := make([]Entry, 0, len(l.links)+len(l.snippets))
	for _, q := range l.links {
		id := "quicklink:" + q.Name
		e := Entry{ID: id, Learn: id, Title: q.Name, Subtitle: q.URL, Keywords: q.Keyword, Icon: linkIcon(q.Icon), Data: q}
		if strings.Contains(q.URL, "{query}") && q.Keyword != "" {
			e.Completion = q.Keyword + " "
		}
		entries = append(entries, e)
	}
	for _, s := range l.snippets {
		id := "snippet:" + s.Name
		first, _, _ := strings.Cut(s.Text, "\n")
		e := Entry{ID: id, Learn: id, Title: s.Name, Subtitle: first, Keywords: s.Keyword, Icon: launcher.IconName("ld-file-text-symbolic"), Data: s}
		if l.previews {
			e.Preview = Preview{Text: s.Text}
		}
		entries = append(entries, e)
	}
	publish(entries)
}

// filledLink is a quick link with its query in.
type filledLink struct {
	name, url string
}

// Answer implements Answerer: "<keyword> <text>" fills that link.
func (l *Links) Answer(query string) []Entry {
	word, rest, ok := strings.Cut(strings.TrimLeft(query, " "), " ")
	rest = strings.TrimSpace(rest)
	if !ok || rest == "" {
		return nil
	}
	var out []Entry
	for _, q := range l.links {
		if q.Keyword != word {
			continue
		}
		if target, ok := fillTemplate(q.URL, rest); ok {
			out = append(out, Entry{
				ID: "quicklink-filled:" + q.Name, Learn: "quicklink:" + q.Name, Title: q.Name + ": " + rest, Subtitle: target,
				Icon: linkIcon(q.Icon), Data: filledLink{name: q.Name, url: target},
			})
		}
	}
	return out
}

// Actions implements Provider.
func (l *Links) Actions(e Entry) []Action {
	switch d := e.Data.(type) {
	case filledLink:
		return l.urlActions(d.url)
	case config.LauncherQuicklink:
		if !strings.Contains(d.URL, "{query}") {
			return l.urlActions(d.URL)
		}
		// Enter on a link that wants a query asks for one.
		return []Action{{Title: i18n.T("launcher-action-open"), Icon: "ld-external-link-symbolic", Run: func(context.Context) Outcome {
			if d.Keyword != "" {
				return SetQuery{Text: d.Keyword + " "}
			}
			return Push{Level: l.queryLevel(d)}
		}}}
	case config.LauncherSnippet:
		return []Action{copyAction(i18n.T("launcher-action-copy"), d.Text)}
	}
	return nil
}

func (l *Links) urlActions(target string) []Action {
	return []Action{
		{Title: i18n.T("launcher-action-open"), Icon: "ld-external-link-symbolic", Run: func(context.Context) Outcome {
			l.env.openURI(target)
			return Close{}
		}},
		copyAction(i18n.T("launcher-action-copy-url"), target),
	}
}

// queryLevel asks for a keyword-less link's query in the field.
func (l *Links) queryLevel(q config.LauncherQuicklink) *Level {
	return &Level{
		Title: q.Name, Placeholder: i18n.T("launcher-placeholder-query"), SubmitTitle: i18n.T("launcher-action-open"),
		Submit: func(_ context.Context, text string) Outcome {
			target, ok := fillTemplate(q.URL, strings.TrimSpace(text))
			if !ok {
				return Stay{}
			}
			l.env.openURI(target)
			return Close{}
		},
	}
}
