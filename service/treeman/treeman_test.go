package treeman

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/wayle/internal/feed"
)

func TestWorstBucket(t *testing.T) {
	for _, tc := range []struct {
		status Status
		want   Bucket
	}{
		{Status{Total: 1, Stable: 1}, BucketStable},
		{Status{Total: 2, Stable: 1, Up: 1}, BucketUp},
		{Status{Total: 3, Stable: 1, Up: 1, Down: 1}, BucketDown},
		{Status{Total: 4, Stable: 1, Up: 1, Down: 1, Failed: 1}, BucketFailed},
	} {
		if got := tc.status.WorstBucket(); got != tc.want {
			t.Errorf("%+v: worst = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestSocketPathLookup(t *testing.T) {
	t.Setenv("TREEMAN_SOCKET", "/custom/treeman.sock")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if p, ok := SocketPath(); !ok || p != "/custom/treeman.sock" {
		t.Errorf("env override = %q ok=%v", p, ok)
	}
	os.Unsetenv("TREEMAN_SOCKET")
	if p, ok := SocketPath(); !ok || p != "/run/user/1000/treeman.sock" {
		t.Errorf("runtime dir = %q ok=%v", p, ok)
	}
	os.Unsetenv("XDG_RUNTIME_DIR")
	t.Setenv("XDG_DATA_HOME", "/data")
	if p, _ := SocketPath(); p != "/data/treeman/treeman.sock" {
		t.Errorf("data home = %q", p)
	}
	os.Unsetenv("XDG_DATA_HOME")
	t.Setenv("HOME", "/home/u")
	if p, _ := SocketPath(); p != "/home/u/.local/share/treeman/treeman.sock" {
		t.Errorf("home fallback = %q", p)
	}
}

// fakeTreemanBin writes a treeman that answers only `status --format
// json`, printing the JSON lines of the file at lines in turn (one per
// run, the last repeating); a line "FAIL" exits non-zero. Any other
// arguments exit 2.
func fakeTreemanBin(t *testing.T, outputs ...string) string {
	t.Helper()
	dir := t.TempDir()
	lines := filepath.Join(dir, "outputs")
	if err := os.WriteFile(lines, []byte(strings.Join(outputs, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	count := filepath.Join(dir, "count")
	bin := filepath.Join(dir, "treeman")
	script := `#!/bin/sh
[ "$*" = "status --format json" ] || exit 2
n=$(cat "` + count + `" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "` + count + `"
total=$(wc -l < "` + lines + `")
[ $n -gt $total ] && n=$total
line=$(sed -n "${n}p" "` + lines + `")
[ "$line" = FAIL ] && exit 1
echo "$line"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// waitStatus waits for a tick that leaves Status at total.
func waitStatus(t *testing.T, s *System, ticks <-chan struct{}, total uint32) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		if st := s.Status(); st != nil && st.Total == total {
			return
		}
		select {
		case <-ticks:
		case <-deadline:
			t.Fatalf("status = %+v, want total %d", s.Status(), total)
		}
	}
}

func TestStatusComesFromTheJSONStatusCommand(t *testing.T) {
	t.Setenv("TREEMAN_SOCKET", filepath.Join(t.TempDir(), "absent.sock"))
	s := New(fakeTreemanBin(t, `{"total":3,"stable":1,"up":1,"down":0,"failed":1,"class":"failed"}`))
	defer s.Stop()
	if s.Status() != nil {
		t.Fatal("a status before the first fetch")
	}
	ticks, _, err := s.Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, s, ticks, 3)
	if got := s.Status().WorstBucket(); got != BucketFailed {
		t.Errorf("worst = %v", got)
	}
}

// Without the binary the service idles: no status, no ticks, and no
// re-spawning (service.rs's Fetch::Unavailable).
func TestAMissingTreemanIdles(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "missing-treeman"))
	if _, outcome := s.fetch(context.Background()); outcome != fetchUnavailable {
		t.Errorf("missing binary outcome = %v, want unavailable", outcome)
	}
	if !New("true").refresh(context.Background()) {
		t.Error("an installed treeman reported unavailable")
	}
	if s.refresh(context.Background()) {
		t.Error("refresh with a missing binary: want the loop to stop")
	}
	if s.Status() != nil {
		t.Errorf("status = %+v, want none", s.Status())
	}
}

// A failed or unparsable run keeps the last status and publishes
// nothing (Fetch::Empty).
func TestAFailedRunKeepsTheLastStatus(t *testing.T) {
	s := New(fakeTreemanBin(t, `{"total":2}`, "FAIL", "not json", `{"total":2}`))
	ticks, stop := s.changes.Subscribe()
	defer stop()
	for range 4 {
		if !s.refresh(context.Background()) {
			t.Fatal("refresh stopped the loop")
		}
		if st := s.Status(); st == nil || st.Total != 2 {
			t.Fatalf("status = %+v, want the first status kept", st)
		}
	}
	<-ticks
	select {
	case <-ticks:
		t.Error("an unchanged status ticked again")
	default:
	}
}

// With the daemon down the service polls the CLI every retry.
func TestPollsWithoutTheDaemon(t *testing.T) {
	t.Setenv("TREEMAN_SOCKET", filepath.Join(t.TempDir(), "absent.sock"))
	s := New(fakeTreemanBin(t, `{"total":1}`, `{"total":2}`))
	s.retry = 20 * time.Millisecond
	defer s.Stop()
	ticks, _, err := s.Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, s, ticks, 1)
	waitStatus(t, s, ticks, 2)
}

func TestDaemonEventsRefetch(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "treeman.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	event := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 256)
				if _, err := c.Read(buf); err != nil {
					return
				}
				<-event
				c.Write([]byte("{\"event\":{}}\n"))
				time.Sleep(2 * time.Second)
			}(conn)
		}
	}()

	t.Setenv("TREEMAN_SOCKET", sock)
	s := New(fakeTreemanBin(t, `{"total":1}`, `{"total":5}`))
	s.retry = time.Hour
	defer s.Stop()
	ticks, _, err := s.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	waitStatus(t, s, ticks, 1)
	close(event)
	waitStatus(t, s, ticks, 5)
}

// Every subscriber sees every change (one bar per output), and Stop
// returns and closes them all.
func TestEverySubscriberTicksAndStopClosesThem(t *testing.T) {
	t.Setenv("TREEMAN_SOCKET", filepath.Join(t.TempDir(), "absent.sock"))
	s := New("true")
	a, _, err := s.Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	feed.Notify(&s.changes)
	for name, ch := range map[string]<-chan struct{}{"first": a, "second": b} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("the %s subscriber missed the tick", name)
		}
	}
	done := make(chan struct{})
	go func() { s.Stop(); s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop deadlocked")
	}
	for _, ch := range []<-chan struct{}{a, b} {
		for range ch { // drains the pending tick, ends on close
		}
	}
}

func TestActionArgs(t *testing.T) {
	for a, want := range map[Action]string{
		ActionPrepare:  "prepare --worktree /w/x",
		ActionReset:    "db reset /w/x",
		ActionTeardown: "worktree delete /w/x --yes",
	} {
		if got := strings.Join(a.Args("/w/x"), " "); got != want {
			t.Errorf("%v args = %q, want %q", a, got, want)
		}
	}
}

func TestRunActionReportsStderr(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	bin := filepath.Join(dir, "treeman")
	script := "#!/bin/sh\necho \"$@\" > " + log + "\nif [ \"$1\" = db ]; then echo 'no such worktree' >&2; exit 3; fi\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s := New(bin)
	if err := s.RunAction(context.Background(), ActionPrepare, "/w/x"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(log); strings.TrimSpace(string(got)) != "prepare --worktree /w/x" {
		t.Errorf("args = %q", got)
	}
	err := s.RunAction(context.Background(), ActionReset, "/w/x")
	if err == nil || err.Error() != "no such worktree" {
		t.Errorf("err = %v, want the stderr", err)
	}
	if err := New(filepath.Join(dir, "missing")).RunAction(context.Background(), ActionPrepare, "/w"); err == nil {
		t.Error("a missing binary: want an error")
	}
}

func TestParseBucketAndFindWorktree(t *testing.T) {
	for s, want := range map[string]Bucket{"up": BucketUp, "down": BucketDown, "failed": BucketFailed, "stable": BucketStable, "?": BucketStable} {
		if got := ParseBucket(s); got != want {
			t.Errorf("ParseBucket(%q) = %v", s, got)
		}
	}
	st := Status{Repos: []Repo{{Repo: "a", Worktrees: []Worktree{{Path: "/a/1"}}}, {Repo: "b", Worktrees: []Worktree{{Path: "/b/1", Branch: "x"}}}}}
	if r, wt, ok := st.FindWorktree("/b/1"); !ok || r.Repo != "b" || wt.Branch != "x" {
		t.Errorf("find = %v %v %v", r, wt, ok)
	}
	if _, _, ok := st.FindWorktree("/gone"); ok {
		t.Error("found a missing worktree")
	}
}
