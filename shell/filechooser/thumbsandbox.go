package filechooser

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/stubbedev/lys-desktop/fileuri"
	"github.com/stubbedev/lys-lib/intconv"
	"golang.org/x/sys/unix"
)

// systemPaths are what a sandboxed thumbnailer sees of the system,
// read only, where they exist: the programs and libraries, the
// loader's cache, the font configuration and cache, and Nix's store
// and system profile.
var systemPaths = []string{
	"/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64",
	"/etc/ld.so.cache", "/etc/alternatives", "/etc/fonts", "/var/cache/fontconfig",
	"/nix/store", "/run/current-system",
}

// The sandbox's writable directory, which holds the output, and the
// name the input is read under there, with the input's extension.
const (
	sandboxDir   = "/tmp"
	sandboxInput = "/tmp/lys-thumbnail-input"
)

// The probe's bound, and how much of a sandboxed command's output is
// kept for its error.
const (
	sandboxProbeTimeout = 5 * time.Second
	sandboxOutputBytes  = 4 << 10
)

// sandbox is bubblewrap set up to run thumbnailers as GNOME's
// thumbnail factory does: the system read only, the input file alone
// readable and one directory writable, no network and every other
// namespace new, a session of its own, dying with its parent, and a
// seccomp filter.
type sandbox struct {
	bwrap string
	// system are the arguments binding the system, bound the paths
	// they make visible; filter is the seccomp program.
	system []string
	bound  []string
	filter []byte
}

// newSandbox finds bubblewrap and checks it can make a sandbox here
// (it may be installed where user namespaces are not allowed).
func newSandbox(ctx context.Context) (sandbox, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return sandbox{}, fmt.Errorf("bubblewrap (bwrap) is not installed: %w", err)
	}
	filter, err := encodeFilter(seccompFilter())
	if err != nil {
		return sandbox{}, err
	}
	s := sandbox{bwrap: bwrap, filter: filter}
	for _, p := range systemPaths {
		info, err := os.Lstat(p)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return sandbox{}, fmt.Errorf("sandbox: reading the link %s: %w", p, err)
			}
			if !filepath.IsAbs(target) {
				s.system = append(s.system, "--symlink", target, p)
				continue
			}
			if target, err = filepath.EvalSymlinks(p); err != nil {
				continue
			}
			s.system = append(s.system, "--ro-bind", target, p)
			s.bound = append(s.bound, p, target)
			continue
		}
		s.system = append(s.system, "--ro-bind", p, p)
		s.bound = append(s.bound, p)
	}
	dir, err := os.MkdirTemp("", "lys-thumbnailer-")
	if err != nil {
		return sandbox{}, fmt.Errorf("sandbox: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := s.run(ctx, "", dir, []string{bwrap, "--version"}, sandboxProbeTimeout); err != nil {
		return sandbox{}, fmt.Errorf("bubblewrap cannot make a sandbox here: %w", err)
	}
	return s, nil
}

// visible is whether path is within the system the sandbox binds.
func (s sandbox) visible(path string) bool {
	for _, b := range s.bound {
		if path == b || strings.HasPrefix(path, b+"/") {
			return true
		}
	}
	return false
}

// sandboxPath is the path input is read under in the sandbox, "" for none.
func sandboxPath(input string) string {
	if input == "" {
		return ""
	}
	return sandboxInput + filepath.Ext(input)
}

// sandboxURI is the URI input is read under in the sandbox.
func sandboxURI(input string) string { return fileuri.FromPath(sandboxPath(input)) }

// run runs argv in the sandbox, with input readable at sandboxPath(input)
// ("" for none) and dir writable at sandboxDir; argv's program,
// resolved on the host, is bound where the system does not hold it.
// bwrap runs in its own process group, killed whole at timeout or when
// ctx ends. The error carries the start of what the command printed.
func (s sandbox) run(ctx context.Context, input, dir string, argv []string, timeout time.Duration) error {
	program, err := exec.LookPath(argv[0])
	if err == nil {
		program, err = filepath.EvalSymlinks(program)
	}
	if err != nil {
		return fmt.Errorf("sandbox: finding %s: %w", argv[0], err)
	}
	args := make([]string, 0, len(s.system)+len(argv)+24)
	args = append(args, s.system...)
	args = append(args,
		"--proc", "/proc", "--dev", "/dev", "--chdir", "/",
		"--unshare-all", "--new-session", "--die-with-parent",
		"--clearenv", "--setenv", "PATH", os.Getenv("PATH"), "--setenv", "HOME", sandboxDir,
		"--bind", dir, sandboxDir,
	)
	if input != "" {
		args = append(args, "--ro-bind", input, sandboxPath(input))
	}
	if !s.visible(program) {
		args = append(args, "--ro-bind", program, program)
	}
	args = append(args, "--seccomp", "3", "--", program)
	args = append(args, argv[1:]...)
	filter, err := seccompFile(s.filter)
	if err != nil {
		return err
	}
	defer func() { _ = filter.Close() }()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out := &cappedBuffer{limit: sandboxOutputBytes}
	cmd := exec.CommandContext(ctx, s.bwrap, args...) //nolint:gosec // bubblewrap running an installed thumbnailer
	cmd.ExtraFiles = []*os.File{filter}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("%w (%w)", err, ctx.Err())
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out.b)))
	}
	return nil
}

// cappedBuffer keeps the first limit bytes written to it and drops the
// rest.
type cappedBuffer struct {
	b     []byte
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.b = append(c.b, p[:min(len(p), max(c.limit-len(c.b), 0))]...)
	return len(p), nil
}

// seccompFile is a pipe holding the seccomp filter, closed for writing,
// for bubblewrap to read whole.
func seccompFile(filter []byte) (*os.File, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("sandbox: the seccomp pipe: %w", err)
	}
	_, err = w.Write(filter)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("sandbox: writing the seccomp filter: %w", err)
	}
	return r, nil
}

// verdict is what the filter answers a call with, in its last
// instructions.
type verdict uint8

const (
	allow verdict = iota + 1
	deny
	noSys
	noFamily
	kill
)

var verdicts = []struct {
	v   verdict
	ret uint32
}{
	{allow, unix.SECCOMP_RET_ALLOW},
	{deny, unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
	{noSys, unix.SECCOMP_RET_ERRNO | uint32(unix.ENOSYS)},
	{noFamily, unix.SECCOMP_RET_ERRNO | uint32(unix.EAFNOSUPPORT)},
	{kill, unix.SECCOMP_RET_KILL_PROCESS},
}

// deniedSyscalls are the calls a thumbnailer has no use for and the
// sandbox's own setup must not be undone by (flatpak's and GNOME's
// lists): namespaces and mounts, tracing, keyrings, kernel modules and
// the kernel's own memory, BPF and perf.
var deniedSyscalls = append([]uint32{
	unix.SYS_SYSLOG, unix.SYS_ACCT, unix.SYS_QUOTACTL,
	unix.SYS_ADD_KEY, unix.SYS_KEYCTL, unix.SYS_REQUEST_KEY,
	unix.SYS_MOVE_PAGES, unix.SYS_MBIND, unix.SYS_GET_MEMPOLICY, unix.SYS_SET_MEMPOLICY, unix.SYS_MIGRATE_PAGES,
	unix.SYS_UNSHARE, unix.SYS_SETNS, unix.SYS_MOUNT, unix.SYS_UMOUNT2, unix.SYS_PIVOT_ROOT, unix.SYS_CHROOT,
	unix.SYS_OPEN_TREE, unix.SYS_MOVE_MOUNT, unix.SYS_FSOPEN, unix.SYS_FSMOUNT, unix.SYS_FSCONFIG, unix.SYS_FSPICK,
	unix.SYS_PTRACE, unix.SYS_PROCESS_VM_READV, unix.SYS_PROCESS_VM_WRITEV, unix.SYS_PERSONALITY,
	unix.SYS_PERF_EVENT_OPEN, unix.SYS_BPF, unix.SYS_USERFAULTFD,
	unix.SYS_INIT_MODULE, unix.SYS_FINIT_MODULE, unix.SYS_DELETE_MODULE, unix.SYS_KEXEC_LOAD,
	unix.SYS_OPEN_BY_HANDLE_AT,
}, archDeniedSyscalls...)

// x32Bit marks amd64's x32 calls, which share its audit arch.
const x32Bit = 0x40000000

// The offsets of struct seccomp_data's fields, the arguments' low
// halves on these little-endian machines.
const (
	dataNr   = 0
	dataArch = 4
	dataArg0 = 16
	dataArg1 = 24
)

// jump is where a test goes: to a verdict, or past skip instructions
// (none is the next one).
type jump struct {
	to   verdict
	skip uint8
}

var onward = jump{}

func to(v verdict) jump { return jump{to: v} }

func past(n uint8) jump { return jump{skip: n} }

// filterOp is one instruction.
type filterOp struct {
	code   uint16
	k      uint32
	jt, jf jump
}

func load(offset uint32) filterOp {
	return filterOp{code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, k: offset}
}

func test(code uint16, k uint32, jt, jf jump) filterOp {
	return filterOp{code: unix.BPF_JMP | code | unix.BPF_K, k: k, jt: jt, jf: jf}
}

// seccompFilter denies the calls a thumbnailer must not make: any of
// another arch or amd64's x32, deniedSyscalls, sockets but Unix ones,
// user namespaces through clone, clone3 (whose flags a filter cannot
// read; libc falls back to clone), and faking terminal input.
func seccompFilter() []filterOp {
	ops := []filterOp{
		load(dataArch),
		test(unix.BPF_JEQ, auditArch, onward, to(kill)),
		load(dataNr),
		test(unix.BPF_JGE, x32Bit, to(noSys), onward),
		test(unix.BPF_JEQ, unix.SYS_CLONE3, to(noSys), onward),
	}
	for _, nr := range deniedSyscalls {
		ops = append(ops, test(unix.BPF_JEQ, nr, to(deny), onward))
	}
	return append(ops,
		test(unix.BPF_JEQ, unix.SYS_SOCKET, onward, past(2)),
		load(dataArg0),
		test(unix.BPF_JEQ, unix.AF_UNIX, to(allow), to(noFamily)),
		test(unix.BPF_JEQ, unix.SYS_CLONE, onward, past(2)),
		load(dataArg0),
		test(unix.BPF_JSET, unix.CLONE_NEWUSER, to(deny), to(allow)),
		test(unix.BPF_JEQ, unix.SYS_IOCTL, onward, to(allow)),
		load(dataArg1),
		test(unix.BPF_JEQ, unix.TIOCSTI, to(deny), onward),
		test(unix.BPF_JEQ, unix.TIOCLINUX, to(deny), to(allow)),
	)
}

// encodeFilter lays the instructions out with the verdicts after them
// and encodes them as the kernel's sock_filter array; a jump further
// than an instruction can reach is an error.
func encodeFilter(ops []filterOp) ([]byte, error) {
	out := make([]byte, 0, (len(ops)+len(verdicts))*8)
	offset := func(i int, j jump) (uint8, error) {
		d := int(j.skip)
		if j.to != 0 {
			d = len(ops) + int(j.to) - 1 - (i + 1)
		}
		o, err := intconv.Exact[uint8](d)
		if err != nil {
			return 0, fmt.Errorf("seccomp: instruction %d jumps %d ahead: %w", i, d, err)
		}
		return o, nil
	}
	for i, op := range ops {
		jt, err := offset(i, op.jt)
		if err != nil {
			return nil, err
		}
		jf, err := offset(i, op.jf)
		if err != nil {
			return nil, err
		}
		out = binary.NativeEndian.AppendUint16(out, op.code)
		out = append(out, jt, jf)
		out = binary.NativeEndian.AppendUint32(out, op.k)
	}
	for i, v := range verdicts {
		if int(v.v) != i+1 {
			return nil, fmt.Errorf("seccomp: verdict %d is laid out at %d", v.v, i+1)
		}
		out = binary.NativeEndian.AppendUint16(out, unix.BPF_RET|unix.BPF_K)
		out = append(out, 0, 0)
		out = binary.NativeEndian.AppendUint32(out, v.ret)
	}
	return out, nil
}
