package bar

import (
	"context"
	"log"
	"math"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/widgets"
	"github.com/stubbedev/lys-shell/styling"
)

// popoverCard is the popover root: lys-ui's popover surface (its fill,
// border and radius), the shell's stylesheet painting only the
// dropdown's surface color and, when shadow is set (the bar's
// dropdown-shadow), its shadow.
func popoverCard(content widget.Widget, shadow bool) *widget.Box {
	card := widget.NewBox(widget.Column, 0, 0)
	card.SetElement("popover")
	card.AddClass("dropdown")
	if shadow {
		card.AddClass("shadow")
	}
	card.Append(content, true)
	return card
}

// dropdownHeader is the dropdown's title strip: the icon, the title,
// and any trailing actions.
func dropdownHeader(font render.Font, icon, title string, actions ...widget.Widget) *widget.Box {
	row, _, _ := dropdownHeaderParts(font, icon, title, actions...)
	return row
}

// dropdownHeaderIcon is dropdownHeader that also hands back its icon,
// for headers whose icon follows state.
func dropdownHeaderIcon(font render.Font, icon, title string, actions ...widget.Widget) (*widget.Box, *widget.Icon) {
	row, glyph, _ := dropdownHeaderParts(font, icon, title, actions...)
	return row, glyph
}

// dropdownHeaderParts is dropdownHeader with its icon and title item,
// for headers whose icon or title follows state. The strip is lys-ui's
// small Item, the icon its bare media and the actions its own, over a
// separator: the shell sets no spacing of its own, and its stylesheet
// only inks the icon (.dropdown-icon).
func dropdownHeaderParts(font render.Font, icon, title string, actions ...widget.Widget) (*widget.Box, *widget.Icon, *widget.Item) {
	item := widget.NewItem(font, title, "")
	item.SetSmall(true)
	item.SetEllipsize(true)
	glyph := widget.NewThemeIcon(icon, dropdownHeaderIconPx)
	glyph.AddClass("dropdown-icon")
	item.SetMedia(glyph, true)
	for _, a := range actions {
		item.AddAction(a)
	}
	header := widget.NewBox(widget.Column, 0, 0)
	header.AddClass("dropdown-header")
	header.Append(item, false)
	header.Append(widget.NewSeparator(widget.Horizontal), false)
	return header, glyph, item
}

// dropdownHeaderIconPx is the header icon's size, lys-ui's icon size
// beside an item's 14px title.
const dropdownHeaderIconPx = 18

// dropdownContent is a dropdown's content column under lys-ui's layout
// metrics: its sections twice the theme's spacing apart, inset by
// twice the theme's padding, which lines the content up with the
// header's icon. It carries .dropdown-content and the given classes.
func dropdownContent(classes ...string) *widget.Box {
	content := widget.NewBox(widget.Column, 2*widget.Current().Spacing, dropdownInset())
	content.AddClass(append([]string{"dropdown-content"}, classes...)...)
	return content
}

// dropdownGroup is a group of a dropdown's content: its parts the
// theme's spacing apart, closer together than the groups are.
func dropdownGroup(parts ...widget.Widget) *widget.Box {
	group := widget.NewBox(widget.Column, widget.Current().Spacing, 0)
	for _, w := range parts {
		group.Append(w, false)
	}
	return group
}

// dropdownSection is a titled group: the label over the body.
func dropdownSection(label *widget.Label, body ...widget.Widget) *widget.Box {
	return dropdownGroup(append([]widget.Widget{label}, body...)...)
}

// atTop is a column holding w at its natural height at the top, for a
// page that fills more height than w needs: the card hugs its rows.
func atTop(w widget.Widget) *widget.Box {
	col := widget.NewBox(widget.Column, 0, 0)
	col.Append(w, false)
	return col
}

// atEnd holds w at its natural size at the end and middle of the slot
// it is given: a row's trailing status that a wider page sizes.
func atEnd(w widget.Widget) *widget.Box {
	box := widget.NewBox(widget.Row, 0, 0)
	box.SetHAlign(widget.AlignEnd)
	box.SetVAlign(widget.AlignCenter)
	box.Append(w, false)
	return box
}

// scrolledCard is a scroll over card that keeps the card at its
// content's height: the scroll fills the page, the card hugs its rows.
func scrolledCard(card widget.Widget, class string) *widget.Scroll {
	return dropdownScroll(atTop(card), class)
}

// growingScroll is a dropdownScroll for a dropdown that grows to fit
// its content (no configured height): it asks for its content's
// height, and scrolls only once the monitor's clamp holds it shorter.
// A sized dropdown's scroll asks for none and fills the card instead.
func growingScroll(s *widget.Scroll) *widget.Scroll {
	s.PropagateNaturalHeight = true
	return s
}

// itemCard is a list card: lys-ui's card around an ItemGroup, which
// sets its rows apart with separators.
func itemCard() (*widget.Box, *widget.ItemGroup) {
	group := widget.NewItemGroup()
	card := widget.NewBox(widget.Column, 0, 0)
	card.AddClass(css.Card)
	card.Append(group, false)
	return card, group
}

// formCard is a prompt card: lys-ui's card holding a small Item (the
// subject, its icon on the media tile, a close action) over a body the
// theme spaces and insets in line with the item.
type formCard struct {
	*widget.Box
	head *widget.Item
	icon *widget.Icon
	body *widget.Box
}

func newFormCard(font render.Font, px float64, icon string, onClose func(), classes ...string) *formCard {
	f := &formCard{Box: widget.NewBox(widget.Column, 0, 0)}
	f.AddClass(append([]string{css.Card}, classes...)...)
	f.head = widget.NewItem(font, "", "")
	f.head.SetSmall(true)
	f.head.SetEllipsize(true)
	f.icon = widget.NewThemeIcon(icon, itemIconPx)
	f.head.SetMedia(f.icon, false)
	closeBtn := dropdownButton(widget.NewThemeIcon("ld-x-symbolic", int(px)), onClose, css.Ghost, css.Icon, css.Small, "form-close")
	f.head.AddAction(closeBtn)
	f.Append(f.head, false)
	f.body = itemBody()
	f.Append(f.body, false)
	return f
}

// itemBody is the column under an Item inside a card: the theme's
// spacing between its parts, inset on the sides and below by twice the
// theme's padding, in line with the item's own.
func itemBody() *widget.Box {
	in := dropdownInset()
	body := widget.NewBox(widget.Column, widget.Current().Spacing, 0)
	body.SetPadding(render.Insets{Left: in, Right: in, Bottom: in})
	return body
}

// dropdownInset is how far a dropdown's content sits inside its card
// on every side: twice the theme's padding.
func dropdownInset() int { return 2 * widget.Current().Padding }

// formActions is a form's button row: the buttons at the end, the
// theme's spacing apart, as lys-ui's dialogs place theirs.
func formActions(buttons ...*widget.Button) *widget.Box {
	row := widget.NewBox(widget.Row, widget.Current().Spacing, 0)
	row.Append(widget.NewSpacer(0, 0), true)
	for _, b := range buttons {
		row.Append(b, false)
	}
	return row
}

// statusDot is a status circle: the stylesheet's .status-dot rounds
// and fills it (its variant class picks the color), sized here.
func statusDot(classes ...string) *widget.Box {
	d := widget.NewBox(widget.Row, 0, 0)
	d.AddClass(append([]string{"status-dot"}, classes...)...)
	d.SetSizeRequest(statusDotPx, statusDotPx)
	return d
}

// statusDotPx is a status dot's diameter.
const statusDotPx = 8

// itemIconPx is an item's media icon size, lys-ui's icon size on the
// item's 32px media tile.
const itemIconPx = 16

// sectionLabel is a heading over a dropdown section, lys-ui's muted
// caption heading.
func sectionLabel(font render.Font, text string) *widget.Label {
	l := widget.NewLabel(font, sectionLabelPx(), text, 0)
	l.AddClass("section-label", css.CaptionHeading, css.DimLabel)
	return l
}

// iconSectionLabel is a section label led by its icon, in the label's
// muted ink.
func iconSectionLabel(font render.Font, icon, title string) *widget.Box {
	row := widget.NewBox(widget.Row, widget.Current().Spacing/2, 0)
	glyph := widget.NewThemeIcon(icon, int(math.Round(sectionLabelPx())))
	glyph.AddClass(css.DimLabel)
	row.AppendAligned(glyph, false, widget.AlignCenter)
	row.AppendAligned(sectionLabel(font, title), true, widget.AlignCenter)
	return row
}

// sectionLabelPx is the caption size, which .caption-heading also sets.
func sectionLabelPx() float64 { return widgets.TextPx(styling.Caption, 1) }

// refresher is a view's reader: every refresh of the view goes through
// it, reads run off the loop one at a time, and requests made while a
// read runs coalesce into one more read after it. Results apply on the
// loop in the order their reads started, so a read that started before
// a newer one can never land last and undo the newer state, and the
// state applied last is never older than the last request.
type refresher[T any] struct {
	mc    ModuleContext
	life  context.Context
	read  func(context.Context) T
	apply func(T)

	mu      sync.Mutex
	running bool
	again   bool
	// thens run on the loop after the apply of the next read to start.
	thens []func()
}

func newRefresher[T any](mc ModuleContext, life context.Context, read func(context.Context) T, apply func(T)) *refresher[T] {
	return &refresher[T]{mc: mc, life: life, read: read, apply: apply}
}

// request asks for a fresh read; safe from any goroutine.
func (r *refresher[T]) request() { r.requestThen(nil) }

// requestThen asks for a fresh read and runs then on the loop once it
// has been applied (nil: nothing).
func (r *refresher[T]) requestThen(then func()) {
	r.mu.Lock()
	if then != nil {
		r.thens = append(r.thens, then)
	}
	if r.running {
		r.again = true
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()
	go r.loop()
}

func (r *refresher[T]) loop() {
	for {
		r.mu.Lock()
		thens := r.thens
		r.thens = nil
		r.mu.Unlock()
		value := r.read(r.life)
		if r.life.Err() != nil {
			r.mu.Lock()
			r.running, r.again = false, false
			r.mu.Unlock()
			return
		}
		r.mc.Invoke(func() {
			r.apply(value)
			for _, fn := range thens {
				fn()
			}
		})
		r.mu.Lock()
		if !r.again {
			r.running = false
			r.mu.Unlock()
			return
		}
		r.again = false
		r.mu.Unlock()
	}
}

// followTicks is a dropdown's watcher: each tick from subscribe asks
// the view's refresher for a read, until life ends or the tick channel
// closes. It returns the refresher, which the view's own refreshes
// (after a write) go through too. A failed subscribe logs and follows
// nothing; the refresher still serves the view's requests.
func followTicks[T any](
	mc ModuleContext,
	life context.Context,
	what string,
	subscribe func(context.Context) (<-chan struct{}, func(), error),
	read func(context.Context) T,
	apply func(T),
) *refresher[T] {
	r := newRefresher(mc, life, read, apply)
	followInto(life, what, subscribe, r)
	return r
}

// followInto feeds subscribe's ticks into an existing refresher, for a
// view that follows several sources with one reader.
func followInto[T any](life context.Context, what string, subscribe func(context.Context) (<-chan struct{}, func(), error), r *refresher[T]) {
	ticks, stop, err := subscribe(life)
	if err != nil {
		log.Printf("%s: subscribe: %v", what, err)
		return
	}
	go func() {
		defer stop()
		for {
			select {
			case <-life.Done():
				return
			case _, ok := <-ticks:
				if !ok {
					return
				}
			}
			r.request()
		}
	}()
}

// dropdownButton is a flat dropdown button: the class's stylesheet
// rules paint it (the hover and active shades, the ink), so nothing is
// set here.
func dropdownButton(child widget.Widget, onClick func(), classes ...string) *widget.Button {
	b := widget.NewButton(child, 0, 0)
	b.AddClass(classes...)
	b.OnClick = onClick
	return b
}

// iconTile wraps icon in the styled tile box the stylesheets target:
// the box paints the tile (overlay background, radius, min size) and
// the image paints through its own class (size, ink, margin; "" for
// none). Each box class is one class name.
func iconTile(icon *widget.Icon, imgClass string, boxClasses ...string) *widget.Box {
	if imgClass != "" {
		icon.AddClass(imgClass)
	}
	tile := widget.NewBox(widget.Row, 0, 0)
	tile.AddClass(boxClasses...)
	tile.Append(icon, false)
	return tile
}

// dropdownScroll is a dropdown's ScrolledWindow: vertical only, as
// every Rust dropdown sets hscrollbar-policy never, so its content is
// the dropdown's width and wrapping or ellipsized text fits it. class
// is the scroll's style class, "" for none.
func dropdownScroll(child widget.Widget, class string) *widget.Scroll {
	s := widget.NewScroll(child)
	s.VerticalOnly = true
	// A GTK viewport gives its child at least its own size: short
	// content packs at the top (and an expanding empty state centers
	// in the whole panel) instead of floating mid-view.
	s.FillX, s.FillY = true, true
	if class != "" {
		s.AddClass(class)
	}
	return s
}

// Stack transition timings: a GtkStack's default duration, the hover
// swaps' HOVER_TRANSITION_MS, and the page slides' interaction-duration.
const (
	gtkStackDuration = 200 * time.Millisecond
	hoverTransition  = 150 * time.Millisecond
)

// pageStack is a stack of a dropdown's pages or states, as tall as its
// visible one (GTK's vhomogeneous off): a short page never takes a
// taller hidden page's height, which would leave the card's foot
// empty.
func pageStack() *widget.Stack {
	s := widget.NewStack()
	s.SetHomogeneous(false)
	return s
}

// pageSlide gives a dropdown's page stack its slide between pages at
// the configured interaction-duration (zero, an instant switch, with
// animations off).
func pageSlide(s *widget.Stack, cfg *config.Config) {
	s.SetTransition(widget.StackSlideLeftRight, time.Duration(cfg.Animations.InteractionDurationMs())*time.Millisecond)
}
