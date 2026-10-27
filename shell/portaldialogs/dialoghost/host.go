package dialoghost

import (
	"log"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/desktopentry"
	"github.com/stubbedev/lys-shell/internal/fileuri"
	"github.com/stubbedev/lys-shell/internal/imagedecode"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/shell/dialogqueue"
	"github.com/stubbedev/lys-shell/shell/portaldialogs"
	"github.com/stubbedev/lys-shell/shell/reveal"
	"github.com/stubbedev/lys-shell/shell/widgets"
	"github.com/stubbedev/lys-shell/styling"
)

// The surface's sizes (shell/portal_dialogs: set_width_request,
// pixel sizes, the app list's min-content-height).
const (
	surfaceWidth = 420
	headerPx     = 64
	// requesterIconPx is the requester's app icon, at the caption's
	// size.
	requesterIconPx = 16
	// requesterChars caps the requester's name in the card header, so a
	// long one ellipsizes instead of squeezing the title.
	requesterChars = 24
	wallpaperPx    = 180
	appIconPx      = 32
	appListHeight  = 360
	escapeKeycode  = 1
)

// Window is the overlay surface the dialog maps (app.LayerWindow).
type Window interface {
	Close()
	SetFocus(w widget.Widget) error
}

// Deps are the dialogs' collaborators.
type Deps struct {
	// Config is the live config (the dialog animation).
	Config func() *config.Config
	// Open maps the overlay; Invoke runs on the UI loop.
	Open   func(cfg app.LayerConfig) (Window, error)
	Invoke func(func())
	// Font shapes the text; Sheet styles the surface, the labels'
	// and entries' ink included.
	Font  render.Font
	Sheet *widget.Stylesheet
	// Candidates are the apps offered for a type (desktopentry's
	// RecommendedFor, else every app); SetDefault records the
	// "always use" choice.
	Candidates func(choices []string, contentType string) []desktopentry.App
	SetDefault func(id, contentType string) error
	// PickImage lets the user choose an image file (the shell's file
	// chooser) and calls chosen with its path on the loop; never on a
	// cancel. It returns at once.
	PickImage func(chosen func(path string))
	// Describe names the app a request comes from: its display name
	// and icon (desktopentry.Describe).
	Describe func(appID string) (name, icon string)
}

// Dialogs is the dev.stubbe.lys.PortalDialogs1 host: one overlay showing
// one request, naming the app that asks; requests arriving meanwhile
// wait their turn, and dismissing a request answers it no (or "" for
// an app chooser), showing or waiting. Its portaldialogs.Host methods may be called
// from any goroutine; the rest runs on the loop.
type Dialogs struct {
	d       Deps
	win     Window
	rev     *widget.Revealer
	answer  func(confirmed bool, choice string)
	queue   dialogqueue.Queue
	closing bool
	// app and appIcon name the requester of the dialog showing.
	app, appIcon string
	// choosers are the app chooser requests open, showing or waiting,
	// by handle.
	choosers map[string]*appChooser
	// focus is the widget the dialog being built types into: its surface
	// takes the keyboard and focuses it. Dialogs without one take the
	// keyboard only when clicked.
	focus widget.Widget
}

// appChooser is one open app chooser request: the request, kept
// current by UpdateChoices, and while it shows the refresh refilling
// its list from it.
type appChooser struct {
	req     portaldialogs.ChooserRequest
	refresh func()
}

// New builds the host; nothing maps until a request.
func New(d Deps) *Dialogs { return &Dialogs{d: d, choosers: map[string]*appChooser{}} }

// ask shows a dialog for c on the loop and waits for its answer.
func (h *Dialogs) ask(c portalreq.Caller, build func(answer func(bool, string)) widget.Widget) (bool, string) {
	type result struct {
		ok     bool
		choice string
	}
	ch := make(chan result, 1)
	h.d.Invoke(func() {
		h.queue.Enqueue(c.Handle, func() {
			h.answer = func(ok bool, choice string) { ch <- result{ok, choice} }
			h.app, h.appIcon = h.d.Describe(c.AppID)
			h.show(build(h.finish))
		}, func() { ch <- result{} })
	})
	r := <-ch
	return r.ok, r.choice
}

// Dismiss answers no to the request at handle: the dialog showing for
// it is taken down, a waiting one never shows. Another request's
// dialog stays.
func (h *Dialogs) Dismiss(handle string) {
	h.d.Invoke(func() {
		if h.queue.Dismiss(handle) {
			h.finish(false, "")
		}
	})
}

// finish answers the dialog showing, plays its exit, and shows the
// next request waiting.
func (h *Dialogs) finish(ok bool, choice string) {
	if h.answer == nil {
		return
	}
	answer := h.answer
	h.answer = nil
	answer(ok, choice)
	h.hide()
	h.queue.Done()
}

// Access is the grant/deny prompt: the permission's icon above the
// title, the subtitle and body below it.
func (h *Dialogs) Access(c portalreq.Caller, r portaldialogs.AccessRequest) (bool, []portalreq.Selection) {
	var picked []portalreq.Selection
	ok, _ := h.ask(c, func(answer func(bool, string)) widget.Widget {
		detail := strings.Join(nonEmpty(r.Subtitle, r.Body), "\n")
		rows, collect := widgets.ChoiceRows(h.d.Font, r.Choices, "portal-dialog-choice")
		grant := func(ok bool, s string) {
			if ok {
				picked = collect()
			}
			answer(ok, s)
		}
		return h.confirm(grant, h.headerIcon(r.Icon), r.Title, detail, r.DenyLabel, r.GrantLabel, rows...)
	})
	if !ok {
		return false, nil
	}
	return true, picked
}

// Account asks to share the user's name and avatar, previewing it.
func (h *Dialogs) Account(c portalreq.Caller, reason string, info portaldialogs.AccountInfo) (bool, portaldialogs.AccountInfo) {
	shared := info
	ok, _ := h.ask(c, func(answer func(bool, string)) widget.Widget {
		if reason == "" {
			reason = "An application is requesting your name and avatar."
		}
		picture := widget.NewBox(widget.Column, 0, 0)
		var image string
		setImage := func(path string) {
			image = path
			picture.Clear()
			if header := h.headerFile(path, headerPx); header != nil {
				picture.Append(header, false)
			} else {
				picture.Append(h.headerIcon("ld-circle-user-round-symbolic"), false)
			}
		}
		setImage(info.Image)
		name := widget.NewEntry(h.d.Font, widgets.TextPx(styling.Body, 1), 0)
		name.AddClass("portal-dialog-entry")
		name.SetText(info.Name)
		choose := widgets.DialogButton(h.d.Font, "Choose picture…", "portal-dialog-picture", css.Outline, func() {
			if h.d.PickImage != nil {
				h.d.PickImage(setImage)
			}
		})
		remove := widgets.DialogButton(h.d.Font, "Remove picture", "portal-dialog-picture", css.Outline, func() { setImage("") })
		share := func(ok bool, s string) {
			if ok {
				shared = portaldialogs.AccountInfo{Name: strings.TrimSpace(name.Text()), Image: image}
				if shared.Name == "" {
					shared.Name = info.Name
				}
			}
			answer(ok, s)
		}
		pictureRow := widget.NewBox(widget.Row, widget.Current().Spacing, 0)
		pictureRow.Append(widget.NewSpacer(0, 0), true)
		pictureRow.Append(choose, false)
		pictureRow.Append(remove, false)
		pictureRow.Append(widget.NewSpacer(0, 0), true)
		return h.confirm(share, picture, "Share account information?", reason, "Cancel", "Share", name, pictureRow)
	})
	if !ok {
		return false, portaldialogs.AccountInfo{}
	}
	return true, shared
}

// Password asks for a password in a masked entry; with Confirm it asks
// twice and accepts only matching, non-empty entries. Enter submits.
func (h *Dialogs) Password(c portalreq.Caller, r portaldialogs.PasswordRequest) (bool, string) {
	return h.ask(c, func(answer func(bool, string)) widget.Widget {
		entry := h.passwordEntry("Password")
		rows := []widget.Widget{entry}
		var again *widget.Entry
		if r.Confirm {
			again = h.passwordEntry("Repeat the password")
			rows = append(rows, again)
		}
		status := h.label("", "portal-dialog-error")
		if r.Retry {
			status.SetText("The password was wrong. Try again.")
		}
		rows = append(rows, status)
		h.focus = entry
		submit := func() {
			switch {
			case entry.Text() == "":
				status.SetText("Enter a password.")
			case again != nil && again.Text() != entry.Text():
				status.SetText("The passwords do not match.")
			default:
				answer(true, entry.Text())
			}
		}
		entry.OnActivate = func(string) {
			if again == nil {
				submit()
			}
		}
		if again != nil {
			again.OnActivate = func(string) { submit() }
		}
		action := "Unlock"
		if r.Confirm {
			action = "Continue"
		}
		return h.surface(r.Title, r.Body, rows, h.cancel("Cancel", answer), h.accept(action, submit))
	})
}

// passwordEntry is a masked dialog entry.
func (h *Dialogs) passwordEntry(placeholder string) *widget.Entry {
	e := widget.NewEntry(h.d.Font, widgets.TextPx(styling.Body, 1), 0)
	e.AddClass("portal-dialog-entry")
	e.SetEcho(widget.EchoPassword)
	e.SetPlaceholder(placeholder)
	return e
}

// ConfirmWallpaper previews a file:// image before it is set.
func (h *Dialogs) ConfirmWallpaper(c portalreq.Caller, uri string) bool {
	ok, _ := h.ask(c, func(answer func(bool, string)) widget.Widget {
		path, _ := fileuri.Path(uri)
		return h.confirm(answer, h.headerFile(path, wallpaperPx), "Set as wallpaper?", "", "Cancel", "Set wallpaper")
	})
	return ok
}

// ChooseDevices asks which USB devices the app may open: a check
// button per device, all checked, naming it and the access asked for.
func (h *Dialogs) ChooseDevices(c portalreq.Caller, devices []portaldialogs.Device) (bool, []string) {
	picked := []string{}
	ok, _ := h.ask(c, func(answer func(bool, string)) widget.Widget {
		checks := make([]*widget.CheckButton, len(devices))
		rows := make([]widget.Widget, len(devices))
		for i, d := range devices {
			access := "read only"
			if d.Writable {
				access = "read and write"
			}
			check := widget.NewCheckButton(true)
			check.AddClass("portal-dialog-choice")
			check.SetChild(h.label(d.Name+" — "+access, "portal-dialog-choice-label"))
			checks[i], rows[i] = check, check
		}
		allow := func(ok bool, s string) {
			if ok {
				for i, check := range checks {
					if check.Checked() {
						picked = append(picked, devices[i].ID)
					}
				}
			}
			answer(ok, s)
		}
		return h.confirm(allow, h.headerIcon("ld-usb-symbolic"), "Allow access to USB devices?",
			"The app can use the devices you leave checked until you revoke it.", "Deny", "Allow", rows...)
	})
	if !ok {
		return false, nil
	}
	return true, picked
}

// ConfirmInstall confirms a dynamic launcher: its icon, the URL a web
// app opens, and its name, in an entry when it is editable. An emptied
// name installs under the default one.
func (h *Dialogs) ConfirmInstall(c portalreq.Caller, r portaldialogs.LauncherRequest) (bool, string) {
	chosen := r.Name
	ok, _ := h.ask(c, func(answer func(bool, string)) widget.Widget {
		var extra []widget.Widget
		title := "Install “" + r.Name + "”?"
		var entry *widget.Entry
		if r.EditableName {
			title = "Install launcher?"
			entry = widget.NewEntry(h.d.Font, widgets.TextPx(styling.Body, 1), 0)
			entry.AddClass("portal-dialog-entry")
			entry.SetText(r.Name)
			extra = append(extra, entry)
		}
		body := ""
		if r.Target != "" {
			body = "Opens " + r.Target
		}
		install := func(ok bool, s string) {
			if ok && entry != nil {
				if name := strings.TrimSpace(entry.Text()); name != "" {
					chosen = name
				}
			}
			answer(ok, s)
		}
		return h.confirm(install, h.headerBytes(r.Icon), title, body, "Cancel", "Install", extra...)
	})
	if !ok {
		return false, ""
	}
	return true, chosen
}

// ChooseApplication is the app chooser: icon + name rows with a live
// search filter, and "always use" when the type is known. Its choices
// follow UpdateChoices while it is open.
func (h *Dialogs) ChooseApplication(c portalreq.Caller, r portaldialogs.ChooserRequest) string {
	ac := &appChooser{req: r}
	if c.Handle != "" {
		h.d.Invoke(func() { h.choosers[c.Handle] = ac })
		defer h.d.Invoke(func() { delete(h.choosers, c.Handle) })
	}
	_, choice := h.ask(c, func(answer func(bool, string)) widget.Widget { return h.chooser(answer, ac) })
	return choice
}

// UpdateChoices replaces the choices of the app chooser open for
// handle, refilling its list when it shows.
func (h *Dialogs) UpdateChoices(handle string, choices []string) bool {
	found := make(chan bool, 1)
	h.d.Invoke(func() {
		ac, ok := h.choosers[handle]
		if ok {
			ac.req.Choices = choices
			if ac.refresh != nil {
				ac.refresh()
			}
		}
		found <- ok
	})
	return <-found
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// label is a wrapping line at the body size.
func (h *Dialogs) label(text string, classes ...string) *widget.Label {
	return widgets.DialogText(h.d.Font, text, classes...)
}

// icon is a themed icon (or an icon file), nil for none.
func (h *Dialogs) icon(name string, px int) *widget.Icon {
	if name == "" {
		return nil
	}
	if filepath.IsAbs(name) {
		return widget.NewFileIcon(name, px)
	}
	return widget.NewThemeIconFallback(name, px)
}

// headerIcon is the header image slot showing a themed icon.
func (h *Dialogs) headerIcon(name string) widget.Widget {
	icon := h.icon(name, headerPx)
	if icon == nil {
		return nil
	}
	icon.AddClass("portal-dialog-image")
	return icon
}

// headerBytes is the header image slot showing an encoded image (PNG
// or JPEG, else SVG) fitted into the header square, nil for none.
func (h *Dialogs) headerBytes(data []byte) widget.Widget {
	if len(data) == 0 {
		return nil
	}
	decoded, err := imagedecode.Decode(data)
	if err != nil {
		icon := widget.NewSVGIcon(data, headerPx)
		icon.AddClass("portal-dialog-image")
		return icon
	}
	img := widget.NewImage(decoded)
	img.AddClass("portal-dialog-image")
	return widgets.Pin(img, headerPx, headerPx)
}

// headerFile is the header image slot showing an image file fitted
// into a px square, nil for none.
func (h *Dialogs) headerFile(path string, px int) widget.Widget {
	if path == "" {
		return nil
	}
	img := widget.NewFileImage(path)
	img.AddClass("portal-dialog-image")
	return widgets.Pin(img, px, px)
}

// requester is the row naming the app that asks: its icon and name.
func (h *Dialogs) requester() widget.Widget {
	row := widget.NewBox(widget.Row, widget.Current().Spacing/2, 0)
	row.AddClass("portal-dialog-requester-row")
	if icon := h.icon(h.appIcon, requesterIconPx); icon != nil {
		row.AppendAligned(icon, false, widget.AlignCenter)
	}
	l := h.label(h.app, "portal-dialog-requester", css.Caption, css.DimLabel)
	l.SetWrap(false)
	l.SetEllipsize(widget.EllipsizeEnd)
	l.SetMaxWidthChars(requesterChars)
	row.AppendAligned(l, false, widget.AlignCenter)
	return row
}

// surface is the dialog card: the title over the muted body ("" for
// none) with the requester at the header's end, the rows as its content
// and the buttons in its footer.
func (h *Dialogs) surface(title, body string, rows []widget.Widget, buttons ...*widget.Button) widget.Widget {
	card := widgets.DialogCard(h.d.Font, title, "portal-dialog-surface", surfaceWidth)
	card.SetDescription(body)
	card.SetAction(h.requester())
	if content := widgets.DialogBody(rows...); len(content.Children()) > 0 {
		card.SetContent(content)
	}
	for _, b := range buttons {
		card.AppendFooter(b)
	}
	return card
}

// confirm is a yes/no dialog.
func (h *Dialogs) confirm(answer func(bool, string), header widget.Widget, title, body, cancel, confirm string, extra ...widget.Widget) widget.Widget {
	var rows []widget.Widget
	if header != nil {
		rows = append(rows, widgets.Center(header))
	}
	rows = append(rows, extra...)
	return h.surface(title, body, rows, h.cancel(cancel, answer), h.accept(confirm, func() { answer(true, "") }))
}

// cancel is the outline button that declines.
func (h *Dialogs) cancel(text string, answer func(bool, string)) *widget.Button {
	return widgets.DialogButton(h.d.Font, text, "portal-dialog-cancel", css.Outline, func() { answer(false, "") })
}

// accept is the suggested action that confirms.
func (h *Dialogs) accept(text string, onClick func()) *widget.Button {
	return widgets.DialogButton(h.d.Font, text, "portal-dialog-confirm", css.SuggestedAction, onClick)
}

// chooser lists the request's apps in a ListBox of icon + name rows,
// the last choice selected; activating one answers its app id. The
// search hides the rows whose name lacks the query (the list's filter,
// as set_filter_func does).
func (h *Dialogs) chooser(answer func(bool, string), ac *appChooser) widget.Widget {
	contentType := ac.req.ContentType
	list := widget.NewListBox()
	list.AddClass("portal-dialog-app-list")
	var apps []desktopentry.App
	var names []string
	ac.refresh = func() {
		selected := ac.req.LastChoice
		if i := list.Selected(); i >= 0 {
			selected = apps[i].AppID()
		}
		for list.Len() > 0 {
			list.RemoveAt(list.Len() - 1)
		}
		apps = h.d.Candidates(ac.req.Choices, contentType)
		names = make([]string, len(apps))
		for i, a := range apps {
			names[i] = strings.ToLower(a.DisplayName())
			list.Append(h.appRow(a))
		}
		list.Select(slices.IndexFunc(apps, func(a desktopentry.App) bool { return a.AppID() == selected }))
		list.InvalidateFilter()
	}
	remember := widget.NewCheckButton(false)
	remember.AddClass("portal-dialog-remember")
	remember.SetChild(h.label("Always use for this file type", "portal-dialog-remember-label", css.DimLabel))
	list.OnActivate = func(i int) {
		if remember.Checked() && contentType != "" {
			if err := h.d.SetDefault(apps[i].ID, contentType); err != nil {
				log.Printf("appchooser: set-default %s for %s: %v", apps[i].ID, contentType, err)
			}
		}
		answer(true, apps[i].AppID())
	}
	query := ""
	list.SetFilterFunc(func(r *widget.ListBoxRow) bool {
		return query == "" || strings.Contains(names[r.Index()], query)
	})
	ac.refresh()
	// gtk::SearchEntry: the list filters on search-changed, after the
	// default delay.
	search := widget.NewSearchEntry(h.d.Font, widgets.TextPx(styling.Body, 1), "Search applications…")
	search.AddClass("portal-dialog-search")
	search.OnSearchChanged = func(q string) {
		query = strings.ToLower(q)
		list.InvalidateFilter()
	}
	scroll := widget.NewScroll(list)
	scroll.FillX, scroll.FillY = true, true
	scroll.SetSizeRequest(0, appListHeight)
	scroll.SetMaxContentHeight(appListHeight)
	var rememberRow widget.Widget
	if contentType != "" {
		rememberRow = remember
	}
	target := ""
	if ac.req.Target != "" {
		target = "Choose an application to open “" + ac.req.Target + "”."
	}
	open := h.accept("Open", func() {
		if i := list.Selected(); i >= 0 {
			list.OnActivate(i)
		}
	})
	return h.surface("Open With", target, []widget.Widget{search, scroll, rememberRow}, h.cancel("Cancel", answer), open)
}

// appRow is app_row: the app icon and its name over the ellipsized
// description, inset by the theme's padding.
func (h *Dialogs) appRow(a desktopentry.App) widget.Widget {
	th := widget.Current()
	row := widget.NewBox(widget.Row, th.Spacing, 0)
	row.SetMargin(render.Insets{Top: th.Padding, Right: th.Padding + th.Padding/2, Bottom: th.Padding, Left: th.Padding + th.Padding/2})
	icon := h.icon(a.Icon(), appIconPx)
	if icon == nil {
		icon = widget.NewThemeIcon("ld-app-window-symbolic", appIconPx)
	}
	icon.AddClass("portal-dialog-app-icon")
	row.Append(icon, false)
	text := widget.NewBox(widget.Column, 0, 0)
	text.SetVAlign(widget.AlignCenter)
	name := h.label(a.DisplayName(), "portal-dialog-app-name")
	name.SetWrap(false)
	text.Append(name, false)
	if desc := a.Comment(); desc != "" {
		// The GTK scrolled window keeps a long description from
		// widening the dialog: it ellipsizes at the width it is given.
		d := h.label(desc, "portal-dialog-app-desc", css.Caption, css.DimLabel)
		d.SetWrap(false)
		d.SetEllipsize(widget.EllipsizeEnd)
		d.SetMaxWidthChars(1)
		text.Append(d, false)
	}
	row.Append(text, true)
	return row
}

// show maps the overlay with tree and plays the entry.
func (h *Dialogs) show(tree widget.Widget) {
	if h.win != nil {
		h.win.Close()
		h.win, h.closing = nil, false
	}
	cfg := h.d.Config()
	// window.portal-dialog-window > revealer > the surface, on an
	// unanchored layer the compositor centers, sized to its content.
	h.rev = widget.NewRevealer(tree)
	root := widget.NewBox(widget.Column, 0, 0)
	root.SetElement("window")
	root.AddClass("portal-dialog-window")
	if h.d.Sheet != nil {
		root.AttachStylesheet(h.d.Sheet)
	}
	root.Append(h.rev, true)
	focus := h.focus
	h.focus = nil
	keyboard := app.KeyboardOnDemand
	if focus != nil {
		keyboard = app.KeyboardExclusive
	}
	win, err := h.d.Open(app.LayerConfig{
		Layer:         app.LayerOverlay,
		ExclusiveZone: -1,
		Keyboard:      keyboard,
		Namespace:     "lys-shell-portal-dialog",
		Root:          root,
		OnKey: func(_ *widget.Router, keycode uint32, _ app.Mods) {
			if keycode == escapeKeycode {
				h.finish(false, "")
			}
		},
	})
	if err != nil {
		log.Printf("portal dialogs: cannot map the overlay: %v", err)
		h.finish(false, "")
		return
	}
	h.win = win
	reveal.Show(h.rev, cfg.Animations, config.AnimDialog)
	if focus != nil {
		if err := win.SetFocus(focus); err != nil {
			log.Printf("portal dialogs: cannot focus the entry: %v", err)
		}
	}
}

// hide plays the exit and closes the surface.
func (h *Dialogs) hide() {
	if h.win == nil || h.closing {
		return
	}
	h.closing = true
	win := h.win
	reveal.Hide(h.rev, h.d.Config().Animations, config.AnimDialog, func() {
		win.Close()
		if h.win == win {
			h.win, h.rev, h.closing = nil, nil, false
		}
	})
}

// CandidateApps is the chooser's list (candidate_apps): the given
// app ids' entries, else the apps recommended for the type, else every app.
func CandidateApps(choices []string, contentType string) []desktopentry.App {
	dirs := desktopentry.ApplicationDirs()
	if len(choices) > 0 {
		ids := make([]string, len(choices))
		for i, id := range choices {
			ids[i] = desktopentry.FileID(id)
		}
		return desktopentry.FindApps(dirs, ids)
	}
	if contentType != "" {
		if rec := desktopentry.RecommendedFor(dirs, desktopentry.MimeAppsLists(), contentType); len(rec) > 0 {
			return rec
		}
	}
	return desktopentry.AllApps(dirs)
}
