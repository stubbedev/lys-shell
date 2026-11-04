package search

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/stubbedev/lys-shell/config"
)

func TestParseProcStat(t *testing.T) {
	const tail = " S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 987654 20 21"
	got, err := parseProcStat("42 (Web (Content) x)" + tail)
	if err != nil {
		t.Fatal(err)
	}
	if want := (procStat{name: "Web (Content) x", state: "S", startTime: "987654", parent: 1}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	for _, bad := range []string{"", "42 no name S 1", "42 (short) S 1 2", "42 (x) S nan" + tail[8:]} {
		if _, err := parseProcStat(bad); err == nil {
			t.Errorf("parseProcStat(%q) took it", bad)
		}
	}
}

func startSleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

func listedPids(t *testing.T, self int) map[int]process {
	t.Helper()
	list, err := listProcesses(procDir, uint32(os.Getuid()), map[int]bool{self: true})
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[int]process, len(list))
	for _, p := range list {
		out[p.pid] = p
	}
	return out
}

func sleeperListed(t *testing.T, pid int) process {
	t.Helper()
	deadline := time.Now().Add(liveness)
	for {
		if p, ok := listedPids(t, os.Getpid())[pid]; ok && p.name == "sleep" {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d never listed as sleep", pid)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestListsTheUsersProcessesButNotTheShell(t *testing.T) {
	cmd := startSleeper(t)
	p := sleeperListed(t, cmd.Process.Pid)
	if !strings.Contains(p.cmdline, "sleep 300") || p.rss == 0 || p.startTime == "" {
		t.Errorf("process %+v", p)
	}
	if _, ok := listedPids(t, os.Getpid())[os.Getpid()]; ok {
		t.Error("the shell lists itself")
	}
}

func TestAnotherUsersProcessIsNotListed(t *testing.T) {
	dir := procDir + "/" + strconv.Itoa(os.Getpid())
	if _, ok, err := readProcess(dir, uint32(os.Getuid())+1, 4096); ok || err != nil {
		t.Errorf("ok %v err %v", ok, err)
	}
}

func TestEndingAProcessEndsIt(t *testing.T) {
	cmd := startSleeper(t)
	p := sleeperListed(t, cmd.Process.Pid)
	if err := signalProcess(procDir, p, unix.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil || !strings.Contains(err.Error(), "terminated") {
		t.Errorf("wait: %v", err)
	}
}

func TestAReusedPidIsNotSignalled(t *testing.T) {
	cmd := startSleeper(t)
	p := sleeperListed(t, cmd.Process.Pid)
	p.startTime = "1"
	err := signalProcess(procDir, p, unix.SIGKILL)
	if err == nil || !strings.Contains(err.Error(), "exited") {
		t.Errorf("err = %v", err)
	}
	if err := cmd.Process.Signal(unix.Signal(0)); err != nil {
		t.Errorf("the other process was hit: %v", err)
	}
}

func TestSignallingAGoneProcessIsAnError(t *testing.T) {
	cmd := startSleeper(t)
	p := sleeperListed(t, cmd.Process.Pid)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("expected the kill to show")
	}
	if err := signalProcess(procDir, p, unix.SIGTERM); err == nil {
		t.Error("no error")
	}
}

func TestEndingAProcessAsksFirst(t *testing.T) {
	cmd := startSleeper(t)
	p := sleeperListed(t, cmd.Process.Pid)
	actions := (&Processes{root: procDir}).Actions(Entry{Data: p})
	if len(actions) != 2 {
		t.Fatalf("actions = %d", len(actions))
	}
	for i, a := range actions {
		o := a.Run(context.Background())
		if _, ok := o.(Push); !ok {
			t.Errorf("action %d outcome %T", i, o)
		}
	}
	if err := cmd.Process.Signal(unix.Signal(0)); err != nil {
		t.Errorf("asking ended it: %v", err)
	}
	level := actions[0].Run(context.Background()).(Push).Level
	var confirm []Entry
	level.Providers[0].Load(context.Background(), func(e []Entry) { confirm = e })
	if len(confirm) != 1 {
		t.Fatalf("confirm rows = %d", len(confirm))
	}
	if _, ok := level.Providers[0].Actions(confirm[0])[0].Run(context.Background()).(Close); !ok {
		t.Error("confirming did not close")
	}
	if err := cmd.Wait(); err == nil {
		t.Error("confirming did not end it")
	}
}

func TestProcessesAreOnlyAPicker(t *testing.T) {
	e, s := controlRoot(t, Deps{}, config.SearchProcesses)
	e.SetQuery("kill process", 1)
	until(t, s, "#Commands", "Kill Process")
	e.SetQuery("sleep", 2)
	until(t, s)
}

func TestWithoutTheProviderThereIsNoPicker(t *testing.T) {
	e, s := controlRoot(t, Deps{}, config.SearchCalculator)
	e.SetQuery("kill process", 1)
	until(t, s)
}

func TestTheProcessesPickerListsARealProcess(t *testing.T) {
	cmd := startSleeper(t)
	sleeperListed(t, cmd.Process.Pid)
	var entries []Entry
	(&Processes{root: procDir, compositor: func(context.Context) (int, error) { return 0, errors.New("no compositor here") }}).Load(context.Background(), func(e []Entry) { entries = e })
	found := slices.ContainsFunc(entries, func(e Entry) bool {
		return e.ID == "process:"+strconv.Itoa(cmd.Process.Pid) && strings.Contains(e.Subtitle, strconv.Itoa(cmd.Process.Pid))
	})
	if !found {
		t.Errorf("not among %d rows", len(entries))
	}
}

func TestTheCompositorIsWhoListensOnTheDisplay(t *testing.T) {
	dir := t.TempDir()
	listener, err := net.Listen("unix", filepath.Join(dir, "wayland-7"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	ctx := context.Background()
	for name, display := range map[string]string{"relative": "wayland-7", "absolute": filepath.Join(dir, "wayland-7")} {
		pid, err := waylandCompositorPid(ctx, dir, display)
		if err != nil || pid != os.Getpid() {
			t.Errorf("%s display: pid %d, err %v", name, pid, err)
		}
	}
}

func TestNoCompositorToFindIsAnError(t *testing.T) {
	ctx := context.Background()
	if _, err := waylandCompositorPid(ctx, t.TempDir(), "wayland-7"); err == nil {
		t.Error("no socket: no error")
	}
	if _, err := waylandCompositorPid(ctx, "", "wayland-7"); err == nil {
		t.Error("no runtime directory: no error")
	}
	if _, err := waylandCompositorPid(ctx, t.TempDir(), ""); err == nil {
		t.Error("no default socket: no error")
	}
}

// startFamily runs a shell with a sleep under it, returning both pids.
func startFamily(t *testing.T) (parent, child int) {
	t.Helper()
	cmd := exec.Command("sh", "-c", "sleep 300 & echo $!; wait")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	child, err = strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = unix.Kill(child, unix.SIGKILL)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid, child
}

func TestSessionPidsFollowEveryoneWhoStartedThem(t *testing.T) {
	parent, child := startFamily(t)
	other := startSleeper(t)
	session := sessionPids(procDir, child)
	for name, pid := range map[string]int{"the process": child, "its parent": parent, "this test": os.Getpid(), "this test's parent": os.Getppid()} {
		if !session[pid] {
			t.Errorf("%s (%d) is not in %v", name, pid, session)
		}
	}
	if session[other.Process.Pid] || session[1] {
		t.Errorf("an unrelated process or init is in %v", session)
	}
}

func loadedProcessIDs(t *testing.T, compositor func(context.Context) (int, error)) []string {
	t.Helper()
	var entries []Entry
	(&Processes{root: procDir, compositor: compositor}).Load(context.Background(), func(e []Entry) { entries = e })
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}
	return ids
}

func TestTheListHidesTheCompositorAndWhatStartedIt(t *testing.T) {
	parent, compositor := startFamily(t)
	other := startSleeper(t)
	sleeperListed(t, compositor)
	sleeperListed(t, other.Process.Pid)
	ids := loadedProcessIDs(t, func(context.Context) (int, error) { return compositor, nil })
	for name, pid := range map[string]int{"the compositor": compositor, "its parent": parent, "the shell": os.Getpid()} {
		if slices.Contains(ids, "process:"+strconv.Itoa(pid)) {
			t.Errorf("%s (%d) is offered", name, pid)
		}
	}
	if !slices.Contains(ids, "process:"+strconv.Itoa(other.Process.Pid)) {
		t.Error("an unrelated process is not offered")
	}
}

func TestACompositorThatCannotBeFoundLeavesTheListUsable(t *testing.T) {
	_, compositor := startFamily(t)
	sleeperListed(t, compositor)
	ids := loadedProcessIDs(t, func(context.Context) (int, error) { return 0, errors.New("no socket") })
	if !slices.Contains(ids, "process:"+strconv.Itoa(compositor)) {
		t.Error("the list is not usable")
	}
	if slices.Contains(ids, "process:"+strconv.Itoa(os.Getpid())) {
		t.Error("the shell is offered")
	}
}
