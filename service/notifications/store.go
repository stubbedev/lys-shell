package notifications

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/godbus/dbus/v5"
	_ "modernc.org/sqlite" // the pure-Go sqlite driver: notifications.db stays the Rust file

	"github.com/stubbedev/lys-shell/internal/intconv"
)

// deleteBatch is the most rows one retention or expiry DELETE removes,
// so a large sweep never holds the write lock for long.
const deleteBatch = 500

// schema is the Rust store's table, plus the indexes the history pages
// and sweeps by. Each CREATE is idempotent, so opening a file in the old
// schema migrates it in place.
const schema = `CREATE TABLE IF NOT EXISTS notifications (
		id INTEGER PRIMARY KEY,
		app_name TEXT,
		replaces_id INTEGER,
		app_icon TEXT,
		summary TEXT NOT NULL,
		body TEXT,
		actions TEXT NOT NULL,
		hints TEXT NOT NULL,
		expire_timeout INTEGER,
		timestamp INTEGER NOT NULL,
		image_path TEXT
	);
	CREATE INDEX IF NOT EXISTS notifications_newest ON notifications (timestamp, id);
	CREATE INDEX IF NOT EXISTS notifications_expiry ON notifications (timestamp + expire_timeout) WHERE expire_timeout > 0;`

const columns = `id, app_name, replaces_id, app_icon, summary, body,
	actions, hints, expire_timeout, timestamp, image_path`

const (
	newestSQL = `SELECT ` + columns + ` FROM notifications
		ORDER BY timestamp DESC, id DESC LIMIT ?`
	olderSQL = `SELECT ` + columns + ` FROM notifications
		WHERE (timestamp, id) < (?, ?) ORDER BY timestamp DESC, id DESC LIMIT ?`
	findSQL      = `SELECT ` + columns + ` FROM notifications WHERE id = ?`
	pruneSQL     = `DELETE FROM notifications WHERE id IN (SELECT id FROM notifications ORDER BY timestamp, id LIMIT ?)`
	expiriesSQL  = `SELECT id, timestamp + expire_timeout FROM notifications WHERE expire_timeout > 0 AND timestamp + expire_timeout > ?`
	sweepSQL     = `DELETE FROM notifications WHERE id IN (SELECT id FROM notifications WHERE expire_timeout > 0 AND timestamp + expire_timeout <= ? LIMIT ?)`
	countSQL     = `SELECT COUNT(*) FROM notifications`
	maxIDSQL     = `SELECT MAX(id) FROM notifications`
	insertSQL    = `INSERT OR REPLACE INTO notifications (` + columns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	removeSQL    = `DELETE FROM notifications WHERE id = ?`
	removeAllSQL = `DELETE FROM notifications`
)

// Store is the notification history on disk (persistence.rs): the same
// sqlite file, table, and column encodings the Rust shell writes, so
// the history survives a restart and a switch between the two.
type Store struct {
	db *sql.DB
}

// StorePath is persistence.rs's path: $HOME/.local/share/lys-shell/
// notifications.db (HOME, not XDG_DATA_HOME, as the Rust store does).
func StorePath() (string, error) {
	home := os.Getenv("HOME")
	if home == "" {
		return "", errors.New("notifications: HOME is not set")
	}
	return filepath.Join(home, ".local", "share", "lys-shell", "notifications.db"), nil
}

// OpenStore opens (creating or migrating if needed) the history at
// path; tests pass a temporary file.
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // the shared data dir, as the Rust store creates it
		return nil, fmt.Errorf("notification store %s: %w", path, err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("notification store %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode = WAL; PRAGMA synchronous = NORMAL;` + schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("notification store %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

// Close releases the database.
func (st *Store) Close() error { return st.db.Close() }

// Add stores or replaces one notification (INSERT OR REPLACE by id)
// and, in the same transaction, drops the prune oldest rows the
// retention limit no longer keeps.
func (st *Store) Add(n *Notification, prune int) error {
	actions := make([]string, 0, 2*len(n.Actions))
	for _, a := range n.Actions {
		actions = append(actions, a.ID, a.Label)
	}
	actionsJSON, err := json.Marshal(actions)
	if err != nil {
		return fmt.Errorf("storing notification %d: %w", n.ID, err)
	}
	hintsJSON, err := encodeHints(n)
	if err != nil {
		return fmt.Errorf("storing notification %d: %w", n.ID, err)
	}
	tx, err := st.db.Begin()
	if err != nil {
		return fmt.Errorf("storing notification %d: %w", n.ID, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(insertSQL,
		n.ID, nullString(n.AppName), nullID(n.ReplacesID), nullString(n.AppIcon), n.Summary,
		nullString(n.Body), string(actionsJSON), hintsJSON, nullTimeout(n.ExpireMS),
		n.Added.UnixMilli(), nullString(n.ImagePath)); err != nil {
		return fmt.Errorf("storing notification %d: %w", n.ID, err)
	}
	if prune > 0 {
		if _, err := tx.Exec(pruneSQL, prune); err != nil {
			return fmt.Errorf("pruning %d past notification %d: %w", prune, n.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storing notification %d: %w", n.ID, err)
	}
	return nil
}

// Remove deletes one notification, reporting whether it was stored.
func (st *Store) Remove(id uint32) (bool, error) {
	res, err := st.db.Exec(removeSQL, id)
	if err != nil {
		return false, fmt.Errorf("removing notification %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("removing notification %d: %w", id, err)
	}
	return n > 0, nil
}

// RemoveAll empties the history.
func (st *Store) RemoveAll() error {
	if _, err := st.db.Exec(removeAllSQL); err != nil {
		return fmt.Errorf("removing every notification: %w", err)
	}
	return nil
}

// Prune deletes the n oldest notifications in batches of deleteBatch,
// each its own transaction, reporting how many it deleted.
func (st *Store) Prune(n int) (int, error) {
	return st.deleteBatched(fmt.Sprintf("pruning the %d oldest notifications", n), n, func(limit int) (sql.Result, error) {
		return st.db.Exec(pruneSQL, limit)
	})
}

// SweepExpired deletes, in batches, every notification whose positive
// timeout ran out by now, reporting how many it deleted.
func (st *Store) SweepExpired(now time.Time) (int, error) {
	return st.deleteBatched("sweeping expired notifications", -1, func(limit int) (sql.Result, error) {
		return st.db.Exec(sweepSQL, now.UnixMilli(), limit)
	})
}

// deleteBatched runs del with batches of at most deleteBatch rows until
// want rows are gone (want < 0: until a batch comes back short).
func (st *Store) deleteBatched(what string, want int, del func(limit int) (sql.Result, error)) (int, error) {
	done := 0
	for want < 0 || done < want {
		limit := deleteBatch
		if want >= 0 {
			limit = min(limit, want-done)
		}
		res, err := del(limit)
		if err != nil {
			return done, fmt.Errorf("%s: %w", what, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return done, fmt.Errorf("%s: %w", what, err)
		}
		done += int(n)
		if int(n) < limit {
			break
		}
	}
	return done, nil
}

// Page reads up to limit notifications strictly older than before
// (nil: from the newest), newest first.
func (st *Store) Page(before *Key, limit int) ([]*Notification, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if before == nil {
		rows, err = st.db.Query(newestSQL, limit)
	} else {
		rows, err = st.db.Query(olderSQL, before.Stamp, before.ID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %d notifications before %v: %w", limit, before, err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]*Notification, 0, limit)
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, fmt.Errorf("reading notifications before %v: %w", before, err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading notifications before %v: %w", before, err)
	}
	return out, nil
}

// Find reads one notification by id.
func (st *Store) Find(id uint32) (*Notification, bool, error) {
	n, err := scanNotification(st.db.QueryRow(findSQL, id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("reading notification %d: %w", id, err)
	}
	return n, true, nil
}

// Count is how many notifications the store holds; read once, when the
// service attaches.
func (st *Store) Count() (int, error) {
	var n int
	if err := st.db.QueryRow(countSQL).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting notifications: %w", err)
	}
	return n, nil
}

// MaxID is the highest stored id, 0 for an empty store.
func (st *Store) MaxID() (uint32, error) {
	var id sql.NullInt64
	if err := st.db.QueryRow(maxIDSQL).Scan(&id); err != nil {
		return 0, fmt.Errorf("reading the highest notification id: %w", err)
	}
	return storedInt(0, "id", id, uint32(0)), nil
}

// expiry is one stored notification's pending timeout.
type expiry struct {
	id uint32
	at time.Time
}

// pendingExpiries are the stored notifications whose positive timeout
// runs out after now.
func (st *Store) pendingExpiries(now time.Time) ([]expiry, error) {
	rows, err := st.db.Query(expiriesSQL, now.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("reading pending expiries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []expiry
	for rows.Next() {
		var (
			id uint32
			at int64
		)
		if err := rows.Scan(&id, &at); err != nil {
			return nil, fmt.Errorf("reading pending expiries: %w", err)
		}
		out = append(out, expiry{id: id, at: time.UnixMilli(at)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading pending expiries: %w", err)
	}
	return out, nil
}

// scanNotification reads one row of columns into a notification, its
// hints parsed into the typed fields and not kept.
func scanNotification(row interface{ Scan(...any) error }) (*Notification, error) {
	var (
		id                                uint32
		appName, appIcon, body, imagePath sql.NullString
		replaces, timeout                 sql.NullInt64
		summary, actionsJSON, hintsJSON   string
		stamp                             int64
	)
	if err := row.Scan(&id, &appName, &replaces, &appIcon, &summary, &body,
		&actionsJSON, &hintsJSON, &timeout, &stamp, &imagePath); err != nil {
		return nil, err
	}
	var actions []string
	if err := json.Unmarshal([]byte(actionsJSON), &actions); err != nil {
		log.Printf("notifications: stored actions of %d: %v", id, err)
	}
	raw := decodeStoredHints(id, hintsJSON)
	if imagePath.Valid {
		raw["image-path"] = dbus.MakeVariant(imagePath.String)
	}
	h := decodeHints(raw, func(imageData) (string, bool) { return "", false })
	expire := storedInt(id, "expire_timeout", timeout, int32(-1))
	n := &Notification{
		ID:           id,
		AppName:      appName.String,
		ReplacesID:   storedInt(id, "replaces_id", replaces, uint32(0)),
		AppIcon:      appIcon.String,
		Summary:      summary,
		Body:         body.String,
		Actions:      ParseActions(actions),
		ExpireMS:     expire,
		Added:        time.UnixMilli(stamp),
		Urgency:      h.urgency,
		ImagePath:    h.imagePath,
		DesktopEntry: h.desktopEntry,
		Resident:     h.resident,
	}
	if expire > 0 {
		n.Expires = n.Added.Add(time.Duration(expire) * time.Millisecond)
	}
	return n, nil
}

// encodeHints writes the typed hints in the Rust store's hints column
// form; image-path has its own column.
func encodeHints(n *Notification) (string, error) {
	out := map[string]storedHint{
		"urgency": {Signature: "y", Value: json.RawMessage(strconv.Itoa(int(n.Urgency)))},
	}
	if n.DesktopEntry != "" {
		entry, err := json.Marshal(n.DesktopEntry)
		if err != nil {
			return "", err
		}
		out["desktop-entry"] = storedHint{Signature: "s", Value: entry}
	}
	if n.Resident {
		out["resident"] = storedHint{Signature: "b", Value: json.RawMessage("true")}
	}
	b, err := json.Marshal(out)
	return string(b), err
}

// storedHint is zvariant's serde form of a value, which the Rust store
// writes: {"signature": "y", "value": 2}.
type storedHint struct {
	Signature string          `json:"signature"`
	Value     json.RawMessage `json:"value"`
}

// decodeStoredHints reads the hints column back into variants,
// dropping what it cannot read (the Rust store warns and keeps none of
// a broken map, one broken value).
func decodeStoredHints(id uint32, text string) map[string]dbus.Variant {
	out := map[string]dbus.Variant{}
	var stored map[string]storedHint
	if err := json.Unmarshal([]byte(text), &stored); err != nil {
		log.Printf("notifications: stored hints of %d: %v", id, err)
		return out
	}
	for key, h := range stored {
		if v, ok := basicVariant(h); ok {
			out[key] = v
		}
	}
	return out
}

// basicVariant rebuilds a basic-type variant from its stored form.
func basicVariant(h storedHint) (dbus.Variant, bool) {
	dec := json.NewDecoder(bytes.NewReader(h.Value))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return dbus.Variant{}, false
	}
	if s, ok := v.(string); ok {
		switch h.Signature {
		case "s":
			return dbus.MakeVariant(s), true
		case "o":
			return dbus.MakeVariant(dbus.ObjectPath(s)), true
		case "g":
			sig, err := dbus.ParseSignature(s)
			return dbus.MakeVariant(sig), err == nil
		}
		return dbus.Variant{}, false
	}
	if b, ok := v.(bool); ok {
		return dbus.MakeVariant(b), h.Signature == "b"
	}
	num, ok := v.(json.Number)
	if !ok {
		return dbus.Variant{}, false
	}
	if h.Signature == "d" {
		f, err := num.Float64()
		return dbus.MakeVariant(f), err == nil
	}
	i, err := num.Int64()
	if err != nil {
		if h.Signature == "t" {
			var u uint64
			_, err := fmt.Sscan(num.String(), &u)
			return dbus.MakeVariant(u), err == nil
		}
		return dbus.Variant{}, false
	}
	switch h.Signature {
	case "y":
		return intVariant[byte](i)
	case "n":
		return intVariant[int16](i)
	case "q":
		return intVariant[uint16](i)
	case "i":
		return intVariant[int32](i)
	case "u":
		return intVariant[uint32](i)
	case "x":
		return dbus.MakeVariant(i), true
	case "t":
		return intVariant[uint64](i)
	}
	return dbus.Variant{}, false
}

func intVariant[T intconv.Integer](i int64) (dbus.Variant, bool) {
	v, err := intconv.Exact[T](i)
	return dbus.MakeVariant(v), err == nil
}

func storedInt[T intconv.Integer](id uint32, column string, v sql.NullInt64, missing T) T {
	if !v.Valid {
		return missing
	}
	t, err := intconv.Exact[T](v.Int64)
	if err != nil {
		log.Printf("notifications: stored %s of %d: %v", column, id, err)
		return missing
	}
	return t
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func nullID(id uint32) sql.NullInt64 { return sql.NullInt64{Int64: int64(id), Valid: id != 0} }

// nullTimeout is the Rust Option<u32>: a negative (server default)
// timeout is none.
func nullTimeout(ms int32) sql.NullInt64 { return sql.NullInt64{Int64: int64(ms), Valid: ms >= 0} }
