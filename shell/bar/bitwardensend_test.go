package bar

import (
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-secrets/vault"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/clipboard"
)

// sendsView is an open vault's dropdown on its Sends page, with a fixed
// clock and the Sends listed.
func sendsView(t *testing.T, v *fakeVault) (*bitwardenView, *[]copied, time.Time) {
	t.Helper()
	view, copies := bitwardenTestView(t, v, config.Defaults())
	at := time.Now()
	onLoop(func() { view.now = func() time.Time { return at } })
	onLoop(view.toggleSends)
	waitIdle(t, view)
	return view, copies, at
}

// The Sends page lists the vault's Sends; creating one sends the form's
// name, text, deletion delay, access limit and password, copies the
// link (which carries the Send's key) as a secret, empties the form and
// lists the new Send.
func TestBitwardenSendCreatesAndCopiesTheLink(t *testing.T) {
	v := unlockedVault()
	v.sends = []vault.Send{{ID: "old", Type: vault.SendText, Name: "Wi-Fi", AccessCount: 2, MaxAccessCount: 5, Deletion: time.Now().Add(time.Hour)}}
	view, copies, at := sendsView(t, v)
	if page(view) != bwPageSends || rowCount(view.sendList) != 1 {
		t.Fatalf("page %s with %d Sends, want the Sends page with one", page(view), rowCount(view.sendList))
	}
	if !onHeadlessLoop(view.sendBtn.Visible) || view.dropdownFocus() != view.sendName {
		t.Errorf("the Sends button shows %v, focus on %T", view.sendBtn.Visible(), view.dropdownFocus())
	}
	onLoop(func() {
		view.sendName.SetText("Door code")
		view.sendText.SetText("4711")
		view.sendLife.SetSelected(0)
		view.sendMax.SetValue(3)
		view.sendPw.entry.SetText("shared")
		view.sendForm.Submit()
	})
	waitIdle(t, view)
	waitHeadless(t, "the new Send listed", func() bool { return !view.busy && rowCount(view.sendList) == 2 })
	if len(v.created) != 1 {
		t.Fatalf("created %+v", v.created)
	}
	want := vault.NewTextSend{Name: "Door code", Text: "4711", Deletion: at.Add(time.Hour), MaxAccessCount: 3, Password: "shared"}
	if got := v.created[0]; got != want {
		t.Errorf("created %+v, want %+v", got, want)
	}
	if len(*copies) != 1 {
		t.Fatalf("%d copies, want the link", len(*copies))
	}
	link := (*copies)[0]
	if copiedText(t, link) != "https://send.example/#/s1/key" || !clipboard.IsSensitive(link.content.Mimes) || link.ttl != 30*time.Second {
		t.Errorf("copied %q, mimes %v, ttl %v, want the link as a secret cleared after 30 s", copiedText(t, link), link.content.Mimes, link.ttl)
	}
	if view.sendName.Text() != "" || view.sendText.Text() != "" || view.sendPw.entry.Text() != "" || view.sendMax.Value() != 0 {
		t.Error("the form kept what was sent")
	}
	if !strings.Contains(view.sendStatus.Text(), i18n.T("dropdown-bitwarden-field-send-link")) {
		t.Errorf("status %q", view.sendStatus.Text())
	}
}

// A form without a name or text creates nothing; a Send the vault
// refuses keeps the form and shows why, and copies nothing.
func TestBitwardenSendRefusals(t *testing.T) {
	v := unlockedVault()
	view, copies, _ := sendsView(t, v)
	onLoop(func() {
		view.sendText.SetText("only text")
		view.sendForm.Submit()
	})
	onLoop(func() {
		view.sendName.SetText("only name")
		view.sendText.SetText("")
		view.sendForm.Submit()
	})
	if len(v.created) != 0 || view.busy {
		t.Fatalf("an incomplete form created %+v", v.created)
	}
	v.set(func(f *fakeVault) { f.sendErr = &vault.ServerError{Status: 400, Message: "Send is too large"} })
	onLoop(func() {
		view.sendText.SetText("text")
		view.sendForm.Submit()
	})
	waitIdle(t, view)
	if len(*copies) != 0 || view.sendName.Text() != "only name" || view.sendText.Text() != "text" {
		t.Errorf("a refused Send copied %d, left the form %q %q", len(*copies), view.sendName.Text(), view.sendText.Text())
	}
	if got := onHeadlessLoop(view.errLabel.Text); got != i18n.T("dropdown-bitwarden-error-server", i18n.Str("message", "Send is too large")) {
		t.Errorf("error %q", got)
	}
}

// Deleting a Send removes it from the vault and the list.
func TestBitwardenSendDelete(t *testing.T) {
	v := unlockedVault()
	v.sends = []vault.Send{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}
	view, _, _ := sendsView(t, v)
	onLoop(func() { view.deleteSend("a") })
	waitHeadless(t, "the Send gone from the list", func() bool { return !view.busy && rowCount(view.sendList) == 1 })
	if len(v.deleted) != 1 || v.deleted[0] != "a" {
		t.Errorf("deleted %v", v.deleted)
	}
	onLoop(func() { view.deleteSend("a") })
	waitIdle(t, view)
	if got := onHeadlessLoop(view.errLabel.Text); got == "" {
		t.Errorf("deleting a missing Send showed %q", got)
	}
}

// The Sends page is the open vault's: locking closes it and empties its
// form, the header hides its button, and hiding the dropdown empties
// the form too.
func TestBitwardenSendFollowsTheLock(t *testing.T) {
	v := unlockedVault()
	view, _, _ := sendsView(t, v)
	onLoop(func() {
		view.sendName.SetText("x")
		view.sendPw.entry.SetText("secret")
	})
	v.set(func(f *fakeVault) { f.status.State = vault.StateLocked })
	onLoop(view.apply)
	if page(view) != bwPageUnlock || view.sending || onHeadlessLoop(view.sendBtn.Visible) {
		t.Errorf("locked: page %s, sending %v, button %v", page(view), view.sending, view.sendBtn.Visible())
	}
	if view.sendName.Text() != "" || view.sendPw.entry.Text() != "" {
		t.Error("locking left the Send form filled")
	}
	v.set(func(f *fakeVault) { f.status.State = vault.StateUnlocked })
	onLoop(view.toggleSends)
	waitIdle(t, view)
	onLoop(func() { view.sendPw.entry.SetText("secret") })
	onLoop(view.dropdownHidden)
	if view.sending || view.sendPw.entry.Text() != "" || page(view) != bwPageVault {
		t.Errorf("hidden: sending %v, password %q, page %s", view.sending, view.sendPw.entry.Text(), page(view))
	}
}
