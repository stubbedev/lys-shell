package printdialog

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-lib/xdg"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/internal/ipp"
	"github.com/stubbedev/lys-shell/shell/widgets"
	"github.com/stubbedev/lys-shell/styling"
)

// lookingForPrinters is the row the printer list shows until the
// scheduler answers.
const lookingForPrinters = "Looking for printers…"

// sheet is one request's dialog: the printer list, which fills once
// the scheduler answers, the form, and Print to File's output. The
// selected printer's state is read again while it is open, until
// close.
type sheet struct {
	h *Dialog
	r PrepareRequest

	root   widget.Widget
	list   *widget.ListBox
	queues []queue
	// details are the queues' detail lines, by queue.
	details []*widget.Label
	// loading is set while the "looking" row heads the list; picked
	// once the user chose a queue; selecting while the sheet itself
	// selects one.
	loading, picked, selecting bool

	ctx        context.Context
	cancel     context.CancelFunc
	refreshing bool

	copies      *widget.SpinButton
	pagesDrop   *choices[pagesKind]
	ranges      *widget.Entry
	orientation *widget.Dropdown
	papersOf    map[string]paper
	paperDrop   *choices[string]
	sideDrop    *choices[string]
	colorDrop   *choices[string]
	qualityDrop *choices[int32]

	outputRow        *widget.Box
	outputLabel      *widget.Label
	replaceNote      *widget.Label
	output           string
	confirmedReplace bool
	acceptLabel      string
}

// newSheet builds the sheet for a request with Print to File ready and
// the printers still being looked for: the title, the printer list in
// a scroll that grows with it from 64 to 240px, the .print-form grid
// of labels and controls, Print to File's output row, and the actions.
func (h *Dialog) newSheet(r PrepareRequest) *sheet {
	s := &sheet{h: h, r: r, papersOf: map[string]paper{}}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.list = widget.NewListBox()
	s.list.SetClasses("print-printer-list")
	s.list.OnSelectionChanged = func(rows []int) {
		if len(rows) != 1 || s.selecting {
			return
		}
		s.picked = true
		s.selectQueue(rows[0])
	}
	printers := widget.NewScroll(s.list)
	printers.FillX = true
	printers.PropagateNaturalHeight = true
	printers.SetMaxContentHeight(listMaxHeight)
	printers.SetSizeRequest(0, listMinHeight)

	file := queue{name: fileQueue, caps: capabilitiesOf(nil)}
	initial := initialForm(r, file.caps)
	th := widget.Current()
	px := widgets.TextPx(styling.Body, 1)
	s.copies = widget.NewSpinButton(h.d.Font, px, 0, minCopies, maxCopies, 1, 0)
	s.copies.SetValue(float64(initial.copies))
	pageKinds := []pagesKind{pagesAll}
	if r.HasCurrentPage {
		pageKinds = append(pageKinds, pagesCurrent)
	}
	if r.HasSelectedPages {
		pageKinds = append(pageKinds, pagesSelection)
	}
	pageKinds = append(pageKinds, pagesRanges)
	s.pagesDrop = &choices[pagesKind]{drop: h.dropdown(nil), label: func(k pagesKind) string {
		return [...]string{"All pages", "Current page", "Selection", "Range"}[k]
	}}
	s.pagesDrop.refill(pageKinds, initial.pages)
	s.ranges = widget.NewEntry(h.d.Font, px, 0)
	s.ranges.SetPlaceholder("e.g. 1-5, 8")
	s.ranges.SetText(initial.ranges)
	s.ranges.OnChanged = func(text string) {
		if text != "" {
			s.pagesDrop.drop.SetSelected(slices.Index(s.pagesDrop.values, pagesRanges))
		}
	}
	s.orientation = h.dropdown(orientations)
	if initial.landscape {
		s.orientation.SetSelected(1)
	}
	s.paperDrop = &choices[string]{drop: h.dropdown(nil), label: func(v string) string { return s.papersOf[v].label }}
	s.sideDrop = &choices[string]{drop: h.dropdown(nil), label: func(v string) string { return sideLabels[v] }}
	s.colorDrop = &choices[string]{drop: h.dropdown(nil), label: func(v string) string { return colorLabels[v] }}
	s.qualityDrop = &choices[int32]{drop: h.dropdown(nil), label: func(v int32) string { return qualityLabels[v] }}
	s.offer(file.caps, initial)

	grid := widget.NewGrid(2*th.Spacing, th.Spacing)
	grid.AddClass("print-form")
	for i, row := range []struct {
		label   string
		control interface {
			widget.Widget
			SetHExpand(bool)
		}
	}{
		{"Copies", s.copies},
		{"Pages", s.pagesDrop.drop},
		{"Range", s.ranges},
		{"Orientation", s.orientation},
		{"Paper size", s.paperDrop.drop},
		{"Color", s.colorDrop.drop},
		{"Two-sided", s.sideDrop.drop},
		{"Quality", s.qualityDrop.drop},
	} {
		grid.Attach(h.label(row.label, "print-form-label", css.DimLabel), 0, i, 1, 1)
		row.control.SetHExpand(true)
		grid.Attach(row.control, 1, i, 1, 1)
	}

	s.acceptLabel = r.AcceptLabel
	if s.acceptLabel == "" {
		s.acceptLabel = "Print"
	}
	output, err := SuggestedOutput(xdg.OS, h.d.Home, outputFormat(r.OutputFormats))
	if err != nil {
		log.Printf("print dialog: %v", err)
	}
	s.output = output
	s.outputLabel = h.label(output, "print-output-path")
	s.replaceNote = widgets.DialogText(h.d.Font, "", "print-output-replace", css.Warning)
	s.replaceNote.SetVisible(false)
	s.outputRow = widget.NewBox(widget.Row, th.Spacing, 0)
	s.outputRow.AddClass("print-output")
	s.outputRow.Append(s.outputLabel, true)
	s.outputRow.Append(widgets.DialogButton(h.d.Font, "Choose…", "print-output-choose", css.Outline, func() {
		if h.d.PickOutput != nil {
			h.d.PickOutput(s.output, s.setOutput)
		}
	}), false)

	s.setQueues([]queue{file}, fileQueue, true)

	surface := widgets.DialogCard(h.d.Font, "Print", "print-surface", surfaceWidth)
	surface.SetContent(widgets.DialogBody(printers, grid, s.outputRow, s.replaceNote))
	surface.AppendFooter(widgets.DialogButton(h.d.Font, "Cancel", "portal-dialog-cancel", css.Outline, func() { h.finish(prepared{}, false) }))
	surface.AppendFooter(widgets.DialogButton(h.d.Font, s.acceptLabel, "portal-dialog-confirm", css.SuggestedAction, s.confirm))
	s.root = surface
	return s
}

// setOutput makes path Print to File's output.
func (s *sheet) setOutput(path string) {
	s.output, s.confirmedReplace = path, false
	s.outputLabel.SetText(path)
	s.replaceNote.SetVisible(false)
}

// offer puts a queue's capabilities in the form, keeping what want
// chose where the queue offers it.
func (s *sheet) offer(c capabilities, want form) {
	names := make([]string, len(c.papers))
	for i, p := range c.papers {
		names[i] = p.gtk
		s.papersOf[p.gtk] = p
	}
	s.paperDrop.refill(names, pick(names, want.paper.gtk, c.paper))
	s.sideDrop.refill(c.sides, pick(c.sides, want.side, c.side))
	s.colorDrop.refill(c.colors, pick(c.colors, want.color, c.color))
	s.qualityDrop.refill(c.qualities, pick(c.qualities, want.quality, c.quality))
}

// form is what the form holds now.
func (s *sheet) form() form {
	return form{
		copies: int(s.copies.Value()), pages: s.pagesDrop.value(), ranges: s.ranges.Text(),
		landscape: s.orientation.Selected() == 1, paper: s.papersOf[s.paperDrop.value()],
		color: s.colorDrop.value(), side: s.sideDrop.value(), quality: s.qualityDrop.value(),
	}
}

// rowOffset is where the queues' rows start: after the "looking" row
// while it shows.
func (s *sheet) rowOffset() int {
	if s.loading {
		return 1
	}
	return 0
}

// selected is the queue selected, false for none.
func (s *sheet) selected() (queue, bool) {
	i := s.list.Selected() - s.rowOffset()
	if i < 0 || i >= len(s.queues) {
		return queue{}, false
	}
	return s.queues[i], true
}

// selectQueue offers the queue at row i, keeping the form's choices
// where it offers them.
func (s *sheet) selectQueue(row int) {
	i := row - s.rowOffset()
	if i < 0 || i >= len(s.queues) {
		return
	}
	q := s.queues[i]
	s.outputRow.SetVisible(q.name == fileQueue)
	s.replaceNote.SetVisible(false)
	s.confirmedReplace = false
	s.offer(q.caps, s.form())
}

// setQueues lists queues, under the "looking" row while loading, and
// selects the one named want: the user's own pick instead once they
// made one.
func (s *sheet) setQueues(queues []queue, want string, loading bool) {
	if q, ok := s.selected(); ok && s.picked {
		want = q.name
	}
	s.loading = loading
	for s.list.Len() > 0 {
		s.list.RemoveAt(0)
	}
	if s.loading {
		looking := widget.NewBox(widget.Column, 0, 0)
		th := widget.Current()
		looking.SetMargin(render.Insets{Top: th.Padding, Right: th.Padding + th.Padding/2, Bottom: th.Padding, Left: th.Padding + th.Padding/2})
		looking.Append(s.h.label(lookingForPrinters, "print-printer-looking", css.DimLabel), false)
		s.list.Append(looking).SetSelectable(false)
	}
	s.queues = queues
	s.details = make([]*widget.Label, len(queues))
	for i, q := range queues {
		row, detail := s.h.printerRow(q)
		s.details[i] = detail
		s.list.Append(row)
	}
	i := max(slices.IndexFunc(queues, func(q queue) bool { return q.name == want }), 0)
	s.selecting = true
	s.list.Select(i + s.rowOffset())
	s.selecting = false
	s.selectQueue(i + s.rowOffset())
}

// load asks the scheduler for its queues and default and the user's
// lpoptions for theirs, off the loop, and lists them once they answer;
// then it follows the selected printer's state.
func (s *sheet) load() {
	cups, lpoptions := s.h.d.CUPS, s.h.d.LPOptions
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, listTimeout)
		defer cancel()
		printers, err := cups.Printers(ctx)
		if err != nil && !ipp.IsNotFound(err) {
			log.Printf("print: listing printers: %v", err)
		}
		def, err := cups.Default(ctx)
		if err != nil {
			log.Printf("print: the default printer: %v", err)
		}
		user := lpoptionsDefault(lpoptions)
		s.h.d.Invoke(func() {
			if s.ctx.Err() != nil {
				return
			}
			queues := make([]queue, 0, len(printers)+1)
			for _, p := range printers {
				queues = append(queues, cupsQueue(p))
			}
			queues = append(queues, queue{name: fileQueue, caps: capabilitiesOf(nil)})
			s.setQueues(queues, preferredQueue(queues, settingOf(s.r.Settings, "printer"), user, def), false)
			go s.follow()
		})
	}()
}

// cupsQueue is a CUPS printer's row.
func cupsQueue(p ipp.Printer) queue {
	return queue{name: p.Name, detail: printerDetail(p), caps: capabilitiesOf(&p), cups: true}
}

// follow reads the selected printer's state again every RefreshEvery
// until the sheet closes.
func (s *sheet) follow() {
	t := time.NewTicker(s.h.d.RefreshEvery)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.h.d.Invoke(s.refreshSelected)
		}
	}
}

// refreshSelected reads the selected printer's state off the loop and
// shows it in its row, one read at a time; the form stays as it is.
func (s *sheet) refreshSelected() {
	q, ok := s.selected()
	if s.ctx.Err() != nil || s.refreshing || !ok || !q.cups {
		return
	}
	s.refreshing = true
	cups, name := s.h.d.CUPS, q.name
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, listTimeout)
		defer cancel()
		p, err := cups.Printer(ctx, name)
		s.h.d.Invoke(func() {
			s.refreshing = false
			if s.ctx.Err() != nil {
				return
			}
			if err != nil {
				log.Printf("print: reading %s: %v", name, err)
				return
			}
			s.showState(p)
		})
	}()
}

// showState shows a printer's state in its row.
func (s *sheet) showState(p ipp.Printer) {
	i := slices.IndexFunc(s.queues, func(q queue) bool { return q.cups && q.name == p.Name })
	if i < 0 {
		return
	}
	s.queues[i].detail = printerDetail(p)
	s.details[i].SetText(s.queues[i].detail)
	s.details[i].SetVisible(s.queues[i].detail != "")
}

// close stops the loading and the state reads.
func (s *sheet) close() { s.cancel() }

// confirm answers the form for the queue selected; Print to File asks
// once before replacing a file that exists.
func (s *sheet) confirm() {
	q, ok := s.selected()
	if !ok {
		s.h.finish(prepared{}, false)
		return
	}
	f := s.form()
	if q.name != fileQueue {
		s.h.finish(prepared{printer: q.name, form: f}, true)
		return
	}
	file := s.output
	go func() {
		_, err := os.Stat(file)
		exists := err == nil
		s.h.d.Invoke(func() {
			if s.ctx.Err() != nil {
				return
			}
			if exists && !s.confirmedReplace {
				s.confirmedReplace = true
				s.replaceNote.SetText(filepath.Base(file) + " exists. " + s.acceptLabel + " again to replace it.")
				s.replaceNote.SetVisible(true)
				return
			}
			f.output = file
			s.h.finish(prepared{printer: q.name, form: f}, true)
		})
	}()
}
