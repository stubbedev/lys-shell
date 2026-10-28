package main

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/sockdir"

	"github.com/stubbedev/lys-shell/secrets/control"
)

// TestMain lets the test binary be lys-secrets: the tests re-exec it
// with LYS_SECRETS_TEST_MAIN set.
func TestMain(m *testing.M) {
	if os.Getenv("LYS_SECRETS_TEST_MAIN") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type daemon struct {
	cmd  *exec.Cmd
	logs chan string
}

// start runs lys-secrets on the test's private session bus, the login
// password on an inherited pipe when given.
func start(t *testing.T, password string) *daemon {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "LYS_SECRETS_TEST_MAIN=1")
	if password != "" {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, password+"\n")
		_ = w.Close()
		cmd.ExtraFiles = []*os.File{r}
		cmd.Args = append(cmd.Args, "--unlock-fd", "3")
		t.Cleanup(func() { _ = r.Close() })
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	d := &daemon{cmd: cmd, logs: make(chan string, 64)}
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			d.logs <- sc.Text()
		}
		close(d.logs)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return d
}

// waitLog waits for a log line containing want.
func (d *daemon) waitLog(t *testing.T, want string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case line, ok := <-d.logs:
			if !ok {
				t.Fatalf("lys-secrets exited before logging %q", want)
			}
			if strings.Contains(line, want) {
				return
			}
		case <-deadline:
			t.Fatalf("lys-secrets never logged %q", want)
		}
	}
}

func setup(t *testing.T) {
	t.Helper()
	dbustest.Session(t)
	t.Setenv("XDG_RUNTIME_DIR", sockdir.Dir(t))
	t.Setenv("XDG_DATA_HOME", t.TempDir())
}

func secretTool(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	tool, err := exec.LookPath("secret-tool")
	if err != nil {
		t.Skip("secret-tool is not installed (the nix dev shell carries it)")
	}
	cmd := exec.Command(tool, args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	return string(out), err
}

func TestLoginPasswordUnlocksAndTheLockScreenRelocks(t *testing.T) {
	setup(t)
	d := start(t, "login-pw")
	d.waitLog(t, "serving org.freedesktop.secrets")
	if _, err := secretTool(t, "v1", "store", "--label=x", "app", "a"); err != nil {
		t.Fatalf("store after --unlock-fd: %v", err)
	}
	if got, err := secretTool(t, "", "lookup", "app", "a"); err != nil || got != "v1" {
		t.Fatalf("lookup = %q, %v", got, err)
	}
	files, _ := filepath.Glob(filepath.Join(os.Getenv("XDG_DATA_HOME"), "lys-secrets", "*.keyring"))
	if len(files) != 1 || filepath.Base(files[0]) != "login.keyring" {
		t.Errorf("collection files = %v", files)
	}

	// A restart starts locked; the control socket unlocks it with the
	// password the lock screen verified, and refuses a wrong one.
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	if err := d.cmd.Wait(); err != nil {
		t.Fatalf("SIGTERM exit: %v", err)
	}
	d = start(t, "")
	d.waitLog(t, "serving org.freedesktop.secrets")
	sock, err := control.SocketPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Unlock(sock, []byte("wrong")); !errors.Is(err, control.ErrWrongPassword) {
		t.Fatalf("wrong password over the socket = %v", err)
	}
	if err := control.Unlock(sock, []byte("login-pw")); err != nil {
		t.Fatalf("unlock over the socket: %v", err)
	}
	if got, err := secretTool(t, "", "lookup", "app", "a"); err != nil || got != "v1" {
		t.Errorf("lookup after the socket unlock = %q, %v", got, err)
	}
}

func TestAWrongLoginPasswordLeavesItLocked(t *testing.T) {
	setup(t)
	d := start(t, "right")
	d.waitLog(t, "serving")
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	_ = d.cmd.Wait()
	d = start(t, "wrong")
	d.waitLog(t, "does not unlock the login collection")
	d.waitLog(t, "serving")
	if got, _ := secretTool(t, "", "lookup", "app", "a"); got != "" {
		t.Errorf("lookup = %q while locked", got)
	}
}

func TestAnotherProviderKeepsTheName(t *testing.T) {
	setup(t)
	first := start(t, "pw")
	first.waitLog(t, "serving")
	second := start(t, "")
	second.waitLog(t, "is already served by")
	if err := second.cmd.Wait(); err != nil {
		t.Errorf("the second instance exit = %v, want a clean exit", err)
	}
	if first.cmd.ProcessState != nil {
		t.Error("the first instance died")
	}
}
