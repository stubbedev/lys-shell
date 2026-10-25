package bar

import (
	"testing"

	"github.com/stubbedev/lys-shell/config"
)

func TestCustomUpdatesDispatch(t *testing.T) {
	cfg := config.Defaults()
	// A custom module with no command: it exists purely for pushes.
	def := config.CustomModuleDefinition{Id: "pushed", LabelShow: true, Format: "{{ text }}"}
	cfg.Custom = append(cfg.Custom, def)
	ctx := newTestContext(t, cfg)
	ctx.CustomUpdates = newCustomUpdates()
	upd := ctx.CustomUpdates

	// Build the module the way newCustom would, minus the loop.
	m := &customModule{ctx: ctx, def: def}
	m.btn = newBarButton(ctx, nil, "")
	upd.register(m)

	upd.dispatch("pushed", `{"text":"pushed-payload"}`)
	if got := m.btn.Label().Text(); got != "pushed-payload" {
		t.Errorf("label = %q, want pushed-payload", got)
	}
	// Plain output replaces the whole label through {{ output }}.
	def2 := def
	def2.Id = "raw"
	def2.Format = "{{ output }}"
	m2 := &customModule{ctx: ctx, def: def2}
	m2.btn = newBarButton(ctx, nil, "")
	upd.register(m2)
	upd.dispatch("raw", "plain text")
	if got := m2.btn.Label().Text(); got != "plain text" {
		t.Errorf("label = %q", got)
	}
	// An unknown id is a quiet no-op.
	upd.dispatch("ghost", "ignored")
	// Stop unregisters.
	m.Stop()
	upd.dispatch("pushed", `{"text":"after-stop"}`)
	if got := m.btn.Label().Text(); got != "pushed-payload" {
		t.Errorf("a stopped module still received pushes: %q", got)
	}
}
