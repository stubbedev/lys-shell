package search

import (
	"context"
	"net/url"
	"strings"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/launcher/modes"
)

// Calculator answers arithmetic and unit conversions inline.
type Calculator struct{}

// Spec implements Provider.
func (Calculator) Spec() Spec { return Spec{Section: SectionAnswer} }

// Load implements Provider: it has nothing to search through.
func (Calculator) Load(context.Context, func([]Entry)) {}

// looksCalculable is what makes a query look like something to calculate: a
// lone number is not a question, and neither is a word that happens to
// parse as a constant ("e", "pi").
func looksCalculable(q string) bool {
	if strings.IndexFunc(q, func(r rune) bool { return r >= '0' && r <= '9' }) < 0 {
		return strings.Contains(q, "(") || strings.HasPrefix(q, "pi ") || strings.HasPrefix(q, "e ")
	}
	return strings.ContainsAny(q, "+-*/%^(") || strings.ContainsFunc(q, func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
	})
}

// Answer implements Answerer.
func (Calculator) Answer(query string) []Entry {
	q := strings.TrimSpace(query)
	if !looksCalculable(q) {
		return nil
	}
	if c, ok := modes.Convert(q); ok {
		return []Entry{{ID: "calc", Title: c.String(), Subtitle: q, Icon: iconCalc, Data: calcData{value: c.Value, shown: c.String(), expr: q}}}
	}
	v, ok := modes.Eval(q)
	if !ok {
		return nil
	}
	s := modes.FormatResult(v)
	return []Entry{{ID: "calc", Title: s, Subtitle: q, Icon: iconCalc, Data: calcData{value: s, shown: s, expr: q}}}
}

type calcData struct{ value, shown, expr string }

// Actions implements Provider.
func (Calculator) Actions(e Entry) []Action {
	d, ok := e.Data.(calcData)
	if !ok {
		return nil
	}
	actions := []Action{copyAction(i18n.T("launcher-action-copy-result"), d.shown)}
	if d.value != d.shown {
		actions = append(actions, copyAction(i18n.T("launcher-action-copy-number"), d.value))
	}
	return append(actions,
		copyAction(i18n.T("launcher-action-copy-equation"), d.expr+" = "+d.shown),
		Action{Title: i18n.T("launcher-action-continue"), Icon: "ld-corner-down-left-symbolic", Run: func(context.Context) Outcome {
			return SetQuery{Text: d.value}
		}},
	)
}

// Web is the fallback at the bottom of every list: search the web for
// what was typed. The template's {query} takes the escaped text.
type Web struct {
	env      Env
	name     string
	template string
}

// NewWeb searches with name's template.
func NewWeb(env Env, name, template string) *Web {
	return &Web{env: env, name: name, template: template}
}

// Spec implements Provider.
func (*Web) Spec() Spec { return Spec{Section: SectionWeb} }

// Load implements Provider.
func (*Web) Load(context.Context, func([]Entry)) {}

// fillTemplate puts the escaped query in a URL template. A template
// with no {query} has nowhere to put it, which is a config mistake to
// show (the row stays out), not a silent search for nothing.
func fillTemplate(template, query string) (string, bool) {
	if !strings.Contains(template, "{query}") {
		return "", false
	}
	return strings.ReplaceAll(template, "{query}", url.QueryEscape(query)), true
}

// Answer implements Answerer.
func (w *Web) Answer(query string) []Entry {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	target, ok := fillTemplate(w.template, q)
	if !ok {
		return nil
	}
	return []Entry{{
		ID: "web", Title: i18n.T("launcher-web-search", i18n.Str("engine", w.name), i18n.Str("query", q)),
		Icon: iconWeb, Data: target,
	}}
}

// Actions implements Provider.
func (w *Web) Actions(e Entry) []Action {
	target, ok := e.Data.(string)
	if !ok {
		return nil
	}
	return []Action{
		{Title: i18n.T("launcher-action-open"), Icon: "ld-external-link-symbolic", Run: func(context.Context) Outcome {
			w.env.openURI(target)
			return Close{}
		}},
		copyAction(i18n.T("launcher-action-copy-url"), target),
	}
}

// Shell answers a query led by ">": run the rest as a command.
type Shell struct{ env Env }

// NewShell builds it.
func NewShell(env Env) *Shell { return &Shell{env: env} }

// Spec implements Provider.
func (*Shell) Spec() Spec { return Spec{Section: SectionAnswer} }

// Load implements Provider.
func (*Shell) Load(context.Context, func([]Entry)) {}

// Answer implements Answerer.
func (*Shell) Answer(query string) []Entry {
	cmd, ok := strings.CutPrefix(strings.TrimSpace(query), ">")
	cmd = strings.TrimSpace(cmd)
	if !ok || cmd == "" {
		return nil
	}
	return []Entry{{ID: "shell", Title: cmd, Subtitle: i18n.T("launcher-shell-subtitle"), Icon: iconTerminal, Data: cmd}}
}

// Actions implements Provider.
func (s *Shell) Actions(e Entry) []Action {
	cmd, ok := e.Data.(string)
	if !ok {
		return nil
	}
	return []Action{
		{Title: i18n.T("launcher-action-run"), Icon: "ld-play-symbolic", Run: func(context.Context) Outcome {
			s.env.SpawnShell(cmd)
			return Close{}
		}},
		{Title: i18n.T("launcher-action-run-in-terminal"), Icon: "ld-terminal-symbolic", Run: func(context.Context) Outcome {
			s.env.inTerminal("sh", "-c", cmd)
			return Close{}
		}},
		copyAction(i18n.T("launcher-action-copy-command"), cmd),
	}
}
