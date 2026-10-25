package bar

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/bitwarden"
	"github.com/stubbedev/lys-shell/service/clipboard"
	engine "github.com/stubbedev/lys-shell/service/launcher"
)

// vaultModeOver is the vault mode over v, copies landing in the
// returned log, the loop's invoke inline.
func vaultModeOver(v *fakeVault) (*vaultMode, *[]copied) {
	copies := &[]copied{}
	cfg := config.Defaults()
	host := testBitwardenHost(v, cfg, copies)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	return newVaultMode(host, func(fn func()) { fn() }, func() time.Time { return now }), copies
}

func reloaded(t *testing.T, a engine.Action) engine.ModeState {
	t.Helper()
	r, ok := a.(engine.ActionReload)
	if !ok {
		t.Fatalf("action %#v, want a reload", a)
	}
	return r.State
}

func custom(text string) engine.ActivateKind { return engine.ActivateCustom{Text: text} }

// A locked vault asks for the master password in the masked input,
// taking typed text: a wrong one stays asking and says so, the right
// one lists the items, which take no typed text.
func TestVaultModeUnlocksInTheMaskedInput(t *testing.T) {
	v := &fakeVault{status: bitwarden.Status{State: bitwarden.StateLocked}, items: bitwardenPreviewItems(), unlockErr: bitwarden.ErrWrongPassword}
	m, copies := vaultModeOver(v)
	ctx := context.Background()
	st := m.Load(ctx)
	if !st.Password || st.NoCustom || !m.AllowsCustom() || len(st.Items) != 0 {
		t.Fatalf("locked state %+v, want a masked prompt taking typed text", st)
	}
	if _, nothing := m.Activate(ctx, engine.NoRow, custom(""), "").(engine.ActionNothing); !nothing {
		t.Error("an empty password was tried")
	}
	st = reloaded(t, m.Activate(ctx, engine.NoRow, custom("wrong"), "wrong"))
	if !st.Password || st.Message == nil || *st.Message != bwErrorText(bitwarden.ErrWrongPassword) {
		t.Errorf("after a wrong password %+v, want the prompt saying so", st)
	}
	v.set(func(f *fakeVault) { f.unlockErr = nil })
	st = reloaded(t, m.Activate(ctx, engine.NoRow, custom("hunter2"), "hunter2"))
	if st.Password || !st.NoCustom || m.AllowsCustom() || len(st.Items) != len(v.items) {
		t.Errorf("after unlocking %+v, want the %d items, unmasked", st, len(v.items))
	}
	if !slices.Equal(v.unlocks, []string{"wrong", "hunter2"}) || len(*copies) != 0 {
		t.Errorf("unlocks %q, copies %d", v.unlocks, len(*copies))
	}
}

// Enter copies an item's primary field through the host, a secret
// marked and cleared as the dropdown's, and closes; Shift+Enter lists
// the item's fields, a pick copying that one as it is.
func TestVaultModeCopiesThroughTheHost(t *testing.T) {
	v := &fakeVault{status: bitwarden.Status{State: bitwarden.StateUnlocked}, items: bitwardenPreviewItems()}
	m, copies := vaultModeOver(v)
	ctx := context.Background()
	m.Load(ctx)
	if _, closed := m.Activate(ctx, engine.RowTarget(0), engine.ActivateDefault{}, "").(engine.ActionClose); !closed {
		t.Fatal("Enter on an item did not close")
	}
	if len(*copies) != 1 {
		t.Fatalf("copies %d, want the password", len(*copies))
	}
	pw := (*copies)[0]
	if b, _ := pw.content.Bytes("text/plain"); string(b) != "pw" || !clipboard.IsSensitive(pw.content.Mimes) || pw.ttl != 30*time.Second {
		t.Errorf("password copy %q, mimes %v, ttl %v", b, pw.content.Mimes, pw.ttl)
	}
	st := reloaded(t, m.Activate(ctx, engine.RowTarget(0), engine.ActivateAlt{}, ""))
	if len(st.Items) != 2 || st.Message == nil || *st.Message != "Example Mail" {
		t.Fatalf("fields %+v, want the login's password and username", st)
	}
	if _, closed := m.Activate(ctx, engine.RowTarget(1), engine.ActivateDefault{}, "").(engine.ActionClose); !closed {
		t.Fatal("picking a field did not close")
	}
	user := (*copies)[1]
	if b, _ := user.content.Bytes("text/plain"); string(b) != "ada@example.com" || clipboard.IsSensitive(user.content.Mimes) || user.ttl != 0 {
		t.Errorf("username copy %q, mimes %v, ttl %v", b, user.content.Mimes, user.ttl)
	}
	if v.touches != 2 {
		t.Errorf("touches %d, want one per copy", v.touches)
	}
}

// A protected item's secret waits for the master password: a wrong one
// copies nothing, the right one copies and closes. Its username, not a
// secret, copies at once.
func TestVaultModeReprompts(t *testing.T) {
	items := []bitwarden.Item{{ID: "1", Type: bitwarden.ItemLogin, Name: "Bank", Username: "ada", Password: "pw2", Reprompt: true}}
	v := &fakeVault{status: bitwarden.Status{State: bitwarden.StateUnlocked}, items: items, verifyOK: "hunter2"}
	m, copies := vaultModeOver(v)
	ctx := context.Background()
	m.Load(ctx)
	st := reloaded(t, m.Activate(ctx, engine.RowTarget(0), engine.ActivateDefault{}, ""))
	if !st.Password || len(*copies) != 0 {
		t.Fatalf("protected copy %+v with %d copies, want a masked reprompt first", st, len(*copies))
	}
	st = reloaded(t, m.Activate(ctx, engine.NoRow, custom("wrong"), "wrong"))
	if !st.Password || len(*copies) != 0 {
		t.Errorf("a wrong reprompt %+v copied %d", st, len(*copies))
	}
	if _, closed := m.Activate(ctx, engine.NoRow, custom("hunter2"), "hunter2").(engine.ActionClose); !closed {
		t.Fatal("the right reprompt did not close")
	}
	if b, _ := (*copies)[0].content.Bytes("text/plain"); string(b) != "pw2" {
		t.Errorf("copied %q, want the password", b)
	}
	m.Load(ctx)
	reloaded(t, m.Activate(ctx, engine.RowTarget(0), engine.ActivateAlt{}, ""))
	if _, closed := m.Activate(ctx, engine.RowTarget(1), engine.ActivateDefault{}, "").(engine.ActionClose); !closed || len(*copies) != 2 {
		t.Error("the username of a protected item waited for the reprompt")
	}
}

// Logged out, the mode says to log in from the bar and does nothing.
func TestVaultModeLoggedOut(t *testing.T) {
	v := &fakeVault{items: bitwardenPreviewItems()}
	m, copies := vaultModeOver(v)
	ctx := context.Background()
	st := m.Load(ctx)
	if st.Password || !st.NoCustom || len(st.Items) != 0 || st.Message == nil {
		t.Fatalf("logged out %+v, want a message and nothing to pick", st)
	}
	if _, nothing := m.Activate(ctx, engine.NoRow, custom("x"), "x").(engine.ActionNothing); !nothing || len(*copies) != 0 || len(v.unlocks) != 0 {
		t.Error("a logged-out vault took input")
	}
}
