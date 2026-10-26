package bar

import (
	"fmt"
	"sync"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/reveal"
)

// dropdownRegistry maps the `dropdown:<name>` actions to content
// builders and owns the open popover per output host. RunWith creates
// one; the builders live next to their modules (calendar, audio, ...)
// exactly as the Rust dropdowns do.
type dropdownRegistry struct {
	app      *app.Application
	cfg      *config.Config
	font     render.Font
	style    *barStyle
	ctx      ModuleContext
	hosts    map[string]app.Host
	builders map[string]func(ctx ModuleContext) widget.Widget

	mu      sync.Mutex
	openPop map[string]*app.Popover
	// openRev is the revealer around each open popover's card; openName
	// is the dropdown it shows, so a second module's click re-anchors
	// instead of toggling off.
	openRev  map[string]*widget.Revealer
	openName map[string]string
	// monH is each connector's monitor height in logical pixels, the
	// popover ceiling's input (GTK keeps a popover on its output).
	monH map[string]int
	// instances is each dropdown's built content, the Rust registry's
	// DropdownInstance cache: built on the first open and reused by
	// every later one on any output, so its state (the visible page,
	// the scroll, a half-filled form) and its service subscriptions
	// persist across open and close. A config change or a rebuild of
	// the bars releases them.
	instances map[string]widget.Widget
}

// dropdownOpener is content that acts each time its popover opens
// (the Rust dropdowns' connect_map: the network list scanning when
// empty), not only when it is built.
type dropdownOpener interface{ dropdownOpened() }

// dropdownFocuser is content with an input that takes the keyboard as
// its popover opens (GTK focusing a popover's first entry); nil when
// none.
type dropdownFocuser interface{ dropdownFocus() widget.Widget }

// dropdownHider is content that acts each time its popover closes,
// while its instance stays cached: the vault forgets what was typed
// into its secret boxes.
type dropdownHider interface{ dropdownHidden() }

// dropdownAnchor is an anchor that shows its dropdown is open; the
// returned func ends the hold as the dropdown closes.
type dropdownAnchor interface {
	holdOpen(freezeLabel bool) (release func())
}

// dropdownCloser is live dropdown content (a service subscription) that
// releases what it holds when its popover goes away.
type dropdownCloser interface{ dropdownClosed() }

// popoverHandle is what open dropdown content may do to its popover:
// close it (GTK's popdown) and move its keyboard focus (grab_focus).
// *app.Popover implements it.
type popoverHandle interface {
	Dismiss()
	SetFocus(w widget.Widget)
}

// dropdownAttacher is content that acts on its own popover; the
// registry hands it the handle once the popover is up.
type dropdownAttacher interface{ attachPopover(h popoverHandle) }

// popoverHook is the dropdownAttacher half content embeds. popdown is a
// no-op until a popover is open; a focus asked for while the content is
// still being built (a form shown before the popover exists) is kept and
// applied once it attaches.
type popoverHook struct {
	h       popoverHandle
	pending widget.Widget
}

func (p *popoverHook) attachPopover(h popoverHandle) {
	p.h = h
	if p.pending != nil {
		h.SetFocus(p.pending)
		p.pending = nil
	}
}

// popdown closes the hosting popover.
func (p *popoverHook) popdown() {
	if p.h != nil {
		p.h.Dismiss()
	}
}

// focus moves keyboard focus to w inside the hosting popover, or once
// it opens.
func (p *popoverHook) focus(w widget.Widget) {
	if p.h == nil {
		p.pending = w
		return
	}
	p.h.SetFocus(w)
}

func newDropdownRegistry(application *app.Application, cfg *config.Config, font render.Font, style *barStyle, base ModuleContext) *dropdownRegistry {
	// Dropdown content paints its own colors (no stylesheet reaches a
	// popover), so builders get the full style: the module style the
	// bar hands its modules leaves fg to the stylesheet (zero), which
	// would paint every default-colored dropdown label transparent.
	base.Style = style
	return &dropdownRegistry{
		app:      application,
		cfg:      cfg,
		font:     font,
		style:    style,
		ctx:      base,
		hosts:    make(map[string]app.Host),
		builders: dropdownBuilders(),
		openPop:  make(map[string]*app.Popover),
		openRev:  make(map[string]*widget.Revealer),
		openName: make(map[string]string),
		monH:     make(map[string]int),

		instances: make(map[string]widget.Widget),
	}
}

// instance is name's content, built on first use with the registry's
// context (get_or_create).
func (r *dropdownRegistry) instance(name string, build func(ModuleContext) widget.Widget) widget.Widget {
	r.mu.Lock()
	content, ok := r.instances[name]
	ctx := r.ctx
	r.mu.Unlock()
	if ok {
		return content
	}
	content = build(ctx)
	if content == nil {
		return nil
	}
	content = dropdownPanel(name, r.cfg, content)
	r.mu.Lock()
	defer r.mu.Unlock()
	if prev, raced := r.instances[name]; raced {
		return prev
	}
	r.instances[name] = content
	return content
}

// dropdownPanel readies name's built content for its popover: the card
// tweens its height when a page of another size shows (watch_pages,
// dropdown_resize::animate_height), so every stack sized to its visible
// page interpolates, and a sized dropdown sits in its panel.
func dropdownPanel(name string, cfg *config.Config, content widget.Widget) widget.Widget {
	walkPages(content, func(w widget.Widget) {
		if st, ok := w.(*widget.Stack); ok && !st.Homogeneous() {
			st.SetInterpolateSize(true)
		}
	})
	if w, h, ok := dropdownDims(name, cfg); ok {
		return newPanelBox(content, w, h)
	}
	return content
}

// release closes every open dropdown and drops the cached instances,
// stopping what each follows; the next open builds afresh.
func (r *dropdownRegistry) release() {
	r.closeAll()
	r.mu.Lock()
	instances := r.instances
	r.instances = make(map[string]widget.Widget)
	r.mu.Unlock()
	for _, content := range instances {
		if c, ok := content.(dropdownCloser); ok {
			c.dropdownClosed()
		}
	}
}

// attachHost registers the layer window a connector's dropdowns open
// on, with the monitor's logical height for the popover ceiling.
func (r *dropdownRegistry) attachHost(connector string, host app.Host, monH int) {
	r.mu.Lock()
	r.hosts[connector] = host
	r.monH[connector] = monH
	r.mu.Unlock()
}

// detachHost forgets an unplugged connector's layer and its open
// popover.
func (r *dropdownRegistry) detachHost(connector string) {
	r.mu.Lock()
	delete(r.hosts, connector)
	delete(r.openPop, connector)
	delete(r.openRev, connector)
	delete(r.openName, connector)
	delete(r.monH, connector)
	r.mu.Unlock()
}

// Names lists the registered dropdowns (tests).
func (r *dropdownRegistry) Names() []string {
	names := make([]string, 0, len(r.builders))
	for name := range r.builders {
		names = append(names, name)
	}
	return names
}

// open shows one dropdown anchored to the module's root widget on the
// connector's host. Re-clicking the same module dismisses; opening
// another while one is up dismisses it and opens the new (the Rust
// registry's reparent_and_show), so the bar never shows two.
func (r *dropdownRegistry) open(connector, name string, anchor widget.Widget) error {
	r.mu.Lock()
	host := r.hosts[connector]
	monH := r.monH[connector]
	// A popover that already went away (click-away, Esc) no longer
	// counts as open: the next click opens instead of toggling off.
	if prev := r.openPop[connector]; prev != nil && !prev.Closed() {
		same := r.openName[connector] == name
		rev := r.openRev[connector]
		delete(r.openPop, connector)
		delete(r.openRev, connector)
		delete(r.openName, connector)
		r.mu.Unlock()
		dismissAnimated(prev, rev, r.cfg.Animations)
		if same {
			return nil
		}
		// The exit plays before the next popover opens.
		r.mu.Lock()
	}
	build, ok := r.builders[name]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("dropdown %q is not registered", name)
	}
	if host == nil || r.app == nil {
		return fmt.Errorf("dropdown %q has no host yet", name)
	}
	bound, ok := anchor.(widget.Boundser)
	if !ok {
		return fmt.Errorf("dropdown %q anchor cannot be measured", name)
	}
	// The instance is shown in one popover at a time: open on another
	// output, it moves here (reparent_and_show).
	r.mu.Lock()
	for other, shown := range r.openName {
		if shown == name && other != connector {
			if pop := r.openPop[other]; pop != nil && !pop.Closed() {
				defer pop.Dismiss()
			}
			delete(r.openPop, other)
			delete(r.openRev, other)
			delete(r.openName, other)
		}
	}
	r.mu.Unlock()
	content := r.instance(name, build)
	if content == nil {
		return fmt.Errorf("dropdown %q has no content", name)
	}
	// The panel is clamped to the monitor as the Rust registry's
	// clamp_height does at every popup.
	if panel, ok := content.(*panelBox); ok && monH > 0 {
		panel.clamp(monH)
	}
	w, _, _ := dropdownDims(name, r.cfg)
	cfg := dropdownPopover(r.cfg, bound, content, monH, w)
	cfg.OnClosed = holdDropdown(anchor, r.cfg.Bar.DropdownFreezeLabel, content)
	if f, ok := content.(dropdownFocuser); ok {
		cfg.Focus = f.dropdownFocus()
	}
	var rev *widget.Revealer
	cfg.Content, rev = dropdownTree(content, r.cfg)
	pop, err := r.app.OpenPopover(host, cfg)
	if err != nil {
		if cfg.OnClosed != nil {
			cfg.OnClosed()
		}
		return err
	}
	if d, ok := content.(dropdownAttacher); ok {
		d.attachPopover(pop)
	}
	if o, ok := content.(dropdownOpener); ok {
		o.dropdownOpened()
	}
	reveal.Show(rev, r.cfg.Animations, config.AnimDropdown)
	r.mu.Lock()
	r.openPop[connector] = pop
	r.openRev[connector] = rev
	r.openName[connector] = name
	r.mu.Unlock()
	return nil
}

// holdDropdown marks anchor while content's dropdown is open (with
// freezeLabel, dropdown-freeze-label, its label held still too) and
// returns what runs as the popover closes: the anchor's hold ends and
// content that acts on each close does; nil when neither.
func holdDropdown(anchor widget.Widget, freezeLabel bool, content widget.Widget) func() {
	var release func()
	if a, ok := anchor.(dropdownAnchor); ok {
		release = a.holdOpen(freezeLabel)
	}
	h, hides := content.(dropdownHider)
	if !hides {
		return release
	}
	return func() {
		if release != nil {
			release()
		}
		h.dropdownHidden()
	}
}

// closeAll dismisses every open dropdown, each playing its exit.
func (r *dropdownRegistry) closeAll() {
	r.mu.Lock()
	type open struct {
		pop *app.Popover
		rev *widget.Revealer
	}
	var closing []open
	for connector, pop := range r.openPop {
		if pop != nil && !pop.Closed() {
			closing = append(closing, open{pop, r.openRev[connector]})
		}
		delete(r.openPop, connector)
		delete(r.openRev, connector)
		delete(r.openName, connector)
	}
	r.mu.Unlock()
	for _, o := range closing {
		dismissAnimated(o.pop, o.rev, r.cfg.Animations)
	}
}

// dropdownPopover declares a dropdown's popover (apply_style,
// clamp_height): opening away from the bar's edge, clamped to the
// monitor less 100, as wide as its configured width at most (width > 0),
// and with dropdown-autohide off a no-grab popover that a click
// elsewhere leaves open. The width caps as well as floors: lys-ui's
// rows fill whatever width they are offered, and a popover offers the
// widest output.
func dropdownPopover(cfg *config.Config, anchor widget.Boundser, content widget.Widget, monH, width int) app.PopoverConfig {
	pc := app.PopoverConfig{
		Anchor:     anchor,
		Gravity:    app.GravityOf(barEdge(cfg.Bar.Location).PopoverSide()),
		Content:    content,
		Gap:        widget.PopoverGap,
		NoAutohide: !cfg.Bar.DropdownAutohide,
	}
	if monH > 0 {
		pc.MaxHeight = monH - 100
	}
	if width > 0 {
		pc.MaxWidth = width
	}
	return pc
}

// dismissAnimated is animate_out: the card plays its exit, then the
// popover goes. Only this programmatic close animates; a click-away or
// Esc closes at once, the compositor's grab having no exit hook.
func dismissAnimated(pop interface{ Dismiss() }, rev *widget.Revealer, anims config.AnimationsConfig) {
	if rev == nil {
		pop.Dismiss()
		return
	}
	reveal.Hide(rev, anims, config.AnimDropdown, pop.Dismiss)
}

// dropdownGenieEdge is the edge a genie collapses toward: the bar's.
func dropdownGenieEdge(location config.Location) widget.Edge {
	switch location {
	case config.LocationBottom:
		return widget.EdgeBottom
	case config.LocationLeft:
		return widget.EdgeLeft
	case config.LocationRight:
		return widget.EdgeRight
	}
	return widget.EdgeTop
}

// setConfig hands dropdowns opened from now on a new snapshot, for a
// reload that leaves the bars standing: the instances built from the
// old one are released.
func (r *dropdownRegistry) setConfig(cfg *config.Config) {
	r.release()
	r.cfg = cfg
	r.ctx.Config = cfg
}

// dropdownTree is the popover's tree around content: the revealer the
// card enters through (animate_in) inside the card (lys-ui's popover
// surface in the dropdown's color). The theme reaches it through the
// bar button it opens from: lys-ui styles a popover as a child of its
// anchor, as GTK's popover CSS node is, so it inherits the bar window's
// fonts and :root variables. The card is its content's size (a sized
// dropdown's panelBox); the popover's gap and shadow gutter keep it off
// the bar and the surface edges.
func dropdownTree(content widget.Widget, cfg *config.Config) (*widget.Box, *widget.Revealer) {
	rev := widget.NewRevealer(content)
	rev.SetGenieEdge(dropdownGenieEdge(cfg.Bar.Location))
	return popoverCard(rev, cfg.Bar.DropdownShadow), rev
}
