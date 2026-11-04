package search

import (
	"context"
	"os"
	"os/exec"
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
	name, state, start, err := parseProcStat("42 (Web (Content) x)" + tail)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Web (Content) x" || state != "S" || start != "987654" {
		t.Errorf("name %q state %q start %q", name, state, start)
	}
	for _, bad := range []string{"", "42 no name S 1", "42 (short) S 1 2"} {
		if _, _, _, err := parseProcStat(bad); err == nil {
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
	list, err := listProcesses(procDir, uint32(os.Getuid()), self)
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
	(&Processes{root: procDir}).Load(context.Background(), func(e []Entry) { entries = e })
	found := slices.ContainsFunc(entries, func(e Entry) bool {
		return e.ID == "process:"+strconv.Itoa(cmd.Process.Pid) && strings.Contains(e.Subtitle, strconv.Itoa(cmd.Process.Pid))
	})
	if !found {
		t.Errorf("not among %d rows", len(entries))
	}
}
