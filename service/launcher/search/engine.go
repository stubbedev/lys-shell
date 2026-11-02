package search

import (
	"context"
	"log"
	"slices"
	"strconv"
	"sync"

	"github.com/stubbedev/lys-lib/intconv"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/launcher"
)

// Row is one line of the list: a section heading (Header set) or an
// entry.
type Row struct {
	// Header is a heading's text; "" for an entry.
	Header string
	// Entry is the row.
	Entry Entry

	query launcher.FuzzyQuery
	title []rune
	acts  func(Entry) []Action
}

// Actions lists the row's actions, the default first.
func (r Row) Actions() []Action {
	if r.acts != nil {
		return r.acts(r.Entry)
	}
	return nil
}

// Highlights are the positions in the title, counted in grapheme
// clusters, of the characters the query matched. It is computed when
// asked, so only rows on screen pay for it.
func (r Row) Highlights() []int {
	if r.query.Empty() {
		return nil
	}
	return r.query.Positions(r.title)
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
	// SetField asks the panel to put Field in the search field: a level
	// was entered or left, or an action set the query.
	SetField bool
	Field    string
	// Late marks rows that arrived while the query stood still: the
	// selection stays on the row it is on.
	Late bool
	// Rows are the list.
	Rows []Row
}

// Sink receives the engine's results; both run through deliver, on the
// UI loop.
type Sink interface {
	// Results is a new list.
	Results(Frame)
	// Finish ends the session: Close or Copy.
	Finish(Outcome)
	// Notice says something in the footer without changing the list.
	Notice(message string)
}

// engineCmd is a panel -> engine command.
type engineCmd any

type (
	cmdQuery    struct{ text string }
	cmdActivate struct {
		id     string
		action int
	}
	cmdSubmit  struct{ text string }
	cmdActions struct{ id string }
	cmdBack    struct{}
	cmdPublish struct {
		state   *levelState
		corpus  *corpus
		entries []Entry
	}
)

// levelState is one level on the stack, with the query it was left at.
type levelState struct {
	level    *Level
	corpora  []*corpus
	query    string
	sections []Section
	cancel   context.CancelFunc
}

// Engine is one session's search, on its own goroutine: providers load
// beside it and publish into it, queries are answered in order, and a
// query already overtaken by a newer one is skipped, so typing fast
// costs one answer rather than one per key.
type Engine struct {
	sink    Sink
	deliver func(func())
	freq    *Frecency
	mu      sync.Mutex
	queue   []engineCmd
	wake    chan struct{}
	cancel  context.CancelFunc
	done    chan struct{}

	stack []*levelState
	rows  []Row
	// scratch is reused between queries.
	scratch []sectionRows
}

// Start runs the engine over root. openFrecency runs on its goroutine,
// as it reads the disk; nil learns nothing. deliver hops onto the UI
// loop.
func Start(root *Level, openFrecency func() (*Frecency, error), sink Sink, deliver func(func())) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{sink: sink, deliver: deliver, wake: make(chan struct{}, 1), cancel: cancel, done: make(chan struct{})}
	go e.run(ctx, root, openFrecency)
	return e
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

// SetQuery answers text.
func (e *Engine) SetQuery(text string) { e.send(cmdQuery{text: text}) }

// Activate runs action number action of the row with this id.
func (e *Engine) Activate(id string, action int) { e.send(cmdActivate{id: id, action: action}) }

// Submit is Enter with no row: the level's Submit gets the text.
func (e *Engine) Submit(text string) { e.send(cmdSubmit{text: text}) }

// ShowActions enters the actions panel of the row with this id.
func (e *Engine) ShowActions(id string) { e.send(cmdActions{id: id}) }

// Back leaves the current level; at the root it finishes with Close.
func (e *Engine) Back() { e.send(cmdBack{}) }

// Stop ends the engine and every provider it started. It does not wait:
// the UI loop calls it, and an action in flight may still be finishing.
func (e *Engine) Stop() { e.cancel() }

// Done closes once the engine and its providers have all returned.
func (e *Engine) Done() <-chan struct{} { return e.done }

// next takes the next command, dropping a query a later one overtook
// and a publish a later one for the same corpus replaced.
func (e *Engine) next(ctx context.Context) (engineCmd, bool) {
	for {
		e.mu.Lock()
		if len(e.queue) > 0 {
			c := e.queue[0]
			e.queue = e.queue[1:]
			if overtaken(c, e.queue) {
				e.mu.Unlock()
				continue
			}
			e.mu.Unlock()
			return c, true
		}
		e.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, false
		case <-e.wake:
		}
	}
}

func overtaken(c engineCmd, rest []engineCmd) bool {
	switch v := c.(type) {
	case cmdQuery:
		// Only an adjacent query: anything between (an accept) must see
		// the text it was typed after.
		if len(rest) > 0 {
			_, again := rest[0].(cmdQuery)
			return again
		}
	case cmdPublish:
		return slices.ContainsFunc(rest, func(o engineCmd) bool {
			p, ok := o.(cmdPublish)
			return ok && p.corpus == v.corpus
		})
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
		cmd, ok := e.next(ctx)
		if !ok {
			return
		}
		switch c := cmd.(type) {
		case cmdQuery:
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
		loads.Go(func() {
			p.Load(ctx, func(entries []Entry) {
				e.send(cmdPublish{state: st, corpus: c, entries: entries})
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
		e.deliver(func() { e.sink.Finish(Close{}) })
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

func (e *Engine) activate(ctx context.Context, c cmdActivate, loads *sync.WaitGroup) {
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
		entries[i] = Entry{ID: strconv.Itoa(i), Title: a.Title, Icon: launcher.IconName(a.Icon), Data: a}
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
	case Close, Copy:
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

// frame computes the list for the current level and query and delivers
// it.
func (e *Engine) frame(o frameOpts) {
	st := e.top()
	rows := e.compute(st, o.late)
	e.rows = rows
	f := Frame{
		Depth: len(e.stack) - 1, Title: st.level.Title, Placeholder: st.level.Placeholder, Message: st.level.Message,
		Mask: st.level.Mask, SubmitTitle: st.level.SubmitTitle, CanSubmit: st.level.Submit != nil,
		Query: st.query, SetField: o.setField, Field: st.query, Late: o.late, Rows: rows,
	}
	e.deliver(func() { e.sink.Results(f) })
}

// isRoot is a level that suggests rather than lists when nothing is
// typed.
func (st *levelState) isRoot() bool { return st.level.Title == "" }

func (e *Engine) compute(st *levelState, late bool) []Row {
	query := launcher.NewFuzzyQuery(st.query)
	if query.Empty() {
		return e.emptyRows(st)
	}
	var boosts map[string]int
	if e.freq != nil {
		boosts = e.freq.QueryBoosts(st.query)
	}
	sections := e.scratch[:0]
	sectionOf := func(s Section) *sectionRows {
		for i := range sections {
			if sections[i].section == s {
				return &sections[i]
			}
		}
		sections = append(sections, sectionRows{section: s})
		return &sections[len(sections)-1]
	}
	queryLen := len([]rune(st.query))
	for _, c := range st.corpora {
		spec := c.spec
		limit := spec.Limit
		if limit == 0 {
			limit = flatRowCap
		}
		if a, ok := c.prov.(Answerer); ok {
			if answers := a.Answer(st.query); len(answers) > 0 {
				sec := sectionOf(spec.Section)
				for i := range answers {
					sec.rows = append(sec.rows, ranked{corpus: c, answer: &answers[i], score: intconv.Clamp[int32](answerScore - i)})
				}
				slices.SortStableFunc(sec.rows, better)
			}
		}
		if queryLen < spec.MinQuery {
			continue
		}
		c.scan(query, st.query)
		if len(c.hits) == 0 {
			continue
		}
		sec := sectionOf(spec.Section)
		for _, h := range c.hits {
			score := h.score + intconv.Clamp[int32](spec.Weight+c.entries[h.index].Rank) + c.boost[h.index]
			if boosts != nil {
				if learn := c.entries[h.index].Learn; learn != "" {
					score += intconv.Clamp[int32](boosts[learn])
				}
			}
			sec.rows = insertTop(sec.rows, ranked{corpus: c, index: h.index, score: score}, limit)
		}
	}
	sections = slices.DeleteFunc(sections, func(s sectionRows) bool { return len(s.rows) == 0 })
	order := orderSections(sections, st.sections, late)
	st.sections = order
	rows := make([]Row, 0, 64)
	flat := len(sections) == 1 && (sections[0].section == SectionNone || !st.isRoot())
	for _, sec := range sections {
		if !flat && sec.section != SectionNone {
			rows = append(rows, Row{Header: sec.section.Title()})
		}
		for _, r := range sec.rows {
			rows = append(rows, rowOf(r, query))
		}
	}
	e.scratch = sections[:0]
	return rows
}

func rowOf(r ranked, query launcher.FuzzyQuery) Row {
	en := r.entry()
	row := Row{Entry: *en, query: query, acts: r.corpus.prov.Actions}
	if r.answer == nil {
		row.title = r.corpus.titles[r.index]
	} else {
		row.query = launcher.FuzzyQuery{}
	}
	return row
}

// emptyRows is an empty query's list: at the root what gets used most;
// in any other level everything, used things first.
func (e *Engine) emptyRows(st *levelState) []Row {
	st.sections = nil
	var cands []ranked
	root := st.isRoot()
	for _, c := range st.corpora {
		if c.spec.MinQuery > 0 && !root {
			continue
		}
		for i := range c.entries {
			if root && c.boost[i] == 0 {
				continue
			}
			cands = append(cands, ranked{corpus: c, index: intconv.Clamp[int32](i), score: c.boost[i]})
		}
	}
	if root {
		slices.SortStableFunc(cands, better)
		cands = cands[:min(len(cands), recentRows)]
		if len(cands) == 0 {
			return nil
		}
		rows := make([]Row, 0, len(cands)+1)
		rows = append(rows, Row{Header: SectionRecent.Title()})
		for _, r := range cands {
			rows = append(rows, rowOf(r, launcher.FuzzyQuery{}))
		}
		return rows
	}
	slices.SortStableFunc(cands, func(a, b ranked) int { return int(b.score) - int(a.score) })
	cands = cands[:min(len(cands), flatRowCap)]
	rows := make([]Row, len(cands))
	for i, r := range cands {
		rows[i] = rowOf(r, launcher.FuzzyQuery{})
	}
	return rows
}
