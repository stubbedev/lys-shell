package clipboard

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/stubbedev/lys-lib/atomicfile"
)

// Digest is the SHA-256 of an entry's bytes: the name its payload file
// is stored under, so the same bytes are written once however many
// records name them.
type Digest [sha256.Size]byte

// DigestOf is data's digest.
func DigestOf(data []byte) Digest { return sha256.Sum256(data) }

// String is the digest in lowercase hex, its file name.
func (d Digest) String() string { return hex.EncodeToString(d[:]) }

// Record is one entry a Store holds: which bytes, under which mime.
type Record struct {
	Digest Digest
	Mime   string
	Size   int
}

// storeItem is one entry a Sync wants on disk. Data is nil for an entry
// whose bytes the store already holds.
type storeItem struct {
	rec  Record
	data []byte
}

// storeIndex is the file naming a Store's records, most recent first.
const storeIndex = "index"

// storePerm keeps the payloads and the index the user's alone: a
// clipboard is where passwords and private messages pass through.
const storePerm = 0o600

// Store keeps clipboard entries in a directory: one payload file per
// distinct content, named by its digest, and an index listing the
// records in order. Opening one reads the index only; a payload is read
// when Read asks for it. Every file is replaced atomically, so a write
// interrupted at any point leaves the store as it was before it or as
// it is after it. Safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	dir     string
	records []Record
}

// OpenStore opens the store in dir, creating it, and reads its index.
// Files the index does not name are what an interrupted write left
// behind, and are removed.
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil { //nolint:gosec // the shell's own data directory
		return nil, fmt.Errorf("clipboard: create the store %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a directory, which needs its search bit
		return nil, fmt.Errorf("clipboard: keep the store %s private: %w", dir, err)
	}
	records, err := readIndex(filepath.Join(dir, storeIndex))
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dir, records: records}
	if err := s.removeUnnamed(); err != nil {
		return nil, err
	}
	return s, nil
}

// Records are the store's records, most recent first.
func (s *Store) Records() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Record(nil), s.records...)
}

// ErrCorrupt is a payload whose bytes are not the ones its record names.
var ErrCorrupt = errors.New("clipboard: a stored entry does not match its record")

// Read copies r's payload into w, checking it against the record.
func (s *Store) Read(r Record, w io.Writer) error {
	path := filepath.Join(s.dir, r.Digest.String())
	f, err := os.Open(path) //nolint:gosec // a digest-named file in the store's own directory
	if err != nil {
		return fmt.Errorf("clipboard: read the stored %s entry: %w", r.Mime, err)
	}
	defer func() { _ = f.Close() }()
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, sum), io.LimitReader(f, int64(r.Size)+1))
	if err != nil {
		return fmt.Errorf("clipboard: read %s: %w", path, err)
	}
	if got := sum.Sum(nil); n != int64(r.Size) || !bytes.Equal(got, r.Digest[:]) {
		return fmt.Errorf("%w: %s holds %d bytes summing to %x, the record names %d summing to %s", ErrCorrupt, path, n, got, r.Size, r.Digest)
	}
	return nil
}

// Sync makes the store hold exactly items, in their order: payloads it
// lacks are written first, then the index naming them, and only then
// are the payloads no record names any more removed. An item without
// data must already be in the store.
func (s *Store) Sync(items []storeItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	held := make(map[Digest]bool, len(s.records))
	for _, r := range s.records {
		held[r.Digest] = true
	}
	records := make([]Record, len(items))
	wanted := make(map[Digest]bool, len(items))
	for i, it := range items {
		records[i] = it.rec
		wanted[it.rec.Digest] = true
		if held[it.rec.Digest] {
			continue
		}
		if it.data == nil {
			return fmt.Errorf("clipboard: the %s entry %s is neither in memory nor stored", it.rec.Mime, it.rec.Digest)
		}
		if err := atomicfile.WriteFile(filepath.Join(s.dir, it.rec.Digest.String()), it.data, storePerm); err != nil {
			return fmt.Errorf("clipboard: store the %s entry: %w", it.rec.Mime, err)
		}
		held[it.rec.Digest] = true
	}
	if err := atomicfile.Write(filepath.Join(s.dir, storeIndex), storePerm, func(w io.Writer) error { return writeIndex(w, records) }); err != nil {
		return fmt.Errorf("clipboard: store the index: %w", err)
	}
	old := s.records
	s.records = records
	var errs []error
	for _, r := range old {
		if wanted[r.Digest] {
			continue
		}
		wanted[r.Digest] = true
		if err := os.Remove(filepath.Join(s.dir, r.Digest.String())); err != nil && !errors.Is(err, os.ErrNotExist) { //nolint:gosec // a digest-named file in the store's own directory
			errs = append(errs, fmt.Errorf("clipboard: remove a stored entry no longer kept: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (s *Store) removeUnnamed() error {
	files, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("clipboard: list the store %s: %w", s.dir, err)
	}
	named := make(map[string]bool, len(s.records)+1)
	named[storeIndex] = true
	for _, r := range s.records {
		named[r.Digest.String()] = true
	}
	var errs []error
	for _, f := range files {
		if named[f.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.dir, f.Name())); err != nil {
			errs = append(errs, fmt.Errorf("clipboard: remove %s left in the store: %w", f.Name(), err))
		}
	}
	return errors.Join(errs...)
}

func writeIndex(w io.Writer, records []Record) error {
	for _, r := range records {
		if _, err := fmt.Fprintf(w, "%s\t%d\t%s\n", r.Digest, r.Size, strconv.Quote(r.Mime)); err != nil {
			return err
		}
	}
	return nil
}

func readIndex(path string) ([]Record, error) {
	f, err := os.Open(path) //nolint:gosec // the store's own index
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("clipboard: read the store index: %w", err)
	}
	defer func() { _ = f.Close() }()
	var records []Record
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		r, err := parseRecord(sc.Text())
		if err != nil {
			return nil, fmt.Errorf("clipboard: %s line %d: %w", path, line, err)
		}
		records = append(records, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("clipboard: read %s: %w", path, err)
	}
	return records, nil
}

func parseRecord(line string) (Record, error) {
	sum, rest, ok1 := strings.Cut(line, "\t")
	size, mime, ok2 := strings.Cut(rest, "\t")
	if !ok1 || !ok2 {
		return Record{}, fmt.Errorf("want digest, size and mime separated by tabs, got %q", line)
	}
	var r Record
	if len(sum) != hex.EncodedLen(len(r.Digest)) {
		return Record{}, fmt.Errorf("bad digest %q", sum)
	}
	if _, err := hex.Decode(r.Digest[:], []byte(sum)); err != nil {
		return Record{}, fmt.Errorf("bad digest %q", sum)
	}
	n, err := strconv.Atoi(size)
	if err != nil || n <= 0 || n > MaxEntryBytes {
		return Record{}, fmt.Errorf("bad size %q", size)
	}
	r.Size = n
	if r.Mime, err = strconv.Unquote(mime); err != nil || r.Mime == "" {
		return Record{}, fmt.Errorf("bad mime %s", mime)
	}
	return r, nil
}
