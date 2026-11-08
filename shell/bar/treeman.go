package bar

import (
	"log"
	"strconv"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/jinja"
	"github.com/stubbedev/lys-shell/service/treeman"
)

// treemanLabel is helpers.rs's format_label: plain replacements of
// the exact "{{ name }}" spellings, not a template.
func treemanLabel(format string, status *treeman.Status) string {
	return jinja.ReplaceVars(format, treemanVars(status))
}

func treemanVars(status *treeman.Status) map[string]string {
	return map[string]string{
		"total": treemanCount(status.Total), "stable": treemanCount(status.Stable), "up": treemanCount(status.Up),
		"down": treemanCount(status.Down), "failed": treemanCount(status.Failed),
	}
}

func treemanCount(v uint32) string { return strconv.FormatUint(uint64(v), 10) }

// treemanButton is the watcher's update: the label ("--" without a
// status), the icon following the worst bucket, the severity class on
// the module root, and whether hide-if-empty collapses it.
type treemanButton struct {
	label, icon, severity string
	visible               bool
}

func treemanDisplay(cfg config.TreemanConfig, status *treeman.Status) treemanButton {
	v := treemanButton{label: "--", icon: cfg.IconName, visible: !cfg.HideIfEmpty}
	if status == nil {
		return v
	}
	v.label = treemanLabel(cfg.Format, status)
	v.visible = !cfg.HideIfEmpty || status.Total > 0
	switch status.WorstBucket() {
	case treeman.BucketFailed:
		v.icon, v.severity = cfg.IconFailed, "failed"
	case treeman.BucketDown:
		v.icon, v.severity = cfg.IconTearingDown, "tearing-down"
	case treeman.BucketUp:
		v.icon, v.severity = cfg.IconPreparing, "preparing"
	}
	return v
}

// treeman is the module: the worktree health aggregate.
type treemanModule struct {
	ctx  ModuleContext
	src  treeman.Source
	btn  *barButton
	icon *widget.Icon
	// chrome is the module root the severity class and visibility go
	// on; view is what was last shown, applied when the chrome arrives.
	chrome moduleChrome
	view   treemanButton
}

func newTreeman(ctx ModuleContext) (Module, error) {
	m := &treemanModule{ctx: ctx, src: ctx.Treeman}
	m.icon = moduleIcon(ctx, ctx.Config.Treeman.Icon())
	m.btn = newBarButton(ctx, m.icon, "")
	m.refresh()
	if ctx.App == nil {
		// Headless construction renders the resting state; no loop to
		// follow events on.
		return m, nil
	}
	go func() {
		ticks, stop, err := ctx.Treeman.Subscribe(ctx.Life())
		if err != nil {
			log.Printf("treeman: subscribe: %v", err)
			return
		}
		follow(ctx, ticks, stop, func(struct{}) { m.refresh() })
		m.ctx.Invoke(m.refresh)
	}()
	return m, nil
}

// refresh shows the service's latest status.
func (m *treemanModule) refresh() {
	cfg := m.ctx.Config.Treeman
	v := treemanDisplay(cfg, m.src.Status())
	text := v.label
	m.btn.SetLabel(text)
	if m.icon != nil {
		m.icon.SetThemeName(v.icon)
	}
	m.show(v)
}

// show moves the severity class and visibility on the module root.
func (m *treemanModule) show(v treemanButton) {
	prev := m.view.severity
	m.view = v
	if m.chrome == nil {
		return
	}
	m.chrome.SetVisible(v.visible)
	if prev == v.severity {
		return
	}
	if prev != "" {
		m.chrome.RemoveClass(prev)
	}
	if v.severity != "" {
		m.chrome.AddClass(v.severity)
	}
}

func (m *treemanModule) setChrome(c moduleChrome) {
	m.chrome = c
	v := m.view
	m.view.severity = ""
	m.show(v)
}

func (m *treemanModule) Root() widget.Widget { return m.btn }
