package notifications

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/fakeclock"
)

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lys-shell", "notifications.db")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, path
}

// newStoredService is a test service on its fake clock, persisting in
// st: no expiry fires unless the test advances the clock.
func newStoredService(t *testing.T, st *Store) *Service {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	svc := newTestService(t)
	if err := svc.AttachStore(st); err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestStoreRoundTrip(t *testing.T) {
	st, _ := openTestStore(t)
	svc := newStoredService(t, st)
	sent := testClock(svc).Now()
	id := svc.NotifyHints("mail", 0, "mail-icon", "Subject", "Body", []string{"default", "Open", "reply", "Reply"},
		map[string]dbus.Variant{
			"urgency":        dbus.MakeVariant(byte(2)),
			"desktop-entry":  dbus.MakeVariant("org.mail"),
			"sender-pid":     dbus.MakeVariant(int64(4242)),
			"suppress-sound": dbus.MakeVariant(false),
			"resident":       dbus.MakeVariant(true),
			"image-path":     dbus.MakeVariant("/tmp/pic.png"),
			"image-data":     dbus.MakeVariant([]any{int32(1), int32(1), int32(4), true, int32(8), int32(4), []byte{1, 2, 3, 4}}),
			"x-list":         dbus.MakeVariant([]string{"a", "b"}),
		}, 5000)

	got, err := st.Page(nil, MaxPage)
	if err != nil || len(got) != 1 {
		t.Fatalf("load = %v, %v", got, err)
	}
	n := got[0]
	if n.ID != id || n.AppName != "mail" || n.AppIcon != "mail-icon" || n.Summary != "Subject" || n.Body != "Body" || n.ExpireMS != 5000 {
		t.Errorf("fields = %+v", n)
	}
	if len(n.Actions) != 2 || n.Actions[1] != (Action{ID: "reply", Label: "Reply"}) {
		t.Errorf("actions = %v", n.Actions)
	}
	// The inline pixels win over image-path, as cached by Notify; the
	// image_path column keeps that file.
	want := history(t, svc)[0].ImagePath
	if n.Urgency != UrgencyCritical || n.DesktopEntry != "org.mail" || n.ImagePath != want || want == "" || want == "/tmp/pic.png" || !n.Resident {
		t.Errorf("decoded hints: urgency %v entry %q image %q resident %v", n.Urgency, n.DesktopEntry, n.ImagePath, n.Resident)
	}
	// The hints column holds the typed hints only, in the Rust store's
	// form: no inline pixels, no hint nothing reads.
	var column string
	if err := st.db.QueryRow(`SELECT hints FROM notifications WHERE id = ?`, id).Scan(&column); err != nil {
		t.Fatal(err)
	}
	if want := `{"desktop-entry":{"signature":"s","value":"org.mail"},"resident":{"signature":"b","value":true},"urgency":{"signature":"y","value":2}}`; column != want {
		t.Errorf("hints column = %s, want %s", column, want)
	}
	if !n.Added.Equal(sent) || !n.Expires.Equal(sent.Add(5*time.Second)) {
		t.Errorf("timestamps: added %v expires %v, want %v and 5s on", n.Added, n.Expires, sent)
	}

	// The row stays until its timeout runs out, then leaves the disk.
	advance(svc, 5*time.Second-time.Millisecond)
	if got, err := st.Page(nil, MaxPage); err != nil || len(got) != 1 {
		t.Fatalf("store before the expiry = %v, %v", got, err)
	}
	advance(svc, time.Millisecond)
	if got, err := st.Page(nil, MaxPage); err != nil || len(got) != 0 {
		t.Errorf("store after the expiry = %v, %v", got, err)
	}
}

// TestStoreFollowsAnExpiryRacingTheArrival pins the store write order:
// an expiry that fires while the arrival waits to be written removes
// the row after it is added, so the disk ends as the history does
// rather than keeping a row the history dropped. The connection held
// by the test stalls every store write until both are queued.
func TestStoreFollowsAnExpiryRacingTheArrival(t *testing.T) {
	st, _ := openTestStore(t)
	svc := newStoredService(t, st)
	for range 20 {
		conn, err := st.db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Go(func() { svc.Notify("mail", 0, "", "soon", "", nil, 1) })
		for svc.Count() == 0 {
			runtime.Gosched()
		}
		wg.Go(func() { advance(svc, time.Millisecond) })
		for svc.Count() != 0 {
			runtime.Gosched()
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		if got, err := st.Page(nil, MaxPage); err != nil || len(got) != 0 {
			t.Fatalf("store after the history expired = %v, %v", got, err)
		}
	}
}

// TestStoreReadsTheRustRows pins compatibility with the file the Rust
// shell wrote: its zvariant hint JSON, NULL columns, and the image_path
// column restoring the image-path hint.
func TestStoreReadsTheRustRows(t *testing.T) {
	st, _ := openTestStore(t)
	_, err := st.db.Exec(`INSERT INTO notifications
		(id, app_name, replaces_id, app_icon, summary, body, actions, hints, expire_timeout, timestamp, image_path)
		VALUES (572, 'Gmail', NULL, NULL, 'New mail', NULL, '["default","Open"]',
		'{"urgency":{"signature":"y","value":2},"desktop-entry":{"signature":"s","value":"gmail"},"sender-pid":{"signature":"x","value":1234},"suppress-sound":{"signature":"b","value":false}}',
		NULL, 1790883830548, '/icons/gmail.svg')`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.Page(nil, MaxPage)
	if err != nil || len(got) != 1 {
		t.Fatalf("load = %v, %v", got, err)
	}
	n := got[0]
	if n.ID != 572 || n.AppName != "Gmail" || n.Body != "" || n.AppIcon != "" || n.ExpireMS != -1 || n.ReplacesID != 0 {
		t.Errorf("fields = %+v", n)
	}
	if n.Urgency != UrgencyCritical || n.DesktopEntry != "gmail" || n.ImagePath != "/icons/gmail.svg" {
		t.Errorf("hints: urgency %v entry %q image %q", n.Urgency, n.DesktopEntry, n.ImagePath)
	}
	if _, ok := n.DefaultAction(); !ok {
		t.Error("the stored default action was lost")
	}
	if n.Added.UnixMilli() != 1790883830548 {
		t.Errorf("timestamp = %d", n.Added.UnixMilli())
	}

	// A broken hints column keeps the row, without hints.
	if _, err := st.db.Exec(`UPDATE notifications SET hints = 'not json' WHERE id = 572`); err != nil {
		t.Fatal(err)
	}
	if got, err := st.Page(nil, MaxPage); err != nil || len(got) != 1 || got[0].Urgency != UrgencyNormal {
		t.Errorf("broken hints: %v, %v", got, err)
	}
}

// stored is every row on disk, newest first.
func stored(t *testing.T, st *Store) []*Notification {
	t.Helper()
	var out []*Notification
	var before *Key
	for {
		page, err := st.Page(before, MaxPage)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, page...)
		if len(page) < MaxPage {
			return out
		}
		k := page[len(page)-1].Key()
		before = &k
	}
}

func storedIDs(t *testing.T, st *Store) map[uint32]bool {
	t.Helper()
	ids := map[uint32]bool{}
	for _, n := range stored(t, st) {
		ids[n.ID] = true
	}
	return ids
}

func TestStoreDropsWhatExpiredWhileDown(t *testing.T) {
	for _, removeExpired := range []bool{true, false} {
		st, _ := openTestStore(t)
		old := time.Now().Add(-time.Hour)
		for _, n := range []*Notification{
			{ID: 1, Summary: "timed out", ExpireMS: 1000, Added: old},
			{ID: 2, Summary: "still due", ExpireMS: int32((2 * time.Hour).Milliseconds()), Added: old},
			{ID: 3, Summary: "never expires", ExpireMS: 0, Added: old},
			{ID: 4, Summary: "server default", ExpireMS: -1, Added: old},
		} {
			if err := st.Add(n, 0); err != nil {
				t.Fatal(err)
			}
		}
		svc := NewService()
		svc.SetRemoveExpired(removeExpired)
		if err := svc.AttachStore(st); err != nil {
			t.Fatal(err)
		}
		ids := storedIDs(t, st)
		if removeExpired && (ids[1] || !ids[2] || !ids[3] || !ids[4] || svc.Count() != 3) {
			t.Errorf("remove-expired kept %v (count %d), want 2, 3 and 4", ids, svc.Count())
		}
		if !removeExpired && (len(ids) != 4 || svc.Count() != 4) {
			t.Errorf("remove-expired off kept %v (count %d), want all 4", ids, svc.Count())
		}
	}
}

// seedRows writes n rows straight into the table, one transaction, the
// i-th (1-based) arriving at base + i/ties seconds, so every ties rows
// share a timestamp.
func seedRows(t *testing.T, db *sql.DB, n, ties int, base time.Time) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for i := 1; i <= n; i++ {
		if _, err := tx.Exec(`INSERT INTO notifications (id, app_name, summary, actions, hints, timestamp)
			VALUES (?, 'app', ?, '[]', '{}', ?)`, i, "n"+strconv.Itoa(i), base.Add(time.Duration(i/ties)*time.Second).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// pageAll reads the whole history through svc.Page, size at a time.
func pageAll(t *testing.T, svc *Service, size int) []*Notification {
	t.Helper()
	var out []*Notification
	var before *Key
	for range 1000 {
		page, err := svc.Page(before, size)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, page...)
		if len(page) < size {
			return out
		}
		k := page[len(page)-1].Key()
		before = &k
	}
	t.Fatal("paging never ended")
	return nil
}

// checkNewestFirst holds pages to the history: every id exactly once,
// strictly newest first by (timestamp, id).
func checkNewestFirst(t *testing.T, got []*Notification, want int) {
	t.Helper()
	seen := map[uint32]bool{}
	for i, n := range got {
		if seen[n.ID] {
			t.Fatalf("id %d paged twice", n.ID)
		}
		seen[n.ID] = true
		if i > 0 && got[i-1].Key().Compare(n.Key()) <= 0 {
			t.Fatalf("page order broke at %d: %v then %v", i, got[i-1].Key(), n.Key())
		}
	}
	if len(got) != want {
		t.Fatalf("paged %d notifications, want %d", len(got), want)
	}
}

func TestMemoryIsBoundedAndOlderEntriesPageIn(t *testing.T) {
	st, path := openTestStore(t)
	svc := newStoredService(t, st)
	memory, _ := NewMemoryLimit(MinHistoryMemory)
	svc.SetHistoryLimits(memory, DefaultStoredLimit())
	const sent = 37
	for i := range sent {
		svc.Notify("app"+strconv.Itoa(i%3), 0, "", "n"+strconv.Itoa(i), "", nil, 0)
		// Three arrivals share each millisecond: the id breaks the tie.
		if i%3 == 2 {
			advance(svc, time.Millisecond)
		}
	}
	svc.mu.Lock()
	inMemory := len(svc.recent)
	svc.mu.Unlock()
	if inMemory != MinHistoryMemory || svc.Count() != sent {
		t.Fatalf("in memory %d of count %d, want %d of %d", inMemory, svc.Count(), MinHistoryMemory, sent)
	}
	for _, size := range []int{1, 4, MinHistoryMemory, 11, MaxPage} {
		got := pageAll(t, svc, size)
		checkNewestFirst(t, got, sent)
		if got[0].ID != sent || got[sent-1].ID != 1 {
			t.Errorf("page size %d: newest %d oldest %d, want %d and 1", size, got[0].ID, got[sent-1].ID, sent)
		}
	}

	// After a restart the window refills from the newest rows.
	_ = st.Close()
	st2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	svc2 := newTestService(t)
	svc2.SetHistoryLimits(memory, DefaultStoredLimit())
	if err := svc2.AttachStore(st2); err != nil {
		t.Fatal(err)
	}
	svc2.mu.Lock()
	inMemory = len(svc2.recent)
	svc2.mu.Unlock()
	if inMemory != MinHistoryMemory || svc2.Count() != sent {
		t.Fatalf("restored %d in memory of count %d", inMemory, svc2.Count())
	}
	checkNewestFirst(t, pageAll(t, svc2, 4), sent)
}

func TestMemoryOnlyHistoryIsBounded(t *testing.T) {
	svc := newTestService(t)
	memory, _ := NewMemoryLimit(MinHistoryMemory)
	svc.SetHistoryLimits(memory, DefaultStoredLimit())
	for i := range 3 * MinHistoryMemory {
		svc.Notify("app", 0, "", "n"+strconv.Itoa(i), "", nil, 0)
	}
	if svc.Count() != MinHistoryMemory {
		t.Errorf("count = %d, want the memory limit %d", svc.Count(), MinHistoryMemory)
	}
	got := pageAll(t, svc, 3)
	checkNewestFirst(t, got, MinHistoryMemory)
	if got[0].ID != 3*MinHistoryMemory {
		t.Errorf("newest = %d, want the last sent", got[0].ID)
	}
}

func TestPagesStoredEntriesWithTiesAcrossTheBoundary(t *testing.T) {
	st, _ := openTestStore(t)
	const rows = 1200
	seedRows(t, st.db, rows, 7, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	svc := newStoredService(t, st)
	if svc.Count() != rows {
		t.Fatalf("count = %d", svc.Count())
	}
	for _, size := range []int{7, 13, MaxPage} {
		checkNewestFirst(t, pageAll(t, svc, size), rows)
	}
}

func TestPageRefusesABadSize(t *testing.T) {
	svc := newTestService(t)
	for _, size := range []int{0, -1, MaxPage + 1} {
		if _, err := svc.Page(nil, size); err == nil {
			t.Errorf("page of %d was served", size)
		}
	}
	if _, err := svc.Page(nil, MaxPage); err != nil {
		t.Errorf("a full page was refused: %v", err)
	}
}

// TestHistoryLimitsAreValidated pins the ranges the config refuses
// outside of.
func TestHistoryLimitsAreValidated(t *testing.T) {
	for _, n := range []int64{MinHistoryMemory, DefaultHistoryMemory, MaxHistoryMemory} {
		if l, err := NewMemoryLimit(n); err != nil || int64(l.Count()) != n {
			t.Errorf("memory %d: %v", n, err)
		}
	}
	for _, n := range []int64{MinHistoryMemory - 1, 0, MaxHistoryMemory + 1} {
		if _, err := NewMemoryLimit(n); !errors.Is(err, ErrHistoryLimit) {
			t.Errorf("memory %d: err = %v", n, err)
		}
	}
	for _, n := range []int64{MinHistoryStored, MaxHistoryStored} {
		if l, err := NewStoredLimit(n); err != nil || int64(l.Count()) != n {
			t.Errorf("stored %d: %v", n, err)
		}
	}
	for _, n := range []int64{MinHistoryStored - 1, MaxHistoryStored + 1} {
		if _, err := NewStoredLimit(n); !errors.Is(err, ErrHistoryLimit) {
			t.Errorf("stored %d: err = %v", n, err)
		}
	}
}

// TestHotQueriesUseTheirIndexes locks the plans in: the page reads
// seek the (timestamp, id) index with no sort, retention walks it from
// the oldest end, and the expiry reads use the partial expression
// index. A regression to a table scan or a temp sort fails here.
func TestHotQueriesUseTheirIndexes(t *testing.T) {
	st, _ := openTestStore(t)
	seedRows(t, st.db, 2000, 1, time.Now())
	if _, err := st.db.Exec(`ANALYZE`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, query string
		args        []any
		want        []string
	}{
		{"newest page", newestSQL, []any{50}, []string{"SCAN notifications USING INDEX notifications_newest"}},
		{"older page", olderSQL, []any{int64(1), uint32(1), 50}, []string{"SEARCH notifications USING INDEX notifications_newest (timestamp<?)"}},
		{"prune", pruneSQL, []any{500}, []string{"SEARCH notifications USING INTEGER PRIMARY KEY (rowid=?)", "SCAN notifications USING COVERING INDEX notifications_newest"}},
		{"sweep", sweepSQL, []any{int64(1), 500}, []string{"SEARCH notifications USING INTEGER PRIMARY KEY (rowid=?)", "SEARCH notifications USING INDEX notifications_expiry (<expr><?)"}},
		{"expiries", expiriesSQL, []any{int64(1)}, []string{"SEARCH notifications USING INDEX notifications_expiry (<expr>>?)"}},
		{"find", findSQL, []any{1}, []string{"SEARCH notifications USING INTEGER PRIMARY KEY (rowid=?)"}},
		{"remove", removeSQL, []any{1}, []string{"SEARCH notifications USING INTEGER PRIMARY KEY (rowid=?)"}},
	} {
		plan := queryPlan(t, st.db, tc.query, tc.args...)
		t.Logf("%s:\n%s", tc.name, plan)
		for _, want := range tc.want {
			if !strings.Contains(plan, want) {
				t.Errorf("%s plan lacks %q:\n%s", tc.name, want, plan)
			}
		}
		if strings.Contains(plan, "TEMP B-TREE") {
			t.Errorf("%s plan sorts:\n%s", tc.name, plan)
		}
		for line := range strings.SplitSeq(plan, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "SCAN notifications") && !strings.Contains(line, "INDEX") {
				t.Errorf("%s plan scans the table:\n%s", tc.name, plan)
			}
		}
	}
}

// queryPlan is EXPLAIN QUERY PLAN's detail column, one step a line.
func queryPlan(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.Query(`EXPLAIN QUERY PLAN `+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var lines []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

// oldSchema is the table as the Rust store and every lys-shell before
// the history was bounded created it: no index but the primary key.
const oldSchema = `CREATE TABLE notifications (
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
)`

func TestOpenMigratesTheOldSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notifications.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(oldSchema); err != nil {
		t.Fatal(err)
	}
	seedRows(t, db, 1300, 3, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if _, err := db.Exec(`UPDATE notifications SET expire_timeout = 60000 WHERE id % 100 = 0`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	for range 2 {
		st, err := OpenStore(path)
		if err != nil {
			t.Fatalf("opening the old schema: %v", err)
		}
		var names []string
		rows, err := st.db.Query(`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'notifications' ORDER BY name`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			names = append(names, name)
		}
		_ = rows.Close()
		if !slices.Equal(names, []string{"notifications_expiry", "notifications_newest"}) {
			t.Errorf("indexes = %v", names)
		}
		if plan := queryPlan(t, st.db, olderSQL, int64(1), uint32(1), 50); !strings.Contains(plan, "notifications_newest") {
			t.Errorf("the migrated file does not page by the index:\n%s", plan)
		}
		got := stored(t, st)
		checkNewestFirst(t, got, 1300)
		if got[0].ID != 1300 || got[0].Summary != "n1300" {
			t.Errorf("newest migrated row = %+v", got[0])
		}
		_ = st.Close()
	}
}

func TestDeletesRunInBatches(t *testing.T) {
	var limits []int
	fake := func(rows ...int) func(int) (sql.Result, error) {
		return func(limit int) (sql.Result, error) {
			limits = append(limits, limit)
			n := min(limit, rows[0])
			rows[0] -= n
			return fakeResult(n), nil
		}
	}
	st := &Store{}
	if n, err := st.deleteBatched("prune", 1234, fake(5000)); err != nil || n != 1234 || !slices.Equal(limits, []int{500, 500, 234}) {
		t.Errorf("prune 1234: deleted %d (%v) in %v, want 1234 in 500, 500, 234", n, err, limits)
	}
	limits = nil
	if n, err := st.deleteBatched("sweep", -1, fake(1100)); err != nil || n != 1100 || !slices.Equal(limits, []int{500, 500, 500}) {
		t.Errorf("sweep: deleted %d (%v) in %v, want 1100, stopping at the short batch", n, err, limits)
	}
	limits = nil
	if n, _ := st.deleteBatched("prune", 0, fake(10)); n != 0 || len(limits) != 0 {
		t.Errorf("pruning nothing ran %v", limits)
	}
}

type fakeResult int64

func (r fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r fakeResult) RowsAffected() (int64, error) { return int64(r), nil }

func TestRetentionDropsTheOldest(t *testing.T) {
	st, _ := openTestStore(t)
	const rows = MinHistoryStored + 1234
	seedRows(t, st.db, rows, 2, fakeclock.New().Now().Add(-time.Hour))
	svc := newStoredService(t, st)
	if svc.Count() != DefaultHistoryStored && svc.Count() != rows {
		t.Fatalf("count = %d", svc.Count())
	}
	least, _ := NewStoredLimit(MinHistoryStored)
	svc.SetHistoryLimits(DefaultMemoryLimit(), least)
	got := stored(t, st)
	checkNewestFirst(t, got, MinHistoryStored)
	if got[len(got)-1].ID != 1235 || svc.Count() != MinHistoryStored {
		t.Errorf("oldest kept %d, count %d; want 1235 and %d", got[len(got)-1].ID, svc.Count(), MinHistoryStored)
	}
	// Each arrival past the limit drops the oldest in its own write.
	id := svc.Notify("app", 0, "", "new", "", nil, 0)
	got = stored(t, st)
	if len(got) != MinHistoryStored || got[0].ID != id || got[len(got)-1].ID != 1236 || svc.Count() != MinHistoryStored {
		t.Errorf("after an arrival: %d rows, newest %d oldest %d, count %d", len(got), got[0].ID, got[len(got)-1].ID, svc.Count())
	}
}

func TestStoredOnlyEntriesCloseReplaceAndInvoke(t *testing.T) {
	st, _ := openTestStore(t)
	svc := newStoredService(t, st)
	memory, _ := NewMemoryLimit(MinHistoryMemory)
	svc.SetHistoryLimits(memory, DefaultStoredLimit())
	var closed []uint32
	var invoked []uint32
	svc.SetEmitter(func(signal string, args ...any) {
		switch signal {
		case Interface + ".NotificationClosed":
			closed = append(closed, args[0].(uint32))
		case Interface + ".ActionInvoked":
			invoked = append(invoked, args[0].(uint32))
		}
	})
	old := svc.Notify("mail", 0, "", "old", "", []string{"default", "Open"}, 0)
	gone := svc.Notify("mail", 0, "", "to close", "", nil, 0)
	for i := range MinHistoryMemory {
		advance(svc, time.Millisecond)
		svc.Notify("chat", 0, "", "n"+strconv.Itoa(i), "", nil, 0)
	}
	total := svc.Count()

	// The owner replaces an entry only on disk: same id, same count.
	if id := svc.Notify("mail", old, "", "old again", "", []string{"default", "Open"}, 0); id != old || svc.Count() != total {
		t.Errorf("replacing a stored entry: id %d (want %d), count %d (want %d)", id, old, svc.Count(), total)
	}
	// Another app cannot.
	if id := svc.Notify("chat", gone, "", "hijack", "", nil, 0); id == gone {
		t.Error("another app replaced a stored entry it does not own")
	}
	total = svc.Count()

	svc.Close(gone, Dismissed)
	if svc.Count() != total-1 || !slices.Equal(closed, []uint32{gone}) || storedIDs(t, st)[gone] {
		t.Errorf("closing a stored entry: count %d, closed %v", svc.Count(), closed)
	}
	// Closing it again changes nothing.
	svc.Close(gone, Dismissed)
	if svc.Count() != total-1 || len(closed) != 1 {
		t.Errorf("a second close: count %d, closed %v", svc.Count(), closed)
	}

	for i := range MinHistoryMemory {
		advance(svc, time.Millisecond)
		svc.Notify("chat", 0, "", "m"+strconv.Itoa(i), "", nil, 0)
	}
	svc.InvokeAction(old, DefaultActionID, "")
	if !slices.Equal(invoked, []uint32{old}) || storedIDs(t, st)[old] {
		t.Errorf("invoking a stored entry: invoked %v", invoked)
	}
}

func TestHintsAreParsedNotKept(t *testing.T) {
	typ := reflect.TypeFor[Notification]()
	for f := range typ.Fields() {
		switch f.Type.Kind() {
		case reflect.Map, reflect.Interface:
			t.Errorf("Notification.%s is a %s: raw hints would be kept", f.Name, f.Type)
		}
	}
	svc := newTestService(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	id := svc.NotifyHints("mail", 0, "", "s", "", []string{"default", "Open", "reply", "Reply"}, map[string]dbus.Variant{
		"urgency":       dbus.MakeVariant(byte(0)),
		"desktop-entry": dbus.MakeVariant("org.mail"),
		"resident":      dbus.MakeVariant(true),
		"image-path":    dbus.MakeVariant("/tmp/pic.png"),
		"category":      dbus.MakeVariant("email.arrived"),
	}, 0)
	n := history(t, svc)[0]
	if n.ID != id || n.Urgency != UrgencyLow || n.DesktopEntry != "org.mail" || !n.Resident || n.Transient || n.ImagePath != "/tmp/pic.png" {
		t.Errorf("typed fields = %+v", n)
	}
	if a, ok := n.DefaultAction(); !ok || a.Label != "Open" || len(n.Actions) != 2 {
		t.Errorf("actions = %v", n.Actions)
	}
}

func TestServiceKeepsTheStoreInStep(t *testing.T) {
	st, path := openTestStore(t)
	svc := newStoredService(t, st)
	a := svc.Notify("mail", 0, "", "A", "", nil, 0)
	b := svc.Notify("chat", 0, "", "B", "", nil, 0)
	svc.NotifyHints("tmp", 0, "", "Transient", "", nil, map[string]dbus.Variant{"transient": dbus.MakeVariant(true)}, 0)
	svc.Close(a, Dismissed)
	got, _ := st.Page(nil, MaxPage)
	if len(got) != 1 || got[0].ID != b {
		t.Fatalf("store = %v, want only B (A closed, the transient never stored)", got)
	}

	// A transient replacement takes the stored one off the disk too.
	svc.NotifyHints("chat", b, "", "B again", "", nil, map[string]dbus.Variant{"transient": dbus.MakeVariant(true)}, 0)
	if got, _ := st.Page(nil, MaxPage); len(got) != 0 {
		t.Errorf("store after a transient replacement = %v", got)
	}

	// A restart: ids continue past the stored ones and their apps own
	// them again.
	c := svc.Notify("mail", 0, "", "C", "", nil, 0)
	_ = st.Close()
	st2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	svc2 := newStoredService(t, st2)
	if svc2.Count() != 1 || history(t, svc2)[0].ID != c {
		t.Fatalf("restored = %v, want C", history(t, svc2))
	}
	if len(svc2.Popups()) != 0 {
		t.Error("a restored notification popped up again")
	}
	if id := svc2.Notify("mail", c, "", "C updated", "", nil, 0); id != c {
		t.Errorf("the owner's replacement got id %d, want %d", id, c)
	}
	if id := svc2.Notify("other", c, "", "hijack", "", nil, 0); id <= c {
		t.Errorf("another app's replaces_id got %d, want a fresh id past %d", id, c)
	}
	svc2.DismissAll()
	if got, _ := st2.Page(nil, MaxPage); len(got) != 0 {
		t.Errorf("store after dismiss all = %v", got)
	}
}

func closedSignals(svc *Service) *[]uint32 {
	var closed []uint32
	svc.SetEmitter(func(signal string, args ...any) {
		if signal == Interface+".NotificationClosed" {
			closed = append(closed, args[0].(uint32))
		}
	})
	return &closed
}

func popupIDs(svc *Service) map[uint32]bool {
	ids := map[uint32]bool{}
	for _, n := range svc.Popups() {
		ids[n.ID] = true
	}
	return ids
}

func TestDismissClosesTheGivenNotifications(t *testing.T) {
	st, _ := openTestStore(t)
	svc := newStoredService(t, st)
	memory, _ := NewMemoryLimit(MinHistoryMemory)
	svc.SetHistoryLimits(memory, DefaultStoredLimit())
	old := svc.Notify("mail", 0, "", "old", "", nil, 0)
	for i := range MinHistoryMemory {
		advance(svc, time.Millisecond)
		svc.Notify("chat", 0, "", "n"+strconv.Itoa(i), "", nil, 0)
	}
	a := svc.Notify("mail", 0, "", "A", "", nil, 0)
	b := svc.Notify("mail", 0, "", "B", "", nil, 0)
	keep := svc.Notify("chat", 0, "", "keep", "", nil, 0)
	closed := closedSignals(svc)
	total := svc.Count()

	svc.Dismiss([]uint32{old, a, b, 9999})
	if svc.Count() != total-3 || !slices.Equal(*closed, []uint32{old, a, b}) {
		t.Errorf("dismiss: count %d (want %d), closed %v", svc.Count(), total-3, *closed)
	}
	onDisk, popped := storedIDs(t, st), popupIDs(svc)
	for _, id := range []uint32{old, a, b} {
		if onDisk[id] || popped[id] {
			t.Errorf("dismissed %d stayed: stored %v, popped %v", id, onDisk[id], popped[id])
		}
	}
	if !onDisk[keep] || !popped[keep] || history(t, svc)[0].ID != keep {
		t.Errorf("dismiss took %d, which it was not given", keep)
	}

	svc.Dismiss([]uint32{old, a, b})
	if svc.Count() != total-3 || len(*closed) != 3 {
		t.Errorf("a second dismiss: count %d, closed %v", svc.Count(), *closed)
	}
}

func TestDismissAllEmptiesThePopupsPastTheWindow(t *testing.T) {
	st, _ := openTestStore(t)
	svc := newStoredService(t, st)
	memory, _ := NewMemoryLimit(MinHistoryMemory)
	svc.SetHistoryLimits(memory, DefaultStoredLimit())
	for i := range MinHistoryMemory + 2 {
		advance(svc, time.Millisecond)
		svc.Notify("chat", 0, "", "n"+strconv.Itoa(i), "", nil, 0)
	}
	if got := len(svc.Popups()); got != MinHistoryMemory+2 {
		t.Fatalf("popups = %d, want every sticky one up", got)
	}

	svc.DismissAll()
	if svc.Count() != 0 || len(history(t, svc)) != 0 || len(stored(t, st)) != 0 || len(svc.Popups()) != 0 {
		t.Errorf("dismiss all left count %d, history %d, store %d, popups %d", svc.Count(), len(history(t, svc)), len(stored(t, st)), len(svc.Popups()))
	}

	next := svc.Notify("chat", 0, "", "after", "", nil, 0)
	if svc.Count() != 1 || !storedIDs(t, st)[next] || !popupIDs(svc)[next] {
		t.Errorf("a notification after dismiss all: count %d", svc.Count())
	}
}

func TestClosingAPopupOutsideTheHistoryUpdatesThePopups(t *testing.T) {
	st, _ := openTestStore(t)
	svc := newStoredService(t, st)
	feed, _ := svc.Subscribe()
	id := svc.NotifyHints("tmp", 0, "", "Transient", "", nil, map[string]dbus.Variant{"transient": dbus.MakeVariant(true)}, 0)
	collect(t, feed, 1)
	closed := closedSignals(svc)

	svc.Close(id, Dismissed)
	if ev := collect(t, feed, 1)[0]; ev.Kind != EventPopups || len(svc.Popups()) != 0 || len(*closed) != 0 {
		t.Errorf("closing a transient popup: event %+v, popups %d, closed %v", ev, len(svc.Popups()), *closed)
	}

	svc.Close(id, Dismissed)
	select {
	case ev := <-feed:
		t.Errorf("closing a popup already gone emitted %+v", ev)
	default:
	}
}

func TestRestoredExpiryStillFires(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	st, _ := openTestStore(t)
	clock := fakeclock.New()
	if err := st.Add(&Notification{ID: 7, AppName: "app", Summary: "soon", ExpireMS: 1500, Added: clock.Now().Add(-100 * time.Millisecond)}, 0); err != nil {
		t.Fatal(err)
	}
	svc := NewService()
	svc.UseClock(clock)
	if err := svc.AttachStore(st); err != nil {
		t.Fatal(err)
	}
	if svc.Count() != 1 {
		t.Fatal("the pending notification was not restored")
	}
	// 100ms of its 1500 had passed before the restore.
	clock.Advance(1399 * time.Millisecond)
	if svc.Count() != 1 {
		t.Fatal("a restored notification expired early")
	}
	clock.Advance(time.Millisecond)
	if svc.Count() != 0 {
		t.Error("a restored notification never expired")
	}
	if got, _ := st.Page(nil, MaxPage); len(got) != 0 {
		t.Errorf("the expired one stayed on disk: %v", got)
	}
}

// The store lives in lys-shell's data directory, following an absolute
// XDG_DATA_HOME and ignoring a relative one, as the XDG spec says.
func TestStorePathFollowsTheDataDirectory(t *testing.T) {
	data, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", data)
	if got, err := StorePath(); err != nil || got != filepath.Join(data, "lys-shell", "notifications.db") {
		t.Errorf("StorePath = %q %v, want it under XDG_DATA_HOME", got, err)
	}
	t.Setenv("XDG_DATA_HOME", "relative/data")
	if got, err := StorePath(); err != nil || got != filepath.Join(home, ".local", "share", "lys-shell", "notifications.db") {
		t.Errorf("a relative XDG_DATA_HOME: StorePath = %q %v, want the HOME fallback", got, err)
	}
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "")
	if got, err := StorePath(); err == nil {
		t.Errorf("StorePath = %q with neither base absolute, want an error", got)
	}
}
