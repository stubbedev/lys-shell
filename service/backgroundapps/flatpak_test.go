package backgroundapps

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func instanceID(t *testing.T, s string) InstanceID {
	t.Helper()
	id, err := ParseInstanceID(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestParseInstanceID(t *testing.T) {
	if id, err := ParseInstanceID("1234567"); err != nil || id.String() != "1234567" {
		t.Errorf("a decimal id = %v %v", id, err)
	}
	for _, bad := range []string{"", "..", "../1", "1/2", "/1", "-1", "12a", "--help", " 1"} {
		if _, err := ParseInstanceID(bad); err == nil || !strings.Contains(err.Error(), strconv.Quote(bad)) {
			t.Errorf("ParseInstanceID(%q) = %v, want an error naming it", bad, err)
		}
	}
}

// instance is a stand-in for a sandbox's first process: a real child,
// with the instance directory and the /proc view flatpak kill reads.
type instance struct {
	cmd  *exec.Cmd
	dir  string
	proc string
	done chan error
}

func startInstance(t *testing.T, id string) *instance {
	t.Helper()
	runtime := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	in := &instance{cmd: cmd, dir: filepath.Join(runtime, ".flatpak", id), proc: t.TempDir(), done: make(chan error, 1)}
	go func() { in.done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-in.done
	})
	in.writeInfo(t, `{"child-pid": `+strconv.Itoa(cmd.Process.Pid)+`, "mnt-namespace": 1}`)
	in.writeFlatpakInfo(t, "[Application]\nname=org.gnome.Fractal\n\n[Instance]\ninstance-id="+id+"\n")
	return in
}

func (in *instance) writeInfo(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(in.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.dir, "bwrapinfo.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (in *instance) writeFlatpakInfo(t *testing.T, body string) {
	t.Helper()
	root := filepath.Join(in.proc, strconv.Itoa(in.cmd.Process.Pid), "root")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".flatpak-info"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (in *instance) killed(t *testing.T) {
	t.Helper()
	select {
	case err := <-in.done:
		in.done <- err
		exit, ok := errors.AsType[*exec.ExitError](err)
		if !ok || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL { //nolint:forcetypeassert // a Linux exit status
			t.Errorf("the instance ended with %v, want SIGKILL", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the instance was not killed")
	}
}

func (in *instance) alive(t *testing.T) {
	t.Helper()
	select {
	case err := <-in.done:
		in.done <- err
		t.Errorf("the instance ended (%v), want it left running", err)
	default:
	}
}

func TestKillInstanceKillsItsFirstProcess(t *testing.T) {
	in := startInstance(t, "42")
	if err := killInstance(instanceID(t, "42"), in.proc); err != nil {
		t.Fatal(err)
	}
	in.killed(t)
}

func TestKillInstanceRefusesAProcessThatIsNotTheInstance(t *testing.T) {
	for name, info := range map[string]string{
		"another instance": "[Instance]\ninstance-id=43\n",
		"no instance id":   "[Application]\nname=org.gnome.Fractal\n",
	} {
		in := startInstance(t, "42")
		in.writeFlatpakInfo(t, info)
		err := killInstance(instanceID(t, "42"), in.proc)
		if err == nil || !strings.Contains(err.Error(), "instance 42") {
			t.Errorf("%s: kill = %v, want an error naming the instance", name, err)
		}
		in.alive(t)
	}
	in := startInstance(t, "42")
	if err := os.RemoveAll(filepath.Join(in.proc, strconv.Itoa(in.cmd.Process.Pid))); err != nil {
		t.Fatal(err)
	}
	if err := killInstance(instanceID(t, "42"), in.proc); err == nil || !strings.Contains(err.Error(), ".flatpak-info") {
		t.Errorf("a process with no .flatpak-info = %v, want refused", err)
	}
	in.alive(t)
}

func TestKillInstanceNamesTheInstanceWhenItCannot(t *testing.T) {
	in := startInstance(t, "42")
	if err := killInstance(instanceID(t, "7"), in.proc); err == nil || !strings.Contains(err.Error(), "instance 7") || !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing instance dir = %v, want a not-exist error naming the instance", err)
	}
	for _, body := range []string{`{"child-pid": 0}`, `{}`, `{"child-pid": "12"}`, `not json`} {
		in.writeInfo(t, body)
		if err := killInstance(instanceID(t, "42"), in.proc); err == nil || !strings.Contains(err.Error(), "instance 42") {
			t.Errorf("bwrapinfo %s = %v, want an error naming the instance", body, err)
		}
	}
	in.alive(t)

	t.Setenv("XDG_RUNTIME_DIR", "relative")
	if err := killInstance(instanceID(t, "42"), in.proc); err == nil || !strings.Contains(err.Error(), "XDG_RUNTIME_DIR") {
		t.Errorf("no runtime dir = %v, want an error naming it", err)
	}
}

func TestKillInstanceOfAnExitedProcessFails(t *testing.T) {
	in := startInstance(t, "42")
	if err := in.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	in.done <- <-in.done
	if err := killInstance(instanceID(t, "42"), in.proc); err == nil || !strings.Contains(err.Error(), "instance 42") {
		t.Errorf("an exited instance = %v, want an error naming it", err)
	}
}
