package clipboard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// session is one shell run over the stores in dir: a fresh history and
// clipboard, as after `lys-shell panel restart`.
func session(t *testing.T, dir string, persist bool) (*Clipboard, *fakeDevice) {
	t.Helper()
	pins, err := OpenPinnedStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	stores := Stores{Pinned: pins}
	if persist {
		if stores.History, err = OpenHistoryStore(dir); err != nil {
			t.Fatal(err)
		}
	}
	h := defaultHistory(t)
	dev := &fakeDevice{}
	c := start(dev, func(fn func()) { fn() }, h, stores)
	t.Cleanup(c.Close)
	return c, dev
}

func copyIn(dev *fakeDevice, mime, data string) {
	offer := &fakeOffer{mimes: []string{mime}}
	dev.announce(offer)
	offer.finish(data)
}

// listed is every entry as mime=bytes, pinned ones starred.
func listed(c *Clipboard) []string {
	snap := c.Entries()
	defer snap.Release()
	var out []string
	for _, e := range snap.Entries() {
		s := fmt.Sprintf("%s=%s", e.Mime, e.Bytes())
		if e.Pinned {
			s = "*" + s
		}
		out = append(out, s)
	}
	return out
}

func idOf(t *testing.T, c *Clipboard, data string) uint64 {
	t.Helper()
	snap := c.Entries()
	defer snap.Release()
	for _, e := range snap.Entries() {
		if string(e.Bytes()) == data {
			return e.ID
		}
	}
	t.Fatalf("no entry holds %q", data)
	return 0
}

func TestAPinnedEntrySurvivesARestartAndAnUnpinnedOneDoesNot(t *testing.T) {
	dir := t.TempDir()
	c, dev := session(t, dir, false)
	png := "\x89PNG\r\n\x1a\n\x00binary"
	copyIn(dev, "image/png", png)
	copyIn(dev, textMime, "gone after restart")
	if err := c.Pin(idOf(t, c, png)); err != nil {
		t.Fatal(err)
	}
	c.Close()

	c2, _ := session(t, dir, false)
	if got := listed(c2); !slices.Equal(got, []string{"*image/png=" + png}) {
		t.Fatalf("after restart: %q", got)
	}
}

func TestStartingReadsTheIndexButNoPayload(t *testing.T) {
	dir := t.TempDir()
	c, dev := session(t, dir, false)
	copyIn(dev, textMime, "pinned text")
	if err := c.Pin(idOf(t, c, "pinned text")); err != nil {
		t.Fatal(err)
	}
	c.Close()
	payload := filepath.Join(dir, pinnedDir, DigestOf([]byte("pinned text")).String())
	if err := os.Chmod(payload, 0); err != nil {
		t.Fatal(err)
	}

	c2, _ := session(t, dir, false)
	c2.mu.Lock()
	waiting := len(c2.history.unloaded(0))
	c2.mu.Unlock()
	if waiting != 1 {
		t.Fatalf("%d entries wait for their bytes, want the pin", waiting)
	}
	if got := listed(c2); len(got) != 0 {
		t.Fatalf("an unreadable payload was listed: %q", got)
	}
	if err := os.Chmod(payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := listed(c2); !slices.Equal(got, []string{"*" + textMime + "=pinned text"}) {
		t.Fatalf("once readable: %q", got)
	}
}

func TestADamagedPinIsForgottenAndTheRestStillShow(t *testing.T) {
	dir := t.TempDir()
	c, dev := session(t, dir, false)
	copyIn(dev, textMime, "one")
	copyIn(dev, textMime, "two")
	for _, s := range []string{"one", "two"} {
		if err := c.Pin(idOf(t, c, s)); err != nil {
			t.Fatal(err)
		}
	}
	c.Close()
	if err := os.WriteFile(filepath.Join(dir, pinnedDir, DigestOf([]byte("one")).String()), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	c2, _ := session(t, dir, false)
	if got := listed(c2); !slices.Equal(got, []string{"*" + textMime + "=two"}) {
		t.Fatalf("entries = %q", got)
	}
	if got := len(c2.pins.Records()); got != 1 {
		t.Errorf("the damaged pin is still in the index (%d records)", got)
	}
}

func TestASecretSelectionCannotBePinned(t *testing.T) {
	dir := t.TempDir()
	c, dev := session(t, dir, false)
	offer := &fakeOffer{mimes: []string{textMime, SensitiveMimes[0]}}
	dev.announce(offer)
	if offer.w != nil {
		offer.finish("hunter2")
	}
	if _, err := c.Put(SecretContent("hunter2")); err != nil {
		t.Fatal(err)
	}
	if got := listed(c); len(got) != 0 {
		t.Fatalf("a secret was remembered: %q", got)
	}
	if err := c.Pin(1); !errors.Is(err, ErrEntryGone) {
		t.Errorf("pinning: %v, want nothing to pin", err)
	}
	if got := c.pins.Records(); len(got) != 0 {
		t.Errorf("a secret reached the disk: %+v", got)
	}
}

func TestForgettingAPinRemovesItsFile(t *testing.T) {
	dir := t.TempDir()
	c, dev := session(t, dir, false)
	copyIn(dev, textMime, "pinned")
	id := idOf(t, c, "pinned")
	if err := c.Pin(id); err != nil {
		t.Fatal(err)
	}
	if !c.Forget(id) {
		t.Fatal("Forget missed the pin")
	}
	if got := dirNames(t, filepath.Join(dir, pinnedDir)); !slices.Equal(got, []string{storeIndex}) {
		t.Errorf("files = %v", got)
	}
}

func TestUnpinningRemovesTheEntryFromDisk(t *testing.T) {
	dir := t.TempDir()
	c, dev := session(t, dir, false)
	copyIn(dev, textMime, "pinned")
	id := idOf(t, c, "pinned")
	if err := c.Pin(id); err != nil {
		t.Fatal(err)
	}
	if err := c.Unpin(id); err != nil {
		t.Fatal(err)
	}
	if got := listed(c); !slices.Equal(got, []string{textMime + "=pinned"}) {
		t.Errorf("entries = %q", got)
	}
	if got := c.pins.Records(); len(got) != 0 {
		t.Errorf("records = %+v", got)
	}
}

func TestAPinThatCannotBeWrittenIsUndone(t *testing.T) {
	dir := t.TempDir()
	c, dev := session(t, dir, false)
	copyIn(dev, textMime, "pinned")
	id := idOf(t, c, "pinned")
	if err := os.Chmod(filepath.Join(dir, pinnedDir), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, pinnedDir), 0o700) })
	if err := c.Pin(id); err == nil {
		t.Fatal("a pin that could not be written succeeded")
	}
	if got := listed(c); !slices.Equal(got, []string{textMime + "=pinned"}) {
		t.Errorf("entries = %q, want the entry unpinned", got)
	}
}

func TestPinningNeedsAStore(t *testing.T) {
	c, dev := newTestClipboard(t, nil)
	copyIn(dev, textMime, "x")
	if c.CanPin() {
		t.Error("CanPin without a store")
	}
	if err := c.Pin(idOf(t, c, "x")); !errors.Is(err, ErrNoPinStore) {
		t.Errorf("pin: %v", err)
	}
}

// waitSaved waits for the persisted history's store to hold n records.
func waitSaved(t *testing.T, s *Store, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(s.Records()) != n {
		if time.Now().After(deadline) {
			t.Fatalf("the saved history holds %+v, want %d records", s.Records(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAPersistedHistorySurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	c, dev := session(t, dir, true)
	copyIn(dev, textMime, "older")
	copyIn(dev, textMime, "newer")
	c.Close()

	c2, dev2 := session(t, dir, true)
	if got := listed(c2); !slices.Equal(got, []string{textMime + "=newer", textMime + "=older"}) {
		t.Fatalf("after restart: %q", got)
	}
	copyIn(dev2, textMime, "newest")
	waitSaved(t, c2.saver.store, 3)
}

func TestTurningPersistenceOffRemovesTheSavedHistory(t *testing.T) {
	dir := t.TempDir()
	c, dev := session(t, dir, true)
	copyIn(dev, textMime, "kept")
	store := c.saver.store
	waitSaved(t, store, 1)
	if err := c.SetPersist(nil); err != nil {
		t.Fatal(err)
	}
	if c.Persisting() {
		t.Error("still persisting")
	}
	if got := dirNames(t, filepath.Join(dir, historyDir)); !slices.Equal(got, []string{storeIndex}) {
		t.Errorf("files = %v, want only an empty index", got)
	}
	if got := listed(c); !slices.Equal(got, []string{textMime + "=kept"}) {
		t.Errorf("memory lost the entry: %q", got)
	}
	if err := c.SetPersist(store); err != nil {
		t.Fatal(err)
	}
	waitSaved(t, store, 1)
}

func TestWithoutPersistenceNothingUnpinnedIsWritten(t *testing.T) {
	dir := t.TempDir()
	c, dev := session(t, dir, false)
	copyIn(dev, textMime, "memory only")
	c.Close()
	if _, err := os.Stat(filepath.Join(dir, historyDir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the history store exists: %v", err)
	}
	if got := mustStore(t, filepath.Join(dir, pinnedDir)).Records(); len(got) != 0 {
		t.Errorf("pinned records = %+v", got)
	}
}
