package filechooser

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// fakeThumbnailerArg makes the test binary a thumbnailer (TestMain).
const fakeThumbnailerArg = "lys-fake-thumbnailer"

// The leaks the hostile thumbnailer reports, as bits of its PNG's width
// past hostileWidth.
const (
	leakHome = 1 << iota
	leakNetwork
	leakInetSocket
	leakDeniedSyscall
	leakNoInput
	leakWritableInput
)

const hostileWidth = 100

var leakNames = []string{"read $HOME", "reached the network", "opened an IPv4 socket", "ran a denied syscall", "could not read its input", "wrote its input"}

// fakeThumbnailer is the test binary as a thumbnailer, args its mode
// and the command line after it: png writes a 200x300 page to its last
// argument, fail exits 1, hang never ends, and hostile (home secret,
// address, input, output) tries to read $HOME, reach the network,
// open an IPv4 socket, query its personality (a denied syscall) and
// write its input, and reports what it could as its PNG's width.
func fakeThumbnailer(args []string) int {
	out := args[len(args)-1]
	switch args[0] {
	case "png":
		return writeFakePNG(out, 200, 300)
	case "fail":
		return 1
	case "hang":
		time.Sleep(time.Hour)
		return 1
	case "hostile":
		home, addr, input := args[1], args[2], args[3]
		leaks := 0
		if _, err := os.ReadDir(filepath.Dir(home)); err == nil {
			leaks |= leakHome
		}
		if _, err := os.ReadFile(home); err == nil {
			leaks |= leakHome
		}
		if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			_ = c.Close()
			leaks |= leakNetwork
		}
		if fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0); err == nil {
			_ = unix.Close(fd)
			leaks |= leakInetSocket
		}
		if _, _, errno := unix.Syscall(unix.SYS_PERSONALITY, 0xffffffff, 0, 0); errno == 0 {
			leaks |= leakDeniedSyscall
		}
		if _, err := os.ReadFile(input); err != nil {
			leaks |= leakNoInput
		}
		if f, err := os.OpenFile(input, os.O_WRONLY, 0); err == nil {
			_ = f.Close()
			leaks |= leakWritableInput
		}
		return writeFakePNG(out, hostileWidth+leaks, 10)
	}
	fmt.Fprintln(os.Stderr, "fake thumbnailer: no mode", args[0])
	return 2
}

func writeFakePNG(path string, w, h int) int {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// leaksOf names the leaks a hostile thumbnailer's PNG reports.
func leaksOf(t *testing.T, img image.Image) []string {
	t.Helper()
	bits := img.Bounds().Dx() - hostileWidth
	if bits < 0 || bits >= 1<<len(leakNames) {
		t.Fatalf("the hostile thumbnailer wrote a %d px wide PNG", img.Bounds().Dx())
	}
	var names []string
	for i, name := range leakNames {
		if bits&(1<<i) != 0 {
			names = append(names, name)
		}
	}
	return names
}

// A thumbnailer runs in the sandbox seeing its input alone: it cannot
// read $HOME, reach a listener on loopback, open an IPv4 socket, make
// a call the seccomp filter denies, or write its input. The same
// thumbnailer run unsandboxed does all but fail to read its input, so each
// probe can tell.
func TestSandboxedThumbnailerSeesOnlyItsInput(t *testing.T) {
	sb, err := newSandbox(context.Background())
	if err != nil {
		t.Fatalf("no sandbox (the dev shell carries bubblewrap): %v", err)
	}
	home := filepath.Join(os.Getenv("HOME"), "secret")
	if err := os.WriteFile(home, []byte("hunter2"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	input := filepath.Join(t.TempDir(), "paper.pdf")
	if err := os.WriteFile(input, []byte("%PDF-1.4"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	hostile := []string{bin, fakeThumbnailerArg, "hostile", home, ln.Addr().String(), "%i", "%o"}

	img, err := thumbnailerEntry{argv: hostile, file: "hostile.thumbnailer"}.run(context.Background(), sb, input, 128, 10*time.Second)
	if err != nil {
		t.Fatalf("the sandboxed thumbnailer failed: %v", err)
	}
	if leaks := leaksOf(t, img); len(leaks) != 0 {
		t.Errorf("the sandboxed thumbnailer %s", strings.Join(leaks, ", "))
	}
	if n := accepted.Load(); n != 0 {
		t.Errorf("the listener on loopback accepted %d connections from the sandbox", n)
	}

	out := filepath.Join(t.TempDir(), "out.png")
	argv := thumbnailerEntry{argv: hostile}.expand(input, "file://"+input, out, 128)
	if b, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("the unsandboxed thumbnailer: %v: %s", err, b)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	free, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"read $HOME", "reached the network", "opened an IPv4 socket", "ran a denied syscall", "wrote its input"}
	if got := leaksOf(t, free); strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Errorf("unsandboxed, the thumbnailer %v; want %v, or a probe cannot tell", got, want)
	}
	if accepted.Load() == 0 {
		t.Error("the unsandboxed thumbnailer never reached the listener")
	}
}

// Without bubblewrap no installed thumbnailer runs, a PDF row gets no
// thumbnail, and the chooser says why once.
func TestNoSandboxRunsNoThumbnailer(t *testing.T) {
	hs := newHarness(t, "")
	doc := pdfFile(t, hs)
	t.Setenv("PATH", t.TempDir())
	installThumbnailer(t, hs, "png")
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	for range 2 {
		if hs.openRows(t, filepath.Dir(doc), []string{doc}) != 0 {
			t.Fatal("a thumbnailer ran without a sandbox")
		}
	}
	if n := strings.Count(logged.String(), "installed thumbnailers are not run without a sandbox: bubblewrap (bwrap) is not installed"); n != 1 {
		t.Errorf("the missing sandbox was logged %d times, want once:\n%s", n, logged.String())
	}
}

// The seccomp filter lays out with every jump in reach and ends in its
// verdicts.
func TestSeccompFilterEncodes(t *testing.T) {
	ops := seccompFilter()
	b, err := encodeFilter(ops)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 8*(len(ops)+len(verdicts)) {
		t.Fatalf("%d bytes for %d instructions and %d verdicts", len(b), len(ops), len(verdicts))
	}
	far := append(make([]filterOp, 300), filterOp{})
	far[0] = test(unix.BPF_JEQ, 0, to(allow), onward)
	if _, err := encodeFilter(far); err == nil {
		t.Error("a jump past 255 instructions encoded")
	}
}
