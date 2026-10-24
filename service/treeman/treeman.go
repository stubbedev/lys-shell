// Package treeman is the treeman daemon client: worktree health as a
// status snapshot from the `treeman status` CLI, refreshed whenever
// the daemon's event socket signals activity.
package treeman

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/stubbedev/lys-shell/internal/feed"
)

// Bucket is the coarse worktree state, mirroring treeman's bucket
// strings.
type Bucket int

// Buckets, most severe last for the comparison in WorstBucket.
const (
	BucketStable Bucket = iota
	BucketUp
	BucketDown
	BucketFailed
)

// Worktree is one active worktree (model.rs's TreemanWorktree).
type Worktree struct {
	// Branch is the branch name ("-" when detached or unknown).
	Branch string `json:"branch"`
	Slug   string `json:"slug"`
	// State is the fine lifecycle state (ready, preparing, error, …);
	// Bucket the coarse one it maps to.
	State  string `json:"state"`
	Bucket string `json:"bucket"`
	IsMain bool   `json:"is_main"`
	// Path is the absolute worktree path.
	Path string `json:"path"`
}

// Repo is one registered repo and its active worktrees.
type Repo struct {
	Repo      string     `json:"repo"`
	Total     uint32     `json:"total"`
	Worktrees []Worktree `json:"worktrees"`
}

// ParseBucket is Bucket::parse: up, down, failed, anything else stable.
func ParseBucket(s string) Bucket {
	switch s {
	case "up":
		return BucketUp
	case "down":
		return BucketDown
	case "failed":
		return BucketFailed
	}
	return BucketStable
}

// FindWorktree is find_worktree: the worktree at path and its repo.
func (s Status) FindWorktree(path string) (Repo, Worktree, bool) {
	for _, r := range s.Repos {
		for _, wt := range r.Worktrees {
			if wt.Path == path {
				return r, wt, true
			}
		}
	}
	return Repo{}, Worktree{}, false
}

// Action is a worktree mutation (service.rs's Action).
type Action int

// Actions.
const (
	// ActionPrepare re-runs the prepare pipeline.
	ActionPrepare Action = iota
	// ActionReset drops and re-seeds the branch-scoped databases.
	ActionReset
	// ActionTeardown removes the worktree entirely.
	ActionTeardown
)

// Args are the treeman CLI arguments for the action on a worktree.
func (a Action) Args(path string) []string {
	switch a {
	case ActionReset:
		return []string{"db", "reset", path}
	case ActionTeardown:
		return []string{"worktree", "delete", path, "--yes"}
	}
	return []string{"prepare", "--worktree", path}
}

// Status is the aggregated worktree health across every registered
// repo (treeman status's JSON).
type Status struct {
	Total  uint32 `json:"total"`
	Stable uint32 `json:"stable"`
	Up     uint32 `json:"up"`
	Down   uint32 `json:"down"`
	Failed uint32 `json:"failed"`
	Class  string `json:"class"`
	Repos  []Repo `json:"repos"`
}

// WorstBucket is model.rs's worst_bucket: failed > down > up > stable.
func (s Status) WorstBucket() Bucket {
	switch {
	case s.Failed > 0:
		return BucketFailed
	case s.Down > 0:
		return BucketDown
	case s.Up > 0:
		return BucketUp
	}
	return BucketStable
}

// SocketPath mirrors treeman's own lookup order: $TREEMAN_SOCKET →
// $XDG_RUNTIME_DIR/treeman.sock → $XDG_DATA_HOME/treeman/treeman.sock
// → ~/.local/share/treeman/treeman.sock.
func SocketPath() (string, bool) {
	if p := os.Getenv("TREEMAN_SOCKET"); p != "" {
		return p, true
	}
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		return filepath.Join(rt, "treeman.sock"), true
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", false
		}
		dataHome = filepath.Join(home, ".local/share")
	}
	return filepath.Join(dataHome, "treeman", "treeman.sock"), true
}

const subscribeRequest = `{"method":"event_subscribe","event_subscribe":{}}` + "\n"

// Source is the module's seam.
type Source interface {
	// Status is the latest snapshot (the Rust service's status
	// property): nil until treeman reported one, and for good when
	// treeman is not installed.
	Status() *Status
	// Subscribe ticks when Status changes. The channel closes when the
	// source stops; stop unsubscribes.
	Subscribe(ctx context.Context) (<-chan struct{}, func(), error)
	// RunAction queues an action with the treeman daemon; the status
	// subscription reports its progress. A failure carries the
	// command's stderr.
	RunAction(ctx context.Context, action Action, path string) error
}

// The service's timing (service.rs): the pause before reconnecting a
// dropped or absent daemon (and so the poll period while it is down),
// and the window an event burst collapses into one fetch.
const (
	reconnectDelay = 5 * time.Second
	debounce       = 200 * time.Millisecond
)

// System is the real client, built by New.
type System struct {
	binary string
	// retry is reconnectDelay (shorter under test).
	retry time.Duration

	mu     sync.Mutex
	status *Status

	changes feed.Tick
	start   sync.Once
	halt    sync.Once
	stopped chan struct{}
}

// New builds the client for the given treeman binary.
func New(binary string) *System {
	if binary == "" {
		binary = "treeman"
	}
	return &System{binary: binary, retry: reconnectDelay, stopped: make(chan struct{})}
}

// Status implements Source.
func (s *System) Status() *Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// fetchOutcome classifies one `treeman status` run (service.rs's
// Fetch).
type fetchOutcome int

const (
	// fetchData parsed a status.
	fetchData fetchOutcome = iota
	// fetchEmpty ran without usable data (non-zero exit, bad JSON):
	// the last status stands.
	fetchEmpty
	// fetchUnavailable could not find the binary: treeman is not
	// installed.
	fetchUnavailable
)

// fetch runs `treeman status --format json` and classifies the run.
func (s *System) fetch(ctx context.Context) (*Status, fetchOutcome) {
	out, err := exec.CommandContext(ctx, s.binary, "status", "--format", "json").Output() //nolint:gosec // the binary name is the client's own
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return nil, fetchUnavailable
	}
	if err != nil {
		return nil, fetchEmpty
	}
	var status Status
	if err := json.Unmarshal(out, &status); err != nil {
		log.Printf("treeman: cannot parse status JSON: %v", err)
		return nil, fetchEmpty
	}
	return &status, fetchData
}

// refresh fetches and publishes a changed status; false when treeman
// is not installed, which ends the loop instead of re-spawning a
// missing command every reconnect.
func (s *System) refresh(ctx context.Context) bool {
	next, outcome := s.fetch(ctx)
	switch outcome {
	case fetchUnavailable:
		return false
	case fetchEmpty:
		return true
	}
	s.mu.Lock()
	changed := !reflect.DeepEqual(s.status, next)
	if changed {
		s.status = next
	}
	s.mu.Unlock()
	if changed {
		feed.Notify(&s.changes)
	}
	return true
}

// Subscribe implements Source; the first subscription starts the
// service loop.
func (s *System) Subscribe(ctx context.Context) (<-chan struct{}, func(), error) {
	s.start.Do(func() { go s.run() })
	ticks, stop := s.changes.Subscribe()
	go func() {
		select {
		case <-ctx.Done():
		case <-s.stopped:
		}
		stop()
	}()
	return ticks, stop, nil
}

// run is service.rs's run: fetch (which works with the daemon down),
// follow the daemon's events, and after the stream drops or the daemon
// is absent wait reconnectDelay and go again - the poll that keeps the
// status fresh without a daemon.
func (s *System) run() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-s.stopped
		cancel()
	}()
	for {
		if !s.refresh(ctx) {
			return
		}
		s.consume(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.retry):
		}
	}
}

// consume follows one event connection, refreshing once per debounced
// burst, until the stream ends or ctx does. An absent daemon returns
// at once.
func (s *System) consume(ctx context.Context) {
	path, ok := SocketPath()
	if !ok {
		return
	}
	conn, err := dial(path)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte(subscribeRequest)); err != nil {
		return
	}
	lines := make(chan string, 8)
	go readLines(conn, lines, ctx.Done())
	timer := time.NewTimer(debounce)
	timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-lines:
			if !ok {
				return
			}
			timer.Reset(debounce)
		case <-timer.C:
			s.refresh(ctx)
		}
	}
}

// Stop terminates the event loop and closes every subscriber.
func (s *System) Stop() {
	s.halt.Do(func() {
		close(s.stopped)
		s.changes.Close()
	})
}

// RunAction implements Source (run_action): the CLI's stderr is the
// error on a non-zero exit.
func (s *System) RunAction(ctx context.Context, action Action, path string) error {
	var stderr strings.Builder
	cmd := exec.CommandContext(ctx, s.binary, action.Args(path)...) //nolint:gosec // the binary name is the client's own
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}
