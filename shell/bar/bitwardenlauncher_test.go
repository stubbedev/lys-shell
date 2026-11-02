package bar

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-secrets/vault"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/clipboard"
	engine "github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/search"
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
	v := &fakeVault{status: vault.Status{State: vault.StateLocked}, entries: bitwardenPreviewItems(), unlockErr: vault.ErrWrongPassword}
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
	if !st.Password || st.Message == nil || *st.Message != bwErrorText(vault.ErrWrongPassword) {
		t.Errorf("after a wrong password %+v, want the prompt saying so", st)
	}
	v.set(func(f *fakeVault) { f.unlockErr = nil })
	st = reloaded(t, m.Activate(ctx, engine.NoRow, custom("hunter2"), "hunter2"))
	if st.Password || !st.NoCustom || m.AllowsCustom() || len(st.Items) != len(v.entries) {
		t.Errorf("after unlocking %+v, want the %d items, unmasked", st, len(v.entries))
	}
	if !slices.Equal(v.unlocks, []string{"wrong", "hunter2"}) || len(*copies) != 0 {
		t.Errorf("unlocks %q, copies %d", v.unlocks, len(*copies))
	}
}

// Enter copies an item's primary field through the host, a secret
// marked and cleared as the dropdown's, and closes; Shift+Enter lists
// the item's fields, a pick copying that one as it is.
func TestVaultModeCopiesThroughTheHost(t *testing.T) {
	v := &fakeVault{status: vault.Status{State: vault.StateUnlocked}, entries: bitwardenPreviewItems()}
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
	if v.readCount() != 2 {
		t.Errorf("reads %q, want one per copy", v.reads)
	}
}

// A protected item's secret waits for the master password: a wrong one
// copies nothing, the right one copies and closes. Its username, not a
// secret, copies at once.
func TestVaultModeReprompts(t *testing.T) {
	bank := entry(vault.Item{ID: "1", Type: vault.ItemLogin, Name: "Bank", Username: "ada", Reprompt: true}, map[vault.Field]string{vault.FieldUsername: "ada", vault.FieldPassword: "pw2"})
	v := &fakeVault{status: vault.Status{State: vault.StateUnlocked}, entries: []fakeEntry{bank}, master: "hunter2"}
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
	v := &fakeVault{entries: bitwardenPreviewItems()}
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

// searchSink is the panel's seat in a search session.
type searchSink struct {
	mu       sync.Mutex
	frames   []search.Frame
	finished []search.Outcome
	notices  []string
}

func (s *searchSink) Results(f search.Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames = append(s.frames, f)
}

func (s *searchSink) Finish(o search.Outcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished = append(s.finished, o)
}

func (s *searchSink) Notice(m string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notices = append(s.notices, m)
}

func (s *searchSink) last() search.Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.frames) == 0 {
		return search.Frame{}
	}
	return s.frames[len(s.frames)-1]
}

func (s *searchSink) ended() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.finished) > 0
}

func searchUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func startVaultSearch(t *testing.T, v *fakeVault) (*search.Engine, *searchSink, *[]copied) {
	t.Helper()
	m, copies := vaultModeOver(v)
	pickers := search.NewPickers(nil, nil, func() *search.Level { return search.ModeLevel(m, "Vault") }, false)
	s := &searchSink{}
	e := search.Start(&search.Level{Providers: []search.Provider{pickers}}, nil, s, func(fn func()) { fn() })
	t.Cleanup(e.Stop)
	e.SetQuery("bitwarden", 1)
	searchUntil(t, "the picker row", func() bool { return len(s.last().Rows) > 0 && s.last().Query == "bitwarden" })
	return e, s, copies
}

func firstEntry(f search.Frame) string {
	for _, r := range f.Rows {
		if r.Header == "" {
			return r.Entry.ID
		}
	}
	return ""
}

// The picker on the real vault mode: a locked vault masks the field and
// takes the master password there, a wrong one stays asking, the items
// then list, and Enter copies through the host, marked as a secret.
func TestSearchPickerUnlocksTheRealVaultAndCopies(t *testing.T) {
	v := &fakeVault{status: vault.Status{State: vault.StateLocked}, entries: bitwardenPreviewItems(), unlockErr: errors.New("wrong")}
	e, s, copies := startVaultSearch(t, v)
	e.Activate(firstEntry(s.last()), 0)
	searchUntil(t, "the masked prompt", func() bool { return s.last().Mask })
	f := s.last()
	if !f.CanSubmit || f.Title != "Vault" || f.Message == "" || f.Depth != 1 {
		t.Fatalf("prompt %+v", f)
	}
	if len(f.Rows) != 0 {
		t.Errorf("a locked vault lists nothing: %+v", f.Rows)
	}
	e.Submit("nope")
	searchUntil(t, "the refusal", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.frames) > 3
	})
	if f := s.last(); !f.Mask {
		t.Errorf("a wrong password stays asking: %+v", f)
	}
	v.set(func(f *fakeVault) { f.unlockErr = nil })
	e.Submit("right")
	searchUntil(t, "the items", func() bool { return !s.last().Mask && len(s.last().Rows) > 0 })
	f = s.last()
	if f.CanSubmit {
		t.Error("an open vault takes no typed text")
	}
	e.Activate(firstEntry(f), 0)
	searchUntil(t, "the copy", s.ended)
	if _, ok := s.finished[0].(search.Close); !ok {
		t.Errorf("finished %+v", s.finished)
	}
	if len(*copies) != 1 || !clipboard.IsSensitive((*copies)[0].content.Mimes) {
		t.Errorf("copies %+v: the mode copies, marked sensitive, through the host", *copies)
	}
}

func TestSearchPickerShowsAnOpenVaultsFieldsOnItsSecondAction(t *testing.T) {
	v := &fakeVault{status: vault.Status{State: vault.StateUnlocked}, entries: bitwardenPreviewItems()}
	e, s, _ := startVaultSearch(t, v)
	e.Activate(firstEntry(s.last()), 0)
	searchUntil(t, "the items", func() bool { return s.last().Depth == 1 && len(s.last().Rows) > 0 })
	e.Activate(firstEntry(s.last()), 1)
	searchUntil(t, "the fields", func() bool { return s.last().Message == "Example Mail" })
	if got := len(s.last().Rows); got == 0 {
		t.Errorf("fields %d", got)
	}
	e.Back()
	searchUntil(t, "the root", func() bool { return s.last().Depth == 0 })
}

func TestSearchPickerOverALoggedOutVaultSaysSo(t *testing.T) {
	e, s, copies := startVaultSearch(t, &fakeVault{entries: bitwardenPreviewItems()})
	e.Activate(firstEntry(s.last()), 0)
	searchUntil(t, "the message", func() bool { return s.last().Depth == 1 })
	f := s.last()
	if f.Message == "" || len(f.Rows) != 0 || f.CanSubmit || f.Mask {
		t.Errorf("frame %+v", f)
	}
	if len(*copies) != 0 {
		t.Errorf("copies %+v", *copies)
	}
}
