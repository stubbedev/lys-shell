package printdialog

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/ipp"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/shell/dialogqueue"
	"github.com/stubbedev/lys-shell/shell/reveal"
	"github.com/stubbedev/lys-shell/shell/widgets"
)

// The sheet's geometry (shell/print: the surface's width request, the
// printer list's content heights, the form grid's spacing).
const (
	surfaceWidth  = 460
	listMinHeight = 64
	listMaxHeight = 240
	escapeKeycode = 1
)

// documentFormat lets CUPS type the document, as GTK's backend does for
// a source fd.
const documentFormat = "application/octet-stream"

// The scheduler calls' bounds: listing the queues, and spooling a job.
const (
	listTimeout  = 10 * time.Second
	spoolTimeout = 2 * time.Minute
)

// tokenLifetime is how long a prepared job waits for its Print.
const tokenLifetime = time.Hour

// Window is the overlay surface the dialog maps (app.LayerWindow).
type Window interface{ Close() }

// Deps are the dialog's collaborators.
type Deps struct {
	// Config is the live config (the print animation).
	Config func() *config.Config
	// Open maps the overlay; Invoke runs on the UI loop.
	Open   func(cfg app.LayerConfig) (Window, error)
	Invoke func(func())
	// Font shapes the text; Sheet styles the surface, the labels'
	// and entries' ink included.
	Font  render.Font
	Sheet *widget.Stylesheet
	// Printers lists the CUPS queues and Spool sends a job to one.
	Printers func(context.Context) ([]ipp.Printer, error)
	Spool    func(context.Context, ipp.Job) error
	// PickOutput lets the user choose the file Print to File writes
	// (the shell's file chooser), starting from suggested, and calls
	// chosen with its path on the loop; never on a cancel. It returns
	// at once.
	PickOutput func(suggested string, chosen func(path string))
	// Home is the home directory, for the suggested output file.
	Home string
	// User names the job's owner.
	User string
}

// queue is one printer row: its name, the detail line, and what it
// offers.
type queue struct {
	name, detail string
	caps         capabilities
}

// prepared is a confirmed form stashed under its token until Print,
// for the app that prepared it until it expires.
type prepared struct {
	printer string
	form    form
	appID   string
	expires time.Time
}

// Dialog is the dev.stubbe.lys.Print1 host: one dialog showing one
// request; requests arriving meanwhile wait their turn, and dismissing
// a request cancels it, showing or waiting. Prepare and Print may be
// called from any goroutine; the UI runs on the loop.
type Dialog struct {
	d       Deps
	win     Window
	rev     *widget.Revealer
	closing bool
	answer  func(p prepared, ok bool)
	queue   dialogqueue.Queue

	mu   sync.Mutex
	jobs map[uint32]prepared
	// now is the clock tokens expire by (time.Now; a fake in tests).
	now func() time.Time
}

// New builds the host; nothing maps until a request.
func New(d Deps) *Dialog { return &Dialog{d: d, jobs: map[uint32]prepared{}, now: time.Now} }

// Prepare shows the printer picker and settings form for the request;
// a confirmed one is stashed under the returned token, a random one
// only the same app can print with, until it expires.
func (h *Dialog) Prepare(c portalreq.Caller, r PrepareRequest) (bool, []Setting, PageSetup, uint32) {
	p, ok := h.ask(c, r)
	if !ok {
		return false, nil, PageSetup{}, 0
	}
	token, err := h.stash(c.AppID, p)
	if err != nil {
		log.Printf("print: %v", err)
		return false, nil, PageSetup{}, 0
	}
	return true, p.form.settings(p.printer), p.form.pageSetup(), token
}

// ask shows the dialog for the request and waits for the user.
func (h *Dialog) ask(c portalreq.Caller, r PrepareRequest) (prepared, bool) {
	queues := h.queues()
	type answer struct {
		p  prepared
		ok bool
	}
	ch := make(chan answer, 1)
	reply := func(p prepared, ok bool) { ch <- answer{p, ok} }
	h.d.Invoke(func() {
		h.queue.Enqueue(c.Handle, func() {
			h.answer = reply
			h.show(h.build(queues, r))
		}, func() { reply(prepared{}, false) })
	})
	a := <-ch
	return a.p, a.ok
}

// stash keeps a prepared job for appID under a fresh random token,
// dropping the expired ones.
func (h *Dialog) stash(appID string, p prepared) (uint32, error) {
	var b [4]byte
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	for t, job := range h.jobs {
		if now.After(job.expires) {
			delete(h.jobs, t)
		}
	}
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return 0, fmt.Errorf("draw a print token: %w", err)
		}
		token := binary.LittleEndian.Uint32(b[:])
		if _, taken := h.jobs[token]; token == 0 || taken {
			continue
		}
		p.appID, p.expires = appID, now.Add(tokenLifetime)
		h.jobs[token] = p
		return token, nil
	}
}

// Dismiss cancels the request at handle: the dialog showing for it is
// taken down, a waiting one never shows. Another request's dialog
// stays.
func (h *Dialog) Dismiss(handle string) {
	h.d.Invoke(func() {
		if h.queue.Dismiss(handle) {
			h.finish(prepared{}, false)
		}
	})
}

// Print spools document to the printer the app prepared under token,
// once; token 0 shows the dialog first. A token another app prepared,
// or one expired, prints nothing. The dialog owns and closes the
// document.
func (h *Dialog) Print(c portalreq.Caller, title string, document *os.File, token uint32) bool {
	defer func() { _ = document.Close() }()
	job, ok := h.claim(c.AppID, token)
	if token == 0 {
		job, ok = h.ask(c, PrepareRequest{Title: title})
	}
	if !ok {
		return false
	}
	if job.printer == fileQueue {
		return toFile(job.form.output, document)
	}
	ctx, cancel := context.WithTimeout(context.Background(), spoolTimeout)
	defer cancel()
	err := h.d.Spool(ctx, ipp.Job{
		Printer: job.printer, Title: title, User: h.d.User, Format: documentFormat,
		Attrs: job.form.jobAttrs(), Document: document,
	})
	if err != nil {
		log.Printf("print: spooling to %s: %v", job.printer, err)
		return false
	}
	return true
}

// claim takes the job prepared under token for appID, once.
func (h *Dialog) claim(appID string, token uint32) (prepared, bool) {
	if token == 0 {
		return prepared{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	job, ok := h.jobs[token]
	switch {
	case !ok:
		log.Printf("print: no prepared printer for token %d", token)
		return prepared{}, false
	case job.appID != appID:
		log.Printf("print: token %d is %q's, not %q's", token, job.appID, appID)
		return prepared{}, false
	}
	delete(h.jobs, token)
	if h.now().After(job.expires) {
		log.Printf("print: token %d expired", token)
		return prepared{}, false
	}
	return job, true
}

// toFile writes the document to the file the user chose for Print to
// File.
func toFile(path string, document io.Reader) bool {
	if path == "" {
		log.Printf("print: to file: no file chosen")
		return false
	}
	out, err := os.Create(path) //nolint:gosec // the file the user chose
	if err != nil {
		log.Printf("print: to file: %v", err)
		return false
	}
	_, err = io.Copy(out, document)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		log.Printf("print: to file %s: %v", path, err)
		return false
	}
	return true
}

// queues are the CUPS printers, then Print to File; an unreachable
// scheduler leaves just the file.
func (h *Dialog) queues() []queue {
	var out []queue
	ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
	defer cancel()
	printers, err := h.d.Printers(ctx)
	if err != nil && !ipp.IsNotFound(err) {
		log.Printf("print: listing printers: %v", err)
	}
	for _, p := range printers {
		out = append(out, queue{p.Name, printerDetail(p.Location, printerStatus(p)), capabilitiesOf(&p)})
	}
	return append(out, queue{fileQueue, "", capabilitiesOf(nil)})
}

// finish answers the request showing, plays the exit, and shows the
// next request waiting.
func (h *Dialog) finish(p prepared, ok bool) {
	if h.answer == nil {
		return
	}
	answer := h.answer
	h.answer = nil
	answer(p, ok)
	h.hide()
	h.queue.Done()
}

// label is a one-line label at lys-ui's text size.
func (h *Dialog) label(text string, classes ...string) *widget.Label {
	l := widget.NewLabel(h.d.Font, widget.Current().TextSize, text, 0)
	l.AddClass(classes...)
	return l
}

func (h *Dialog) dropdown(items []string) *widget.Dropdown {
	return widget.NewDropdown(h.d.Font, widget.Current().TextSize, items, 0)
}

// labelsOf is a choice list's labels.
func labelsOf[T comparable](items []T, label func(T) string) []string {
	out := make([]string, len(items))
	for i, v := range items {
		out[i] = label(v)
	}
	return out
}

// printerRow is printer_row: the name over the location and status as
// a dim caption, inset by the theme's padding.
func (h *Dialog) printerRow(q queue) widget.Widget {
	th := widget.Current()
	row := widget.NewBox(widget.Column, 0, 0)
	row.SetMargin(render.Insets{Top: th.Padding, Right: th.Padding + th.Padding/2, Bottom: th.Padding, Left: th.Padding + th.Padding/2})
	row.Append(h.label(q.name, "print-printer-name"), false)
	if q.detail != "" {
		row.Append(h.label(q.detail, "print-printer-detail", css.Caption, css.DimLabel), false)
	}
	return row
}

// choices is one dropdown bound to a queue's list of values: refill
// puts a queue's in, keeping the chosen value when that queue offers
// it, else selecting want.
type choices[T comparable] struct {
	drop   *widget.Dropdown
	values []T
	label  func(T) string
}

func (c *choices[T]) refill(values []T, want T) {
	if i := c.drop.Selected(); i >= 0 && i < len(c.values) && slices.Contains(values, c.values[i]) {
		want = c.values[i]
	}
	c.values = values
	c.drop.SetItems(labelsOf(values, c.label), max(slices.Index(values, want), 0))
}

func (c *choices[T]) value() T { return c.values[max(c.drop.Selected(), 0)] }

// build is the sheet (the print view!): the title, the printer list in
// a scroll that grows with it from 64 to 240px, the .print-form grid of
// labels and controls, Print to File's output row, and the actions.
// The form starts from the app's settings; the paper, sides, color and
// quality offered follow the printer selected.
func (h *Dialog) build(queues []queue, r PrepareRequest) widget.Widget {
	list := widget.NewListBox()
	list.SetClasses("print-printer-list")
	for _, q := range queues {
		list.Append(h.printerRow(q))
	}
	start := max(slices.IndexFunc(queues, func(q queue) bool { return q.name == settingOf(r.Settings, "printer") }), 0)
	list.Select(start)
	printers := widget.NewScroll(list)
	printers.FillX = true
	printers.PropagateNaturalHeight = true
	printers.SetMaxContentHeight(listMaxHeight)
	printers.SetSizeRequest(0, listMinHeight)

	initial := initialForm(r, queues[start].caps)
	th := widget.Current()
	copies := widget.NewSpinButton(h.d.Font, th.TextSize, 0, minCopies, maxCopies, 1, 0)
	copies.SetValue(float64(initial.copies))
	pageKinds := []pagesKind{pagesAll}
	if r.HasCurrentPage {
		pageKinds = append(pageKinds, pagesCurrent)
	}
	if r.HasSelectedPages {
		pageKinds = append(pageKinds, pagesSelection)
	}
	pageKinds = append(pageKinds, pagesRanges)
	pagesDrop := &choices[pagesKind]{drop: h.dropdown(nil), label: func(k pagesKind) string {
		return [...]string{"All pages", "Current page", "Selection", "Range"}[k]
	}}
	pagesDrop.refill(pageKinds, initial.pages)
	ranges := widget.NewEntry(h.d.Font, th.TextSize, 0)
	ranges.SetPlaceholder("e.g. 1-5, 8")
	ranges.SetText(initial.ranges)
	ranges.OnChanged = func(text string) {
		if text != "" {
			pagesDrop.drop.SetSelected(slices.Index(pagesDrop.values, pagesRanges))
		}
	}
	orientation := h.dropdown(orientations)
	if initial.landscape {
		orientation.SetSelected(1)
	}
	papersOf := map[string]paper{}
	paperDrop := &choices[string]{drop: h.dropdown(nil), label: func(v string) string { return papersOf[v].label }}
	sideDrop := &choices[string]{drop: h.dropdown(nil), label: func(v string) string { return sideLabels[v] }}
	colorDrop := &choices[string]{drop: h.dropdown(nil), label: func(v string) string { return colorLabels[v] }}
	qualityDrop := &choices[int32]{drop: h.dropdown(nil), label: func(v int32) string { return qualityLabels[v] }}
	offer := func(c capabilities, want form) {
		names := make([]string, len(c.papers))
		for i, p := range c.papers {
			names[i] = p.gtk
			papersOf[p.gtk] = p
		}
		paperDrop.refill(names, pick(names, want.paper.gtk, c.paper))
		sideDrop.refill(c.sides, pick(c.sides, want.side, c.side))
		colorDrop.refill(c.colors, pick(c.colors, want.color, c.color))
		qualityDrop.refill(c.qualities, pick(c.qualities, want.quality, c.quality))
	}
	offer(queues[start].caps, initial)

	grid := widget.NewGrid(2*th.Spacing, th.Spacing)
	grid.AddClass("print-form")
	for i, row := range []struct {
		label   string
		control interface {
			widget.Widget
			SetHExpand(bool)
		}
	}{
		{"Copies", copies},
		{"Pages", pagesDrop.drop},
		{"Range", ranges},
		{"Orientation", orientation},
		{"Paper size", paperDrop.drop},
		{"Color", colorDrop.drop},
		{"Two-sided", sideDrop.drop},
		{"Quality", qualityDrop.drop},
	} {
		grid.Attach(h.label(row.label, "print-form-label", css.DimLabel), 0, i, 1, 1)
		row.control.SetHExpand(true)
		grid.Attach(row.control, 1, i, 1, 1)
	}

	acceptLabel := r.AcceptLabel
	if acceptLabel == "" {
		acceptLabel = "Print"
	}
	output := SuggestedOutput(h.d.Home, outputFormat(r.OutputFormats))
	outputLabel := h.label(output, "print-output-path")
	replaceNote := widgets.DialogText(h.d.Font, "", "print-output-replace", css.Warning)
	replaceNote.SetVisible(false)
	confirmedReplace := false
	setOutput := func(path string) {
		output, confirmedReplace = path, false
		outputLabel.SetText(path)
		replaceNote.SetVisible(false)
	}
	outputRow := widget.NewBox(widget.Row, th.Spacing, 0)
	outputRow.AddClass("print-output")
	outputRow.Append(outputLabel, true)
	outputRow.Append(widgets.DialogButton(h.d.Font, "Choose…", "print-output-choose", css.Outline, func() {
		if h.d.PickOutput != nil {
			h.d.PickOutput(output, setOutput)
		}
	}), false)

	selectQueue := func(i int) {
		if i < 0 || i >= len(queues) {
			return
		}
		outputRow.SetVisible(queues[i].name == fileQueue)
		replaceNote.SetVisible(false)
		confirmedReplace = false
		offer(queues[i].caps, form{paper: papersOf[paperDrop.value()], side: sideDrop.value(), color: colorDrop.value(), quality: qualityDrop.value()})
	}
	list.OnSelectionChanged = func(rows []int) {
		if len(rows) == 1 {
			selectQueue(rows[0])
		}
	}
	selectQueue(start)

	confirm := func() {
		sel := list.Selected()
		if sel < 0 || sel >= len(queues) {
			h.finish(prepared{}, false)
			return
		}
		f := form{
			copies: int(copies.Value()), pages: pagesDrop.value(), ranges: ranges.Text(),
			landscape: orientation.Selected() == 1, paper: papersOf[paperDrop.value()],
			color: colorDrop.value(), side: sideDrop.value(), quality: qualityDrop.value(),
		}
		if queues[sel].name == fileQueue {
			if _, err := os.Stat(output); err == nil && !confirmedReplace {
				confirmedReplace = true
				replaceNote.SetText(filepath.Base(output) + " exists. " + acceptLabel + " again to replace it.")
				replaceNote.SetVisible(true)
				return
			}
			f.output = output
		}
		h.finish(prepared{printer: queues[sel].name, form: f}, true)
	}
	actions := widgets.DialogActions(
		widgets.DialogButton(h.d.Font, "Cancel", "portal-dialog-cancel", css.Outline, func() { h.finish(prepared{}, false) }),
		widgets.DialogButton(h.d.Font, acceptLabel, "portal-dialog-confirm", css.SuggestedAction, confirm),
	)

	surface := widgets.DialogCard("print-surface", surfaceWidth)
	surface.Append(widgets.DialogTitle(h.d.Font, "Print", "print-title"), false)
	surface.Append(printers, false)
	surface.Append(grid, false)
	surface.Append(outputRow, false)
	surface.Append(replaceNote, false)
	surface.Append(actions, false)
	return surface
}

// settingOf is the value of key among settings, "" without one.
func settingOf(settings []Setting, key string) string {
	for _, s := range settings {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}

// outputFormat is the file format Print to File writes: pdf when the
// app can write it (or names none), else the first it names.
func outputFormat(formats []string) string {
	if len(formats) == 0 || slices.Contains(formats, "pdf") {
		return "pdf"
	}
	return formats[0]
}

// show maps the overlay with tree and plays the entry.
func (h *Dialog) show(tree widget.Widget) {
	if h.win != nil {
		h.win.Close()
		h.win, h.closing = nil, false
	}
	// window.print-window > revealer > the sheet, on an unanchored
	// layer the compositor centers, sized to its content.
	h.rev = widget.NewRevealer(tree)
	root := widget.NewBox(widget.Column, 0, 0)
	root.SetElement("window")
	root.AddClass("print-window")
	if h.d.Sheet != nil {
		root.AttachStylesheet(h.d.Sheet)
	}
	root.Append(h.rev, true)
	win, err := h.d.Open(app.LayerConfig{
		Layer:         app.LayerOverlay,
		ExclusiveZone: -1,
		Keyboard:      app.KeyboardOnDemand,
		Namespace:     "lys-shell-print",
		Root:          root,
		OnKey: func(_ *widget.Router, keycode uint32, _ app.Mods) {
			if keycode == escapeKeycode {
				h.finish(prepared{}, false)
			}
		},
	})
	if err != nil {
		log.Printf("print: cannot map the overlay: %v", err)
		h.finish(prepared{}, false)
		return
	}
	h.win = win
	reveal.Show(h.rev, h.d.Config().Animations, config.AnimPrint)
}

// hide plays the exit and closes the surface.
func (h *Dialog) hide() {
	if h.win == nil || h.closing {
		return
	}
	h.closing = true
	win := h.win
	reveal.Hide(h.rev, h.d.Config().Animations, config.AnimPrint, func() {
		win.Close()
		if h.win == win {
			h.win, h.rev, h.closing = nil, nil, false
		}
	})
}
