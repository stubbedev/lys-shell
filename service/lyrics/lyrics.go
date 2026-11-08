package lyrics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/stubbedev/lys-desktop/identity"
	"github.com/stubbedev/lys-lib/atomicfile"

	"github.com/stubbedev/lys-shell/internal/version"
)

// Endpoint is LRCLIB's lookup by signature.
const Endpoint = "https://lrclib.net/api/get"

const (
	requestTimeout = 10 * time.Second
	maxBody        = 1 << 20
	keptInMemory   = 32
	userAgent      = identity.Name + "/" + version.Version + " (https://github.com/stubbedev/lys-shell)"
)

// Track is what LRCLIB matches lyrics on: the MPRIS xesam fields and
// mpris:length.
type Track struct {
	Artist string
	Title  string
	Album  string
	Length time.Duration
}

func (t Track) key() key {
	return key{artist: t.Artist, title: t.Title, album: t.Album, seconds: int64((t.Length + time.Second/2) / time.Second)}
}

type key struct {
	artist, title, album string
	seconds              int64
}

func (k key) file() string {
	sum := sha256.Sum256([]byte(k.artist + "\x00" + k.title + "\x00" + k.album + "\x00" + strconv.FormatInt(k.seconds, 10)))
	return hex.EncodeToString(sum[:16]) + ".json"
}

type lookup struct {
	done   chan struct{}
	lyrics Lyrics
	err    error
}

// Service looks lyrics up, once per track: a result, a miss included,
// is kept in memory and in the cache directory, and concurrent asks for
// one track share a request.
type Service struct {
	endpoint string
	dir      string
	client   *http.Client

	mu      sync.Mutex
	known   map[key]Lyrics
	order   []key
	pending map[key]*lookup
}

// New looks lyrics up at endpoint (Endpoint, or a test server) and
// caches them in dir.
func New(endpoint, dir string) *Service {
	return &Service{
		endpoint: endpoint,
		dir:      dir,
		client:   &http.Client{Timeout: requestTimeout},
		known:    make(map[key]Lyrics),
		pending:  make(map[key]*lookup),
	}
}

// Get returns the track's lyrics; a track without an artist or title
// has none and asks nothing. A failed request is an error and is not
// cached, so the next Get asks again.
func (s *Service) Get(ctx context.Context, t Track) (Lyrics, error) {
	if t.Artist == "" || t.Title == "" {
		return Lyrics{}, nil
	}
	k := t.key()
	s.mu.Lock()
	if l, ok := s.known[k]; ok {
		s.mu.Unlock()
		return l, nil
	}
	if p, ok := s.pending[k]; ok {
		s.mu.Unlock()
		select {
		case <-p.done:
			return p.lyrics, p.err
		case <-ctx.Done():
			return Lyrics{}, ctx.Err()
		}
	}
	p := &lookup{done: make(chan struct{})}
	s.pending[k] = p
	s.mu.Unlock()

	p.lyrics, p.err = s.resolve(ctx, t, k)
	s.mu.Lock()
	delete(s.pending, k)
	if p.err == nil {
		s.keep(k, p.lyrics)
	}
	s.mu.Unlock()
	close(p.done)
	return p.lyrics, p.err
}

func (s *Service) keep(k key, l Lyrics) {
	if len(s.order) == keptInMemory {
		delete(s.known, s.order[0])
		s.order = append(s.order[:0], s.order[1:]...)
	}
	s.known[k] = l
	s.order = append(s.order, k)
}

func (s *Service) resolve(ctx context.Context, t Track, k key) (Lyrics, error) {
	path := filepath.Join(s.dir, k.file())
	l, err := readCached(s.dir, k.file())
	if err == nil {
		return l, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		log.Printf("lyrics: dropping unreadable cache entry %s: %v", path, err)
	}
	l, err = s.fetch(ctx, t)
	if err != nil {
		return Lyrics{}, err
	}
	if err := writeCached(path, l); err != nil {
		return Lyrics{}, err
	}
	return l, nil
}

func readCached(dir, name string) (Lyrics, error) {
	f, err := os.OpenInRoot(dir, name)
	if err != nil {
		return Lyrics{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return Lyrics{}, err
	}
	var l Lyrics
	if err := json.Unmarshal(data, &l); err != nil {
		return Lyrics{}, fmt.Errorf("decode: %w", err)
	}
	return l, nil
}

func writeCached(path string, l Lyrics) error {
	data, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("lyrics: encode %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("lyrics: cache directory: %w", err)
	}
	if err := atomicfile.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("lyrics: cache %s: %w", path, err)
	}
	return nil
}

type record struct {
	Instrumental bool    `json:"instrumental"`
	PlainLyrics  *string `json:"plainLyrics"`
	SyncedLyrics *string `json:"syncedLyrics"`
}

func (s *Service) fetch(ctx context.Context, t Track) (Lyrics, error) {
	q := url.Values{"track_name": {t.Title}, "artist_name": {t.Artist}}
	if t.Album != "" {
		q.Set("album_name", t.Album)
	}
	if k := t.key(); k.seconds > 0 {
		q.Set("duration", strconv.FormatInt(k.seconds, 10))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return Lyrics{}, fmt.Errorf("lyrics: request for %q by %q: %w", t.Title, t.Artist, err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return Lyrics{}, fmt.Errorf("lyrics: look up %q by %q: %w", t.Title, t.Artist, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Lyrics{}, nil
	case resp.StatusCode != http.StatusOK:
		return Lyrics{}, fmt.Errorf("lyrics: look up %q by %q: %s", t.Title, t.Artist, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return Lyrics{}, fmt.Errorf("lyrics: read the reply for %q by %q: %w", t.Title, t.Artist, err)
	}
	var r record
	if err := json.Unmarshal(body, &r); err != nil {
		return Lyrics{}, fmt.Errorf("lyrics: decode the reply for %q by %q: %w", t.Title, t.Artist, err)
	}
	return r.lyrics(), nil
}

func (r record) lyrics() Lyrics {
	if r.Instrumental {
		return Lyrics{}
	}
	if r.SyncedLyrics != nil {
		if lines := ParseLRC(*r.SyncedLyrics); len(lines) > 0 {
			return Lyrics{Lines: lines, Synced: true}
		}
	}
	if r.PlainLyrics != nil {
		return Lyrics{Lines: plainLines(*r.PlainLyrics)}
	}
	return Lyrics{}
}
