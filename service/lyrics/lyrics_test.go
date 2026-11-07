package lyrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseLRC(t *testing.T) {
	got := ParseLRC("[ar:Band]\n[ti:Song]\n[00:12.34]First line\r\n[00:05.5][01:02.345]Chorus\nno tag\n[00:20]\n[xx:yy]junk\n")
	want := []Line{
		{At: 5500 * time.Millisecond, Text: "Chorus"},
		{At: 12340 * time.Millisecond, Text: "First line"},
		{At: 20 * time.Second, Text: ""},
		{At: time.Minute + 2345*time.Millisecond, Text: "Chorus"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseLRC = %+v\nwant %+v", got, want)
	}
	shifted := ParseLRC("[offset:+500]\n[00:01.00]a\n[00:00.20]b\n")
	if len(shifted) != 2 || shifted[0].At != 0 || shifted[1].At != 500*time.Millisecond {
		t.Errorf("an offset of +500ms gave %+v, want b at 0 (clamped) and a at 0.5s", shifted)
	}
	if lines := ParseLRC("[00:61.00]bad seconds\n[00:01.1234]too fine\n[ar:x]\nplain\n"); len(lines) != 0 {
		t.Errorf("malformed stamps parsed into %+v", lines)
	}
}

func TestCurrentLine(t *testing.T) {
	l := Lyrics{Synced: true, Lines: []Line{{At: time.Second, Text: "a"}, {At: 3 * time.Second, Text: "b"}, {At: 3 * time.Second, Text: "c"}}}
	for _, tc := range []struct {
		pos  time.Duration
		want int
	}{{0, -1}, {time.Second, 0}, {2 * time.Second, 0}, {3 * time.Second, 2}, {time.Hour, 2}} {
		if got := l.Current(tc.pos); got != tc.want {
			t.Errorf("Current(%v) = %d, want %d", tc.pos, got, tc.want)
		}
	}
	plain := Lyrics{Lines: []Line{{Text: "a"}}}
	if plain.Current(time.Hour) != -1 {
		t.Error("plain lyrics have a current line")
	}
}

type fakeLRCLIB struct {
	*httptest.Server
	requests atomic.Int32
	mu       sync.Mutex
	queries  []string
	status   int
	body     string
	gate     chan struct{}
}

func newFakeLRCLIB(t *testing.T, status int, body string) *fakeLRCLIB {
	t.Helper()
	f := &fakeLRCLIB{status: status, body: body}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		f.mu.Lock()
		f.queries = append(f.queries, r.URL.RawQuery)
		status, body, gate := f.status, f.body, f.gate
		f.mu.Unlock()
		if gate != nil {
			<-gate
		}
		if r.Header.Get("User-Agent") != userAgent {
			http.Error(w, "no user agent", http.StatusBadRequest)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.Close)
	return f
}

var song = Track{Artist: "Band", Title: "Song", Album: "Record", Length: 185400 * time.Millisecond}

func TestOneRequestPerTrackAcrossMemoryAndDisk(t *testing.T) {
	server := newFakeLRCLIB(t, http.StatusOK, `{"instrumental":false,"plainLyrics":"a\nb","syncedLyrics":"[00:01.00]a\n[00:02.00]b"}`)
	dir := t.TempDir()
	s := New(server.URL, dir)
	ctx := context.Background()
	for range 3 {
		l, err := s.Get(ctx, song)
		if err != nil {
			t.Fatal(err)
		}
		if !l.Synced || len(l.Lines) != 2 || l.Lines[1] != (Line{At: 2 * time.Second, Text: "b"}) {
			t.Fatalf("lyrics = %+v", l)
		}
	}
	if l, err := New(server.URL, dir).Get(ctx, song); err != nil || !l.Found() {
		t.Fatalf("a restarted service: %+v, %v", l, err)
	}
	if n := server.requests.Load(); n != 1 {
		t.Errorf("%d requests for one track, want 1", n)
	}
	if want := "album_name=Record&artist_name=Band&duration=185&track_name=Song"; server.queries[0] != want {
		t.Errorf("query = %q, want %q", server.queries[0], want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache dir holds %v (%v), want one entry", entries, err)
	}
	info, err := entries[0].Info()
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("cache entry mode %v, want 0600", info.Mode().Perm())
	}
}

func TestAMissIsCachedToo(t *testing.T) {
	server := newFakeLRCLIB(t, http.StatusNotFound, `{"code":404,"name":"TrackNotFound"}`)
	dir := t.TempDir()
	for range 2 {
		l, err := New(server.URL, dir).Get(context.Background(), song)
		if err != nil || l.Found() {
			t.Fatalf("a 404 = %+v, %v; want no lyrics and no error", l, err)
		}
	}
	if n := server.requests.Load(); n != 1 {
		t.Errorf("%d requests for a missing track, want 1", n)
	}
}

func TestAFailureIsAnErrorAndAsksAgain(t *testing.T) {
	server := newFakeLRCLIB(t, http.StatusInternalServerError, "boom")
	s := New(server.URL, t.TempDir())
	for range 2 {
		if _, err := s.Get(context.Background(), song); err == nil {
			t.Fatal("a 500 was no error")
		}
	}
	if n := server.requests.Load(); n != 2 {
		t.Errorf("%d requests, want each failed Get to ask again", n)
	}
}

func TestInstrumentalAndPlainLyrics(t *testing.T) {
	instrumental := newFakeLRCLIB(t, http.StatusOK, `{"instrumental":true,"plainLyrics":null,"syncedLyrics":null}`)
	if l, err := New(instrumental.URL, t.TempDir()).Get(context.Background(), song); err != nil || l.Found() {
		t.Errorf("an instrumental = %+v, %v; want no lyrics", l, err)
	}
	plain := newFakeLRCLIB(t, http.StatusOK, `{"instrumental":false,"plainLyrics":"one\ntwo\n\n","syncedLyrics":null}`)
	l, err := New(plain.URL, t.TempDir()).Get(context.Background(), song)
	if err != nil || l.Synced || !reflect.DeepEqual(l.Lines, []Line{{Text: "one"}, {Text: "two"}}) {
		t.Errorf("plain lyrics = %+v, %v", l, err)
	}
}

func TestATrackWithoutArtistAsksNothing(t *testing.T) {
	server := newFakeLRCLIB(t, http.StatusOK, `{}`)
	l, err := New(server.URL, t.TempDir()).Get(context.Background(), Track{Title: "Song"})
	if err != nil || l.Found() || server.requests.Load() != 0 {
		t.Errorf("no artist: %+v, %v, %d requests; want nothing asked", l, err, server.requests.Load())
	}
}

func TestConcurrentGetsShareOneRequest(t *testing.T) {
	server := newFakeLRCLIB(t, http.StatusOK, `{"plainLyrics":"x"}`)
	server.gate = make(chan struct{})
	s := New(server.URL, t.TempDir())
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if l, err := s.Get(context.Background(), song); err != nil || !l.Found() {
				t.Errorf("Get = %+v, %v", l, err)
			}
		})
	}
	for server.requests.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	close(server.gate)
	wg.Wait()
	if n := server.requests.Load(); n != 1 {
		t.Errorf("%d requests for concurrent Gets, want 1", n)
	}
}

func TestAnUnreadableCacheEntryIsFetchedAgain(t *testing.T) {
	server := newFakeLRCLIB(t, http.StatusOK, `{"plainLyrics":"x"}`)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, song.key().file()), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if l, err := New(server.URL, dir).Get(context.Background(), song); err != nil || !l.Found() {
		t.Fatalf("Get over a corrupt entry = %+v, %v", l, err)
	}
	if l, err := readCached(dir, song.key().file()); err != nil || !l.Found() {
		t.Errorf("the corrupt entry was not replaced: %+v, %v", l, err)
	}
}
