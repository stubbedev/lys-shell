package filechooser

import (
	"context"
	"errors"
	"image"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/transfer"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"
	"golang.org/x/sys/unix"

	"github.com/stubbedev/lys-shell/internal/fileuri"
	"github.com/stubbedev/lys-shell/internal/fswatch"
	"github.com/stubbedev/lys-shell/internal/mime"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/shell/dialogqueue"
	"github.com/stubbedev/lys-shell/shell/widgets"
)

// The panel's geometry (shell/file_chooser: its default size,
// column widths, icon sizes). The panel's floor keeps the
// sidebar and columns usable, its ceiling a sane maximum on huge
// outputs.
const (
	panelW, panelH       = 760, 520
	panelMinW, panelMinH = 480, 320
	panelMaxW, panelMaxH = 1600, 1100
	sidebarW             = 180
	searchW              = 150
	previewW             = 220
	listRowH             = 34
	gridCellW            = 112
	gridCellH            = 104
	rowIconPx            = 24
	gridIconPx           = 48
	previewIconPx        = 96
	navIconPx            = 16
	minColumnW           = 50
	maxColumnW           = 400
	crumbMaxChars        = 16
	colGripPx            = 7
	thumbWorkers         = 4
)

// The keys and buttons the chooser binds: evdev keycodes and the
// mouse's thumb buttons.
const (
	keyEscape    = 1
	keyBackspace = 14
	keyF         = 33
	keyH         = 35
	keyL         = 38
	keySpace     = 57
	keyUp        = 103
	keyLeft      = 105
	keyRight     = 106
	btnBack      = 0x113 // BTN_SIDE
	btnForward   = 0x114 // BTN_EXTRA
)

// uriListMime is what a file drag offers.
const uriListMime = "text/uri-list"

// Window is the overlay surface the chooser maps (app.LayerWindow).
type Window interface {
	Close()
	SetFocus(widget.Widget)
}

// Deps are the chooser's collaborators.
type Deps struct {
	// Open maps the overlay; Invoke runs on the UI loop.
	Open   func(cfg app.LayerConfig) (Window, error)
	Invoke func(func())
	// Font shapes the text; Sheet styles the surface, the labels'
	// and entries' ink included.
	Font  render.Font
	Sheet *widget.Stylesheet
	// MIME guesses types for filters and icons.
	MIME *mime.Database
	// Home is where a request without a folder starts and what the
	// breadcrumbs shorten to Home.
	Home string
	// Places and Locations fill the sidebar for each request;
	// WatchLocations calls changed (off the loop) whenever the
	// locations may have changed (a mount came or went) until its stop
	// is called; nil watches nothing.
	Places, Locations func() []Place
	WatchLocations    func(changed func()) (stop func())
	// ViewFile persists the sort and layout; "" keeps them in memory.
	ViewFile string
	// RecentFile is the recently-used list the Recent place lists
	// (RecentFile()); "" offers no Recent.
	RecentFile string
	// TrashDirs are the trash directories the Trash place lists
	// (SystemTrashDirs); nil offers no Trash.
	TrashDirs func() []string
	// Target is the output a dialog maps on (the focused one, else the
	// first); nil while none is known, when the compositor picks one.
	Target func() *app.Output
}

// Chooser is the dev.stubbe.lys.FileChooser1 host: one overlay showing one
// request; requests arriving meanwhile wait their turn, and dismissing
// a request answers it with nothing, showing or waiting. Open and Save
// may be called from any goroutine; the rest runs on the loop.
type Chooser struct {
	d     Deps
	win   Window
	reply func(Result)
	queue dialogqueue.Queue
	// readChoices reads the request's choices as the user left them.
	readChoices func() []portalreq.Selection
	// replacing is the existing file a save asked to replace, which a
	// second confirm of the same name replaces.
	replacing string
	// modal is whether the request showing holds the keyboard.
	modal bool
	s     *session
	ui    *ui

	// view, hidden and recursive stick across requests.
	view              view
	hidden, recursive bool
	// columns are the size, modified and kind widths.
	columns [3]int

	stopWalk context.CancelFunc
	// watcher follows the folder shown on disk, stopMounts the
	// locations; reloading marks a listing read again for a change, which
	// keeps the scroll and the selection.
	watcher    *fswatch.Watcher
	stopMounts func()
	reloading  bool
	// reloadKeep is the path selected when the reload started, and
	// selectNext the path to select once the coming listing lands (a
	// dropped file).
	reloadKeep, selectNext string
	// navigating is set while one gesture's activations are being
	// applied: after the first descended, the rest index a listing
	// that is gone.
	navigating bool
	// size is the panel's size as the user left it, kept across
	// requests. at is where it was moved to on the output placedOn,
	// which sticks across requests there, as the Rust window does;
	// moved is false while it centres.
	size     widget.Size
	at       widget.Point
	moved    bool
	placedOn string

	// thumbs are the thumbnails found (nil for a file without one),
	// waiting the slots each lookup in flight fills.
	thumbs  map[thumbKey]image.Image
	waiting map[thumbKey][]func(image.Image)
	workers chan struct{}
}

type thumbKey struct {
	path string
	px   int
}

// New builds the host; nothing maps until a request.
func New(d Deps) *Chooser {
	c := &Chooser{
		d: d, view: defaultView, columns: [3]int{90, 150, 90}, workers: make(chan struct{}, thumbWorkers),
		size: widget.Size{W: panelW, H: panelH},
	}
	if d.ViewFile != "" {
		if b, err := os.ReadFile(d.ViewFile); err == nil {
			c.view = parseView(string(b))
		}
	}
	return c
}

// request is one dialog, open or save.
type request struct {
	title, folder, name, acceptLabel string
	mode                             Mode
	filters                          []Filter
	currentFilter                    int32
	choices                          []portalreq.Choice
}

// Open shows the open dialog.
func (c *Chooser) Open(caller portalreq.Caller, r OpenRequest) Result {
	return c.ask(caller, request{
		title: r.Title, folder: r.CurrentFolder, acceptLabel: r.AcceptLabel, mode: openMode(r),
		filters: r.Filters, currentFilter: r.CurrentFilter, choices: r.Choices,
	})
}

// Save shows the save dialog seeded with the suggested name.
func (c *Chooser) Save(caller portalreq.Caller, r SaveRequest) Result {
	return c.ask(caller, request{
		title: r.Title, folder: r.CurrentFolder, name: r.CurrentName, acceptLabel: r.AcceptLabel, mode: ModeSave,
		filters: r.Filters, currentFilter: r.CurrentFilter, choices: r.Choices,
	})
}

// Dismiss answers the request at handle with nothing: the dialog
// showing for it is taken down, a waiting one never shows. Another
// request's dialog stays.
func (c *Chooser) Dismiss(handle string) {
	c.d.Invoke(func() {
		if c.queue.Dismiss(handle) {
			c.finish(nil)
		}
	})
}

// ask queues a request on the loop and waits for its answer.
func (c *Chooser) ask(caller portalreq.Caller, r request) Result {
	ch := make(chan Result, 1)
	c.d.Invoke(func() {
		c.queue.Enqueue(caller.Handle, func() {
			c.reply = func(res Result) { ch <- res }
			c.modal = caller.Modal
			c.begin(r)
		}, func() { ch <- Result{} })
	})
	return <-ch
}

// finish answers the request showing with uris (none for a cancel),
// the filter active and the choices made, stops its search, plays the
// exit, and shows the next request waiting.
func (c *Chooser) finish(uris []string) {
	c.cancelWalk()
	c.unwatch()
	if c.stopMounts != nil {
		c.stopMounts()
		c.stopMounts = nil
	}
	if c.reply == nil {
		return
	}
	res := Result{URIs: uris}
	if len(uris) > 0 {
		if c.s.active >= 0 && c.s.active < len(c.s.filters) {
			res.Filter = c.s.filters[c.s.active]
		}
		res.Choices = c.readChoices()
	}
	reply := c.reply
	c.reply = nil
	reply(res)
	c.hide()
	c.queue.Done()
}

func (c *Chooser) begin(r request) {
	c.thumbs, c.waiting = map[thumbKey]image.Image{}, map[thumbKey][]func(image.Image){}
	c.replacing = ""
	c.s = newSession(r.mode, r.folder, r.filters, c.d.MIME, c.d.Home, c.view)
	c.s.hidden, c.s.recursive, c.s.recentFile, c.s.trashDirs = c.hidden, c.recursive, c.d.RecentFile, c.d.TrashDirs
	c.s.request = c.requestListing
	if r.currentFilter > 0 && int(r.currentFilter) < len(c.s.filters) {
		c.s.active = int(r.currentFilter)
	}
	c.build(r)
	c.s.refresh()
	c.relist()
	c.show()
	if c.d.WatchLocations != nil {
		c.stopMounts = c.d.WatchLocations(func() { c.d.Invoke(c.refreshLocations) })
	}
}

// requestListing reads dir off the loop and lists it once read, unless
// the dialog moved on; it starts following dir on disk.
func (c *Chooser) requestListing(dir string) {
	s := c.s
	go func() {
		l := s.read(dir)
		c.d.Invoke(func() {
			if c.s != s || !s.setListing(dir, l) {
				return
			}
			if c.reloading {
				c.reloading = false
				c.relistKeeping(c.reloadKeep)
				return
			}
			c.relist()
			c.applySelectNext()
		})
	}()
	c.watch(dir)
}

// watch follows dir on disk, reloading its listing when it changes;
// Recent, Trash and a folder that cannot be watched are not followed.
func (c *Chooser) watch(dir string) {
	c.unwatch()
	if virtualLocation(dir) {
		return
	}
	w, err := fswatch.New(fswatch.Changes, false)
	if err != nil {
		log.Printf("file chooser: cannot follow %s: %v", dir, err)
		return
	}
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return
	}
	c.watcher = w
	s := c.s
	go func() {
		for range w.Changed() {
			c.d.Invoke(func() {
				if c.s == s && c.watcher == w && s.dir == dir && !s.loading {
					c.reloading, c.reloadKeep = true, c.selectedPath()
					s.reload()
				}
			})
		}
	}()
}

func (c *Chooser) unwatch() {
	if c.watcher != nil {
		_ = c.watcher.Close()
		c.watcher = nil
	}
}

// --- the tree -------------------------------------------------------

// ui holds the widgets a request updates.
type ui struct {
	root                    *widget.FloatingPanel
	crumbs                  *widget.Box
	search                  *widget.SearchEntry
	name                    *widget.Entry
	list, grid              *widget.List
	listView, gridView      *typeAhead
	colHeader               *widget.Box
	sortButtons             [4]*widget.Button
	sortLabels              [4]*widget.Label
	columns                 [3]widget.Widget
	empty                   *widget.Label
	quicklook               *widget.Box
	qlIcon                  *widget.Box
	qlName, qlInfo          *widget.Label
	preview                 *widget.Box
	pvIcon                  *widget.Box
	pvName, pvInfo          *widget.Label
	filterButton            *widget.ToggleButton
	filterLabel             *widget.Label
	filterPopup             *widget.Box
	viewToggle              *widget.Button
	viewIcon                *widget.Box
	previewToggle, hiddenTg *widget.ToggleButton
	recursiveToggle         *widget.ToggleButton
	// notice is a warning or the replace prompt over the footer.
	notice      *widget.Box
	noticeLabel *widget.Label
	replace     *widget.Button
	// location is the typed path entry (Ctrl+L) the crumbs give way to.
	location *widget.Entry
	// newFolder is the row naming a folder to create.
	newFolder     *widget.Box
	newFolderName *widget.Entry
	// locations is the sidebar's Locations list.
	locations *widget.Box
}

// label is a one-line label at lys-ui's text size.
func (c *Chooser) label(text string, classes ...string) *widget.Label {
	l := widget.NewLabel(c.d.Font, widget.Current().TextSize, text, 0)
	l.AddClass(classes...)
	return l
}

// textButton is a text button in lys-ui's variant (css.Ghost,
// css.Outline), carrying class and its label class+"-label".
func (c *Chooser) textButton(text, class, variant string, onClick func()) (*widget.Button, *widget.Label) {
	l := c.label(text, class+"-label")
	l.SetAlignment(render.AlignCenter)
	b := widget.NewButton(l, 0, 0)
	b.AddClass(class, variant)
	b.OnClick = onClick
	return b, l
}

// section is a band of the panel padded by the theme: a bar of
// controls between the separators.
func section(dir widget.Axis, class string) *widget.Box {
	th := widget.Current()
	b := widget.NewBox(dir, th.Spacing, 0)
	b.SetPadding(render.Insets{Top: th.Padding, Right: 2 * th.Padding, Bottom: th.Padding, Left: 2 * th.Padding})
	b.AddClass(class)
	return b
}

// iconToggle is a gtk::ToggleButton with an icon: on starts it
// active, and onToggled hears every later change.
func (c *Chooser) iconToggle(icon, tooltip string, on bool, onToggled func(bool)) *widget.ToggleButton {
	holder := widget.NewBox(widget.Row, 0, 0)
	setIcon(holder, icon)
	b := widget.NewToggleButton(widgets.Center(holder), 0, 0)
	b.AddClass("toggle", "file-chooser-nav", css.Ghost, css.Icon, css.Small)
	b.SetTooltip(tooltip)
	b.SetActive(on)
	b.OnToggled = onToggled
	return b
}

func (c *Chooser) iconButton(icon, tooltip string, onClick func()) *widget.Button {
	b, _ := c.swappableIconButton(icon, tooltip, onClick)
	return b
}

// swappableIconButton is an icon button whose icon setIcon can swap.
func (c *Chooser) swappableIconButton(icon, tooltip string, onClick func()) (*widget.Button, *widget.Box) {
	holder := widget.NewBox(widget.Row, 0, 0)
	setIcon(holder, icon)
	b := widget.NewButton(widgets.Center(holder), 0, 0)
	b.AddClass("file-chooser-nav", css.Ghost, css.Icon, css.Small)
	b.SetTooltip(tooltip)
	b.OnClick = onClick
	return b, holder
}

func setIcon(holder *widget.Box, icon string) {
	holder.Clear()
	holder.Append(widget.NewThemeIcon(icon, navIconPx), false)
}

func (c *Chooser) build(r request) {
	u := &ui{}
	s := c.s
	title, name := r.title, r.name

	// Header: up, the centred title, the view toggles. The bar is the
	// panel's drag handle.
	th := widget.Current()
	headerRow := widget.NewBox(widget.Row, th.Spacing/4, 0)
	headerRow.Append(c.iconButton("go-up-symbolic", "Up", c.goUp), false)
	if s.mode == ModeSave || s.mode.folders() {
		headerRow.Append(c.iconButton("folder-new-symbolic", "Create Folder", c.showNewFolder), false)
	}
	headerRow.Append(widget.NewSpacer(0, 0), true)
	u.viewToggle, u.viewIcon = c.swappableIconButton(viewIcon(c.view.grid), "Toggle grid view", c.toggleView)
	u.previewToggle = c.iconToggle("view-paged-symbolic", "Preview pane", false, c.togglePreview)
	u.hiddenTg = c.iconToggle("view-reveal-symbolic", "Show hidden files", c.hidden, c.toggleHidden)
	u.recursiveToggle = c.iconToggle("folder-saved-search-symbolic", "Search subfolders", c.recursive, c.toggleRecursive)
	headerRow.Append(u.viewToggle, false)
	for _, b := range []*widget.ToggleButton{u.previewToggle, u.hiddenTg, u.recursiveToggle} {
		headerRow.Append(b, false)
	}
	headerStack := widget.NewOverlay()
	headerStack.Append(headerRow)
	titleLabel := widget.NewLabel(c.d.Font, th.TitleSize(), title, 0)
	titleLabel.AddClass("file-chooser-title", css.Heading)
	headerStack.AppendAligned(titleLabel, widget.AlignCenter, widget.AlignCenter)
	header := section(widget.Column, "file-chooser-header")
	header.Append(headerStack, false)

	// Path bar: breadcrumbs and the search box.
	crumbBar := section(widget.Row, "file-chooser-crumbbar")
	u.crumbs = widget.NewBox(widget.Row, th.Spacing/4, 0)
	u.crumbs.AddClass("file-chooser-crumbs")
	crumbBar.Append(u.crumbs, true)
	u.location = widget.NewEntry(c.d.Font, th.TextSize, 0)
	u.location.AddClass("file-chooser-location")
	u.location.SetPlaceholder("Type a path")
	u.location.OnActivate = c.goToLocation
	u.location.OnEscape = func() bool { c.hideLocation(); return true }
	u.location.SetVisible(false)
	crumbBar.Append(u.location, true)
	// gtk::SearchEntry with no debounce: filtering the cached listing
	// is an in-memory scan, and a recursive walk is cancelled by its
	// generation instead.
	u.search = widget.NewSearchEntry(c.d.Font, th.TextSize, "Filter this folder")
	u.search.AddClass("file-chooser-search")
	u.search.SetDelay(0)
	u.search.OnSearchChanged = c.setSearch
	u.search.SetSizeRequest(searchW, 0)
	u.search.SetVAlign(widget.AlignCenter)
	crumbBar.Append(u.search, false)

	// Body: sidebar, the file views, the preview pane.
	body := widget.NewBox(widget.Row, 0, 0)
	body.AddClass("file-chooser-body")
	body.Append(c.sidebar(u), false)
	body.Append(c.views(u), true)
	var card *widget.Box
	card, u.pvIcon, u.pvName, u.pvInfo = c.previewCard("file-chooser-preview", 22)
	// The pane, not just its card, hides: a hidden card would still
	// hold its width in the row.
	card.SetSizeRequest(previewW, 0)
	u.preview = card
	u.preview.SetVisible(false)
	body.Append(u.preview, false)

	u.name = widget.NewEntry(c.d.Font, th.TextSize, 0)
	u.name.AddClass("file-chooser-name")
	u.name.SetMargin(render.Insets{Right: 2 * th.Padding, Bottom: th.Padding, Left: 2 * th.Padding})
	u.name.SetPlaceholder("File name")
	u.name.SetText(name)
	u.name.OnActivate = func(string) { c.confirm() }
	u.name.OnChanged = func(string) { c.clearNotice() }
	u.name.SetVisible(s.mode == ModeSave)

	// The notice: a warning, or the replace prompt with its button.
	u.notice = section(widget.Row, "file-chooser-notice")
	u.noticeLabel = c.label("", "file-chooser-notice-label", css.Warning)
	u.noticeLabel.SetWrap(true)
	u.notice.Append(u.noticeLabel, true)
	u.replace, _ = c.textButton("Replace", "file-chooser-replace", css.DestructiveAction, c.confirm)
	u.notice.Append(u.replace, false)
	u.notice.SetVisible(false)

	// The new folder row: a name and Create.
	u.newFolder = section(widget.Row, "file-chooser-new-folder")
	u.newFolderName = widget.NewEntry(c.d.Font, th.TextSize, 0)
	u.newFolderName.SetPlaceholder("Folder name")
	u.newFolderName.OnActivate = func(string) { c.createFolder() }
	u.newFolderName.OnEscape = func() bool { c.ui.newFolder.SetVisible(false); return true }
	u.newFolder.Append(u.newFolderName, true)
	create, _ := c.textButton("Create", "file-chooser-create-folder", css.SuggestedAction, c.createFolder)
	u.newFolder.Append(create, false)
	u.newFolder.SetVisible(false)

	choiceRows, readChoices := widgets.ChoiceRows(c.d.Font, r.choices, "file-chooser-choice")
	c.readChoices = readChoices

	// Footer: the type filter, cancel, confirm.
	footer := section(widget.Row, "file-chooser-footer")
	u.filterLabel = c.label("", "file-chooser-filter-label")
	u.filterLabel.SetAlignment(render.AlignCenter)
	u.filterButton = widget.NewToggleButton(u.filterLabel, 0, 0)
	u.filterButton.AddClass("toggle", "file-chooser-filter", css.Outline)
	u.filterButton.OnToggled = c.setFilterPopup
	u.filterLabel.SetAlignment(render.AlignStart)
	u.filterLabel.SetEllipsize(widget.EllipsizeEnd)
	u.filterLabel.SetMaxWidthChars(28)
	u.filterButton.SetVisible(len(s.filters) > 0)
	if len(s.filters) > 0 {
		u.filterLabel.SetText(FilterLabel(s.filters[s.active]))
	}
	footer.Append(u.filterButton, false)
	for _, row := range choiceRows {
		footer.Append(row, false)
	}
	footer.Append(widget.NewSpacer(0, 0), true)
	cancel, _ := c.textButton("Cancel", "file-chooser-cancel", css.Outline, func() { c.finish(nil) })
	accept := r.acceptLabel
	if accept == "" {
		accept = s.mode.confirmLabel()
	}
	confirm, _ := c.textButton(accept, "file-chooser-confirm", css.SuggestedAction, c.confirm)
	footer.Append(cancel, false)
	footer.Append(confirm, false)

	content := widget.NewBox(widget.Column, 0, 0)
	content.Append(widget.NewSeparator(widget.Horizontal), false)
	content.Append(crumbBar, false)
	content.Append(u.newFolder, false)
	content.Append(widget.NewSeparator(widget.Horizontal), false)
	content.Append(body, true)
	content.Append(widget.NewSeparator(widget.Horizontal), false)
	content.Append(u.name, false)
	content.Append(u.notice, false)

	// The filter popup sits on the footer in the panel itself rather
	// than an xdg_popup, which an exclusive overlay may not route input
	// to, inset like the footer's filter button.
	u.filterPopup = widget.NewBox(widget.Column, 0, th.Padding/2)
	u.filterPopup.AddClass(css.Card, "file-chooser-filter-popup")
	filterList := widget.NewBox(widget.Column, 0, 0)
	filterList.AddClass("file-chooser-filter-list")
	for i, f := range s.filters {
		row, l := c.textButton(FilterLabel(f), "file-chooser-filter-row", css.Ghost, func() { c.selectFilter(i) })
		l.SetAlignment(render.AlignStart)
		l.SetEllipsize(widget.EllipsizeEnd)
		l.SetMaxWidthChars(40)
		filterList.Append(row, false)
	}
	u.filterPopup.Append(widget.NewScroll(filterList), true)
	u.filterPopup.SetVisible(false)
	u.filterPopup.SetMargin(render.Insets{Left: 2 * th.Padding})
	above := widget.NewOverlay()
	above.Append(content)
	above.AppendAligned(u.filterPopup, widget.AlignStart, widget.AlignEnd)

	main := &dropZone{Box: widget.NewBox(widget.Column, 0, 0), onDrop: c.dropped}
	main.Append(above, true)
	main.Append(footer, false)

	u.root = widget.NewFloatingPanel(header, main)
	u.root.SetSizeLimits(widget.Size{W: panelMinW, H: panelMinH}, widget.Size{W: panelMaxW, H: panelMaxH})
	u.root.SetPanelSize(c.size.W, c.size.H)
	u.root.OnGeometryChanged = c.placedAt
	if c.d.Sheet != nil {
		u.root.AttachStylesheet(c.d.Sheet)
	}

	c.ui = u
	c.updateSortLabels()
	c.relist()
}

func viewIcon(grid bool) string {
	if grid {
		return "view-list-symbolic"
	}
	return "view-grid-symbolic"
}

// sidebar is Favorites (the user's places) and Locations (/ and the
// mounts, which refreshLocations rebuilds).
func (c *Chooser) sidebar(u *ui) widget.Widget {
	th := widget.Current()
	box := widget.NewBox(widget.Column, th.Spacing/4, th.Padding)
	box.AddClass("file-chooser-sidebar")
	section := func(title string, places []Place) *widget.Box {
		header := c.label(title, "file-chooser-sidebar-header", css.CaptionHeading, css.DimLabel)
		header.SetMargin(render.Insets{Top: th.Padding / 2, Right: th.Padding, Bottom: th.Padding / 2, Left: th.Padding})
		box.Append(header, false)
		list := widget.NewBox(widget.Column, 0, 0)
		list.AddClass("file-chooser-places")
		c.fillPlaces(list, places)
		box.Append(list, false)
		return list
	}
	favorites := c.d.Places()
	// Recent and Trash list files to open, never a folder to save in.
	opening := c.s.mode == ModeOpenFile || c.s.mode == ModeOpenMultiple
	if c.d.RecentFile != "" && opening {
		favorites = append([]Place{{"Recent", recentLocation, "document-open-recent-symbolic"}}, favorites...)
	}
	if c.d.TrashDirs != nil && opening {
		favorites = append(favorites, Place{"Trash", trashLocation, "user-trash-symbolic"})
	}
	section("Favorites", favorites)
	u.locations = section("Locations", c.d.Locations())
	box.Append(widget.NewSpacer(0, 0), true)
	scroll := widget.NewScroll(box)
	// Filled, so the sections pack at the top instead of floating mid-view.
	scroll.FillX, scroll.FillY = true, true
	scroll.AddClass("file-chooser-sidebar-scroll")
	scroll.SetSizeRequest(sidebarW, 0)
	return scroll
}

// placeIcon is a place's icon, through GTK's dash fallbacks as the GTK
// sidebar's GThemedIcon resolves it.
func placeIcon(p Place) *widget.Icon {
	icon := widget.NewThemeIconFallback(p.Icon, navIconPx)
	icon.AddClass("file-chooser-place-icon")
	return icon
}

// fillPlaces lists places in list, a button each.
func (c *Chooser) fillPlaces(list *widget.Box, places []Place) {
	list.Clear()
	for _, p := range places {
		row := widget.NewBox(widget.Row, widget.Current().Spacing, 0)
		row.AppendAligned(placeIcon(p), false, widget.AlignCenter)
		l := c.label(p.Label)
		l.SetEllipsize(widget.EllipsizeEnd)
		row.AppendAligned(l, true, widget.AlignCenter)
		b := widget.NewButton(row, 0, 0)
		b.AddClass("file-chooser-place", css.Ghost)
		path := p.Path
		b.OnClick = func() { c.goTo(path) }
		list.Append(b, false)
	}
}

// refreshLocations rebuilds Locations: a mount came or went.
func (c *Chooser) refreshLocations() {
	if c.reply != nil && c.ui != nil {
		c.fillPlaces(c.ui.locations, c.d.Locations())
	}
}

// views is the column header over the list and grid, with the empty
// label and the quick look card over them.
func (c *Chooser) views(u *ui) widget.Widget {
	u.colHeader = widget.NewBox(widget.Row, 0, 0)
	u.colHeader.AddClass("file-chooser-colheader")
	for col := range u.sortButtons {
		b, l := c.textButton("", "file-chooser-col", css.Ghost, func() { c.sortBy(SortColumn(col)) })
		b.AddClass(css.Small)
		l.AddClass(css.Caption, css.DimLabel)
		l.SetAlignment(render.AlignStart)
		u.sortButtons[col], u.sortLabels[col] = b, l
		if col == int(SortName) {
			u.colHeader.Append(b, true)
			continue
		}
		u.colHeader.Append(c.columnGrip(col-1), false)
		u.columns[col-1] = widgets.Pin(b, c.columns[col-1], 0)
		u.colHeader.Append(u.columns[col-1], false)
	}

	u.list = widget.NewList[widget.Widget](rows{c, false}, listRowH)
	u.list.AddClass("file-chooser-list")
	u.list.DragContent = c.dragOut
	u.grid = widget.NewList[widget.Widget](rows{c, true}, gridCellH)
	u.grid.AddClass("file-chooser-grid")
	u.grid.DragContent = c.dragOut
	u.grid.SetCellWidth(gridCellW)
	u.listView, u.gridView = &typeAhead{List: u.list, typed: c.typeAhead}, &typeAhead{List: u.grid, typed: c.typeAhead}
	mode := widget.SelectionSingle
	if c.s.mode.multiple() {
		mode = widget.SelectionMultiple
	}
	for _, l := range []*widget.List{u.list, u.grid} {
		l.SetSelectionMode(mode)
		l.OnActivate = c.activate
		l.OnSelectionChanged = func([]int) { c.refreshPreview() }
	}

	u.empty = c.label("No items", "file-chooser-empty", css.DimLabel)
	u.quicklook, u.qlIcon, u.qlName, u.qlInfo = c.previewCard("file-chooser-quicklook", 32)
	u.quicklook.AddClass(css.Card)
	u.quicklook.SetVisible(false)

	stack := widget.NewOverlay()
	stack.Append(u.listView)
	stack.Append(u.gridView)
	stack.AppendAligned(u.empty, widget.AlignCenter, widget.AlignCenter)
	stack.AppendAligned(u.quicklook, widget.AlignCenter, widget.AlignCenter)

	col := widget.NewBox(widget.Column, 0, 0)
	col.Append(u.colHeader, false)
	col.Append(stack, true)
	return col
}

// columnGrip resizes column i (size, modified, kind) by dragging its
// left edge: left grows it.
func (c *Chooser) columnGrip(i int) widget.Widget {
	var base int
	g := newColumnGrip()
	g.AddClass("file-chooser-col-grip")
	g.onStart = func() { base = c.columns[i] }
	g.onDrag = func(dx int) {
		c.columns[i] = min(max(base-dx, minColumnW), maxColumnW)
		widgets.Pin(c.ui.columns[i], c.columns[i], 0)
	}
	g.onEnd = func() { c.ui.list.Refresh() }
	return g
}

// previewCard is an icon, a name and a detail line: the quick look
// card and the preview pane.
func (c *Chooser) previewCard(class string, nameChars int) (card, icon *widget.Box, name, info *widget.Label) {
	th := widget.Current()
	card = widget.NewBox(widget.Column, th.Spacing, 2*th.Padding)
	card.AddClass(class)
	icon = widget.NewBox(widget.Row, 0, 0)
	icon.AddClass(class + "-icon")
	name = c.label("", class+"-name")
	name.SetAlignment(render.AlignCenter)
	name.SetWrap(true)
	name.SetMaxWidthChars(nameChars)
	info = c.label("", class+"-info", css.Caption, css.DimLabel)
	info.SetAlignment(render.AlignCenter)
	info.SetWrap(true)
	card.Append(widgets.Center(icon), false)
	card.Append(name, false)
	card.Append(info, false)
	return card, icon, name, info
}

// rows is a List model over the session's entries: list rows, or grid
// cells.
type rows struct {
	c    *Chooser
	grid bool
}

func (m rows) Len() int { return len(m.c.s.entries) }

func (m rows) Row(i int) widget.Widget {
	e := m.c.s.entries[i]
	if m.grid {
		return m.c.gridCell(e)
	}
	return m.c.fileRow(e)
}

// fileRow is the name (icon and label) with the size, modified and
// kind cells under their column headers.
func (c *Chooser) fileRow(e Entry) widget.Widget {
	th := widget.Current()
	row := widget.NewBox(widget.Row, th.Spacing, 0)
	row.SetPadding(render.Insets{Left: th.Padding, Right: th.Padding})
	icon := c.entryIcon(e, rowIconPx)
	row.Append(icon, false)
	name := c.label(c.s.displayName(e))
	// A recursive match is a relative path: keep both ends in view.
	if c.s.searching() {
		name.SetEllipsize(widget.EllipsizeMiddle)
	} else {
		name.SetEllipsize(widget.EllipsizeEnd)
	}
	row.Append(name, true)
	size := "--"
	if !e.IsDir {
		size = HumanSize(e.Size)
	}
	for i, text := range []string{size, FormatTime(e.Modified), e.Kind()} {
		cell := c.label(text, "file-chooser-cell", css.DimLabel)
		cell.SetEllipsize(widget.EllipsizeEnd)
		if i < 2 {
			cell.SetAlignment(render.AlignEnd)
		}
		row.Append(widgets.Pin(cell, c.columns[i], 0), false)
	}
	return row
}

// gridCell is a large icon over a centred name.
func (c *Chooser) gridCell(e Entry) widget.Widget {
	th := widget.Current()
	cell := widget.NewBox(widget.Column, th.Spacing/2, th.Padding/2)
	cell.AddClass("file-chooser-grid-cell")
	cell.Append(widgets.Center(c.entryIcon(e, gridIconPx)), false)
	name := c.label(c.s.displayName(e))
	name.SetAlignment(render.AlignCenter)
	name.SetWrap(true)
	name.SetMaxLines(2)
	name.SetEllipsize(widget.EllipsizeEnd)
	cell.Append(name, false)
	return cell
}

// entryIcon is the folder glyph, or a file's thumbnail (the cached
// one any app made, or a raster image decoded) over its type's themed
// icon until one is ready, or for good when there is none.
func (c *Chooser) entryIcon(e Entry, px int) widget.Widget {
	if e.IsDir {
		return widget.NewThemeIcon("folder-symbolic", px)
	}
	return c.thumb(e.Path, px, widget.NewThemeIconFallback(c.d.MIME.GenericIcon(c.d.MIME.TypeByName(e.Name())), px))
}

// thumb is a px square showing fallback until one of the bounded
// workers found the file's thumbnail (at twice the size, for HiDPI);
// each file is looked up once per request however often its row is
// rebuilt.
func (c *Chooser) thumb(path string, px int, fallback widget.Widget) widget.Widget {
	slot := widget.NewBox(widget.Row, 0, 0)
	slot.SetHAlign(widget.AlignCenter)
	slot.SetVAlign(widget.AlignCenter)
	show := func(done image.Image) {
		img := widget.NewImage(done)
		img.SetScale(widget.ImageCover)
		img.AddClass("file-chooser-thumb")
		slot.Clear()
		slot.Append(widgets.Pin(img, px, px), false)
	}
	key := thumbKey{path, px}
	if done, ok := c.thumbs[key]; ok {
		if done != nil {
			show(done)
		} else {
			slot.Append(fallback, false)
		}
		return slot
	}
	slot.Append(fallback, false)
	if _, pending := c.waiting[key]; !pending {
		thumbs, waiting := c.thumbs, c.waiting
		go func() {
			c.workers <- struct{}{}
			done, err := thumbnail(path, 2*px)
			<-c.workers
			c.d.Invoke(func() {
				shows := waiting[key]
				delete(waiting, key)
				if err != nil {
					thumbs[key] = nil
					return
				}
				thumbs[key] = done
				for _, show := range shows {
					show(done)
				}
			})
		}()
	}
	c.waiting[key] = append(c.waiting[key], show)
	return slot
}

// --- surface --------------------------------------------------------

func (c *Chooser) show() {
	if c.win != nil {
		c.win.Close()
		c.win = nil
	}
	var output *app.Output
	if c.d.Target != nil {
		output = c.d.Target()
	}
	if name := outputName(output); name != c.placedOn {
		c.moved, c.placedOn = false, name
	}
	if c.moved {
		c.ui.root.SetPosition(c.at)
	}
	keyboard := app.KeyboardExclusive
	if !c.modal {
		keyboard = app.KeyboardOnDemand
	}
	// The overlay spans the output and the panel floats on it, taking
	// input only over itself.
	win, err := c.d.Open(app.LayerConfig{
		Output:        output,
		Layer:         app.LayerOverlay,
		Anchor:        app.AnchorTop | app.AnchorBottom | app.AnchorLeft | app.AnchorRight,
		ExclusiveZone: -1,
		Keyboard:      keyboard,
		Namespace:     "lys-shell-file-chooser",
		Root:          c.ui.root,
		OnKey:         c.key,
		OnPress:       func(button uint32, _ widget.Widget) { c.press(button) },
	})
	if err != nil {
		log.Printf("file chooser: cannot map the overlay: %v", err)
		c.finish(nil)
		return
	}
	c.win = win
	if c.s.mode == ModeSave {
		win.SetFocus(c.ui.name)
	} else {
		win.SetFocus(c.focusView())
	}
}

// outputName names o, "" for the compositor's pick.
func outputName(o *app.Output) string {
	if o == nil {
		return ""
	}
	return o.Name
}

func (c *Chooser) hide() {
	if c.win == nil {
		return
	}
	c.win.Close()
	c.win = nil
}

// key binds GTK's chooser keys: Escape closes the filter popup, then
// quick look, then cancels; plain Space toggles quick look, Backspace
// and Alt+Up go up, Alt+Left and Alt+Right walk the history (none of
// these while a text field has the key); Ctrl+H shows hidden files,
// Ctrl+L the location entry, Ctrl+F the search.
func (c *Chooser) key(r *widget.Router, keycode uint32, mods app.Mods) {
	if c.reply == nil {
		return
	}
	typing := false
	switch r.Focused().(type) {
	case *widget.Entry, *widget.SearchEntry:
		typing = true
	}
	ctrl, alt := mods&app.ModCtrl != 0, mods&app.ModAlt != 0
	plain := mods&(app.ModCtrl|app.ModAlt|app.ModSuper|app.ModShift) == 0
	switch {
	case keycode == keyEscape:
		c.escape()
	case keycode == keySpace && plain && !typing:
		c.toggleQuicklook()
	case keycode == keyBackspace && plain && !typing, keycode == keyUp && alt:
		c.goUp()
	case keycode == keyLeft && alt:
		c.step(true)
	case keycode == keyRight && alt:
		c.step(false)
	case keycode == keyH && ctrl:
		// The toggle's handler applies it.
		c.ui.hiddenTg.SetActive(!c.hidden)
	case keycode == keyL && ctrl:
		c.showLocation("")
	case keycode == keyF && ctrl:
		c.win.SetFocus(c.ui.search)
	}
}

func (c *Chooser) escape() {
	switch u := c.ui; {
	case u.filterPopup.Visible():
		c.setFilterPopup(false)
	case u.quicklook.Visible():
		u.quicklook.SetVisible(false)
	default:
		c.finish(nil)
	}
}

// press walks the history on the mouse's thumb buttons.
func (c *Chooser) press(button uint32) {
	if c.reply == nil {
		return
	}
	switch button {
	case btnBack:
		c.step(true)
	case btnForward:
		c.step(false)
	}
}

// placedAt keeps the panel's size and place as a move or resize left
// them, for the requests after.
func (c *Chooser) placedAt(r render.Rect) {
	c.size, c.at, c.moved = widget.Size{W: r.W, H: r.H}, widget.Point{X: r.X, Y: r.Y}, true
}

// --- state changes --------------------------------------------------

func (c *Chooser) activeView() *widget.List {
	if c.s.view.grid {
		return c.ui.grid
	}
	return c.ui.list
}

// focusView is the active view's focus target, which takes typing.
func (c *Chooser) focusView() widget.Widget {
	if c.s.view.grid {
		return c.ui.gridView
	}
	return c.ui.listView
}

// relist repaints after the entries changed: the breadcrumbs, both
// views from the top, the empty label, the preview.
func (c *Chooser) relist() {
	u := c.ui
	u.crumbs.Clear()
	for i, cr := range Crumbs(c.s.dir, c.d.Home) {
		if i > 0 {
			u.crumbs.AppendAligned(c.label("›", "file-chooser-crumb-sep", css.DimLabel), false, widget.AlignCenter)
		}
		b, l := c.textButton(truncate(cr.Label, crumbMaxChars), "file-chooser-crumb", css.Ghost, nil)
		b.AddClass(css.Small)
		if cr.Current {
			b.AddClass("current")
		} else {
			l.AddClass(css.DimLabel)
		}
		path := cr.Path
		b.OnClick = func() { c.goTo(path) }
		u.crumbs.Append(b, false)
	}
	grid := c.s.view.grid
	u.colHeader.SetVisible(!grid)
	u.list.SetVisible(!grid)
	u.grid.SetVisible(grid)
	u.list.Reset()
	u.grid.Reset()
	empty := "No items"
	switch {
	case c.s.loading:
		empty = "Loading…"
	case c.s.listErr != nil && !c.s.searching():
		empty = "Cannot read this folder: " + errorText(c.s.listErr)
	}
	u.empty.SetText(empty)
	u.empty.SetVisible(len(c.s.entries) == 0)
	u.quicklook.SetVisible(false)
	c.refreshPreview()
}

// relistKeeping repaints a listing read again where it stands: the
// scroll stays, and the entry selected (the first, in a multiple
// pick: the list sets one at a time) stays selected where it still
// is. kept is the path selected before the listing changed.
func (c *Chooser) relistKeeping(kept string) {
	view := c.activeView()
	u := c.ui
	u.list.Refresh()
	u.grid.Refresh()
	u.empty.SetVisible(len(c.s.entries) == 0)
	view.Select(slices.IndexFunc(c.s.entries, func(e Entry) bool { return e.Path == kept }))
	c.refreshPreview()
}

// selectedPath is the path of the first entry selected, "" for none.
func (c *Chooser) selectedPath() string {
	if e, ok := c.selected(); ok {
		return e.Path
	}
	return ""
}

// truncate shortens s to n runes with an ellipsis.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func (c *Chooser) goTo(dir string) {
	c.s.goTo(dir)
	c.afterNavigate()
}

func (c *Chooser) goUp() {
	if c.s.up() {
		c.afterNavigate()
	}
}

func (c *Chooser) step(back bool) {
	if c.s.step(back) {
		c.afterNavigate()
	}
}

// dropped opens a dropped folder, or the folder of a dropped file with
// the file selected.
func (c *Chooser) dropped(path string) {
	c.selectNext = path
	c.s.dropped(path)
	c.afterNavigate()
	if !c.s.loading {
		c.applySelectNext()
	}
}

// applySelectNext selects the entry waiting to be, once listed.
func (c *Chooser) applySelectNext() {
	if c.selectNext == "" {
		return
	}
	c.activeView().Select(slices.IndexFunc(c.s.entries, func(e Entry) bool { return e.Path == c.selectNext }))
	c.selectNext = ""
	c.refreshPreview()
}

// typeAhead is the type-ahead of the file views: / or ~ typed with a
// view focused starts a path in the location entry, any other
// character a search for it.
func (c *Chooser) typeAhead(r rune) {
	if c.reply == nil {
		return
	}
	if r == '/' || r == '~' {
		c.showLocation(string(r))
		return
	}
	c.win.SetFocus(c.ui.search)
	c.ui.search.SetText(c.ui.search.Text() + string(r))
}

// showLocation swaps the crumbs for the location entry, seeded with
// text (GTK opens it on Ctrl+L, and on typing / or ~).
func (c *Chooser) showLocation(text string) {
	u := c.ui
	u.crumbs.SetVisible(false)
	u.location.SetVisible(true)
	u.location.SetText(text)
	c.win.SetFocus(u.location)
}

func (c *Chooser) hideLocation() {
	c.ui.location.SetVisible(false)
	c.ui.crumbs.SetVisible(true)
	c.win.SetFocus(c.focusView())
}

// goToLocation goes where the location entry names: a folder opens; a
// file opens (or, saving, becomes the name in its folder); anything
// else warns.
func (c *Chooser) goToLocation(text string) {
	base := c.s.dir
	if virtualLocation(base) {
		base = c.d.Home
	}
	path := (&session{dir: base}).savePath(strings.TrimSpace(text), c.d.Home)
	if path == "" {
		return
	}
	info, err := os.Stat(path)
	switch {
	case err == nil && info.IsDir():
		c.hideLocation()
		c.goTo(path)
	case c.s.mode == ModeSave:
		c.hideLocation()
		c.goTo(filepath.Dir(path))
		c.ui.name.SetText(filepath.Base(path))
		c.win.SetFocus(c.ui.name)
	case err == nil && !c.s.mode.folders():
		c.finish([]string{fileuri.FromPath(path)})
	default:
		c.warn("No folder or file at " + path + ".")
	}
}

// showNewFolder opens the row naming a folder to create.
func (c *Chooser) showNewFolder() {
	u := c.ui
	u.newFolderName.SetText("")
	u.newFolder.SetVisible(true)
	c.win.SetFocus(u.newFolderName)
}

// createFolder creates the named folder in the one shown and opens
// it; a name that is taken or cannot be made warns.
func (c *Chooser) createFolder() {
	name := strings.TrimSpace(c.ui.newFolderName.Text())
	if name == "" || virtualLocation(c.s.dir) {
		return
	}
	if name == "." || name == ".." || strings.Contains(name, "/") {
		c.warn("“" + name + "” is not a folder name.")
		return
	}
	path := filepath.Join(c.s.dir, name)
	if err := os.Mkdir(path, 0o755); err != nil { //nolint:gosec // a folder the user creates, as any file manager does
		c.warn("Cannot create “" + name + "”: " + errorText(err))
		return
	}
	c.ui.newFolder.SetVisible(false)
	c.clearNotice()
	c.goTo(path)
}

// errorText is an error's last, human part (the path is shown apart).
func errorText(err error) string {
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		return pe.Err.Error()
	}
	return err.Error()
}

// afterNavigate empties the search box (the session already dropped
// the query) and repaints the new folder.
func (c *Chooser) afterNavigate() {
	c.cancelWalk()
	c.ui.search.SetText("")
	c.relist()
}

func (c *Chooser) cancelWalk() {
	if c.stopWalk != nil {
		c.stopWalk()
		c.stopWalk = nil
	}
}

// setSearch filters the cached listing in memory, or, searching
// subfolders, starts a walk that streams its matches in; a newer query
// cancels it.
func (c *Chooser) setSearch(query string) {
	c.cancelWalk()
	c.s.setSearch(query)
	c.relist()
	if !c.s.searching() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.stopWalk = cancel
	s, root, hidden := c.s, c.s.dir, c.s.hidden
	go WalkSearch(ctx, root, query, hidden, func(batch []string) {
		found := searchEntries(batch, s.mode)
		c.d.Invoke(func() {
			if ctx.Err() == nil && c.s == s && s.addFound(query, found) {
				c.relist()
			}
		})
	})
}

// restartSearch re-runs the query: a walk depends on the hidden and
// subfolder toggles.
func (c *Chooser) restartSearch() { c.setSearch(c.s.search) }

func (c *Chooser) toggleHidden(on bool) {
	c.hidden = on
	c.s.hidden = on
	if c.s.searching() {
		c.restartSearch()
		return
	}
	c.s.refresh()
	c.relist()
}

func (c *Chooser) toggleRecursive(on bool) {
	c.recursive = on
	c.s.recursive = on
	c.restartSearch()
}

func (c *Chooser) toggleView() {
	c.s.view.grid = !c.s.view.grid
	c.saveView()
	setIcon(c.ui.viewIcon, viewIcon(c.s.view.grid))
	c.relist()
	if c.win != nil {
		c.win.SetFocus(c.focusView())
	}
}

func (c *Chooser) togglePreview(on bool) {
	c.ui.preview.SetVisible(on)
	c.refreshPreview()
}

func (c *Chooser) sortBy(col SortColumn) {
	c.s.sortBy(col)
	c.saveView()
	c.updateSortLabels()
	c.relist()
}

// saveView keeps the sort and layout for the next request and, with a
// file, across restarts (best effort).
func (c *Chooser) saveView() {
	c.view = c.s.view
	if c.d.ViewFile == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.d.ViewFile), 0o700); err == nil {
		_ = os.WriteFile(c.d.ViewFile, []byte(c.view.String()), 0o600)
	}
}

// updateSortLabels marks the sort column with its direction.
func (c *Chooser) updateSortLabels() {
	for col, l := range c.ui.sortLabels {
		text := [...]string{"Name", "Size", "Modified", "Kind"}[col]
		if SortColumn(col) == c.s.view.sort {
			if c.s.view.asc {
				text += " ↑"
			} else {
				text += " ↓"
			}
		}
		l.SetText(text)
	}
}

// setFilterPopup shows or hides the filter list with the filter
// toggle's state (set_active), from the toggle or a choice.
func (c *Chooser) setFilterPopup(on bool) {
	c.ui.filterPopup.SetVisible(on)
	c.ui.filterButton.SetActive(on)
}

// selectFilter makes filter i the active one; a save name takes its
// extension.
func (c *Chooser) selectFilter(i int) {
	c.s.selectFilter(i)
	c.ui.filterLabel.SetText(FilterLabel(c.s.filters[c.s.active]))
	c.setFilterPopup(false)
	if c.s.mode == ModeSave {
		c.ui.name.SetText(withExtension(c.ui.name.Text(), c.s.filters[c.s.active]))
	}
	c.relist()
}

// activate is a double-click or Enter on row i.
func (c *Chooser) activate(i int) {
	if c.reply == nil || c.navigating || i < 0 || i >= len(c.s.entries) {
		return
	}
	name := c.s.entries[i].Name()
	switch c.s.activate(i) {
	case activateDescend:
		c.afterNavigate()
		c.navigating = true
		c.d.Invoke(func() { c.navigating = false })
	case activateName:
		c.ui.name.SetText(name)
	case activateConfirm:
		c.confirm()
	}
}

func (c *Chooser) confirm() {
	if c.reply == nil {
		return
	}
	if c.s.mode == ModeSave {
		c.confirmSave()
		return
	}
	if uris, ok := c.s.result(c.activeView().Selection()); ok {
		c.finish(uris)
	}
}

// confirmSave answers the path the name entry names, once it can be
// written: a folder it names opens instead, a folder missing or not
// writable is a warning, and an existing file is replaced only once the
// user confirms it in the notice.
func (c *Chooser) confirmSave() {
	path := c.s.savePath(strings.TrimSpace(c.ui.name.Text()), c.d.Home)
	if path == "" {
		return
	}
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		c.ui.name.SetText("")
		c.goTo(path)
		return
	}
	dir := filepath.Dir(path)
	if d, derr := os.Stat(dir); derr != nil || !d.IsDir() {
		c.warn("The folder " + dir + " does not exist.")
		return
	}
	if unix.Access(dir, unix.W_OK) != nil {
		c.warn("You cannot save in " + dir + ".")
		return
	}
	if err == nil && c.replacing != path {
		c.replacing = path
		c.ui.noticeLabel.SetText("“" + filepath.Base(path) + "” already exists in " + dir + ". Replace it?")
		c.ui.replace.SetVisible(true)
		c.ui.notice.SetVisible(true)
		return
	}
	c.finish([]string{fileuri.FromPath(path)})
}

// warn shows a warning in the notice.
func (c *Chooser) warn(text string) {
	c.replacing = ""
	c.ui.noticeLabel.SetText(text)
	c.ui.replace.SetVisible(false)
	c.ui.notice.SetVisible(true)
}

// clearNotice takes the notice down: the name changed.
func (c *Chooser) clearNotice() {
	c.replacing = ""
	c.ui.notice.SetVisible(false)
}

// selected is the first selected entry of the active view.
func (c *Chooser) selected() (Entry, bool) {
	sel := c.activeView().Selection()
	if len(sel) == 0 || sel[0] >= len(c.s.entries) {
		return Entry{}, false
	}
	return c.s.entries[sel[0]], true
}

func (c *Chooser) toggleQuicklook() {
	u := c.ui
	if u.quicklook.Visible() {
		u.quicklook.SetVisible(false)
		return
	}
	if e, ok := c.selected(); ok {
		c.fillPreview(u.qlIcon, u.qlName, u.qlInfo, e)
		u.quicklook.SetVisible(true)
	}
}

func (c *Chooser) refreshPreview() {
	u := c.ui
	if u == nil || !u.preview.Visible() {
		return
	}
	if e, ok := c.selected(); ok {
		c.fillPreview(u.pvIcon, u.pvName, u.pvInfo, e)
		return
	}
	u.pvIcon.Clear()
	u.pvName.SetText("No selection")
	u.pvInfo.SetText("")
}

func (c *Chooser) fillPreview(icon *widget.Box, name, info *widget.Label, e Entry) {
	icon.Clear()
	icon.Append(c.entryIcon(e, previewIconPx), false)
	name.SetText(e.Name())
	info.SetText(previewInfo(e))
}

// --- system defaults ------------------------------------------------

// gnomeCopiedFiles is the clipboard and drag format GNOME's file
// managers read: the operation on the first line, then one URI a line.
const gnomeCopiedFiles = "x-special/gnome-copied-files"

// dragOut is what dragging rows of the listing shown offers another
// app: their files as a uri list (and the GNOME file managers' form),
// to copy, never to move, so a drop cannot take the files away. A
// listing still loading shows stale rows and offers nothing.
func (c *Chooser) dragOut(rows []int) *widget.DragContent {
	if c.s == nil || c.s.loading {
		return nil
	}
	var paths []string
	for _, i := range rows {
		if i >= 0 && i < len(c.s.entries) {
			paths = append(paths, c.s.entries[i].Path)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	return &widget.DragContent{Content: filesContent(paths), Actions: transfer.ActionCopy}
}

// filesContent offers paths as a uri list and GNOME's copied-files
// form.
func filesContent(paths []string) transfer.Content {
	copied := []string{"copy"}
	for _, p := range paths {
		copied = append(copied, transfer.FileURI(p))
	}
	return transfer.Merge(transfer.Files(paths...), transfer.Bytes(gnomeCopiedFiles, []byte(strings.Join(copied, "\n"))))
}

// droppedPath is the first local path in a text/uri-list payload.
func droppedPath(data []byte) (string, bool) {
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if p, ok := fileuri.Path(line); ok {
			return p, true
		}
	}
	return "", false
}

// typeAhead is a file view that hands characters typed into it to
// typed: the view keeps every other key.
type typeAhead struct {
	*widget.List
	typed func(rune)
}

// InsertRune implements widget.RuneHandler: / or ~ starts a typed
// path, any other character a search.
func (t *typeAhead) InsertRune(r rune) { t.typed(r) }

// HitTest answers the view itself as the wrapper, so a click focuses
// what takes the typing.
func (t *typeAhead) HitTest(p widget.Point) widget.Widget {
	w := t.List.HitTest(p)
	if w == widget.Widget(t.List) {
		return t
	}
	return w
}
