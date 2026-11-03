package launcher

import (
	"context"
	"errors"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/rivo/uniseg"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/launcheripc"
	engine "github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/search"
)

// SearchMode is the mode name that opens the search panel rather than a
// rofi session: `-show search`, or `lys-shell launcher` with no arguments.
const SearchMode = "search"

// wantsSearch reports a session asking for the panel.
func wantsSearch(o launcheripc.SessionOptions) bool {
	return !o.Dmenu && o.ErrorMessage == nil && o.Mode != nil && *o.Mode == SearchMode
}

// previewSettle is how long the selection must rest before its preview
// loads: arrowing through a list reads no files.
const previewSettle = 40 * time.Millisecond

// panel is one search session on the UI loop: the widgets, the frame
// they show, and the engine behind them.
type panel struct {
	s       *Surface
	sess    *launcheripc.Session
	eng     *search.Engine
	v       *panelViews
	win     Window
	ctx     context.Context
	cancel  context.CancelFunc
	frame   search.Frame
	sel     int
	depth   int
	selBack []string
	// moved is the person having chosen a row for this query: only then do
	// rows arriving late leave the selection where it is. Otherwise the
	// best row takes it, or typing fast and pressing Enter would take the
	// web search fallback, the first row to exist.
	moved bool
	// querySeq numbers the edits of the field sent to the engine.
	querySeq uint64
	// actions caches the selected row's actions, for the entry they were
	// listed for: typing rarely changes the top row, and listing them
	// builds closures.
	actions     []search.Action
	actionsFor  *search.Entry
	actionCache map[*search.Entry][]search.Action
	// hl is the query's copy for highlighting, on this goroutine.
	hl engine.FuzzyQuery
	// accept is an Enter pressed before the rows for the typed text
	// arrived: it runs when they do, so typing fast and pressing Enter
	// takes the row the text finds, not the row showing when the key
	// came.
	accept      int
	acceptArmed bool
	notice      string
	closing     bool
	thumbs      search.Thumbnailing
	previewStop func()
	previewFor  string
	// afterClose runs once the surface has gone and the keyboard with it.
	afterClose func()
}

// openPanel starts a search session.
func (s *Surface) openPanel(sess *launcheripc.Session) {
	cfg := s.d.Config()
	ctx, cancel := context.WithCancel(context.Background())
	p := &panel{s: s, sess: sess, ctx: ctx, cancel: cancel, sel: -1, thumbs: engine.NewThumbnailer(previewCmd(sess.Options, cfg.Launcher))}
	s.panel = p
	p.v = newPanelViews(p, cfg)
	win, err := s.d.Open(app.LayerConfig{
		Layer:          app.LayerOverlay,
		Anchor:         app.AnchorTop | app.AnchorBottom | app.AnchorLeft | app.AnchorRight,
		ExclusiveZone:  -1,
		Keyboard:       app.KeyboardExclusive,
		Namespace:      "lys-shell-launcher",
		Root:           p.v.root,
		Output:         s.outputFor(cfg.Launcher.Monitor),
		OwnMotion:      true,
		BackgroundBlur: true,
		KeyCapture:     p.keyCaptured,
		OnPress: func(_ uint32, over widget.Widget) {
			if over == widget.Widget(p.v.root) {
				p.finish(search.Cancel{})
			}
		},
		OnClosed: func() {
			if s.panel == p {
				p.win = nil
				p.finish(search.Cancel{})
			}
		},
	})
	if err != nil {
		log.Printf("launcher: cannot map the surface: %v", err)
		p.finish(search.Cancel{})
		return
	}
	p.win = win
	if err := win.SetFocus(p.v.entry); err != nil {
		log.Printf("launcher: cannot focus the entry: %v", err)
		p.finish(search.Cancel{})
		return
	}
	root := search.NewRoot(cfg.Launcher.Search, cfg.Power, s.d.Search(), os.Getenv("HOME"))
	if sess.Options.Filter != nil {
		root.Query = *sess.Options.Filter
	}
	if sess.Options.Prompt != nil {
		root.Placeholder = *sess.Options.Prompt
	}
	var openFrecency func() (*search.Frecency, error)
	if cfg.Launcher.History.Enable {
		openFrecency = s.d.OpenFrecency
	}
	p.v.entry.SetPlaceholder(root.Placeholder)
	p.eng = search.Start(root, openFrecency, panelSink{p}, s.d.Invoke)
	p.v.motion.open(cfg.Animations)
}

// panelSink delivers engine results to a panel still showing.
type panelSink struct{ p *panel }

func (k panelSink) live() bool { return k.p.s.panel == k.p && !k.p.closing }

// Results implements search.Sink.
func (k panelSink) Results(f search.Frame) {
	if k.live() {
		k.p.results(f)
	}
}

// Finish implements search.Sink.
func (k panelSink) Finish(o search.Outcome) {
	if k.live() {
		k.p.finish(o)
	}
}

// Notice implements search.Sink.
func (k panelSink) Notice(m string) {
	if k.live() {
		k.p.notice = m
		k.p.v.setFooter(k.p)
	}
}

// results shows a new list: the field if the level changed, the
// selection where it belongs, the height to spring to.
func (p *panel) results(f search.Frame) {
	prev := p.frame
	selectedID := ""
	if p.sel >= 0 && p.sel < len(prev.Rows) && prev.Rows[p.sel].Entry != nil {
		selectedID = prev.Rows[p.sel].Entry.ID
	}
	switch {
	case f.Depth > p.depth:
		p.selBack = append(p.selBack, selectedID)
		selectedID = ""
	case f.Depth < p.depth:
		selectedID = ""
		if n := len(p.selBack); n > 0 {
			selectedID = p.selBack[n-1]
			p.selBack = p.selBack[:n-1]
		}
	case !f.Late && f.Query != prev.Query:
		selectedID = ""
		p.moved = false
	case f.Late && !p.moved:
		selectedID = ""
	}
	p.depth = f.Depth
	p.frame = f
	p.hl.Set(f.Query)
	p.notice = ""
	if f.SetField && f.QuerySeq >= p.querySeq {
		p.v.setField(f.Field)
	}
	p.v.setLevel(f)
	sel := firstItem(f.Rows)
	if selectedID != "" {
		if at := rowIndexOf(f.Rows, selectedID); at >= 0 {
			sel = at
		}
	}
	p.setSelected(sel, true)
	p.v.list.setRows(f.Rows, p.sel)
	p.v.setHasPreview(hasPreview(f.Rows), p)
	p.v.setFooter(p)
	p.v.retarget(p)
	// Nothing reads the old rows now: the list took the new ones.
	if p.eng != nil {
		p.eng.Recycle(prev.Rows)
	}
	if p.acceptArmed && f.Query == p.v.entry.Text() {
		p.acceptArmed = false
		p.activate(p.accept)
	}
}

func firstItem(rows []search.Row) int {
	for i, r := range rows {
		if r.Header == "" {
			return i
		}
	}
	return -1
}

func rowIndexOf(rows []search.Row, id string) int {
	for i, r := range rows {
		if r.Header == "" && r.Entry.ID == id {
			return i
		}
	}
	return -1
}

func hasPreview(rows []search.Row) bool {
	for _, r := range rows {
		if r.Header == "" && r.Entry.Preview.Any() {
			return true
		}
	}
	return false
}

// setSelected moves the selection and refreshes what depends on it.
func (p *panel) setSelected(i int, fresh bool) {
	if i == p.sel && !fresh {
		return
	}
	p.sel = i
	var entry *search.Entry
	if i >= 0 && i < len(p.frame.Rows) {
		entry = p.frame.Rows[i].Entry
	}
	if entry != p.actionsFor {
		p.actionsFor, p.actions = entry, nil
		if entry != nil {
			p.actions = p.actionsOf(p.frame.Rows[i])
		}
	}
	if !fresh {
		p.moved = true
		p.v.list.setSelected(i)
		p.v.setFooter(p)
	}
	p.schedulePreview()
}

// actionsCacheMax bounds what is remembered between clears.
const actionsCacheMax = 256

// actionsOf lists a row's actions, once per entry: listing builds closures,
// and arrowing over a list revisits the same few rows. An entry is never
// written after it is published, so its pointer is its identity.
func (p *panel) actionsOf(row search.Row) []search.Action {
	if a, ok := p.actionCache[row.Entry]; ok {
		return a
	}
	if p.actionCache == nil || len(p.actionCache) >= actionsCacheMax {
		p.actionCache = make(map[*search.Entry][]search.Action, actionsCacheMax)
	}
	a := row.Actions()
	p.actionCache[row.Entry] = a
	return a
}

// selected is the selected row.
func (p *panel) selected() (search.Row, bool) {
	if p.sel < 0 || p.sel >= len(p.frame.Rows) {
		return search.Row{}, false
	}
	return p.frame.Rows[p.sel], true
}

// move steps the selection over headings, wrapping at the ends.
func (p *panel) move(delta int) {
	rows := p.frame.Rows
	n := len(rows)
	if n == 0 || p.sel < 0 {
		return
	}
	dir := 1
	if delta < 0 {
		dir = -1
		delta = -delta
	}
	at := p.sel
	if delta > 1 {
		// A page clamps; only single steps wrap.
		for range delta {
			next := at + dir
			for next >= 0 && next < n && rows[next].Header != "" {
				next += dir
			}
			if next < 0 || next >= n {
				break
			}
			at = next
		}
		p.setSelected(at, false)
		return
	}
	for range n {
		at = (at + dir + n) % n
		if rows[at].Header == "" {
			break
		}
	}
	p.setSelected(at, false)
}

// activate runs action number on the selected row, or holds it until
// the typed text's rows are in.
func (p *panel) activate(action int) {
	if p.eng == nil {
		return
	}
	if p.frame.Query != p.v.entry.Text() {
		p.accept, p.acceptArmed = action, true
		return
	}
	row, ok := p.selected()
	if !ok {
		if action == 0 && p.frame.CanSubmit {
			p.eng.Submit(p.v.entry.Text())
		}
		return
	}
	if action < len(p.actions) {
		p.eng.Activate(row.Entry.ID, action)
	}
}

// tab completes the selected row, or opens its actions.
func (p *panel) tab() {
	row, ok := p.selected()
	if !ok {
		return
	}
	if c := row.Entry.Completion; c != "" {
		p.v.entry.SetText(c)
		return
	}
	p.eng.ShowActions(row.Entry.ID)
}

// keyCaptured is the panel's keyboard: navigation here, everything else
// to the field.
func (p *panel) keyCaptured(press app.Accel) bool {
	if p.closing || p.eng == nil {
		return false
	}
	k, ok := lookupPanelKey(press)
	if !ok {
		return false
	}
	switch k.do {
	case panelDown:
		p.move(1)
	case panelUp:
		p.move(-1)
	case panelPageDown:
		p.move(p.v.pageRows())
	case panelPageUp:
		p.move(-p.v.pageRows())
	case panelAccept:
		p.activate(0)
	case panelAcceptSecond:
		p.activate(1)
	case panelTab:
		p.tab()
	case panelActions:
		if row, ok := p.selected(); ok {
			p.eng.ShowActions(row.Entry.ID)
		}
	case panelBack:
		p.eng.Back()
	case panelBackspace:
		if p.depth == 0 || p.v.entry.Text() != "" {
			return false
		}
		p.eng.Back()
	case panelNumber:
		if at := p.nthItem(k.n); at >= 0 {
			p.setSelected(at, false)
			p.activate(0)
		}
	}
	return true
}

// nthItem is the row index of the n-th (1-based) item shown from the top
// of the list's view, -1 past the end.
func (p *panel) nthItem(n int) int {
	for i := p.v.list.firstShown(); i < len(p.frame.Rows); i++ {
		if p.frame.Rows[i].Header == "" {
			n--
			if n == 0 {
				return i
			}
		}
	}
	return -1
}

// rowPressed is a click on a row: select and accept; the right button
// opens its actions.
func (p *panel) rowPressed(i int, button uint32) {
	if i < 0 || i >= len(p.frame.Rows) || p.frame.Rows[i].Header != "" {
		return
	}
	p.setSelected(i, false)
	if button == widget.BTNRight {
		p.eng.ShowActions(p.frame.Rows[i].Entry.ID)
		return
	}
	p.activate(0)
}

// schedulePreview loads the selected row's preview once it rests, and
// drops one in flight for another row.
func (p *panel) schedulePreview() {
	row, ok := p.selected()
	id := ""
	if ok && row.Entry.Preview.Any() {
		id = row.Entry.ID
	}
	// The same row again, as a frame for rows arriving late: whatever is
	// loading or loaded for it stands.
	if id != "" && id == p.previewFor {
		return
	}
	if p.previewStop != nil {
		p.previewStop()
		p.previewStop = nil
	}
	if id == "" {
		p.previewFor = ""
		p.v.showPreview(search.PreviewContent{}, nil, p)
		return
	}
	p.previewFor = id
	preview := row.Entry.Preview
	ctx, cancel := context.WithCancel(p.ctx)
	stopTimer := p.s.d.After(previewSettle, func() {
		go func() {
			content, err := search.LoadPreview(ctx, preview, p.thumbs)
			p.s.d.Invoke(func() {
				if ctx.Err() != nil || p.s.panel != p || p.previewFor != id {
					return
				}
				if err != nil && !errors.Is(err, search.ErrNoPreview) {
					log.Printf("launcher: preview: %v", err)
				}
				p.v.showPreview(content, err, p)
			})
		}()
	})
	p.previewStop = func() {
		stopTimer()
		cancel()
	}
}

// finish ends the session: the outcome's effect, the CLI's answer, the
// engine stopped, and the exit played.
func (p *panel) finish(o search.Outcome) {
	if p.closing {
		return
	}
	p.closing = true
	frame := launcheripc.ServerFrame{Type: launcheripc.FrameResult, Filter: p.v.entry.Text()}
	switch v := o.(type) {
	case search.Cancel:
		frame = launcheripc.ServerFrame{Type: launcheripc.FrameCancelled, Code: 1}
	case search.Copy:
		if p.s.d.Copy != nil {
			p.s.d.Copy(v.Text)
		}
		frame.Selected = []launcheripc.Selected{{Index: 0, Text: v.Text}}
	case search.Type:
		p.afterClose = func() { p.s.typeOnceClosed(v.Text) }
		frame.Selected = []launcheripc.Selected{{Index: 0, Text: v.Text}}
	}
	p.teardown()
	p.sess.Reply(frame)
}

// release ends the session for a client that went away.
func (p *panel) release() {
	if p.closing {
		return
	}
	p.closing = true
	p.teardown()
	p.sess.Release()
}

func (p *panel) teardown() {
	if p.s.panel == p {
		p.s.panel = nil
	}
	if p.previewStop != nil {
		p.previewStop()
	}
	p.cancel()
	if p.eng != nil {
		p.eng.Stop()
	}
	if p.win == nil {
		return
	}
	win := p.win
	p.v.motion.close(p.s.d.Config().Animations, func() {
		win.Close()
		if p.afterClose != nil {
			p.afterClose()
		}
	})
}

// panelAction is what a panel key does.
type panelAction uint8

const (
	panelDown panelAction = iota + 1
	panelUp
	panelPageDown
	panelPageUp
	panelAccept
	panelAcceptSecond
	panelTab
	panelActions
	panelBack
	panelBackspace
	panelNumber
)

type panelKey struct {
	do panelAction
	n  int
}

// panelKeys is the panel's fixed key table. Plain digits and letters go
// to the field: they are what you search with.
var panelKeys = func() map[app.Accel]panelKey {
	table := map[string]panelKey{
		"Down": {do: panelDown}, "Control+n": {do: panelDown}, "Control+j": {do: panelDown},
		"Up": {do: panelUp}, "Control+p": {do: panelUp},
		"Page_Down": {do: panelPageDown}, "Page_Up": {do: panelPageUp},
		"Return": {do: panelAccept}, "KP_Enter": {do: panelAccept},
		"Control+Return": {do: panelAcceptSecond},
		"Tab":            {do: panelTab},
		"Control+k":      {do: panelActions},
		"Escape":         {do: panelBack},
		"BackSpace":      {do: panelBackspace},
	}
	for i := 1; i <= 9; i++ {
		table["Control+"+strconv.Itoa(i)] = panelKey{do: panelNumber, n: i}
	}
	out := make(map[app.Accel]panelKey, len(table))
	for spec, k := range table {
		accel, err := app.ParseAccel(spec)
		if err != nil {
			panic("launcher: bad panel key " + spec + ": " + err.Error())
		}
		out[accel] = k
	}
	return out
}()

func lookupPanelKey(press app.Accel) (panelKey, bool) {
	k, ok := panelKeys[press]
	return k, ok
}

// hintFor is the footer's text: the default action, the second, how to
// open the actions, and how to leave.
func hintFor(p *panel, hints []footerHint) []footerHint {
	row, ok := p.selected()
	switch {
	case ok && len(p.actions) > 0:
		hints = append(hints, footerHint{"Enter", p.actions[0].Title})
		if len(p.actions) > 1 {
			hints = append(hints, footerHint{"Ctrl+Enter", p.actions[1].Title}, footerHint{"Ctrl+K", i18n.T("launcher-footer-actions")})
		}
		if row.Entry.Completion != "" {
			hints = append(hints, footerHint{"Tab", i18n.T("launcher-footer-complete")})
		}
	case !ok && p.frame.CanSubmit && p.frame.SubmitTitle != "":
		hints = append(hints, footerHint{"Enter", p.frame.SubmitTitle})
	}
	esc := i18n.T("launcher-footer-close")
	if p.depth > 0 {
		esc = i18n.T("launcher-footer-back")
	}
	return append(hints, footerHint{"Esc", esc})
}

// footerHint is one key and what it does.
type footerHint struct{ key, does string }

// appendHighlightMarkup appends a title in lys-ui markup to dst, the
// positions in grapheme clusters marked with open.
func appendHighlightMarkup(dst []byte, title string, positions []int, open string) []byte {
	if len(positions) == 0 {
		return appendEscaped(dst, title)
	}
	marked := false
	next := 0
	state := -1
	for i := 0; len(title) > 0; i++ {
		var cluster string
		cluster, title, _, state = uniseg.FirstGraphemeClusterInString(title, state)
		hit := next < len(positions) && positions[next] == i
		if hit {
			next++
		}
		if hit != marked {
			if hit {
				dst = append(dst, open...)
			} else {
				dst = append(dst, "</span>"...)
			}
			marked = hit
		}
		dst = appendEscaped(dst, cluster)
	}
	if marked {
		dst = append(dst, "</span>"...)
	}
	return dst
}

// appendEscaped appends text escaped for lys-ui markup, as pango.Escape
// does, with no intermediate string.
func appendEscaped(dst []byte, text string) []byte {
	for i := range len(text) {
		switch c := text[i]; c {
		case '&':
			dst = append(dst, "&amp;"...)
		case '<':
			dst = append(dst, "&lt;"...)
		case '>':
			dst = append(dst, "&gt;"...)
		case '\'':
			dst = append(dst, "&apos;"...)
		case '"':
			dst = append(dst, "&quot;"...)
		default:
			dst = append(dst, c)
		}
	}
	return dst
}
