package search

import (
	"context"
	"log"
	"slices"
	"strconv"
	"sync"

	"github.com/stubbedev/lys-desktop/i18n"
	"github.com/stubbedev/lys-lib/intconv"

	"github.com/stubbedev/lys-shell/service/launcher"
)

// Row is one line of the list: a section heading (Header set) or an
// entry. It points at its entry rather than copying it, and a row of the
// list is 64 bytes, so a frame costs little whatever an entry holds.
type Row struct {
	// Header is a heading's text; "" for an entry.
	Header string
	// Entry is the row; nil for a heading. It is never written after its
	// provider published it.
	Entry *Entry

	prov Provider
	// title is the title as the matcher read it; nil for an answer, whose
	// text is the answer rather than a match.
	title []rune
}

// Actions lists the row's actions, the default first.
func (r Row) Actions() []Action {
	if r.prov == nil {
		return nil
	}
	return r.prov.Actions(*r.Entry)
}

// AppendHighlights appends the positions in the title, counted in
// grapheme clusters, of the characters q matched. It is computed when
// asked, so only rows on screen pay for it, on the reader's own q and
// buffer.
func (r Row) AppendHighlights(dst []int, q *launcher.FuzzyQuery) []int {
	if r.title == nil {
		return dst
	}
	return q.AppendPositions(dst, r.title)
}

// Frame is everything the panel shows for one state of the search.
type Frame struct {
	// Depth is how many levels sit under this one; 0 is the root.
	Depth int
	// Title, Placeholder, Message, Mask and SubmitTitle are the level's.
	Title, Placeholder, Message string
	Mask                        bool
	SubmitTitle                 string
	// CanSubmit is Enter accepting the typed text with no row selected.
	CanSubmit bool
	// Query is the text these rows answer.
	Query string
	// QuerySeq is the last edit of the field the engine had seen. A panel
	// that has sent a later one keeps its text: what was typed since must
	// not be overwritten by a frame that predates it.
	QuerySeq uint64
	// SetField asks the panel to put Field in the search field: a level
	// was entered or left, or an action set the query.
	SetField bool
	Field    string
	// Late marks rows that arrived while the query stood still: the
	// selection stays on the row it is on.
	Late bool
	// Rows are the list. They stay the receiver's until it hands them
	// back with Engine.Recycle.
	Rows []Row
}

// Sink receives the engine's results; both run through deliver, on the
// UI loop.
type Sink interface {
	// Results is a new list.
	Results(Frame)
	// Finish ends the session: Close, Cancel or Copy.
	Finish(Outcome)
	// Notice says something in the footer without changing the list.
	Notice(message string)
}

// cmdKind is a panel -> engine command's kind.
type cmdKind uint8

const (
	cmdQuery cmdKind = iota + 1
	cmdActivate
	cmdSubmit
	cmdActions
	cmdBack
	cmdPublish
)

// engineCmd is one command, a plain value, so sending one per keystroke
// boxes nothing.
type engineCmd struct {
	kind cmdKind
	// text is a query or a submission; id a row.
	text, id string
	seq      uint64
	action   int
	state    *levelState
	corpus   *corpus
	entries  []Entry
}

// levelState is one level on the stack, with the query it was left at.
type levelState struct {
	level    *Level
	corpora  []*corpus
	query    string
	sections []Section
	cancel   context.CancelFunc
}

// frameBox is a pooled frame with its delivery bound once, so delivering
// one allocates no closure.
type frameBox struct {
	frame Frame
	run   func()
}

// Engine is one session's search, on its own goroutine: providers load
// beside it and publish into it, queries are answered in order, and a
// query already overtaken by a newer one is skipped, so typing fast
// costs one answer rather than one per key. Answering a query reuses its
// memory: only what a provider's answer or the query's own text needs is
// allocated.
type Engine struct {
	sink    Sink
	deliver func(func())
	freq    *Frecency

	mu    sync.Mutex
	queue []engineCmd
	head  int
	// freeRows and freeBoxes come back from the UI loop.
	freeRows  [][]Row
	freeBoxes []*frameBox

	wake   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}

	stack []*levelState
	rows  []Row
	// seq is the last edit of the search field answered.
	seq uint64

	// What one query's answer works in, reused.
	query     launcher.FuzzyQuery
	sections  []sectionRows
	secBuf    [sectionCount][]ranked
	boosts    map[string]int
	emptyCand []ranked
}

// Start runs the engine over root on a goroutine spawn starts, which
// ends once the engine and its providers have all returned.
// openFrecency runs on that goroutine, as it reads the disk; nil learns
// nothing. deliver hops onto the UI loop.
func Start(spawn func(func()), root *Level, openFrecency func() (*Frecency, error), sink Sink, deliver func(func())) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	e := newEngine()
	e.sink, e.deliver, e.cancel = sink, deliver, cancel
	spawn(func() { e.run(ctx, root, openFrecency) })
	return e
}

func newEngine() *Engine {
	return &Engine{wake: make(chan struct{}, 1), done: make(chan struct{}), boosts: map[string]int{}}
}

func (e *Engine) send(c engineCmd) {
	e.mu.Lock()
	e.queue = append(e.queue, c)
	e.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// SetQuery answers text; seq numbers the edit, so a frame can say which
// edits it has seen.
func (e *Engine) SetQuery(text string, seq uint64) {
	e.send(engineCmd{kind: cmdQuery, text: text, seq: seq})
}

// Activate runs action number action of the row with this id.
func (e *Engine) Activate(id string, action int) {
	e.send(engineCmd{kind: cmdActivate, id: id, action: action})
}

// Submit is Enter with no row: the level's Submit gets the text.
func (e *Engine) Submit(text string) { e.send(engineCmd{kind: cmdSubmit, text: text}) }

// ShowActions enters the actions panel of the row with this id.
func (e *Engine) ShowActions(id string) { e.send(engineCmd{kind: cmdActions, id: id}) }

// Back leaves the current level; at the root it finishes with Cancel.
func (e *Engine) Back() { e.send(engineCmd{kind: cmdBack}) }

// Recycle hands a frame's rows back once nothing reads them.
func (e *Engine) Recycle(rows []Row) {
	if cap(rows) == 0 {
		return
	}
	clear(rows)
	e.mu.Lock()
	e.freeRows = append(e.freeRows, rows[:0])
	e.mu.Unlock()
}

// Stop ends the engine and every provider it started. It does not wait:
// the UI loop calls it, and an action in flight may still be finishing.
func (e *Engine) Stop() { e.cancel() }

// Done closes once the engine and its providers have all returned.
func (e *Engine) Done() <-chan struct{} { return e.done }

// next takes the next command, dropping a query a later one overtook
// and a publish a later one for the same corpus replaced. The queue's
// memory is reused once it drains.
func (e *Engine) next(ctx context.Context) (engineCmd, bool) {
	for {
		e.mu.Lock()
		for e.head < len(e.queue) {
			c := e.queue[e.head]
			e.queue[e.head] = engineCmd{}
			e.head++
			rest := e.queue[e.head:]
			if e.head == len(e.queue) {
				e.queue, e.head = e.queue[:0], 0
			}
			if overtaken(&c, rest) {
				continue
			}
			e.mu.Unlock()
			return c, true
		}
		e.mu.Unlock()
		select {
		case <-ctx.Done():
			return engineCmd{}, false
		case <-e.wake:
		}
	}
}

func overtaken(c *engineCmd, rest []engineCmd) bool {
	switch c.kind {
	case cmdQuery:
		// Only an adjacent query: anything between (an accept) must see
		// the text it was typed after.
		return len(rest) > 0 && rest[0].kind == cmdQuery
	case cmdPublish:
		for i := range rest {
			if rest[i].kind == cmdPublish && rest[i].corpus == c.corpus {
				return true
			}
		}
	}
	return false
}

func (e *Engine) run(ctx context.Context, root *Level, openFrecency func() (*Frecency, error)) {
	defer close(e.done)
	if openFrecency != nil {
		f, err := openFrecency()
		if err != nil {
			log.Printf("launcher: %v", err)
		}
		e.freq = f
	}
	var loads sync.WaitGroup
	defer loads.Wait()
	defer e.leaveAll()
	e.push(ctx, root, &loads)
	e.frame(frameOpts{setField: true})
	for {
		c, ok := e.next(ctx)
		if !ok {
			return
		}
		switch c.kind {
		case cmdQuery:
			e.seq = c.seq
			e.top().query = c.text
			e.frame(frameOpts{})
		case cmdPublish:
			c.corpus.set(c.entries, e.boost)
			if c.state == e.top() {
				e.frame(frameOpts{late: true})
			}
		case cmdActivate:
			e.activate(ctx, c, &loads)
		case cmdSubmit:
			if s := e.top().level.Submit; s != nil {
				e.apply(ctx, s(ctx, c.text), &loads)
			}
		case cmdActions:
			e.pushActions(ctx, c.id, &loads)
		case cmdBack:
			e.back()
		}
	}
}

func (e *Engine) top() *levelState { return e.stack[len(e.stack)-1] }

func (e *Engine) boost(key string) int {
	if e.freq == nil || key == "" {
		return 0
	}
	return e.freq.Boost(key)
}

// push enters a level and starts its providers.
func (e *Engine) push(ctx context.Context, l *Level, loads *sync.WaitGroup) {
	e.stack = append(e.stack, e.enter(ctx, l, loads))
}

func (e *Engine) enter(ctx context.Context, l *Level, loads *sync.WaitGroup) *levelState {
	levelCtx, cancel := context.WithCancel(ctx)
	st := &levelState{level: l, query: l.Query, cancel: cancel}
	st.start(levelCtx, e, loads)
	return st
}

func (st *levelState) start(ctx context.Context, e *Engine, loads *sync.WaitGroup) {
	st.corpora = st.corpora[:0]
	for _, p := range st.level.Providers {
		c := newCorpus(p)
		st.corpora = append(st.corpora, c)
		if _, now := p.(Immediate); now {
			p.Load(ctx, func(entries []Entry) { c.set(entries, e.boost) })
			continue
		}
		if k, ok := p.(*kept); ok {
			entries, has, fresh := k.rows.snapshot(k.now())
			if has {
				c.set(entries, e.boost)
			}
			if fresh {
				continue
			}
		}
		loads.Go(func() {
			p.Load(ctx, func(entries []Entry) {
				e.send(engineCmd{kind: cmdPublish, state: st, corpus: c, entries: entries})
			})
		})
	}
}

func (e *Engine) leaveAll() {
	for _, st := range e.stack {
		st.cancel()
	}
}

func (e *Engine) back() {
	if len(e.stack) == 1 {
		e.deliver(func() { e.sink.Finish(Cancel{}) })
		return
	}
	e.top().cancel()
	e.stack = e.stack[:len(e.stack)-1]
	e.frame(frameOpts{setField: true})
}

// rowByID finds a row of the last frame.
func (e *Engine) rowByID(id string) (Row, bool) {
	for _, r := range e.rows {
		if r.Header == "" && r.Entry.ID == id {
			return r, true
		}
	}
	return Row{}, false
}

func (e *Engine) record(learn, query string) {
	if e.freq == nil || learn == "" {
		return
	}
	if err := e.freq.Record(learn, query); err != nil {
		log.Printf("launcher: %v", err)
	}
}

func (e *Engine) activate(ctx context.Context, c engineCmd, loads *sync.WaitGroup) {
	row, ok := e.rowByID(c.id)
	if !ok {
		return
	}
	actions := row.Actions()
	if c.action >= len(actions) {
		return
	}
	e.record(row.Entry.Learn, e.top().query)
	e.apply(ctx, actions[c.action].Run(ctx), loads)
}

// actionsProvider lists a row's actions as rows of their own.
type actionsProvider struct {
	actions []Action
}

func (*actionsProvider) Spec() Spec { return Spec{Section: SectionNone} }

func (*actionsProvider) Immediate() {}

func (p *actionsProvider) Load(_ context.Context, publish func([]Entry)) {
	entries := make([]Entry, len(p.actions))
	for i, a := range p.actions {
		entries[i] = Entry{ID: strconv.Itoa(i), Title: a.Title, Hint: a.Key.Label(), Icon: launcher.IconName(a.Icon), Data: a}
	}
	publish(entries)
}

func (*actionsProvider) Actions(e Entry) []Action {
	a, ok := e.Data.(Action)
	if !ok {
		return nil
	}
	return []Action{a}
}

func (e *Engine) pushActions(ctx context.Context, id string, loads *sync.WaitGroup) {
	row, ok := e.rowByID(id)
	if !ok {
		return
	}
	actions := row.Actions()
	if len(actions) == 0 {
		return
	}
	query := e.top().query
	learn := row.Entry.Learn
	wrapped := make([]Action, len(actions))
	for i, a := range actions {
		run := a.Run
		a.Run = func(ctx context.Context) Outcome {
			e.record(learn, query)
			return run(ctx)
		}
		wrapped[i] = a
	}
	e.push(ctx, &Level{
		Title:       row.Entry.Title,
		Placeholder: i18n.T("launcher-actions-placeholder"),
		Providers:   []Provider{&actionsProvider{actions: wrapped}},
	}, loads)
	e.frame(frameOpts{setField: true})
}

// apply does what an action asked.
func (e *Engine) apply(ctx context.Context, o Outcome, loads *sync.WaitGroup) {
	switch v := o.(type) {
	case nil:
	case Close, CloseThen, Copy, Type:
		e.deliver(func() { e.sink.Finish(o) })
	case SetQuery:
		// A query typed in an actions panel belongs to that panel: setting
		// one means searching again, from the level that was searching.
		for len(e.stack) > 1 && isActionsLevel(e.top()) {
			e.top().cancel()
			e.stack = e.stack[:len(e.stack)-1]
		}
		e.top().query = v.Text
		e.frame(frameOpts{setField: true})
	case Push:
		e.push(ctx, v.Level, loads)
		e.frame(frameOpts{setField: true})
	case Replace:
		old := e.top()
		old.cancel()
		e.stack[len(e.stack)-1] = e.enter(ctx, v.Level, loads)
		e.frame(frameOpts{setField: true})
	case Reload:
		st := e.top()
		st.cancel()
		levelCtx, cancel := context.WithCancel(ctx)
		st.cancel = cancel
		st.start(levelCtx, e, loads)
	case Stay:
		if v.Message != "" {
			e.deliver(func() { e.sink.Notice(v.Message) })
		}
	}
}

func isActionsLevel(st *levelState) bool {
	if len(st.level.Providers) != 1 {
		return false
	}
	_, ok := st.level.Providers[0].(*actionsProvider)
	return ok
}

type frameOpts struct {
	setField bool
	late     bool
}

// takeRows is an empty row slice from the UI loop's returns.
func (e *Engine) takeRows() []Row {
	e.mu.Lock()
	defer e.mu.Unlock()
	if n := len(e.freeRows); n > 0 {
		rows := e.freeRows[n-1]
		e.freeRows = e.freeRows[:n-1]
		return rows
	}
	return make([]Row, 0, 64)
}

func (e *Engine) takeBox() *frameBox {
	e.mu.Lock()
	defer e.mu.Unlock()
	if n := len(e.freeBoxes); n > 0 {
		box := e.freeBoxes[n-1]
		e.freeBoxes = e.freeBoxes[:n-1]
		return box
	}
	box := &frameBox{}
	box.run = func() {
		e.sink.Results(box.frame)
		box.frame = Frame{}
		e.mu.Lock()
		e.freeBoxes = append(e.freeBoxes, box)
		e.mu.Unlock()
	}
	return box
}

// frame computes the list for the current level and query and delivers
// it.
func (e *Engine) frame(o frameOpts) {
	st := e.top()
	rows := e.compute(st, o.late, e.takeRows())
	if o.late && sameRows(rows, e.rows) {
		e.Recycle(rows)
		return
	}
	// e.rows reads the frame's rows; the UI loop only writes them when it
	// recycles, and it recycles a frame only after a newer one reached it.
	e.rows = rows
	box := e.takeBox()
	box.frame = Frame{
		Depth: len(e.stack) - 1, Title: st.level.Title, Placeholder: st.level.Placeholder, Message: st.level.Message,
		Mask: st.level.Mask, SubmitTitle: st.level.SubmitTitle, CanSubmit: st.level.Submit != nil,
		Query: st.query, QuerySeq: e.seq, SetField: o.setField, Field: st.query, Late: o.late, Rows: rows,
	}
	e.deliver(box.run)
}

// sameRows reports a list that reads as the one showing: rows arriving
// late that change nothing on screen are not delivered.
func sameRows(a, b []Row) bool {
	return slices.EqualFunc(a, b, func(x, y Row) bool {
		if x.Header != y.Header || (x.Entry == nil) != (y.Entry == nil) {
			return false
		}
		if x.Entry == nil {
			return true
		}
		ex, ey := x.Entry, y.Entry
		return ex.ID == ey.ID && ex.Title == ey.Title && ex.Subtitle == ey.Subtitle && ex.Hint == ey.Hint && ex.Glyph == ey.Glyph
	})
}

// isRoot is a level that suggests rather than lists when nothing is
// typed.
func (st *levelState) isRoot() bool { return st.level.Title == "" }

// sectionOf is the working section for s, opened on first use.
func (e *Engine) sectionOf(s Section) *sectionRows {
	for i := range e.sections {
		if e.sections[i].section == s {
			return &e.sections[i]
		}
	}
	e.sections = append(e.sections, sectionRows{section: s, rows: e.secBuf[s][:0]})
	return &e.sections[len(e.sections)-1]
}

// compute answers st's query into rows.
func (e *Engine) compute(st *levelState, late bool, rows []Row) []Row {
	e.query.Set(st.query)
	if e.query.Empty() {
		return e.emptyRows(st, rows)
	}
	useBoosts := false
	if e.freq != nil {
		clear(e.boosts)
		useBoosts = e.freq.QueryBoostsInto(e.boosts, st.query)
	}
	e.sections = e.sections[:0]
	queryLen := 0
	for range st.query {
		queryLen++
	}
	for _, c := range st.corpora {
		spec := c.spec
		limit := spec.Section.rowCap()
		if a, ok := c.prov.(Answerer); ok {
			if answers := a.Answer(st.query); len(answers) > 0 {
				sec := e.sectionOf(spec.Section)
				for i := range answers {
					sec.rows = append(sec.rows, ranked{corpus: c, answer: &answers[i], score: intconv.Clamp[int32](answerScore - i)})
				}
				slices.SortStableFunc(sec.rows, better)
			}
		}
		if queryLen < spec.MinQuery {
			continue
		}
		if s, ok := c.prov.(Searcher); ok {
			e.searched(c, s.Search(&e.query, st.query, limit), limit)
			continue
		}
		c.scan(&e.query, st.query)
		if len(c.hits) == 0 {
			continue
		}
		sec := e.sectionOf(spec.Section)
		for _, h := range c.hits {
			entry := &c.entries[h.index]
			score := h.score + intconv.Clamp[int32](spec.Weight+entry.Rank) + c.boost[h.index]
			if useBoosts && entry.Learn != "" {
				score += intconv.Clamp[int32](e.boosts[entry.Learn])
			}
			sec.rows = insertTop(sec.rows, ranked{corpus: c, index: h.index, score: score}, limit)
		}
	}
	kept := e.sections[:0]
	for _, s := range e.sections {
		if len(s.rows) > 0 {
			kept = append(kept, s)
		}
	}
	e.sections = kept
	st.sections = orderSections(e.sections, st.sections, late)
	flat := len(e.sections) == 1 && (e.sections[0].section == SectionNone || !st.isRoot())
	for i := range e.sections {
		sec := &e.sections[i]
		if !flat && sec.section != SectionNone {
			rows = append(rows, Row{Header: sec.section.Title()})
		}
		for _, r := range sec.rows {
			rows = append(rows, rowOf(r))
		}
		e.secBuf[sec.section] = sec.rows[:0]
	}
	return rows
}

// searched ranks a Searcher's rows into its section.
func (e *Engine) searched(c *corpus, found []Found, limit int) {
	if len(found) == 0 {
		return
	}
	sec := e.sectionOf(c.spec.Section)
	for i := range found {
		f := &found[i]
		score := f.Score + intconv.Clamp[int32](c.spec.Weight)
		sec.rows = insertTop(sec.rows, ranked{corpus: c, answer: &f.Entry, title: f.Title, score: score}, limit)
	}
}

func rowOf(r ranked) Row {
	row := Row{Entry: r.entry(), prov: r.corpus.prov, title: r.title}
	if r.answer == nil {
		row.title = r.corpus.titles[r.index]
	}
	return row
}

// emptyRows is an empty query's list: at the root what gets used most;
// in any other level everything, used things first.
func (e *Engine) emptyRows(st *levelState, rows []Row) []Row {
	st.sections = st.sections[:0]
	cands := e.emptyCand[:0]
	root := st.isRoot()
	for _, c := range st.corpora {
		if c.spec.MinQuery > 0 && !root {
			continue
		}
		if s, ok := c.prov.(Searcher); ok {
			if !root {
				for _, f := range s.Search(&e.query, "", flatRowCap) {
					cands = append(cands, ranked{corpus: c, answer: &f.Entry, title: f.Title})
				}
			}
			continue
		}
		for i := range c.entries {
			if root && c.boost[i] == 0 {
				continue
			}
			cands = append(cands, ranked{corpus: c, index: intconv.Clamp[int32](i), score: c.boost[i]})
		}
	}
	e.emptyCand = cands
	if root {
		slices.SortStableFunc(cands, better)
		cands = cands[:min(len(cands), recentRows)]
		if len(cands) == 0 {
			return rows
		}
		rows = append(rows, Row{Header: SectionRecent.Title()})
		for _, r := range cands {
			rows = append(rows, rowOf(r))
		}
		return rows
	}
	slices.SortStableFunc(cands, func(a, b ranked) int { return int(b.score) - int(a.score) })
	for _, r := range cands[:min(len(cands), flatRowCap)] {
		rows = append(rows, rowOf(r))
	}
	return rows
}
