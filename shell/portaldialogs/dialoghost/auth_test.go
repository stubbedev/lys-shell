package dialoghost

import (
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/shell/treetest"
)

const authHandle = "/r/auth"

var authCaller = portaldialogs.Caller{Handle: authHandle, AppID: "polkit"}

// authStep is a prompt for root, with the user as a second identity.
func authStep(notes ...portaldialogs.AuthNote) portaldialogs.AuthRequest {
	return portaldialogs.AuthRequest{
		Message:    "Authentication is needed to run /usr/bin/true",
		Icon:       "ld-shield-check-symbolic",
		Details:    []portaldialogs.AuthDetail{{Label: "Program", Value: "/usr/bin/true"}},
		Identities: []portaldialogs.AuthIdentity{{UID: 0, Name: "root"}, {UID: 1000, Name: "ada"}},
		Selected:   0,
		Notes:      notes,
		Prompt:     "Password:",
	}
}

// authenticate calls Authenticate on a goroutine; the reply arrives on
// the returned channel.
func (hs *harness) authenticate(r portaldialogs.AuthRequest) chan portaldialogs.AuthReply {
	ch := make(chan portaldialogs.AuthReply, 1)
	go func() { ch <- hs.h.Authenticate(authCaller, r) }()
	return ch
}

func reply(t *testing.T, ch chan portaldialogs.AuthReply) portaldialogs.AuthReply {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(treetest.Liveness):
		t.Fatal("Authenticate never replied")
		return nil
	}
}

func pendingReply(t *testing.T, ch chan portaldialogs.AuthReply) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("replied %T before the user acted", r)
	case <-time.After(50 * time.Millisecond):
	}
}

func signal(t *testing.T, hs *harness) signalled {
	t.Helper()
	select {
	case s := <-hs.signals:
		return s
	case <-time.After(treetest.Liveness):
		t.Fatal("no signal")
		return signalled{}
	}
}

func authEntry(t *testing.T, c app.LayerConfig) *widget.Entry {
	t.Helper()
	entries := treetest.All[*widget.Entry](c.Root)
	if len(entries) != 1 {
		t.Fatalf("%d entries, want the prompt's", len(entries))
	}
	return entries[0]
}

func (hs *harness) windowsOpen() (opened, closed int) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	for _, w := range hs.windows {
		if w.closed {
			closed++
		}
	}
	return len(hs.windows), closed
}

// An answer goes to the waiting call and clears the entry, which takes
// nothing more until the next step; the next step updates the same
// window, with its notes, instead of closing and reopening it.
func TestAuthenticateAnswersAndKeepsTheWindowAcrossSteps(t *testing.T) {
	hs := newHarness(t, nil)
	first := hs.authenticate(authStep())
	c := hs.open(t)
	if c.Keyboard != app.KeyboardExclusive {
		t.Errorf("keyboard = %v, want exclusive", c.Keyboard)
	}
	entry := authEntry(t, c)
	var focused widget.Widget
	hs.do(func() {
		hs.mu.Lock()
		focused = hs.windows[0].focused
		hs.mu.Unlock()
	})
	if focused != entry {
		t.Error("the prompt's entry is not focused")
	}
	if l := treetest.Labels(c.Root); !slices.Contains(l, "Authentication is needed to run /usr/bin/true") || !slices.Contains(l, "Program: /usr/bin/true") {
		t.Errorf("labels = %q", l)
	}
	if entry.Echo() != widget.EchoPassword || !entry.Visible() {
		t.Error("the prompt is not a visible masked entry")
	}
	hs.do(func() {
		entry.SetText("secret")
		entry.OnActivate(entry.Text())
	})
	got := reply(t, first)
	if a, ok := got.(portaldialogs.AuthAnswer); !ok || a.Response.Reveal() != "secret" {
		t.Fatalf("reply = %#v, want the answer", got)
	}
	var text string
	var readOnly bool
	hs.do(func() { text, readOnly = entry.Text(), entry.ReadOnly() })
	if text != "" || !readOnly {
		t.Errorf("after the answer the entry holds %q, read-only %v; want it cleared and held", text, readOnly)
	}
	hs.do(func() {
		entry.SetText("again")
		entry.OnActivate(entry.Text())
	})

	second := hs.authenticate(authStep(portaldialogs.AuthNote{Error: true, Text: "Sorry, try again."}))
	pendingReply(t, second)
	select {
	case <-hs.opened:
		t.Fatal("the next step opened another window")
	case <-time.After(50 * time.Millisecond):
	}
	if opened, closed := hs.windowsOpen(); opened != 1 || closed != 0 {
		t.Fatalf("windows opened %d, closed %d; want the one kept", opened, closed)
	}
	var labels []string
	hs.do(func() { labels = treetest.Labels(c.Root) })
	if !slices.Contains(labels, "Sorry, try again.") {
		t.Errorf("the step's note is missing: %q", labels)
	}
	hs.do(func() {
		entry.SetText("right")
		treetest.Button(t, c.Root, "Authenticate").OnClick()
	})
	if a, ok := reply(t, second).(portaldialogs.AuthAnswer); !ok || a.Response.Reveal() != "right" {
		t.Errorf("second reply = %#v", a)
	}
}

// ShowAuthentication shows the notes with no entry, and what the user
// does then goes out as signals: a cancel, and a switch of identity.
func TestShowAuthenticationShowsNotesAndSignalsActions(t *testing.T) {
	hs := newHarness(t, nil)
	hs.h.ShowAuthentication(authCaller, authStep(portaldialogs.AuthNote{Text: "Place your finger on the reader"}))
	c := hs.open(t)
	if l := treetest.Labels(c.Root); !slices.Contains(l, "Place your finger on the reader") {
		t.Errorf("labels = %q", l)
	}
	if entry := authEntry(t, c); entry.Visible() {
		t.Error("a step without a prompt shows the entry")
	}
	hs.do(func() { c.OnKey(nil, escapeKeycode, 0) })
	if s := signal(t, hs); s.handle != authHandle || s.action != (portaldialogs.AuthCancel{}) {
		t.Errorf("Escape signalled %+v", s)
	}
	drop := treetest.First[*widget.Dropdown](t, c.Root)
	hs.do(func() { drop.SetSelected(1) })
	if s := signal(t, hs); s.handle != authHandle || s.action != (portaldialogs.AuthSwitch{UID: 1000}) {
		t.Errorf("picking ada signalled %+v", s)
	}
	if opened, closed := hs.windowsOpen(); opened != 1 || closed != 0 {
		t.Errorf("windows opened %d, closed %d; the dialog stays until Dismiss", opened, closed)
	}
}

// Picking another identity answers the waiting call with its uid;
// picking the selected one does nothing.
func TestAuthenticateSwitchesIdentity(t *testing.T) {
	hs := newHarness(t, nil)
	r := authStep()
	r.Selected = 1000
	ch := hs.authenticate(r)
	c := hs.open(t)
	drop := treetest.First[*widget.Dropdown](t, c.Root)
	if drop.Selected() != 1 {
		t.Errorf("the dropdown shows identity %d, want the selected ada", drop.Selected())
	}
	hs.do(func() { drop.SetSelected(1) })
	pendingReply(t, ch)
	hs.do(func() { drop.SetSelected(0) })
	if got := reply(t, ch); got != (portaldialogs.AuthSwitch{UID: 0}) {
		t.Errorf("reply = %#v, want the switch to root", got)
	}
	select {
	case s := <-hs.signals:
		t.Errorf("a switch answering a waiting call also signalled %+v", s)
	default:
	}
}

// Cancel answers the waiting call false; Dismiss unmaps the window, and
// a call still waiting then is cancelled.
func TestAuthenticateCancelAndDismiss(t *testing.T) {
	hs := newHarness(t, nil)
	ch := hs.authenticate(authStep())
	c := hs.open(t)
	hs.do(func() { treetest.Button(t, c.Root, "Cancel").OnClick() })
	if got := reply(t, ch); got != (portaldialogs.AuthCancel{}) {
		t.Errorf("Cancel replied %#v", got)
	}
	if opened, closed := hs.windowsOpen(); opened != 1 || closed != 0 {
		t.Errorf("windows opened %d, closed %d; a cancel waits for Dismiss", opened, closed)
	}

	waiting := hs.authenticate(authStep())
	pendingReply(t, waiting)
	hs.h.Dismiss(authHandle)
	if got := reply(t, waiting); got != (portaldialogs.AuthCancel{}) {
		t.Errorf("Dismiss replied %#v to the waiting call", got)
	}
	if _, closed := hs.windowsOpen(); closed != 1 {
		t.Error("Dismiss left the window mapped")
	}

	again := hs.authenticate(authStep())
	hs.open(t)
	hs.h.Dismiss(authHandle)
	if got := reply(t, again); got != (portaldialogs.AuthCancel{}) {
		t.Errorf("a new conversation after Dismiss replied %#v", got)
	}
}

// An authentication arriving while another dialog shows waits its
// turn; dismissed while waiting, it is cancelled and never shows.
func TestAuthenticateWaitsItsTurn(t *testing.T) {
	hs := newHarness(t, nil)
	other := make(chan bool, 1)
	go func() { other <- hs.h.ConfirmWallpaper(portaldialogs.Caller{Handle: "/r/w"}, "file:///a.png") }()
	hs.open(t)
	ch := hs.authenticate(authStep())
	pendingReply(t, ch)
	hs.h.Dismiss(authHandle)
	if got := reply(t, ch); got != (portaldialogs.AuthCancel{}) {
		t.Errorf("a dismissed waiting authentication replied %#v", got)
	}
	hs.h.Dismiss("/r/w")
	<-other
	select {
	case <-hs.opened:
		t.Error("the dismissed authentication showed")
	case <-time.After(50 * time.Millisecond):
	}
}
