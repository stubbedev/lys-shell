package popups

import (
	"slices"
	"testing"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/notifications"
)

// layoutPasses lays the surface out as a frame does after a change: one
// more arrange for as long as a measure is stale, counted.
func (s *surface) layoutPasses() int {
	passes := 0
	for passes < 4 && widget.LayoutPending(s.host) {
		s.layout()
		passes++
	}
	return passes
}

func progress(percent int32, tag string) map[string]dbus.Variant {
	return map[string]dbus.Variant{"value": dbus.MakeVariant(percent), "x-dunst-stack-tag": dbus.MakeVariant(tag)}
}

func TestAProgressReplacementUpdatesOneBarWithoutALayout(t *testing.T) {
	s := newSurface(t, config.DefaultsNotification())
	id := s.svc.NotifyHints("files", 0, "", "Copying", "photos", nil, progress(40, "copy"), -1)
	s.sync()
	c := s.p.cards[id]
	if !c.progress.Visible() || c.progress.Value() != 0.4 {
		t.Fatalf("bar visible %v at %v, want 0.4", c.progress.Visible(), c.progress.Value())
	}
	if n := s.layoutPasses(); n == 4 {
		t.Fatal("the surface never settled")
	}
	again := s.svc.NotifyHints("files", 0, "", "Copying", "photos", nil, progress(80, "copy"), -1)
	s.p.sync()
	if again != id || len(s.p.cards) != 1 || s.p.cards[id] != c {
		t.Fatalf("the replacement made another card: ids %d %d, %d cards", id, again, len(s.p.cards))
	}
	if c.progress.Value() != 0.8 {
		t.Errorf("bar at %v, want 0.8", c.progress.Value())
	}
	if n := s.layoutPasses(); n != 0 {
		t.Errorf("a progress-only replacement laid the stack out again: %d passes", n)
	}
	s.svc.NotifyHints("files", 0, "", "Copied", "photos", nil, progress(100, "copy"), -1)
	s.p.sync()
	if n := s.layoutPasses(); n == 0 || c.summary.Text() != "Copied" {
		t.Errorf("a new summary: %q after %d layout passes; want the text laid out", c.summary.Text(), n)
	}
}

func TestACardWithoutTheValueHintHasNoBar(t *testing.T) {
	s := newSurface(t, config.DefaultsNotification())
	id := s.notify("mail", "New mail")
	s.sync()
	if s.p.cards[id].progress.Visible() {
		t.Error("a bar without a value hint")
	}
}

func replyCard(t *testing.T) (*surface, *card, *signals) {
	t.Helper()
	s := newSurface(t, config.DefaultsNotification())
	sig := &signals{}
	s.svc.SetEmitter(sig.emit)
	id := s.svc.NotifyHints("chat", 0, "", "Ada", "lunch?", []string{notifications.InlineReplyActionID, "Reply"},
		map[string]dbus.Variant{"x-kde-reply-placeholder-text": dbus.MakeVariant("Reply to Ada")}, -1)
	s.sync()
	c := s.p.cards[id]
	if c.reply == nil {
		t.Fatal("no reply field for an inline-reply action")
	}
	if len(c.actions.Children()) != 0 {
		t.Error("the inline-reply action also drew as a button")
	}
	return s, c, sig
}

func TestEnterSendsTheReplyAndEscapeSendsNothing(t *testing.T) {
	s, c, sig := replyCard(t)
	c.reply.Insert("on my way")
	c.reply.KeyAction(widget.KeyDismiss, 0)
	if c.reply.Text() != "" || sig.count("NotificationReplied") != 0 {
		t.Fatalf("Escape: field %q, %d replies", c.reply.Text(), sig.count("NotificationReplied"))
	}
	c.reply.KeyAction(widget.KeyEnter, 0)
	if sig.count("NotificationReplied") != 0 {
		t.Fatal("Enter on an empty field replied")
	}
	c.reply.Insert("on my way")
	c.reply.KeyAction(widget.KeyEnter, 0)
	if sig.count("NotificationReplied") != 1 || sig.count("NotificationClosed") != 1 {
		t.Fatalf("Enter: signals %v", sig.seen)
	}
	if want := "NotificationReplied" + "1on my way"; sig.seen[0] != want {
		t.Errorf("signal = %q, want %q", sig.seen[0], want)
	}
	s.sync()
	if len(s.svc.Popups()) != 0 {
		t.Error("the card stayed after the reply")
	}
}

func TestOnlyWebAndMailLinksOpen(t *testing.T) {
	s := newSurface(t, config.DefaultsNotification())
	var opened []string
	s.p.launch = func(uri, _ string) error { opened = append(opened, uri); return nil }
	id := s.svc.Notify("chat", 0, "", "Link", `see <a href="https://example.com/a">this</a>`, nil, -1)
	s.sync()
	c := s.p.cards[id]
	for _, href := range []string{"https://example.com/a", "file:///etc/passwd", "javascript:alert(1)", "mailto:ada@example.com"} {
		c.body.OnLinkClick(href)
	}
	if len(opened) != 2 || opened[0] != "https://example.com/a" || opened[1] != "mailto:ada@example.com" {
		t.Errorf("opened %v, want the web and mail links only", opened)
	}
}

func TestCallButtonsAreStyledByTheirPurpose(t *testing.T) {
	s := newSurface(t, config.DefaultsNotification())
	purposes := map[string]string{"accept": "call.accept", "decline": "call.decline"}
	id := s.svc.NotifyHints("chat", 0, "", "Ada calling", "", []string{"accept", "Accept", "decline", "Decline", "later", "Later"},
		map[string]dbus.Variant{notifications.ActionPurposesHint: dbus.MakeVariant(purposes)}, 0)
	s.sync()
	c := s.p.cards[id]
	var classes [][]string
	for _, row := range c.actions.Children() {
		for _, b := range row.(*widget.Box).Children() {
			classes = append(classes, b.(*widget.Button).Classes())
		}
	}
	if len(classes) != 3 {
		t.Fatalf("%d buttons", len(classes))
	}
	if !slices.Contains(classes[0], css.SuggestedAction) || !slices.Contains(classes[1], css.DestructiveAction) {
		t.Errorf("accept %v, decline %v", classes[0], classes[1])
	}
	if slices.Contains(classes[2], css.SuggestedAction) || slices.Contains(classes[2], css.DestructiveAction) {
		t.Errorf("a button without a purpose styled %v", classes[2])
	}
}
