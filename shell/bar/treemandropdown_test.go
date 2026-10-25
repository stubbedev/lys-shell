package bar

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/treeman"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

func treemanFixture() *treeman.Status {
	return &treeman.Status{Total: 3, Stable: 2, Failed: 1, Repos: []treeman.Repo{
		{Repo: "lys-shell", Total: 2, Worktrees: []treeman.Worktree{
			{Branch: "master", Slug: "lys-shell-master", State: "ready", Bucket: "stable", IsMain: true, Path: "/w/lys-shell"},
			{Branch: "feat", Slug: "lys-shell-feat", State: "error", Bucket: "failed", Path: "/w/feat"},
		}},
		{Repo: "lys-ui", Total: 1, Worktrees: []treeman.Worktree{
			{Branch: "main", State: "ready", Bucket: "stable", IsMain: true, Path: "/w/lys-ui"},
		}},
	}}
}

func TestTreemanBucketHelpers(t *testing.T) {
	for b, want := range map[treeman.Bucket]string{
		treeman.BucketStable: "success", treeman.BucketUp: "info", treeman.BucketDown: "warning", treeman.BucketFailed: "error",
	} {
		if got := treemanVariant(b); got != want {
			t.Errorf("variant(%v) = %q", b, got)
		}
	}
	if treemanBucketLabel(treeman.BucketDown) != i18n.T("dropdown-treeman-bucket-down") {
		t.Error("bucket label")
	}
}

func TestTreemanDropdownEmpty(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	v := treemanDropdown(ctx).(*treemanView)
	if len(v.list.Children()) != 1 || v.back.Visible() {
		t.Errorf("no service: %d children, back shown %v", len(v.list.Children()), v.back.Visible())
	}
	ctx.Treeman = &fakeTreeman{status: &treeman.Status{}}
	v = treemanDropdown(ctx).(*treemanView)
	defer v.dropdownClosed()
	if _, ok := v.list.Children()[0].(*widget.StatusPage); !ok || len(v.list.Children()) != 1 {
		t.Error("no repos: want only the empty state")
	}
	// views.rs empty_state wraps its description; the title stays one
	// line.
	for _, l := range treetest.All[*widget.Label](v.list) {
		switch {
		case l.HasClass("description") && !l.Wrap():
			t.Error("the empty description does not wrap")
		case l.HasClass("title") && l.Wrap():
			t.Error("the empty title wraps")
		}
	}
}

func TestTreemanDropdownListAndCollapse(t *testing.T) {
	src := &fakeTreeman{status: treemanFixture(), ticks: make(chan struct{}, 1)}
	ctx := newTestContext(t, config.Defaults())
	ctx.Treeman = src
	v := treemanDropdown(ctx).(*treemanView)
	defer v.dropdownClosed()

	if chips := treetest.WithClass(v.list, "treeman-stat"); len(chips) != 2 {
		t.Errorf("summary chips = %d, want ready and failed only", len(chips))
	}
	cards := treetest.WithClass(v.list, "treeman-repo")
	if len(cards) != 2 {
		t.Fatalf("repo cards = %d, want two", len(cards))
	}
	card := cards[0]
	header := treetest.WithClass(card, "treeman-repo-header")[0].(*widget.Item)
	fold := treetest.First[*widget.Revealer](t, card)
	if n := len(treetest.WithClass(fold, "treeman-wt")); n != 2 || !fold.Revealed() {
		t.Fatalf("lys-shell rows = %d revealed %v", n, fold.Revealed())
	}
	// A fresh card shows its open fold at once: a status refresh
	// rebuilds every card, so an animated fold would slide in on each.
	if p := fold.Progress(); p != 1 {
		t.Errorf("a fresh card's fold is at %v, want fully open", p)
	}
	header.ClickAt(widget.Point{})
	if fold.Revealed() || !v.collapsed["lys-shell"] {
		t.Error("the header did not fold the card")
	}
	// The fold survives a status refresh.
	src.ticks <- struct{}{}
	waitHeadless(t, "the refresh", func() bool { return treetest.WithClass(v.list, "treeman-repo")[0] != card })
	if treetest.First[*widget.Revealer](t, treetest.WithClass(v.list, "treeman-repo")[0]).Revealed() {
		t.Error("a refresh reopened the folded card")
	}

	// A row is the worktree's Item, its path as the tooltip and its
	// trailing slot showing the state badge at rest.
	row := treetest.WithClass(treetest.WithClass(v.list, "treeman-repo")[1], "treeman-wt")[0].(*widget.Item)
	if row.TooltipText() != "/w/lys-ui" {
		t.Errorf("tooltip = %q", row.TooltipText())
	}
	slot := treetest.First[*widget.Stack](t, row)
	if slot.Visible() != "state" {
		t.Errorf("the trailing slot shows %q at rest, want the state", slot.Visible())
	}
}

// Hovering a worktree row swaps its state badge for the action cluster;
// leaving it brings the badge back.
func TestTreemanRowHoverSwapsTheActions(t *testing.T) {
	restore := widget.SetAnimationsInstant(true)
	t.Cleanup(restore)
	ctx := styledContext(t, config.Defaults())
	ctx.Treeman = &fakeTreeman{status: treemanFixture()}
	v := treemanDropdown(ctx).(*treemanView)
	defer v.dropdownClosed()
	ctx.Theme.Attach(v)
	sz := v.Measure(widget.Constraints{Max: widget.Size{W: 440, H: 2000}})
	v.Arrange(render.Rect{W: 440, H: sz.H})

	row := treetest.WithClass(v.list, "treeman-wt")[0].(*widget.Item)
	slot := treetest.First[*widget.Stack](t, row)
	r := &widget.Router{Root: v}
	b := row.Bounds()
	r.Move(widget.Point{X: b.X + b.W/2, Y: b.Y + b.H/2})
	if slot.Visible() != "actions" {
		t.Errorf("hovered, the slot shows %q, want the actions", slot.Visible())
	}
	r.Leave()
	if slot.Visible() != "state" {
		t.Errorf("after leaving, the slot shows %q, want the state", slot.Visible())
	}
}

func TestTreemanDropdownDetailAndActions(t *testing.T) {
	src := &fakeTreeman{status: treemanFixture(), ticks: make(chan struct{}, 1)}
	ctx := newTestContext(t, config.Defaults())
	ctx.Treeman = src
	alerts := &fakeAlert{}
	ctx.Alert = alerts.ask
	var toastMu sync.Mutex
	var toasts []string
	ctx.Toast = func(label, _ string) {
		toastMu.Lock()
		toasts = append(toasts, label)
		toastMu.Unlock()
	}
	v := treemanDropdown(ctx).(*treemanView)
	defer v.dropdownClosed()

	v.openDetail("/w/feat")
	if v.pages.Visible() != "detail" || itemText(v.title, "title") != "feat" || !v.back.Visible() {
		t.Fatalf("detail: page %q title %q back %v", v.pages.Visible(), itemText(v.title, "title"), v.back.Visible())
	}
	fields := treetest.WithClass(v.details, "treeman-detail-field")
	if len(fields) != 4 {
		t.Errorf("fields = %d, want repo, bucket, slug, path", len(fields))
	}

	// Prepare and teardown run at once; reset asks first, in a modal
	// alert naming the worktree.
	v.run(treeman.ActionPrepare, "/w/feat")
	v.confirmReset("/w/feat")
	if len(alerts.specs) != 1 || alerts.specs[0].detail != "/w/feat" ||
		alerts.specs[0].accept != i18n.T("dropdown-treeman-confirm-reset-accept") {
		t.Fatalf("alerts = %+v, want the reset question", alerts.specs)
	}
	alerts.answers[0](false) // cancel
	if v.pages.Visible() != "detail" {
		t.Errorf("cancel left the page at %q", v.pages.Visible())
	}
	v.confirmReset("/w/feat")
	alerts.answers[1](true) // accept
	waitHeadless(t, "the actions", func() bool { return len(src.ran()) == 2 })
	// Each action runs on its own goroutine: the order is not theirs.
	if got := slices.Sorted(slices.Values(src.ran())); !slices.Equal(got, []string{"db reset /w/feat", "prepare --worktree /w/feat"}) {
		t.Errorf("ran = %q", got)
	}

	// A failed action toasts its reason.
	src.mu.Lock()
	src.actionErr = errors.New("busy")
	src.mu.Unlock()
	v.run(treeman.ActionTeardown, "/w/feat")
	waitHeadless(t, "the failure toast", func() bool {
		toastMu.Lock()
		defer toastMu.Unlock()
		return len(toasts) == 1
	})
	if !strings.HasSuffix(toasts[0], ": busy") {
		t.Errorf("toast = %q", toasts[0])
	}

	// The worktree going away drops back to the list.
	next := treemanFixture()
	next.Repos[0].Worktrees = next.Repos[0].Worktrees[:1]
	src.setStatus(next)
	src.ticks <- struct{}{}
	waitHeadless(t, "back to the list", func() bool { return v.pages.Visible() == "list" })
	if itemText(v.title, "title") != i18n.T("dropdown-treeman-title") || v.back.Visible() {
		t.Error("the header kept the detail state")
	}
}
