package keyring

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// testKDF keeps Argon2 cheap; the format records it, so files under it
// are as valid as under DefaultKDF.
var testKDF = KDF{Time: 1, MemKiB: 64, Threads: 1}

var t0 = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

func newTestCollection(t *testing.T, password string) *Collection {
	t.Helper()
	c, err := New("Login", []byte(password), testKDF, t0)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustCreate(t *testing.T, c *Collection, label string, attrs map[string]string, secret string) uint32 {
	t.Helper()
	id, _, err := c.CreateItem(label, attrs, []byte(secret), "text/plain", false, t0)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func secretOf(t *testing.T, c *Collection, id uint32) string {
	t.Helper()
	s, err := c.Secret(id)
	if err != nil {
		t.Fatal(err)
	}
	return string(s)
}

func TestRoundTripThroughTheFile(t *testing.T) {
	c := newTestCollection(t, "hunter2")
	git := mustCreate(t, c, "git", map[string]string{"service": "git", "user": "me"}, "tok-1")
	mustCreate(t, c, "mail", map[string]string{"service": "mail"}, "pw-2")

	data, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"tok-1", "pw-2", "git", "mail", "Login", "service"} {
		if bytes.Contains(data, []byte(leak)) {
			t.Errorf("the file holds %q in the clear", leak)
		}
	}

	back, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Locked() {
		t.Fatal("a parsed collection is unlocked")
	}
	if _, err := back.Secret(git); !errors.Is(err, ErrLocked) {
		t.Errorf("Secret while locked = %v, want ErrLocked", err)
	}
	if it, _ := back.Item(git); it.Label != "" || len(it.Attributes) != 0 {
		t.Errorf("a locked item shows %+v", it)
	}
	// Locked, the hashes still answer a search.
	if got := back.Search(map[string]string{"service": "git"}); !slices.Equal(got, []uint32{git}) {
		t.Errorf("locked search = %v, want [%d]", got, git)
	}

	if err := back.Unlock([]byte("hunter2")); err != nil {
		t.Fatal(err)
	}
	if back.Label() != "Login" || !back.Created().Equal(t0) {
		t.Errorf("label %q created %v", back.Label(), back.Created())
	}
	it, err := back.Item(git)
	if err != nil || it.Label != "git" || it.Attributes["user"] != "me" || it.ContentType != "text/plain" {
		t.Errorf("item = %+v, %v", it, err)
	}
	if got := secretOf(t, back, git); got != "tok-1" {
		t.Errorf("secret = %q", got)
	}
}

func TestWrongPasswordStaysLocked(t *testing.T) {
	c := newTestCollection(t, "right")
	id := mustCreate(t, c, "x", nil, "s")
	data, _ := c.Marshal()
	back, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := back.Unlock([]byte("wrong")); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("Unlock(wrong) = %v, want ErrWrongPassword", err)
	}
	if !back.Locked() {
		t.Fatal("a wrong password unlocked the collection")
	}
	if err := back.Unlock([]byte("right")); err != nil {
		t.Fatalf("the right password after a wrong one: %v", err)
	}
	if secretOf(t, back, id) != "s" {
		t.Error("secret lost")
	}
}

func TestLockSealsChangesAndUnlockRestoresThem(t *testing.T) {
	c := newTestCollection(t, "pw")
	id := mustCreate(t, c, "a", map[string]string{"k": "v"}, "one")
	if err := c.SetSecret(id, []byte("two"), "text/plain", t0); err != nil {
		t.Fatal(err)
	}
	if err := c.Lock(); err != nil {
		t.Fatal(err)
	}
	if err := c.SetSecret(id, []byte("three"), "text/plain", t0); !errors.Is(err, ErrLocked) {
		t.Errorf("SetSecret locked = %v", err)
	}
	if _, _, err := c.CreateItem("b", nil, []byte("x"), "", false, t0); !errors.Is(err, ErrLocked) {
		t.Errorf("CreateItem locked = %v", err)
	}
	if err := c.Unlock([]byte("nope")); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("relocked Unlock(wrong) = %v", err)
	}
	if err := c.Unlock([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	if secretOf(t, c, id) != "two" {
		t.Error("the change before Lock was lost")
	}
	// A locked collection marshals the body it sealed.
	_ = c.Lock()
	data, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, _ := Parse(data)
	if err := back.Unlock([]byte("pw")); err != nil || secretOf(t, back, id) != "two" {
		t.Errorf("file of a locked collection: %v", err)
	}
}

func TestSearchMatchesEveryAttribute(t *testing.T) {
	c := newTestCollection(t, "pw")
	a := mustCreate(t, c, "a", map[string]string{"service": "git", "user": "me"}, "1")
	b := mustCreate(t, c, "b", map[string]string{"service": "git", "user": "you"}, "2")
	if got := c.Search(map[string]string{"service": "git"}); !slices.Equal(got, []uint32{a, b}) {
		t.Errorf("service=git = %v", got)
	}
	if got := c.Search(map[string]string{"service": "git", "user": "you"}); !slices.Equal(got, []uint32{b}) {
		t.Errorf("service=git user=you = %v", got)
	}
	if got := c.Search(map[string]string{"service": "mail"}); len(got) != 0 {
		t.Errorf("service=mail = %v", got)
	}
	// Name and value are hashed apart: "a\x00b"="c" is not "a"="b\x00c".
	c2 := newTestCollection(t, "pw")
	mustCreate(t, c2, "x", map[string]string{"a": "b\x00c"}, "1")
	if got := c2.Search(map[string]string{"a\x00b": "c"}); len(got) != 0 {
		t.Errorf("ambiguous attribute matched: %v", got)
	}
	if got := c.Search(nil); !slices.Equal(got, []uint32{a, b}) {
		t.Errorf("empty search = %v, want every item", got)
	}
}

func TestReplaceUpdatesTheItemWithTheSameAttributes(t *testing.T) {
	c := newTestCollection(t, "pw")
	attrs := map[string]string{"service": "git"}
	id := mustCreate(t, c, "old", attrs, "1")
	got, created, err := c.CreateItem("new", attrs, []byte("2"), "text/plain", true, t0)
	if err != nil || created || got != id {
		t.Fatalf("replace = %d created %v, %v; want %d updated", got, created, err, id)
	}
	if secretOf(t, c, id) != "2" {
		t.Error("replace kept the old secret")
	}
	// A superset of attributes is a different item.
	other, created, _ := c.CreateItem("x", map[string]string{"service": "git", "user": "me"}, []byte("3"), "", true, t0)
	if !created || other == id {
		t.Error("replace matched an item with different attributes")
	}
	// Without replace, the same attributes make a second item.
	dup, created, _ := c.CreateItem("dup", attrs, []byte("4"), "", false, t0)
	if !created || dup == id {
		t.Error("CreateItem without replace updated an existing item")
	}
}

func TestEditAndDelete(t *testing.T) {
	c := newTestCollection(t, "pw")
	id := mustCreate(t, c, "a", map[string]string{"k": "v"}, "1")
	later := t0.Add(time.Hour)
	if err := c.SetItemLabel(id, "renamed", later); err != nil {
		t.Fatal(err)
	}
	if err := c.SetItemAttributes(id, map[string]string{"k": "w"}, later); err != nil {
		t.Fatal(err)
	}
	if got := c.Search(map[string]string{"k": "v"}); len(got) != 0 {
		t.Error("old attributes still match")
	}
	if got := c.Search(map[string]string{"k": "w"}); !slices.Equal(got, []uint32{id}) {
		t.Error("new attributes do not match")
	}
	it, _ := c.Item(id)
	if it.Label != "renamed" || !it.Modified.Equal(later) || !c.Modified().Equal(later) {
		t.Errorf("item %+v, collection modified %v", it, c.Modified())
	}
	if err := c.DeleteItem(id, later); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Item(id); !errors.Is(err, ErrNoItem) {
		t.Errorf("deleted item = %v", err)
	}
	if err := c.DeleteItem(id, later); !errors.Is(err, ErrNoItem) {
		t.Errorf("second delete = %v", err)
	}
	// Ids are never reused within a collection.
	if next := mustCreate(t, c, "b", nil, "2"); next == id {
		t.Error("a deleted item's id was reused")
	}
}

func TestChangePassword(t *testing.T) {
	c := newTestCollection(t, "old")
	id := mustCreate(t, c, "a", nil, "s")
	if err := c.ChangePassword([]byte("new"), testKDF); err != nil {
		t.Fatal(err)
	}
	data, _ := c.Marshal()
	back, _ := Parse(data)
	if err := back.Unlock([]byte("old")); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("old password = %v", err)
	}
	if err := back.Unlock([]byte("new")); err != nil || secretOf(t, back, id) != "s" {
		t.Errorf("new password: %v", err)
	}
	_ = c.Lock()
	if err := c.ChangePassword([]byte("x"), testKDF); !errors.Is(err, ErrLocked) {
		t.Errorf("ChangePassword locked = %v", err)
	}
}

// Corruption is a precise error, never a silent empty collection, and
// tampering with the cleartext header breaks the body's authentication.
func TestCorruptFilesFailPrecisely(t *testing.T) {
	c := newTestCollection(t, "pw")
	mustCreate(t, c, "a", map[string]string{"k": "v"}, "s")
	good, _ := c.Marshal()

	for _, tc := range []struct {
		name  string
		data  []byte
		parse string
	}{
		{"empty", nil, "bad magic"},
		{"other file", []byte("not a keyring at all"), "bad magic"},
		{"truncated header", good[:20], "truncated or corrupt header"},
		{"zero kdf", func() []byte { d := bytes.Clone(good); copy(d[8:12], []byte{0, 0, 0, 0}); return d }(), "invalid KDF"},
	} {
		if _, err := Parse(tc.data); err == nil || !strings.Contains(err.Error(), tc.parse) {
			t.Errorf("%s: Parse = %v, want %q", tc.name, err, tc.parse)
		}
	}

	// A flipped bit in the body, or in an attribute hash, fails to open
	// as the wrong password would: AEAD cannot tell the two apart.
	for _, at := range []int{len(good) - 1, len(magic) + 4 + 4 + 1 + saltLen + 4 + 4 + 4} {
		d := bytes.Clone(good)
		d[at] ^= 1
		back, err := Parse(d)
		if err != nil {
			t.Fatalf("flip at %d: Parse = %v", at, err)
		}
		if err := back.Unlock([]byte("pw")); !errors.Is(err, ErrWrongPassword) {
			t.Errorf("flip at %d: Unlock = %v, want an authentication failure", at, err)
		}
	}
}

func TestSecretsAreBoundToTheirItem(t *testing.T) {
	c := newTestCollection(t, "pw")
	a := mustCreate(t, c, "a", nil, "secret-a")
	b := mustCreate(t, c, "b", nil, "secret-b")
	c.entries[a].sealed, c.entries[b].sealed = c.entries[b].sealed, c.entries[a].sealed
	if _, err := c.Secret(a); err == nil {
		t.Error("a secret swapped between items opened")
	}
}

func FuzzParse(f *testing.F) {
	c, _ := New("L", []byte("pw"), testKDF, t0)
	_, _, _ = c.CreateItem("a", map[string]string{"k": "v"}, []byte("s"), "", false, t0)
	good, _ := c.Marshal()
	f.Add(good)
	f.Add([]byte{})
	f.Add(magic[:])
	f.Fuzz(func(t *testing.T, data []byte) {
		back, err := Parse(data)
		if err != nil {
			return
		}
		if back.kdf.MemKiB > 1024 || back.kdf.Time > 2 {
			return
		}
		_ = back.Unlock([]byte("pw"))
	})
}
