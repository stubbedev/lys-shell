package search

import (
	"context"
	"log"
	"strings"

	"github.com/stubbedev/lys-desktop/i18n"

	"github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/modes"
)

// Windows offers the open windows to jump to, on whichever compositor
// the shell runs on. The focused window stays out: nothing to jump to.
type Windows struct {
	backend modes.WindowBackend
}

// NewWindows uses backend; nil (no compositor it knows) offers nothing.
func NewWindows(backend modes.WindowBackend) *Windows { return &Windows{backend: backend} }

// Spec implements Provider.
func (*Windows) Spec() Spec { return Spec{Section: SectionWindows, Weight: 8} }

// Load implements Provider.
func (w *Windows) Load(ctx context.Context, publish func([]Entry)) {
	if w.backend == nil {
		return
	}
	list, err := w.backend.List(ctx)
	if err != nil {
		log.Printf("launcher: list windows: %v", err)
		return
	}
	entries := make([]Entry, 0, len(list))
	for _, win := range list {
		if win.Focused {
			continue
		}
		subtitle := win.Class
		if win.Workspace != "" {
			subtitle = i18n.T("launcher-window-on-workspace", i18n.Str("class", win.Class), i18n.Str("workspace", win.Workspace))
		}
		title := win.Title
		if title == "" {
			title = win.Class
		}
		entries = append(entries, Entry{
			ID: "window:" + win.ID, Learn: "window:" + strings.ToLower(win.Class), Title: title, Subtitle: subtitle,
			Icon: launcher.IconName(strings.ToLower(win.Class)), Data: win, Preview: win.Preview(),
		})
	}
	publish(entries)
}

// Actions implements Provider.
func (w *Windows) Actions(e Entry) []Action {
	win, ok := e.Data.(modes.WindowInfo)
	if !ok {
		return nil
	}
	return []Action{
		{Title: i18n.T("launcher-action-switch-window"), Icon: "ld-app-window-symbolic", Run: func(ctx context.Context) Outcome {
			if err := w.backend.Focus(ctx, win.ID); err != nil {
				return Stay{Message: i18n.T("launcher-failed", i18n.Str("what", win.Title), i18n.Str("error", err.Error()))}
			}
			return Close{}
		}},
		{Title: i18n.T("launcher-action-close-window"), Icon: "ld-x-symbolic", Run: func(ctx context.Context) Outcome {
			if err := w.backend.Close(ctx, win.ID); err != nil {
				return Stay{Message: i18n.T("launcher-failed", i18n.Str("what", win.Title), i18n.Str("error", err.Error()))}
			}
			return Close{}
		}},
		copyAction(i18n.T("launcher-action-copy-title"), win.Title),
	}
}
