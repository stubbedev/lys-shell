package bar

import (
	"errors"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/shell/reveal"
	"github.com/stubbedev/lys-desktop/styling"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
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
	// openName is the dropdown each open popover shows, so a second
	// module's click re-anchors instead of toggling off.
	openName map[string]string
	// monH is each connector's monitor height in logical pixels, the
	// popover ceiling's input (GTK keeps a popover on its output).
	monH map[string]int
	// instances is each dropdown's built content, the Rust registry's
	// DropdownInstance cache: built on the first open and reused by
	// every later one on any output, so its state (the visible page,
	// the scroll, a half-filled form) and its service subscriptions
	// persist across open and close. A change to what they are built
	// from releases them (reconfigure).
	instances        map[string]widget.Widget
	dashboardAnchors map[string]widget.Widget
	// revealHolds keep each connector's auto-hiding bar revealed while one
	// of its dropdowns is open.
	revealHolds map[string]func() (release func())
	// idle runs fn on the loop once the bars have had their first frames,
	// and invoke on the loop's next turn; nil idle builds nothing ahead
	// of an open (buildAhead sets them). prebuilt is the build of the
	// bars' dropdowns having been asked for.
	idle     func(fn func())
	invoke   func(fn func())
	prebuilt bool
	// released counts the releases, which end a prebuild under way.
	released uint64
	look     dropdownLook
}

type dropdownLook struct {
	style   barStyle
	palette styling.Palette
}

func lookOf(style *barStyle) dropdownLook {
	look := dropdownLook{style: *style}
	if style.palette != nil {
		look.palette = *style.palette
	}
	return look
}

// dropdownPrebuildDelay is how long after the first bar attaches its
// dropdowns start building, one per loop turn: the bars' first frames
// come first.
const dropdownPrebuildDelay = 500 * time.Millisecond

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
	SetFocus(w widget.Widget) error
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
		p.focus(p.pending)
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
// it opens; a refusal is logged.
func (p *popoverHook) focus(w widget.Widget) {
	if p.h == nil {
		p.pending = w
		return
	}
	if err := p.h.SetFocus(w); err != nil {
		log.Printf("bar: dropdown: cannot focus %T: %v", w, err)
	}
}

func newDropdownRegistry(application *app.Application, cfg *config.Config, font render.Font, style *barStyle, base ModuleContext) *dropdownRegistry {
	// Dropdown content paints its own colors (no stylesheet reaches a
	// popover), so builders get the full style: the module style the
	// bar hands its modules leaves fg to the stylesheet (zero), which
	// would paint every default-colored dropdown label transparent.
	r := &dropdownRegistry{
		app:         application,
		cfg:         cfg,
		font:        font,
		style:       style,
		look:        lookOf(style),
		hosts:       make(map[string]app.Host),
		revealHolds: make(map[string]func() func()),
		builders:    dropdownBuilders(),
		openPop:     make(map[string]*app.Popover),
		openName:    make(map[string]string),
		monH:        make(map[string]int),

		instances:        make(map[string]widget.Widget),
		dashboardAnchors: make(map[string]widget.Widget),
	}
	base.Style, base.Dropdowns, base.gen = style, r, base.gen.next()
	r.ctx = base
	return r
}

// buildAhead has the bars' dropdowns built while the loop is idle, from
// once a bar has attached, so a dropdown's first open builds nothing.
func (r *dropdownRegistry) buildAhead() {
	if r.app == nil {
		return
	}
	application := r.app
	r.mu.Lock()
	r.invoke = application.Invoke
	r.idle = func(fn func()) {
		time.AfterFunc(dropdownPrebuildDelay, func() { application.Invoke(fn) })
	}
	hosted := len(r.hosts) > 0
	r.mu.Unlock()
	if hosted {
		r.prebuild()
	}
}

func (r *dropdownRegistry) setDashboardAnchor(connector string, anchor widget.Widget) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dashboardAnchors[connector] = anchor
}

func (r *dropdownRegistry) toggleDashboard(connector, preferred string) error {
	r.mu.Lock()
	target := connector
	if target == "" {
		target = preferred
		if _, ok := r.dashboardAnchors[target]; !ok {
			target = ""
			for name := range r.dashboardAnchors {
				if target == "" || name < target {
					target = name
				}
			}
		}
	}
	anchor, ok := r.dashboardAnchors[target]
	r.mu.Unlock()
	if !ok {
		if connector != "" {
			return fmt.Errorf("no dashboard module in the bar on %s", connector)
		}
		return errors.New("no dashboard module in any bar")
	}
	return r.open(target, "dashboard", anchor)
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
// stopping what each follows and ending the generation they ran in; the
// next open, or the next prebuild, builds afresh.
func (r *dropdownRegistry) release() {
	r.closeAll()
	r.mu.Lock()
	r.released++
	r.prebuilt = false
	instances := r.instances
	r.instances = make(map[string]widget.Widget)
	retired := r.ctx.gen
	r.ctx.gen = retired.next()
	r.mu.Unlock()
	for _, content := range instances {
		if c, ok := content.(dropdownCloser); ok {
			c.dropdownClosed()
		}
	}
	retired.retire()
}

func (r *dropdownRegistry) remount(ctx ModuleContext) {
	r.closeAll()
	r.mu.Lock()
	clear(r.hosts)
	clear(r.monH)
	clear(r.dashboardAnchors)
	r.mu.Unlock()
	r.reconfigure(ctx)
}

func (r *dropdownRegistry) reconfigure(ctx ModuleContext) {
	look := lookOf(r.style)
	r.mu.Lock()
	stale := barsAffected(r.cfg, ctx.Config) || look != r.look
	r.cfg, r.font, r.look = ctx.Config, ctx.Font, look
	ctx.Style, ctx.Dropdowns, ctx.gen = r.style, r, r.ctx.gen
	r.ctx = ctx
	hosted := len(r.hosts) > 0
	r.mu.Unlock()
	if !stale {
		return
	}
	r.release()
	if hosted {
		r.prebuild()
	}
}

// attachHost registers the layer window a connector's dropdowns open
// on, with the monitor's logical height for the popover ceiling.
// holdReveal keeps connector's bar revealed until the returned release;
// a bar that does not auto-hide has nothing to hold.
func (r *dropdownRegistry) holdReveal(connector string) (release func()) {
	r.mu.Lock()
	hold := r.revealHolds[connector]
	r.mu.Unlock()
	if hold == nil {
		return func() {}
	}
	return hold()
}

// setRevealHold is what keeps connector's bar revealed while a dropdown
// is open on it; nil drops it.
func (r *dropdownRegistry) setRevealHold(connector string, hold func() (release func())) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if hold == nil {
		delete(r.revealHolds, connector)
		return
	}
	r.revealHolds[connector] = hold
}

func (r *dropdownRegistry) attachHost(connector string, host app.Host, monH int) {
	r.mu.Lock()
	r.hosts[connector] = host
	r.monH[connector] = monH
	r.mu.Unlock()
	r.prebuild()
}

// prebuild builds the content of every dropdown the bars' modules open,
// one per loop turn once the bars are up, so a first open builds
// nothing. It runs once per config.
func (r *dropdownRegistry) prebuild() {
	r.mu.Lock()
	if r.prebuilt || r.idle == nil {
		r.mu.Unlock()
		return
	}
	r.prebuilt = true
	cfg, released := r.cfg, r.released
	r.mu.Unlock()
	names := barDropdowns(cfg, r.builders)
	var step func()
	step = func() {
		r.mu.Lock()
		stale := r.released != released
		r.mu.Unlock()
		if stale || len(names) == 0 {
			return
		}
		name := names[0]
		names = names[1:]
		if h, ok := r.instance(name, r.builders[name]).(dropdownHider); ok {
			h.dropdownHidden()
		}
		if len(names) > 0 {
			r.invoke(step)
		}
	}
	r.idle(step)
}

// barDropdowns are the registered dropdowns a click on one of cfg's bar
// modules opens, each once, in the bars' order.
func barDropdowns(cfg *config.Config, builders map[string]func(ModuleContext) widget.Widget) []string {
	var names []string
	add := func(item config.BarItem) {
		c := moduleBinding(string(item.Module), cfg)
		for _, a := range [...]config.ClickAction{c.LeftClick, c.RightClick, c.MiddleClick, c.ScrollUp, c.ScrollDown} {
			if _, ok := builders[a.Dropdown]; ok && a.Kind == config.ClickDropdown && !slices.Contains(names, a.Dropdown) {
				names = append(names, a.Dropdown)
			}
		}
	}
	for _, l := range cfg.Bar.Layout {
		layout, ok := barLayoutFor(cfg, l.Monitor)
		if !ok {
			continue
		}
		for _, section := range [...][]config.BarItem{layout.Left, layout.Center, layout.Right} {
			for _, item := range section {
				if item.IsGroup() {
					for _, member := range item.Group.Modules {
						add(member)
					}
					continue
				}
				add(item)
			}
		}
	}
	return names
}

// detachHost forgets an unplugged connector's layer and its open
// popover.
func (r *dropdownRegistry) detachHost(connector string) {
	r.mu.Lock()
	delete(r.hosts, connector)
	delete(r.openPop, connector)
	delete(r.openName, connector)
	delete(r.monH, connector)
	delete(r.dashboardAnchors, connector)
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
		delete(r.openPop, connector)
		delete(r.openName, connector)
		r.mu.Unlock()
		prev.Dismiss()
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
				defer pop.Close()
			}
			delete(r.openPop, other)
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
	release, closed := r.holdReveal(connector), cfg.OnClosed
	cfg.OnClosed = func() {
		release()
		if closed != nil {
			closed()
		}
	}
	if f, ok := content.(dropdownFocuser); ok {
		cfg.Focus = f.dropdownFocus()
	}
	if cfg.Focus == nil && widget.FocusVisible(anchor) {
		cfg.Focus = widget.FirstFocusable(content)
	}
	var rev *widget.Revealer
	cfg.Content, rev = dropdownTree(content, r.cfg)
	cfg.Exit = dropdownExit(rev, r.cfg.Animations)
	reveal.Show(rev, r.cfg.Animations, config.AnimDropdown)
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
	r.mu.Lock()
	r.openPop[connector] = pop
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
	var closing []*app.Popover
	for connector, pop := range r.openPop {
		if pop != nil && !pop.Closed() {
			closing = append(closing, pop)
		}
		delete(r.openPop, connector)
		delete(r.openName, connector)
	}
	r.mu.Unlock()
	for _, pop := range closing {
		pop.Dismiss()
	}
}

// dropdownPopover declares a dropdown's popover (apply_style,
// clamp_height): opening away from the bar's edge, clamped to the
// monitor less 100, as wide as its configured width at most (width > 0),
// closing on a click elsewhere without taking the keyboard from the
// focused window, or with dropdown-autohide off staying open. The width
// caps as well as floors: lys-ui's
// rows fill whatever width they are offered, and a popover offers the
// widest output. A translucent panel (dropdown-opacity) asks the
// compositor to blur the desktop behind it, which its own backdrop-filter
// could not see; where there is no blur the card carries lys-ui's
// blur-unavailable class and rests opaque.
func dropdownPopover(cfg *config.Config, anchor widget.Boundser, content widget.Widget, monH, width int) app.PopoverConfig {
	pc := app.PopoverConfig{
		Anchor:         anchor,
		Gravity:        app.GravityOf(barEdge(cfg.Bar.Location).PopoverSide()),
		Content:        content,
		Gap:            widget.PopoverGap,
		Dismissal:      app.PopoverSticky,
		BackgroundBlur: cfg.Styling.SurfaceOpacity(cfg.Bar.DropdownOpacity) < 100,
	}
	if cfg.Bar.DropdownAutohide {
		pc.Dismissal = app.PopoverClickAway
	}
	if monH > 0 {
		pc.MaxHeight = monH - 100
	}
	if width > 0 {
		pc.MaxWidth = width
	}
	return pc
}

// dropdownExit is animate_out, whatever closes the popover: the card
// plays its exit, then the popover goes.
func dropdownExit(rev *widget.Revealer, anims config.AnimationsConfig) func(done func()) {
	return func(done func()) { reveal.Hide(rev, anims, config.AnimDropdown, done) }
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
// reload that leaves the bars standing.
func (r *dropdownRegistry) setConfig(cfg *config.Config) {
	r.mu.Lock()
	ctx := r.ctx
	r.mu.Unlock()
	ctx.Config = cfg
	r.reconfigure(ctx)
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
	rev.SetFadeFrom(reveal.FadeFrom)
	rev.SetGenieEdge(dropdownGenieEdge(cfg.Bar.Location))
	return popoverCard(rev, cfg.Bar.DropdownShadow), rev
}
