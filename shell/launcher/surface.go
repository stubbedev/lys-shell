package launcher

import (
	"context"
	"log"
	"math"
	"slices"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/launcheripc"
	"github.com/stubbedev/lys-shell/internal/pango"
	engine "github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/modes"
	"github.com/stubbedev/lys-shell/service/launcher/search"
)

// dumpDebounce is how long -dump waits for the matches to settle.
const dumpDebounce = 150 * time.Millisecond

// typeSettle is what the compositor gets to hand the keyboard back to
// the window under a closed surface: closing and typing go over separate
// connections, so nothing orders them.
const typeSettle = 40 * time.Millisecond

// Window is the launcher's layer surface (*app.LayerWindow).
type Window interface {
	Close()
	SetFocus(w widget.Widget) error
	// SetOpacity fades the surface in the compositor, with the blur behind
	// it; an error is a compositor without the protocol.
	SetOpacity(alpha float64) error
	// Scale is the device scale the surface presents at, num device pixels
	// per denom logical ones; OnScaleChanged hears each change.
	Scale() (num, denom int)
	OnScaleChanged(fn func(num, denom int)) (cancel func())
}

// Deps are what the shell hands the surface.
type Deps struct {
	// Invoke runs fn on the UI loop (app.Invoke).
	Invoke func(fn func())
	// After runs fn on the UI loop after d; the returned stop cancels.
	After func(d time.Duration, fn func()) (stop func())
	// Config is the live config.
	Config func() *config.Config
	// Open maps the layer surface for one session.
	Open func(cfg app.LayerConfig) (Window, error)
	// Font paints every label; Variants gives the message line and
	// markup rows their bold and italic faces.
	Font     render.Font
	Variants widget.VariantFunc
	// Sheet is the shell's stylesheet (the .launcher-* rules), and
	// SheetPriority its priority, which a session's -font/-style CSS
	// sits above.
	Sheet         *widget.Stylesheet
	SheetPriority int
	// Outputs lists the connected outputs, for launcher.monitor.
	Outputs func() []*app.Output
	// MonitorWidth is the width a percent -width resolves against (the
	// compositor picks the output, so the first one stands in, as the
	// Rust surface's fallback does before it is realized).
	MonitorWidth func() int
	// Clipboard is the history the clipboard mode reads (nil: none).
	Clipboard modes.ClipboardHistory
	// Vault builds the vault mode for a session (nil: no vault mode).
	Vault func() engine.Mode
	// Copy puts calc's result on the clipboard.
	Copy func(text string)
	// Type enters text in the focused window (typetext.Type).
	Type func(text string) error
	// OpenHistory opens the launch history (a test seam).
	OpenHistory func() (*engine.History, error)
	// Search is what the search panel's providers draw on.
	Search func() search.Deps
	// OpenFrecency opens what the search panel has learned.
	OpenFrecency func() (*search.Frecency, error)
	// WatchFiles calls fn on the UI loop when anything under paths changes
	// (app.WatchFiles); the search panel's kept sources load again then.
	// Nil loads them again in every session.
	WatchFiles search.WatchFunc
	// Wallpapers is what the wallpaper picker sets wallpapers through; nil
	// leaves the shell without the picker.
	Wallpapers Wallpapers
	// FocusedOutput is the compositor's focused output, false when it
	// cannot tell.
	FocusedOutput func() (string, bool)
	// Emoji is the emoji table the grid shows (modes.AvailableEmoji).
	Emoji func() []modes.Emoji
	// FireHook runs a -on-* hook (engine.FireHook; a test seam).
	FireHook func(command string, ctx engine.HookContext)
}

// pendingSelection is where the next match list puts the selection.
type pendingSelection struct {
	keep bool
	at   *int
}

// active is one live session (ActiveSession).
type active struct {
	sess       *launcheripc.Session
	act        *actor
	ui         uiSettings
	history    *engine.History
	modePrompt string
	modeName   string
	// masked is the mode asking for a secret: the input shows as dots
	// and hooks are not told it.
	masked bool
	// decided: the user accepted or cancelled, so tearing the list down
	// fires no -on-selection-changed.
	decided bool
	// dialog is -e: message only, any bound key closes with code 0.
	dialog bool
	dump   bool
	// dumpStop cancels the pending -dump reply.
	dumpStop func()
	pending  pendingSelection
	win      Window
	views    *views
	thumbs   engine.Thumbnailer
	ctx      context.Context
	cancel   context.CancelFunc
	keys     []keyBinding
	mouse    []mouseEntry
	// afterClose runs once the surface has gone and the keyboard with it.
	afterClose func()
	// previews is the active mode describing its rows; preview loads the
	// selected one once it rests, previewGen counts the states loaded, and
	// previewing is the row asked for, nil for none.
	previews   bool
	preview    *previewLoader
	previewGen uint64
	previewing *rowPreviewRequest
}

// rowPreviewRequest is the row the pane is loading or showing, and the
// context its load runs under.
type rowPreviewRequest struct {
	key rowPreviewKey
	ctx context.Context
}

// Surface is the launcher (shell/launcher/mod.rs): it serves the
// launcher socket's sessions on the UI loop.
type Surface struct {
	d   Deps
	cur *active
	// panel is the live search session, picker the live wallpaper picker
	// and emojiGrid the live emoji grid; at most one of cur, panel, picker
	// and emojiGrid lives.
	panel     *panel
	picker    *picker
	emojiGrid *emojiGrid
	// views are the search panel's, kept between panels.
	views *panelViews
	// sources are the search panel's, kept for the surface's lifetime.
	sources      *search.Sources
	openFrecency func() (*search.Frecency, error)
	model        matchModel
	multi        multiSelect
	sel          int
	// spawn runs work off the loop (the picker's thumbnails).
	spawn func(func())
}

// New builds the surface over its collaborators.
func New(d Deps) *Surface {
	if d.FireHook == nil {
		d.FireHook = engine.FireHook
	}
	if d.Emoji == nil {
		d.Emoji = modes.AvailableEmoji
	}
	s := &Surface{d: d, sel: -1, sources: search.NewSources(d.WatchFiles, time.Now), spawn: func(fn func()) { go fn() }}
	s.openFrecency = s.sources.Frecency(func() (*search.Frecency, error) { return s.d.OpenFrecency() })
	return s
}

// Open implements launcheripc.Handler: the session moves onto the loop.
func (s *Surface) Open(sess *launcheripc.Session) {
	s.d.Invoke(func() { s.openSession(sess) })
}

// ClientGone implements launcheripc.Handler: a CLI that went away ends
// its session if it is still the live one, and its connection is
// released either way.
func (s *Surface) ClientGone(id uint64) {
	s.d.Invoke(func() {
		if s.cur != nil && s.cur.sess.ID == id {
			s.end(nil)
		}
		if s.panel != nil && s.panel.sess.ID == id {
			s.panel.release()
		}
		if s.picker != nil && s.picker.sess.ID == id {
			s.picker.release()
		}
		if s.emojiGrid != nil && s.emojiGrid.sess.ID == id {
			s.emojiGrid.release()
		}
	})
}

// openSession is open_session.
func (s *Surface) openSession(sess *launcheripc.Session) {
	if s.picker != nil {
		switch {
		case wantsPicker(sess.Options):
			s.picker.finish(false, "")
			sess.Reply(launcheripc.ServerFrame{Type: launcheripc.FrameCancelled, Code: 1})
			return
		case !sess.Replace:
			sess.Reply(launcheripc.ServerFrame{Type: launcheripc.FrameBusy})
			return
		}
		s.picker.finish(false, "")
	}
	if s.emojiGrid != nil {
		switch {
		case wantsEmojiGrid(sess.Options):
			s.emojiGrid.finish("", false)
			sess.Reply(launcheripc.ServerFrame{Type: launcheripc.FrameCancelled, Code: 1})
			return
		case !sess.Replace:
			sess.Reply(launcheripc.ServerFrame{Type: launcheripc.FrameBusy})
			return
		}
		s.emojiGrid.finish("", false)
	}
	if s.panel != nil {
		// The bind that opened the panel closes it; anything else asking
		// for the screen takes it only when it says -replace.
		switch {
		case wantsSearch(sess.Options):
			s.panel.finish(search.Cancel{})
			sess.Reply(launcheripc.ServerFrame{Type: launcheripc.FrameCancelled, Code: 1})
			return
		case !sess.Replace:
			sess.Reply(launcheripc.ServerFrame{Type: launcheripc.FrameBusy})
			return
		}
		s.panel.finish(search.Cancel{})
	}
	if s.cur != nil {
		if !sess.Replace {
			sess.Reply(launcheripc.ServerFrame{Type: launcheripc.FrameBusy})
			return
		}
		s.end(&launcheripc.ServerFrame{Type: launcheripc.FrameCancelled, Code: 1})
	}
	if wantsSearch(sess.Options) && s.d.Search != nil {
		s.openPanel(sess)
		return
	}
	if wantsPicker(sess.Options) {
		s.openPicker(sess)
		return
	}
	if wantsEmojiGrid(sess.Options) {
		s.openEmojiGrid(sess)
		return
	}
	cfg := s.d.Config()
	setup := buildSession(sess.Options, cfg, sess.Rows, setupDeps{clipboard: s.d.Clipboard, vault: s.d.Vault, openHistory: s.d.OpenHistory})
	dialog := setup.ui.errorMessage != nil
	if len(setup.modes) == 0 && !dialog {
		log.Printf("launcher: no usable modes in session request")
		// The one thing lys-shell's launcher calls a menu error: it was
		// asked for a menu it cannot build.
		s.d.FireHook(setup.ui.hooks.MenuError, engine.HookContext{Error: "no usable modes"})
		sess.Reply(launcheripc.ServerFrame{Type: launcheripc.FrameCancelled, Code: 1})
		closeHistory(setup.history)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &active{
		sess: sess, ui: setup.ui, history: setup.history, dialog: dialog, dump: sess.Options.Dump,
		thumbs: engine.NewThumbnailer(setup.ui.previewCmd), ctx: ctx, cancel: cancel,
		keys: compileKeys(setup.ui.keybindings), mouse: compileMouse(setup.ui.mouseBindings),
	}
	a.preview = newPreviewLoader(s.d, a.thumbs)
	s.multi = multiSelect{picked: map[uint32]bool{}, ballotSelected: setup.ui.ballotSelected, ballotUnselected: setup.ui.ballotUnselected}
	s.model.update(nil, nil)
	s.sel = -1
	s.cur = a
	a.views = s.buildViews(a)
	if !dialog {
		esess, err := engine.NewSession(setup.modes, setup.matcher)
		if err != nil {
			log.Printf("launcher: %v", err)
			s.end(&launcheripc.ServerFrame{Type: launcheripc.FrameCancelled, Code: 1})
			return
		}
		esess.SetCompleter(setup.ui.completer)
		a.act = startActor(esess, setup.initialMode, &sessionSink{s: s, a: a}, s.d.Invoke)
	}
	if a.dump {
		if a.ui.filter != nil && a.act != nil {
			a.act.send(cmdSetQuery{query: *a.ui.filter})
		}
		return
	}
	s.reveal(a)
}

// sessionSink delivers one session's engine events, dropping any that
// arrive after the session ended.
type sessionSink struct {
	s *Surface
	a *active
}

func (k *sessionSink) live() bool { return k.s.cur == k.a }

func (k *sessionSink) onState(st engineState) {
	if k.live() {
		k.s.onState(st)
	}
}

func (k *sessionSink) onMatches(m engineMatches) {
	if k.live() {
		k.s.onMatches(m)
	}
}

func (k *sessionSink) onAction(act engine.Action) {
	if k.live() {
		k.s.onAction(act)
	}
}

func (k *sessionSink) onPreview(p enginePreview) {
	if k.live() {
		k.s.onPreview(p)
	}
}

// onMatches is EngineEvent::Matches: the list swaps, the selection
// lands where the session asked, -select/-selected-row apply once, and
// -auto-select accepts a lone result.
func (s *Surface) onMatches(m engineMatches) {
	a := s.cur
	previous := s.sel
	s.model.update(m.items, m.matched)
	a.views.list.reset(s.model.len())
	a.views.setRows(s.model.len())
	a.views.showPane(a.previews && s.model.len() > 0)
	if n := s.model.len(); n > 0 {
		target := 0
		switch {
		case a.pending.at != nil:
			target = min(*a.pending.at, n-1)
		case a.pending.keep:
			target = min(max(previous, 0), n-1)
		}
		s.selectRow(target)
		s.applyOneShotSelection()
		if a.ui.autoSelect && n == 1 && a.views.entry.Text() != "" {
			s.activatePosition(0)
		}
	} else {
		s.selectRow(-1)
	}
	s.schedulePreview()
	s.resize()
	s.armDump()
}

// onState is on_state: prompt, message, tabs, the selection intent, the
// multi-select flag, and the filter a script reload clears.
func (s *Surface) onState(st engineState) {
	a := s.cur
	s.multi.enabled = st.multi
	if !st.multi {
		clear(s.multi.picked)
	}
	if st.afterActivate && !st.keepFilter && a.views.entry.Text() != "" {
		a.views.entry.SetText("")
	}
	switch {
	case st.newSelection != nil:
		at := int(*st.newSelection)
		a.pending = pendingSelection{at: &at}
	case st.keepSelection:
		a.pending = pendingSelection{keep: true}
	default:
		a.pending = pendingSelection{}
	}
	a.previews = st.previews && !a.dump && s.d.Config().Launcher.Search.Previews
	a.previewGen++
	a.modePrompt = st.prompt
	a.masked = st.password
	a.views.setMasked(a.ui.password || st.password)
	// A state arrives on every reload, so the hook fires on a change of
	// mode, not on being told the mode again.
	name := ""
	if st.activeMode >= 0 && st.activeMode < len(st.modeNames) {
		name = st.modeNames[st.activeMode]
	}
	if name != "" && name != a.modeName {
		s.d.FireHook(a.ui.hooks.ModeChanged, engine.HookContext{Input: a.hookInput(), Mode: name})
	}
	a.modeName = name
	shown := st.prompt
	if a.ui.prompt != nil {
		shown = *a.ui.prompt
	} else if named, ok := a.ui.displayNames[st.prompt]; ok {
		shown = named
	}
	a.views.setPrompt(shown)
	message := st.message
	if a.ui.errorMessage != nil {
		message = a.ui.errorMessage
	} else if a.ui.mesg != nil {
		message = a.ui.mesg
	}
	a.views.showMessage(message)
	if a.ui.sidebar {
		s.rebuildTabs(st.modeNames, st.activeMode)
	}
	s.resize()
}

// onAction is EngineEvent::Action: the terminal and input actions.
func (s *Surface) onAction(act engine.Action) {
	filter := s.cur.views.entry.Text()
	switch v := act.(type) {
	case engine.ActionClose:
		s.end(&launcheripc.ServerFrame{Type: launcheripc.FrameResult, Filter: filter})
	case engine.ActionExit:
		sel := make([]launcheripc.Selected, len(v.Selected))
		for i, x := range v.Selected {
			sel[i] = launcheripc.Selected{Index: x.Index, Text: x.Text}
		}
		s.end(&launcheripc.ServerFrame{Type: launcheripc.FrameResult, Code: v.Code, Selected: sel, Filter: filter})
	case engine.ActionType:
		s.cur.afterClose = func() { s.typeOnceClosed(v.Text) }
		s.end(&launcheripc.ServerFrame{
			Type:     launcheripc.FrameResult,
			Selected: []launcheripc.Selected{{Index: 0, Text: v.Text}}, Filter: filter,
		})
	case engine.ActionSetInput:
		s.cur.views.entry.SetText(v.Text)
	case engine.ActionCopy:
		if s.d.Copy != nil {
			s.d.Copy(v.Text)
		}
		s.end(&launcheripc.ServerFrame{
			Type:     launcheripc.FrameResult,
			Selected: []launcheripc.Selected{{Index: 0, Text: v.Text}}, Filter: filter,
		})
	}
}

// applyOneShotSelection is apply_one_shot_selection: -selected-row,
// else -select, on the session's first populated list.
func (s *Surface) applyOneShotSelection() {
	a := s.cur
	switch {
	case a.ui.selectedRow != nil:
		s.selectRow(min(int(*a.ui.selectedRow), s.model.len()-1))
		a.ui.selectedRow = nil
	case a.ui.selectRow != nil:
		if pos, ok := s.model.findPosition(*a.ui.selectRow); ok {
			s.selectRow(pos)
		}
		a.ui.selectRow = nil
	}
}

// selectRow moves the selection, firing -on-selection-changed when it
// changed.
func (s *Surface) selectRow(pos int) {
	if pos == s.sel {
		return
	}
	s.sel = pos
	if s.cur != nil {
		s.cur.views.list.setSelected(pos)
		s.fireSelectionHook()
		s.schedulePreview()
	}
}

// schedulePreview asks the mode for the selected row's preview once the
// selection rests. The row already loading or shown stands; any other
// row's wait and load are dropped.
func (s *Surface) schedulePreview() {
	a := s.cur
	if a == nil || !a.views.pane.box.Visible() {
		return
	}
	item, ok := s.model.itemIndex(s.sel)
	key := rowPreviewKey{gen: a.previewGen, item: item}
	if ok && a.previewing != nil && a.previewing.key == key {
		return
	}
	ctx := a.preview.start(a.ctx)
	if !ok {
		a.previewing = nil
		a.views.pane.show(nil, engine.PreviewContent{}, nil)
		return
	}
	a.previewing = &rowPreviewRequest{key: key, ctx: ctx}
	a.preview.settle(func() { s.send(cmdPreview{index: item, key: key}) })
}

// onPreview loads the mode's description of the row still asked for.
func (s *Surface) onPreview(p enginePreview) {
	a := s.cur
	req := a.previewing
	if req == nil || req.key != p.key || req.ctx.Err() != nil {
		return
	}
	if !p.ok {
		a.views.pane.show(nil, engine.PreviewContent{}, nil)
		return
	}
	a.preview.fetch(req.ctx, p.preview, func(c engine.PreviewContent, err error) {
		a.views.pane.show(p.preview.Facts, c, err)
		s.resize()
	})
}

// fireSelectionHook is fire_selection_hook.
func (s *Surface) fireSelectionHook() {
	a := s.cur
	if a.decided || a.ui.hooks.SelectionChanged == "" {
		return
	}
	text := s.model.textAt(s.sel)
	s.d.FireHook(a.ui.hooks.SelectionChanged, engine.HookContext{Input: a.hookInput(), Entry: text, Mode: a.modeName})
}

// hookInput is the typed text a hook is told: nothing while the mode
// asks for a secret.
func (a *active) hookInput() string {
	if a.masked {
		return ""
	}
	return a.views.entry.Text()
}

// fireDecisionHook is fire_decision_hook: -on-entry-accepted and
// -on-menu-canceled, fired before the decision tears the session down.
func (s *Surface) fireDecisionHook(action keyAction) {
	a := s.cur
	if !a.ui.hooks.Any() {
		return
	}
	selected := s.model.textAt(s.sel)
	typed := a.hookInput()
	var command, entry string
	decided := true
	switch {
	case action == keyCancel:
		command, entry = a.ui.hooks.MenuCanceled, selected
	case action == keyAccept && s.model.len() == 0:
		// With no rows there is nothing to accept but the typed text.
		command, entry = a.ui.hooks.EntryAccepted, typed
	case action == keyAccept:
		command, entry = a.ui.hooks.EntryAccepted, selected
	case action == keyAcceptAlt && !s.multi.enabled:
		// In multi-select the alternate accept toggles a row instead.
		command, entry = a.ui.hooks.EntryAccepted, selected
	case action == keyAcceptCustom:
		command, entry = a.ui.hooks.EntryAccepted, typed
	default:
		decided = false
	}
	a.decided = a.decided || decided
	s.d.FireHook(command, engine.HookContext{Input: typed, Entry: entry, Mode: a.modeName})
}

// onKey is on_key.
func (s *Surface) onKey(b keyBinding) {
	a := s.cur
	if a == nil {
		return
	}
	if a.dialog {
		// -e: any bound action dismisses with success.
		s.end(&launcheripc.ServerFrame{Type: launcheripc.FrameResult})
		return
	}
	s.fireDecisionHook(b.action)
	switch b.action {
	case keyCancel:
		s.end(&launcheripc.ServerFrame{Type: launcheripc.FrameCancelled, Code: 1})
	case keyAccept:
		picked := make([]uint32, 0, len(s.multi.picked))
		for i := range s.multi.picked {
			picked = append(picked, i)
		}
		slices.Sort(picked)
		switch {
		case len(picked) > 0:
			s.send(cmdActivateMulti{indices: picked})
		case s.model.len() == 0:
			s.activateCustom()
		default:
			s.activatePosition(s.sel)
		}
	case keyAcceptAlt:
		if s.multi.enabled {
			// rofi multi-select: Shift+Enter toggles and advances.
			s.togglePicked()
			s.moveSelection(1)
		} else if i, ok := s.model.itemIndex(s.sel); ok {
			s.send(cmdActivate{target: engine.RowTarget(i), kind: engine.ActivateAlt{}})
		}
	case keyAcceptCustom:
		s.activateCustom()
	case keyCustom:
		target := engine.Target{}
		if i, ok := s.model.itemIndex(s.sel); ok {
			target = engine.RowTarget(i)
		}
		s.send(cmdActivate{target: target, kind: engine.ActivateKbCustom{N: b.custom}})
	case keyDeleteEntry:
		if i, ok := s.model.itemIndex(s.sel); ok {
			s.send(cmdDelete{index: i})
		}
	case keyModeNext:
		s.send(cmdModeNext{})
	case keyModePrevious:
		s.send(cmdModePrevious{})
	case keyModeComplete:
		s.send(cmdModeComplete{})
	case keyRowUp:
		s.moveSelection(-1)
	case keyRowDown:
		s.moveSelection(1)
	case keyRowFirst:
		if s.model.len() > 0 {
			s.selectRow(0)
		}
	case keyRowLast:
		if n := s.model.len(); n > 0 {
			s.selectRow(n - 1)
		}
	case keyPagePrev:
		s.moveSelection(-a.ui.lines)
	case keyPageNext:
		s.moveSelection(a.ui.lines)
	}
}

// onMouse is on_mouse: a click accepts the row it landed on, not
// whatever was selected when the press came.
func (s *Surface) onMouse(action mouseAction, row int) {
	switch action {
	case mouseSelect:
		if row >= 0 {
			s.selectRow(row)
		}
	case mouseAccept:
		if row >= 0 {
			s.selectRow(row)
		}
		s.onKey(keyBinding{action: keyAccept})
	case mouseAcceptCustom:
		s.onKey(keyBinding{action: keyAcceptCustom})
	case mouseRowUp:
		s.moveSelection(-1)
	case mouseRowDown:
		s.moveSelection(1)
	}
}

// rowPressed runs the button bindings for a press on row pos.
func (s *Surface) rowPressed(pos int, button engine.MouseButton, presses int, mods engine.MouseModifiers) {
	if s.cur == nil {
		return
	}
	for _, act := range lookupButton(s.cur.mouse, button, presses, mods) {
		s.onMouse(act, pos)
	}
}

// listScrolled runs the wheel bindings; false lets the list scroll.
func (s *Surface) listScrolled(dy int, mods engine.MouseModifiers) bool {
	if s.cur == nil || dy == 0 {
		return false
	}
	dir := engine.ScrollDown
	if dy < 0 {
		dir = engine.ScrollUp
	}
	actions := lookupScroll(s.cur.mouse, dir, mods)
	for _, act := range actions {
		s.onMouse(act, -1)
	}
	return len(actions) > 0
}

// keyCaptured is the capture-phase key path: a bound key acts, every
// other key reaches the entry.
func (s *Surface) keyCaptured(press app.Accel) bool {
	if s.cur == nil {
		return false
	}
	b, ok := lookupKey(s.cur.keys, press)
	if !ok {
		return false
	}
	s.onKey(b)
	return true
}

// togglePicked flips the selected row in the multi-select set.
func (s *Surface) togglePicked() {
	i, ok := s.model.itemIndex(s.sel)
	if !ok {
		return
	}
	if s.multi.picked[i] {
		delete(s.multi.picked, i)
	} else {
		s.multi.picked[i] = true
	}
	s.cur.views.list.refresh(s.sel)
}

// moveSelection is move_selection: single steps wrap under -cycle, the
// rest clamp.
func (s *Surface) moveSelection(delta int) {
	n := s.model.len()
	if n == 0 {
		return
	}
	cur := max(s.sel, 0)
	var target int
	if s.cur.ui.cycle && (delta == 1 || delta == -1) {
		target = ((cur+delta)%n + n) % n
	} else {
		target = min(max(cur+delta, 0), n-1)
	}
	s.selectRow(target)
}

// activatePosition activates the row at pos.
func (s *Surface) activatePosition(pos int) {
	if i, ok := s.model.itemIndex(pos); ok {
		s.send(cmdActivate{target: engine.RowTarget(i), kind: engine.ActivateDefault{}})
	}
}

// activateCustom accepts the typed text.
func (s *Surface) activateCustom() {
	s.send(cmdActivate{target: engine.Target{}, kind: engine.ActivateCustom{Text: s.cur.views.entry.Text()}})
}

func (s *Surface) send(c engineCmd) {
	if s.cur != nil && s.cur.act != nil {
		s.cur.act.send(c)
	}
}

// armDump schedules the -dump reply once the matches settle.
func (s *Surface) armDump() {
	a := s.cur
	if !a.dump {
		return
	}
	if a.dumpStop != nil {
		a.dumpStop()
	}
	a.dumpStop = s.d.After(dumpDebounce, func() {
		if s.cur == a {
			s.end(&launcheripc.ServerFrame{Type: launcheripc.FrameDump, Items: s.model.texts()})
		}
	})
}

// end is end_session: stop the engine, answer the CLI (or release it),
// close the surface.
func (s *Surface) end(frame *launcheripc.ServerFrame) {
	a := s.cur
	if a == nil {
		return
	}
	s.cur = nil
	if a.act != nil {
		a.act.stop()
	}
	if a.dumpStop != nil {
		a.dumpStop()
	}
	a.preview.stop()
	a.cancel()
	if frame != nil {
		a.sess.Reply(*frame)
	} else {
		a.sess.Release()
	}
	closeHistory(a.history)
	if a.win != nil {
		// The panel plays its exit, then the surface goes; a new session
		// meanwhile opens its own.
		a.views.motion.close(s.d.Config().Animations, func() {
			a.win.Close()
			if a.afterClose != nil {
				a.afterClose()
			}
		})
	}
	clear(s.multi.picked)
	s.model.update(nil, nil)
	s.sel = -1
}

// typeOnceClosed enters text in the window the closed surface leaves
// focused. A failure copies it instead, so the pick is not lost.
func (s *Surface) typeOnceClosed(text string) {
	if s.d.Type == nil {
		return
	}
	s.d.After(typeSettle, func() {
		go func() {
			err := s.d.Type(text)
			if err == nil {
				return
			}
			log.Printf("launcher: cannot enter the pick, copying it instead: %v", err)
			s.d.Invoke(func() {
				if s.d.Copy != nil {
					s.d.Copy(text)
				}
			})
		}()
	})
}

// scale is the device scale to decode icons for.
// outputFor is the output launcher.monitor names; empty is the focused
// output, the compositor's choice. A name no output has is said in the
// log and the launcher still opens: it may be how you would fix it.
func (s *Surface) outputFor(name string) *app.Output {
	if name == "" || s.d.Outputs == nil {
		return nil
	}
	for _, o := range s.d.Outputs() {
		if o.Name == name {
			return o
		}
	}
	log.Printf("launcher: no output named %q (launcher.monitor): using the focused one", name)
	return nil
}

// reveal maps the surface, focuses the entry and plays the panel's enter.
func (s *Surface) reveal(a *active) {
	cfg := s.d.Config()
	win, err := s.d.Open(panelLayer(cfg.Styling.Translucent(), s.outputFor(cfg.Launcher.Monitor), a.views.panelFrame, s.keyCaptured,
		func() { s.onKey(keyBinding{action: keyCancel}) },
		func() {
			// The compositor took the surface away (output gone).
			if s.cur == a {
				a.win = nil
				s.end(&launcheripc.ServerFrame{Type: launcheripc.FrameCancelled, Code: 1})
			}
		},
	))
	if err != nil {
		log.Printf("launcher: cannot map the surface: %v", err)
		s.end(&launcheripc.ServerFrame{Type: launcheripc.FrameCancelled, Code: 1})
		return
	}
	a.win = win
	if err := win.SetFocus(a.views.entry); err != nil {
		log.Printf("launcher: cannot focus the entry: %v", err)
		s.end(&launcheripc.ServerFrame{Type: launcheripc.FrameCancelled, Code: 1})
		return
	}
	a.views.motion.open(cfg.Animations)
}

// resize sends the panel toward its size for what the session now shows.
func (s *Surface) resize() {
	if a := s.cur; a != nil && a.win != nil {
		a.views.retarget()
	}
}

// resolveWidth is resolve_width: [launcher] width unless -width
// overrode it, a percent against the monitor and a character count
// against the font's advance of "0".
func (s *Surface) resolveWidth(ui uiSettings) int {
	w := ui.widthOverride
	if w == nil {
		return max(ui.width, 1)
	}
	switch w.Unit {
	case launcheripc.WidthPixels:
		return max(int(w.Value), 1)
	case launcheripc.WidthPercent:
		monitor := ui.width
		if s.d.MonitorWidth != nil {
			if m := s.d.MonitorWidth(); m > 0 {
				monitor = m
			}
		}
		return max(int(float64(monitor)*w.Value/100), 1)
	case launcheripc.WidthChars:
		advance := max(int(math.Ceil(s.d.Font.Shape("0", fontPx()).Advance())), 1)
		return max(advance*int(w.Value), 1)
	}
	return max(ui.width, 1)
}

// rebuildTabs is rebuild_tabs: one button per mode, the active one
// marked.
func (s *Surface) rebuildTabs(names []string, activeMode int) {
	v := s.cur.views
	v.tabs.Clear()
	for i, name := range names {
		label := name
		if shown, ok := s.cur.ui.displayNames[name]; ok {
			label = shown
		}
		b := widget.NewButton(widget.NewLabel(s.d.Font, fontPx(), label, 0), 0, 0)
		b.AddClass("launcher-tab")
		if i == activeMode {
			b.AddClass("active")
			b.SetState(widget.StateChecked, true)
		}
		index := i
		b.OnClick = func() { s.send(cmdModeTo{index: index}) }
		v.tabs.Append(b, true)
	}
}

// messageMarkup is the message line's markup in lys-ui's dialect: the
// Pango markup translated, its weights and colors kept; markup that
// does not parse shows as the literal text, like a row's.
func messageMarkup(markup string) string {
	if translated, ok := pango.Translate(markup); ok {
		return translated
	}
	return pango.Escape(markup)
}

// closeHistory closes off the loop, behind whatever the session's actor is
// still doing with the database.
func closeHistory(h *engine.History) {
	if h == nil {
		return
	}
	go func() {
		if err := h.Close(); err != nil {
			log.Printf("launcher: close the history: %v", err)
		}
	}()
}
