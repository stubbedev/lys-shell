package printdialog

import (
	"cmp"
	"context"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-lib/xdg"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/internal/ipp"
	"github.com/stubbedev/lys-shell/service/dnssd"
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
	h     *Dialog
	r     PrepareRequest
	appID string

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

	// cupsQueues are the scheduler's queues once it answered, network
	// the printers found over DNS-SD, browser the browse finding them.
	cupsQueues []queue
	network    []queue
	browser    *dnssd.Browser

	copies      *widget.SpinButton
	pagesDrop   *choices[pagesKind]
	ranges      *widget.Entry
	orientation *widget.Dropdown
	papersOf    map[string]paper
	paperDrop   *choices[string]
	sideDrop    *choices[string]
	colorDrop   *choices[string]
	qualityDrop *choices[int32]
	// The layout controls, and the rows that hide with what a queue
	// does not offer.
	numberUpDrop  *choices[int32]
	layoutDrop    *choices[string]
	pageSetDrop   *choices[string]
	reverse       *widget.CheckButton
	collate       *widget.CheckButton
	scale         *widget.SpinButton
	scalingDrop   *choices[string]
	sourceDrop    *choices[string]
	mediaTypeDrop *choices[string]
	outputBinDrop *choices[string]
	optional      map[widget.Widget]*widget.Label

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
func (h *Dialog) newSheet(c portaldialogs.Caller, r PrepareRequest) *sheet {
	s := &sheet{h: h, r: r, appID: c.AppID, papersOf: map[string]paper{}, optional: map[widget.Widget]*widget.Label{}}
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

	file := fileQueueRow()
	initial := initialForm(h.presets.withPreset(r, c.AppID, file.name), file.caps)
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
	s.numberUpDrop = &choices[int32]{drop: h.dropdown(nil), label: func(v int32) string { return strconv.Itoa(int(v)) }}
	s.layoutDrop = &choices[string]{drop: h.dropdown(nil), label: func(v string) string { return layoutLabels[v] }}
	s.layoutDrop.refill(allLayouts, initial.layout)
	s.pageSetDrop = &choices[string]{drop: h.dropdown(nil), label: func(v string) string { return pageSetLabels[v] }}
	s.pageSetDrop.refill(allPageSets, initial.pageSet)
	s.reverse = widget.NewCheckButton(initial.reverse)
	s.collate = widget.NewCheckButton(initial.collate)
	s.scale = widget.NewSpinButton(h.d.Font, px, 0, minScale, maxScale, 1, 0)
	s.scale.SetValue(float64(initial.scale))
	s.scalingDrop = &choices[string]{drop: h.dropdown(nil), label: func(v string) string { return scalingLabels[v] }}
	automatic := func(v string) string { return cmp.Or(keywordLabel(v), "Automatic") }
	s.sourceDrop = &choices[string]{drop: h.dropdown(nil), label: automatic}
	s.mediaTypeDrop = &choices[string]{drop: h.dropdown(nil), label: automatic}
	s.outputBinDrop = &choices[string]{drop: h.dropdown(nil), label: keywordLabel}

	grid := widget.NewGrid(2*th.Spacing, th.Spacing)
	grid.AddClass("print-form")
	for i, row := range []struct {
		label   string
		control interface {
			widget.Widget
			SetHExpand(bool)
		}
		optional bool
	}{
		{"Copies", s.copies, false},
		{"Pages", s.pagesDrop.drop, false},
		{"Range", s.ranges, false},
		{"Orientation", s.orientation, false},
		{"Paper size", s.paperDrop.drop, false},
		{"Color", s.colorDrop.drop, false},
		{"Two-sided", s.sideDrop.drop, false},
		{"Quality", s.qualityDrop.drop, false},
		{"Pages per sheet", s.numberUpDrop.drop, true},
		{"Page order", s.layoutDrop.drop, true},
		{"Only print", s.pageSetDrop.drop, false},
		{"Reverse order", s.reverse, false},
		{"Collate copies", s.collate, false},
		{"Scale (%)", s.scale, false},
		{"Scaling", s.scalingDrop.drop, true},
		{"Paper source", s.sourceDrop.drop, true},
		{"Paper type", s.mediaTypeDrop.drop, true},
		{"Output tray", s.outputBinDrop.drop, true},
	} {
		label := h.label(row.label, "print-form-label", css.DimLabel)
		grid.Attach(label, 0, i, 1, 1)
		row.control.SetHExpand(true)
		grid.Attach(row.control, 1, i, 1, 1)
		if row.optional {
			s.optional[row.control] = label
		}
	}
	s.offer(file.caps, initial)
	reset := widgets.DialogButton(h.d.Font, "Reset to printer defaults", "print-reset", css.Flat, s.reset)

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
	surface.SetContent(widgets.DialogBody(printers, grid, reset, s.outputRow, s.replaceNote))
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
	s.numberUpDrop.refill(c.numberUp, pick(c.numberUp, want.numberUp, 1))
	s.scalingDrop.refill(c.scalings, pick(c.scalings, want.scaling, c.scaling))
	s.sourceDrop.refill(withAutomatic(c.sources), pick(c.sources, want.source, ""))
	s.mediaTypeDrop.refill(withAutomatic(c.mediaTypes), pick(c.mediaTypes, want.mediaType, ""))
	s.outputBinDrop.refill(c.outputBins, pick(c.outputBins, want.outputBin, c.outputBin))
	s.show(s.numberUpDrop.drop, len(c.numberUp) > 1)
	s.show(s.layoutDrop.drop, len(c.numberUp) > 1)
	s.show(s.scalingDrop.drop, len(c.scalings) > 0)
	s.show(s.sourceDrop.drop, len(c.sources) > 0)
	s.show(s.mediaTypeDrop.drop, len(c.mediaTypes) > 0)
	s.show(s.outputBinDrop.drop, len(c.outputBins) > 0)
}

// withAutomatic is a queue's keywords after "" for its own choice.
func withAutomatic(keywords []string) []string {
	if len(keywords) == 0 {
		return nil
	}
	return append([]string{""}, keywords...)
}

// show shows an optional control and its label, or hides both.
func (s *sheet) show(control widget.Widget, on bool) {
	if v, ok := control.(interface{ SetVisible(bool) }); ok {
		v.SetVisible(on)
	}
	s.optional[control].SetVisible(on)
}

// setForm puts f in the controls; the queue selected offers what it
// names.
func (s *sheet) setForm(f form, c capabilities) {
	s.copies.SetValue(float64(f.copies))
	s.pagesDrop.set(f.pages)
	s.ranges.SetText(f.ranges)
	s.orientation.SetSelected(map[bool]int{false: 0, true: 1}[f.landscape])
	for _, d := range []interface{ clear() }{s.paperDrop, s.sideDrop, s.colorDrop, s.qualityDrop, s.numberUpDrop, s.scalingDrop, s.sourceDrop, s.mediaTypeDrop, s.outputBinDrop} {
		d.clear()
	}
	s.offer(c, f)
	s.layoutDrop.set(f.layout)
	s.pageSetDrop.set(f.pageSet)
	s.reverse.SetChecked(f.reverse)
	s.collate.SetChecked(f.collate)
	s.scale.SetValue(float64(f.scale))
}

// reset puts the selected queue's defaults in the form.
func (s *sheet) reset() {
	q, ok := s.selected()
	if !ok {
		return
	}
	s.setForm(initialForm(PrepareRequest{HasCurrentPage: s.r.HasCurrentPage, HasSelectedPages: s.r.HasSelectedPages}, q.caps), q.caps)
}

// form is what the form holds now.
func (s *sheet) form() form {
	return form{
		copies: int(s.copies.Value()), pages: s.pagesDrop.value(), ranges: s.ranges.Text(),
		landscape: s.orientation.Selected() == 1, paper: s.papersOf[s.paperDrop.value()],
		color: s.colorDrop.value(), side: s.sideDrop.value(), quality: s.qualityDrop.value(),
		numberUp: max(s.numberUpDrop.value(), 1), layout: s.layoutDrop.value(), pageSet: s.pageSetDrop.value(),
		reverse: s.reverse.Checked(), collate: s.collate.Checked(), scale: int(s.scale.Value()),
		scaling: s.scalingDrop.value(), source: s.sourceDrop.value(), mediaType: s.mediaTypeDrop.value(), outputBin: s.outputBinDrop.value(),
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
	s.outputRow.SetVisible(q.kind == queueFile)
	s.replaceNote.SetVisible(false)
	s.confirmedReplace = false
	s.offer(q.caps, s.form())
	if q.kind == queueNetwork && !q.capsRead {
		s.readCaps(q)
	}
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
	s.browse()
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
			s.cupsQueues = make([]queue, 0, len(printers))
			for _, p := range printers {
				s.cupsQueues = append(s.cupsQueues, cupsQueue(p))
			}
			queues := s.compose()
			picked := s.picked
			s.setQueues(queues, preferredQueue(queues, settingOf(s.r.Settings, "printer"), user, def), false)
			if q, ok := s.selected(); ok && !picked {
				s.setForm(initialForm(s.h.presets.withPreset(s.r, s.appID, q.name), q.caps), q.caps)
			}
			go s.follow()
		})
	}()
}

// cupsQueue is a CUPS printer's row.
func cupsQueue(p ipp.Printer) queue {
	return queue{kind: queueCUPS, name: p.Name, detail: printerDetail(p), caps: capabilitiesOf(&p), uuid: p.UUID, device: p.DeviceURI}
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
	if s.ctx.Err() != nil || s.refreshing || !ok || q.kind != queueCUPS {
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
	i := slices.IndexFunc(s.queues, func(q queue) bool { return q.kind == queueCUPS && q.name == p.Name })
	if i < 0 {
		return
	}
	s.queues[i].detail = printerDetail(p)
	s.details[i].SetText(s.queues[i].detail)
	s.details[i].SetVisible(s.queues[i].detail != "")
}

// close stops the loading, the state reads and the browsing.
func (s *sheet) close() {
	s.cancel()
	if s.browser != nil {
		stopBrowsing(s.browser)
		s.browser = nil
	}
}

// confirm answers the form for the queue selected; Print to File asks
// once before replacing a file that exists.
func (s *sheet) confirm() {
	q, ok := s.selected()
	if !ok {
		s.h.finish(prepared{}, false)
		return
	}
	f := s.form()
	switch q.kind {
	case queueCUPS:
		s.done(prepared{printer: q.name, form: f})
		return
	case queueNetwork:
		s.done(prepared{printer: q.name, device: q.device, form: f})
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
			s.done(prepared{printer: q.name, form: f})
		})
	}()
}

// done answers the confirmed form, kept as the app's preset for the
// printer.
func (s *sheet) done(p prepared) {
	if s.appID != "" {
		s.h.presets.remember(s.appID, p.printer, p.form.settings(p.printer))
	}
	s.h.finish(p, true)
}
