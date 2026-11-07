package printdialog

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/atomicfile"
	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/ipp"
	"github.com/stubbedev/lys-shell/service/dnssd"
	"github.com/stubbedev/lys-shell/shell/dialogqueue"
	"github.com/stubbedev/lys-shell/shell/dialogsurface"
	"github.com/stubbedev/lys-shell/shell/keys"
	"github.com/stubbedev/lys-shell/shell/reveal"
	"github.com/stubbedev/lys-shell/shell/widgets"
	"github.com/stubbedev/lys-shell/styling"
)

// The sheet's geometry (shell/print: the surface's width request, the
// printer list's content heights, the form grid's spacing).
const (
	surfaceWidth  = 460
	listMinHeight = 64
	listMaxHeight = 240
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
	// Open maps the dialog; Invoke runs on the UI loop; KeySym reads
	// its keys through the seat's keymap.
	Open   func(dialogsurface.Spec) (Window, error)
	Invoke func(func())
	KeySym keys.Translator
	// Font shapes the text; Sheet styles the surface, the labels'
	// and entries' ink included.
	Font  render.Font
	Sheet *widget.Stylesheet
	// CUPS is the scheduler the queues, their state and the jobs go
	// through (*ipp.Client).
	CUPS Scheduler
	// LPOptions is the user's lpoptions file, whose Default names the
	// printer they chose over the scheduler's; "" for none.
	LPOptions string
	// RefreshEvery is how often the selected printer's state is read
	// again while the dialog is open.
	RefreshEvery time.Duration
	// DNSSD browses the network for driverless printers while the
	// dialog is open; nil, or a nil client while the system bus is not
	// up, lists the CUPS queues alone. Device reads a network printer's
	// attributes at its own URI (ipp.DeviceAttributes).
	DNSSD  func() *dnssd.Client
	Device func(ctx context.Context, uri string) (ipp.Printer, error)
	// PickOutput lets the user choose the file Print to File writes
	// (the shell's file chooser), starting from suggested, and calls
	// chosen with its path on the loop; never on a cancel. It returns
	// at once.
	PickOutput func(suggested string, chosen func(path string))
	// PresetsFile keeps the form each app last confirmed for each
	// printer; "" keeps them in memory.
	PresetsFile string
	// Home is the home directory, for the suggested output file.
	Home string
	// User names the job's owner.
	User string
}

// Scheduler is what the dialog asks CUPS: the queues, the default
// destination, one queue's state, and a job to spool.
type Scheduler interface {
	Printers(ctx context.Context) ([]ipp.Printer, error)
	Default(ctx context.Context) (string, error)
	Printer(ctx context.Context, name string) (ipp.Printer, error)
	Print(ctx context.Context, j ipp.Job) (int32, error)
	CreateLocalPrinter(ctx context.Context, deviceURI, name string) (string, error)
}

// queueKind is where a printer row's jobs go.
type queueKind uint8

const (
	// queueCUPS is a CUPS queue, whose state is followed.
	queueCUPS queueKind = iota
	// queueNetwork is a driverless printer found over DNS-SD, printed
	// to through a temporary queue CUPS makes for it.
	queueNetwork
	// queueFile is Print to File.
	queueFile
)

// queue is one printer row: its name, the detail line, and what it
// offers. A CUPS queue carries its printer-uuid and device URI, which
// a network printer is told apart from it by; a network printer its
// DNS-SD key, the URI CUPS is to reach it at, the URI its own
// attributes are read at, its UUID, and whether its capabilities were
// read yet.
type queue struct {
	kind         queueKind
	name, detail string
	caps         capabilities
	uuid, device string
	key, query   string
	capsRead     bool
}

// prepared is a confirmed form stashed under its token until Print,
// for the app that prepared it until it expires; device is a network
// printer's URI, which a temporary queue is made for.
type prepared struct {
	printer string
	device  string
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
	// sheet is the request's showing; presets are the forms each app
	// last confirmed per printer.
	sheet   *sheet
	presets *presets

	mu   sync.Mutex
	jobs map[uint32]prepared
	// now is the clock tokens expire by (time.Now; a fake in tests).
	now func() time.Time
}

// New builds the host; nothing maps until a request.
func New(d Deps) *Dialog {
	return &Dialog{d: d, jobs: map[uint32]prepared{}, now: time.Now, presets: loadPresets(d.PresetsFile)}
}

// Prepare shows the printer picker and settings form for the request;
// a confirmed one is stashed under the returned token, a random one
// only the same app can print with, until it expires.
func (h *Dialog) Prepare(c portaldialogs.Caller, r PrepareRequest) (bool, []Setting, PageSetup, uint32) {
	p, ok := h.ask(c, r)
	if !ok {
		return false, nil, PageSetup{}, 0
	}
	token, err := h.stash(c.AppID, p)
	if err != nil {
		log.Printf("print: %v", err)
		return false, nil, PageSetup{}, 0
	}
	return true, p.form.appSettings(p.printer), p.form.pageSetup(), token
}

// ask shows the dialog for the request and waits for the user.
func (h *Dialog) ask(c portaldialogs.Caller, r PrepareRequest) (prepared, bool) {
	type answer struct {
		p  prepared
		ok bool
	}
	ch := make(chan answer, 1)
	reply := func(p prepared, ok bool) { ch <- answer{p, ok} }
	h.d.Invoke(func() {
		h.queue.Enqueue(c.Handle, func() {
			h.answer = reply
			s := h.newSheet(c, r)
			h.sheet = s
			h.show(c, s.root)
			if h.sheet == s {
				s.load()
			}
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
func (h *Dialog) Print(c portaldialogs.Caller, title string, document *os.File, token uint32) bool {
	defer func() { _ = document.Close() }()
	job, ok := h.claim(c.AppID, token)
	drawn := drawnToSettings
	if token == 0 {
		job, ok = h.ask(c, PrepareRequest{Title: title})
		drawn = drawnAsIs
	}
	if !ok {
		return false
	}
	if job.printer == fileQueue {
		return toFile(job.form.output, document)
	}
	ctx, cancel := context.WithTimeout(context.Background(), spoolTimeout)
	defer cancel()
	printer := job.printer
	if job.device != "" {
		local, err := h.d.CUPS.CreateLocalPrinter(ctx, job.device, localQueueName(job.printer))
		if err != nil {
			log.Printf("print: a queue for %s: %v", job.printer, err)
			return false
		}
		printer = local
	}
	_, err := h.d.CUPS.Print(ctx, ipp.Job{
		Printer: printer, Title: title, User: h.d.User, Format: documentFormat,
		Attrs: job.form.jobAttrs(drawn), Document: document,
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
// File, whole: it goes to a temporary file beside it, renamed into
// place only once the copy is complete, so a document that fails
// midway leaves a file that was there as it was.
func toFile(path string, document io.Reader) bool {
	if path == "" {
		log.Printf("print: to file: no file chosen")
		return false
	}
	err := atomicfile.Write(path, 0o644, func(w io.Writer) error {
		_, err := io.Copy(w, document)
		return err
	})
	if err != nil {
		log.Printf("print: to file %s: %v", path, err)
		return false
	}
	return true
}

// finish answers the request showing, plays the exit, and shows the
// next request waiting.
func (h *Dialog) finish(p prepared, ok bool) {
	if h.answer == nil {
		return
	}
	answer := h.answer
	h.answer = nil
	if h.sheet != nil {
		h.sheet.close()
		h.sheet = nil
	}
	answer(p, ok)
	h.hide()
	h.queue.Done()
}

// label is a one-line label at the body size.
func (h *Dialog) label(text string, classes ...string) *widget.Label {
	l := widget.NewLabel(h.d.Font, widgets.TextPx(styling.Body, 1), text, 0)
	l.AddClass(classes...)
	return l
}

func (h *Dialog) dropdown(items []string) *widget.Dropdown {
	return widget.NewDropdown(h.d.Font, widgets.TextPx(styling.Body, 1), items, 0)
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
// a dim caption, inset by the theme's padding; the caption hides while
// there is nothing to say.
func (h *Dialog) printerRow(q queue) (widget.Widget, *widget.Label) {
	th := widget.Current()
	row := widget.NewBox(widget.Column, 0, 0)
	row.SetMargin(render.Insets{Top: th.Padding, Right: th.Padding + th.Padding/2, Bottom: th.Padding, Left: th.Padding + th.Padding/2})
	row.Append(h.label(q.name, "print-printer-name"), false)
	detail := h.label(q.detail, "print-printer-detail", css.Caption, css.DimLabel)
	detail.SetVisible(q.detail != "")
	row.Append(detail, false)
	return row, detail
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

// value is the value chosen, the zero value while there are none.
func (c *choices[T]) value() T {
	if len(c.values) == 0 {
		var zero T
		return zero
	}
	return c.values[max(c.drop.Selected(), 0)]
}

// set chooses want where offered.
func (c *choices[T]) set(want T) {
	if i := slices.Index(c.values, want); i >= 0 {
		c.drop.SetSelected(i)
	}
}

// clear forgets the choice, so the next refill takes the value it is
// asked for.
func (c *choices[T]) clear() { c.values = nil }

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
func (h *Dialog) show(caller portaldialogs.Caller, tree widget.Widget) {
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
	win, err := h.d.Open(dialogsurface.Spec{
		Caller: caller, Title: "Print",
		Overlay: app.LayerConfig{
			Layer:         app.LayerOverlay,
			ExclusiveZone: -1,
			Keyboard:      app.KeyboardOnDemand,
			Namespace:     "lys-shell-print",
			Root:          root,
			OnKey:         keys.OnEscape(h.d.KeySym, func() { h.finish(prepared{}, false) }),
		},
		Failed: func() { h.finish(prepared{}, false) },
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
