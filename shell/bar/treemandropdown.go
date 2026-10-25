package bar

import (
	"context"
	"log"
	"strconv"
	"sync"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/treeman"
)

// treemanBucketLabel is bucket_label.
func treemanBucketLabel(b treeman.Bucket) string {
	switch b {
	case treeman.BucketUp:
		return i18n.T("dropdown-treeman-bucket-up")
	case treeman.BucketDown:
		return i18n.T("dropdown-treeman-bucket-down")
	case treeman.BucketFailed:
		return i18n.T("dropdown-treeman-bucket-failed")
	}
	return i18n.T("dropdown-treeman-bucket-stable")
}

// treemanVariant is dot_variant: the status-dot and badge color class.
func treemanVariant(b treeman.Bucket) string {
	switch b {
	case treeman.BucketUp:
		return "info"
	case treeman.BucketDown:
		return "warning"
	case treeman.BucketFailed:
		return "error"
	}
	return "success"
}

// treemanView is the treeman dropdown: the repo list (bucket summary
// chips over accordion repo cards of worktree rows) and a worktree's
// detail page; the header's back button returns to the list.
type treemanView struct {
	ctx  ModuleContext
	font render.Font
	px   float64

	*widget.Box
	title   *widget.Item
	back    *widget.Button
	pages   *widget.Stack
	list    *widget.Box
	details *widget.Box

	status    *treeman.Status
	detail    string
	collapsed map[string]bool

	once   sync.Once
	cancel context.CancelFunc
}

func treemanDropdown(ctx ModuleContext) widget.Widget {
	font, px := dropdownFont(ctx)
	v := &treemanView{ctx: ctx, font: font, px: px, collapsed: map[string]bool{}, cancel: func() {}}
	v.Box = widget.NewBox(widget.Column, 0, 0)
	v.AddClass("dropdown", "treeman-dropdown")
	backIcon := widget.NewThemeIcon("ld-arrow-left-symbolic", int(px))
	v.back = dropdownButton(backIcon, func() { v.openDetail("") }, css.Ghost, css.Icon)
	v.back.SetTooltip(i18n.T("dropdown-treeman-back"))
	header, _, title := dropdownHeaderParts(font, "ld-layers-symbolic", i18n.T("dropdown-treeman-title"), v.back)
	v.title = title
	v.Append(header, false)

	th := widget.Current()
	v.list = widget.NewBox(widget.Column, 2*th.Spacing, 0)
	v.list.AddClass("treeman-list")
	v.details = widget.NewBox(widget.Column, 2*th.Spacing, 0)
	v.details.AddClass("treeman-list", "treeman-detail")
	v.pages = widget.NewStack()
	pageSlide(v.pages, ctx.Config)
	v.pages.Add("list", v.list)
	v.pages.Add("detail", v.details)
	scroll := dropdownScroll(v.pages, "treeman-scroll")
	content := dropdownContent()
	content.Append(scroll, true)
	v.Append(content, true)

	v.apply(v.read(context.Background()))
	v.follow()
	return v
}

func (v *treemanView) read(context.Context) *treeman.Status {
	if v.ctx.Treeman == nil {
		return nil
	}
	return v.ctx.Treeman.Status()
}

// apply is StatusChanged: the list re-renders and the page resyncs.
func (v *treemanView) apply(s *treeman.Status) {
	v.status = s
	v.renderList()
	v.syncPage()
}

// openDetail is OpenDetail and Back ("" is the list).
func (v *treemanView) openDetail(path string) {
	v.detail = path
	v.syncPage()
}

// syncPage is sync_page: the detail page while its worktree exists,
// else back to the list (a teardown finishing under the open page).
func (v *treemanView) syncPage() {
	if v.detail != "" && v.status != nil {
		if repo, wt, ok := v.status.FindWorktree(v.detail); ok {
			v.title.SetTitle(wt.Branch)
			v.back.SetVisible(true)
			v.renderDetail(repo, wt)
			v.pages.Show("detail")
			return
		}
	}
	v.detail = ""
	v.title.SetTitle(i18n.T("dropdown-treeman-title"))
	v.back.SetVisible(false)
	v.pages.Show("list")
}

// badge is lys-ui's Badge: the quiet secondary fill, or with a status
// variant the status color's fill.
func (v *treemanView) badge(text, variant string) *widget.Badge {
	b := widget.NewBadge(v.font, text)
	if variant == "" {
		b.SetVariant(widget.BadgeSecondary)
	} else {
		b.AddClass(variant)
	}
	return b
}

// mainBadge marks the repository's main worktree: lys-ui's outline
// badge.
func (v *treemanView) mainBadge() *widget.Badge {
	b := widget.NewBadge(v.font, i18n.T("dropdown-treeman-main"))
	b.SetVariant(widget.BadgeOutline)
	b.AddClass("treeman-badge", "main")
	return b
}

// worktreeItem is a worktree as lys-ui's Item: its status dot as the
// bare media, its branch as the one-line title, the main badge when it
// is the main worktree.
func (v *treemanView) worktreeItem(wt treeman.Worktree) *widget.Item {
	bucket := treeman.ParseBucket(wt.Bucket)
	it := widget.NewItem(v.font, wt.Branch, "")
	it.SetSmall(true)
	it.SetEllipsize(true)
	it.SetMedia(statusDot(treemanVariant(bucket)), true)
	if wt.IsMain {
		it.AddAction(v.mainBadge())
	}
	return it
}

// renderList is render_list.
func (v *treemanView) renderList() {
	v.list.Clear()
	if v.status == nil || len(v.status.Repos) == 0 {
		v.list.Append(emptyState(v.font, v.px, "ld-layers-symbolic", i18n.T("dropdown-treeman-empty-title"), i18n.T("dropdown-treeman-empty-desc")), false)
		return
	}
	v.list.Append(v.summary(), false)
	for _, repo := range v.status.Repos {
		v.list.Append(v.repoCard(repo), false)
	}
}

// summary is the per-bucket chip row; empty buckets are left out.
func (v *treemanView) summary() widget.Widget {
	th := widget.Current()
	row := widget.NewBox(widget.Row, 2*th.Spacing, 0)
	row.AddClass("treeman-summary")
	s := v.status
	for _, c := range []struct {
		n uint32
		b treeman.Bucket
	}{{s.Stable, treeman.BucketStable}, {s.Up, treeman.BucketUp}, {s.Down, treeman.BucketDown}, {s.Failed, treeman.BucketFailed}} {
		if c.n == 0 {
			continue
		}
		chip := widget.NewBox(widget.Row, th.Spacing/2, 0)
		chip.AddClass("treeman-stat")
		chip.AppendAligned(statusDot(treemanVariant(c.b)), false, widget.AlignCenter)
		label := widget.NewLabel(v.font, v.px, strconv.FormatUint(uint64(c.n), 10)+" "+treemanBucketLabel(c.b), 0)
		label.AddClass("treeman-stat-label", css.Caption, css.DimLabel)
		chip.AppendAligned(label, false, widget.AlignCenter)
		row.Append(chip, false)
	}
	return row
}

// repoCard is repo_card: lys-ui's card whose header Item (the chevron,
// the repo, its worktree count) folds the worktree rows into a
// slide-down revealer, the fold kept across status refreshes.
func (v *treemanView) repoCard(repo treeman.Repo) widget.Widget {
	card := widget.NewBox(widget.Column, 0, 0)
	card.AddClass(css.Card, "treeman-repo")
	rows := widget.NewItemGroup()
	for _, wt := range repo.Worktrees {
		rows.Append(v.worktreeRow(wt))
	}
	expanded := !v.collapsed[repo.Repo]
	// The accordion: a revealer sliding the rows in and out, the Rust
	// Revealer's SlideDown.
	fold := widget.NewRevealer(rows)
	fold.SetTransition(widget.RevealSlideDown)
	fold.SetRevealed(expanded)
	// A fresh card shows its fold as it stands; only a click slides it.
	fold.Finish()

	head := widget.NewItem(v.font, repo.Repo, "")
	head.SetSmall(true)
	head.SetEllipsize(true)
	head.AddClass("treeman-repo-header")
	chevron := widget.NewThemeIcon(treemanChevron(expanded), itemIconPx)
	head.SetMedia(chevron, true)
	head.AddAction(v.badge(strconv.FormatUint(uint64(repo.Total), 10), ""))
	name := repo.Repo
	head.SetOnActivate(func() {
		open := !fold.Revealed()
		fold.SetRevealed(open)
		chevron.SetThemeName(treemanChevron(open))
		if open {
			delete(v.collapsed, name)
		} else {
			v.collapsed[name] = true
		}
	})
	card.Append(head, false)
	card.Append(fold, false)
	return card
}

func treemanChevron(expanded bool) string {
	if expanded {
		return "ld-chevron-down-symbolic"
	}
	return "ld-chevron-right-symbolic"
}

// worktreeRow is worktree_row: the worktree's Item, whose trailing
// slot shows its state badge at rest and, while hovered, its action
// cluster in the badge's place.
func (v *treemanView) worktreeRow(wt treeman.Worktree) *widget.Item {
	bucket := treeman.ParseBucket(wt.Bucket)
	row := v.worktreeItem(wt)
	row.AddClass("treeman-wt")
	row.SetTooltip(wt.Path)
	state := v.badge(wt.State, treemanVariant(bucket))
	if wt.Path == "" {
		row.AddAction(state)
		return row
	}
	cluster := v.actions(wt.Path)
	cluster.InsertAt(0, v.ghostIcon("ld-info-symbolic", i18n.T("dropdown-treeman-action-info"), func() { v.openDetail(wt.Path) }), false)
	slot := widget.NewStack()
	slot.SetTransition(widget.StackCrossfade, hoverTransition)
	slot.Add("state", atEnd(state))
	slot.Add("actions", cluster)
	row.SetOnHoverWithin(func(on bool) {
		if on {
			slot.Show("actions")
		} else {
			slot.Show("state")
		}
	})
	row.AddAction(slot)
	return row
}

func (v *treemanView) ghostIcon(icon, tooltip string, onClick func()) *widget.Button {
	glyph := widget.NewThemeIcon(icon, itemIconPx)
	b := dropdownButton(glyph, onClick, css.Ghost, css.Icon, css.Small)
	b.SetTooltip(tooltip)
	return b
}

// actions is Actions::buttons: prepare, reset (confirmed), teardown.
func (v *treemanView) actions(path string) *widget.Box {
	row := widget.NewBox(widget.Row, widget.Current().Spacing/2, 0)
	row.AddClass("treeman-actions")
	row.Append(v.ghostIcon("tb-refresh-symbolic", i18n.T("dropdown-treeman-action-prepare"),
		func() { v.run(treeman.ActionPrepare, path) }), false)
	row.Append(v.ghostIcon("ld-rotate-ccw-symbolic", i18n.T("dropdown-treeman-action-reset"),
		func() { v.confirmReset(path) }), false)
	// Deliberately unconfirmed, as in Rust: a worktree is cheap to
	// recreate from its branch.
	row.Append(v.ghostIcon("ld-trash-2-symbolic", i18n.T("dropdown-treeman-action-teardown"),
		func() { v.run(treeman.ActionTeardown, path) }), false)
	return row
}

// run queues the action off the loop; a failure toasts.
func (v *treemanView) run(action treeman.Action, path string) {
	src := v.ctx.Treeman
	if src == nil {
		return
	}
	go func() {
		if err := src.RunAction(context.Background(), action, path); err != nil {
			log.Printf("treeman: action failed: %v", err)
			if v.ctx.Toast != nil {
				v.ctx.Toast(i18n.T("dropdown-treeman-action-failed")+": "+err.Error(), "tb-alert-triangle-symbolic")
			}
		}
	}()
}

// confirmReset is confirm_then_run for reset: a modal alert with the
// reset title and the worktree path, Cancel the default, and the
// action runs only when the accept button answers.
func (v *treemanView) confirmReset(path string) {
	v.ctx.showAlert(alertSpec{
		message: i18n.T("dropdown-treeman-confirm-reset-title"),
		detail:  path,
		cancel:  i18n.T("dropdown-treeman-confirm-cancel"),
		accept:  i18n.T("dropdown-treeman-confirm-reset-accept"),
	}, func(accepted bool) {
		if accepted {
			v.run(treeman.ActionReset, path)
		}
	})
}

// renderDetail is detail_page: an item card with the worktree as its
// subject (its dot, branch and badges) over a row per field, and the
// actions under it at the end.
func (v *treemanView) renderDetail(repo treeman.Repo, wt treeman.Worktree) {
	v.details.Clear()
	bucket := treeman.ParseBucket(wt.Bucket)
	card, group := itemCard()
	card.AddClass("treeman-detail-fields")
	head := v.worktreeItem(wt)
	head.AddClass("treeman-detail-head")
	head.AddAction(v.badge(wt.State, treemanVariant(bucket)))
	group.Append(head)
	group.Append(v.field(i18n.T("dropdown-treeman-detail-repo"), repo.Repo, false))
	group.Append(v.field(i18n.T("dropdown-treeman-detail-bucket"), treemanBucketLabel(bucket), false))
	if wt.Slug != "" {
		group.Append(v.field(i18n.T("dropdown-treeman-detail-slug"), wt.Slug, false))
	}
	if wt.Path != "" {
		group.Append(v.field(i18n.T("dropdown-treeman-detail-path"), wt.Path, true))
	}
	v.details.Append(card, false)
	if wt.Path != "" {
		row := widget.NewBox(widget.Row, 0, 0)
		row.AddClass("treeman-detail-actions")
		row.Append(widget.NewSpacer(0, 0), true)
		row.Append(v.actions(wt.Path), false)
		v.details.Append(row, false)
	}
}

// field is one key/value row: the value over its key, as an Item's
// title over its description; the path wraps instead of truncating.
func (v *treemanView) field(key, value string, wrap bool) *widget.Item {
	it := widget.NewItem(v.font, value, key)
	it.SetSmall(true)
	it.SetEllipsize(!wrap)
	it.AddClass("treeman-detail-field")
	return it
}

// follow re-renders on every status change until the dropdown closes.
func (v *treemanView) follow() {
	if v.ctx.Treeman == nil {
		return
	}
	life, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	followTicks(v.ctx, life, "treeman", v.ctx.Treeman.Subscribe, v.read, v.apply)
}

// dropdownClosed implements dropdownCloser; reopening lands on the
// list, as the Rust popover's closed handler does.
func (v *treemanView) dropdownClosed() { v.once.Do(func() { v.cancel() }) }
