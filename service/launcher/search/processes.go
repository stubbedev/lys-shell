package search

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/stubbedev/lys-desktop/i18n"
	"golang.org/x/sys/unix"

	"github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/sysinfo"
)

const (
	// procDir is where the kernel lists processes.
	procDir = "/proc"
	// procParentField and procStartTimeField are ppid's and starttime's
	// places in /proc/pid/stat, counted from the state field that follows
	// the command name.
	procParentField    = 1
	procStartTimeField = 19
	// waylandDefaultDisplay is the socket libwayland falls back to.
	waylandDefaultDisplay = "wayland-0"
	// waylandDialTimeout bounds asking who the compositor is.
	waylandDialTimeout = time.Second
)

// process is one process the user can signal. startTime tells it from a
// later process that was given the same pid.
type process struct {
	pid       int
	name      string
	cmdline   string
	rss       uint64
	startTime string
}

// procStat is what a /proc/pid/stat says.
type procStat struct {
	name, state, startTime string
	parent                 int
}

// parseProcStat reads a /proc/pid/stat. The name may hold spaces and
// parentheses, so it ends at the last ')'.
func parseProcStat(stat string) (procStat, error) {
	open, end := strings.IndexByte(stat, '('), strings.LastIndexByte(stat, ')')
	if open < 0 || end < open {
		return procStat{}, fmt.Errorf("no command name in %q", stat)
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) <= procStartTimeField {
		return procStat{}, fmt.Errorf("%d fields after the command name in %q", len(fields), stat)
	}
	parent, err := strconv.Atoi(fields[procParentField])
	if err != nil {
		return procStat{}, fmt.Errorf("parent in %q: %w", stat, err)
	}
	return procStat{name: stat[open+1 : end], state: fields[0], startTime: fields[procStartTimeField], parent: parent}, nil
}

// readProcess reads one /proc entry; false for what is not the user's to
// end: another user's, a kernel thread (no command line), a zombie.
func readProcess(dir string, uid uint32, pageSize uint64) (process, bool, error) {
	pid, err := strconv.Atoi(filepath.Base(dir))
	if err != nil {
		return process{}, false, nil //nolint:nilerr // /proc also lists what is not a process
	}
	info, err := os.Stat(dir)
	if err != nil {
		return process{}, false, err
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != uid {
		return process{}, false, nil
	}
	stat, err := os.ReadFile(filepath.Join(dir, "stat")) //nolint:gosec // under /proc
	if err != nil {
		return process{}, false, err
	}
	parsed, err := parseProcStat(string(stat))
	if err != nil {
		return process{}, false, err
	}
	cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline")) //nolint:gosec // under /proc
	if err != nil {
		return process{}, false, err
	}
	if len(cmdline) == 0 || parsed.state == "Z" {
		return process{}, false, nil
	}
	statm, err := os.ReadFile(filepath.Join(dir, "statm")) //nolint:gosec // under /proc
	if err != nil {
		return process{}, false, err
	}
	var rss uint64
	if fields := strings.Fields(string(statm)); len(fields) > 1 {
		if rss, err = strconv.ParseUint(fields[1], 10, 64); err != nil {
			return process{}, false, fmt.Errorf("statm of %d: %w", pid, err)
		}
	}
	return process{
		pid: pid, name: parsed.name, startTime: parsed.startTime, rss: rss * pageSize,
		cmdline: strings.TrimSpace(strings.ReplaceAll(string(cmdline), "\x00", " ")),
	}, true, nil
}

// waylandCompositorPid asks the kernel who listens on the display's socket
// (libwayland's rules for finding it): that process is the compositor, and
// no name has to be guessed.
func waylandCompositorPid(ctx context.Context, runtimeDir, display string) (int, error) {
	if display == "" {
		display = waylandDefaultDisplay
	}
	path := display
	if !filepath.IsAbs(path) {
		if !filepath.IsAbs(runtimeDir) {
			return 0, fmt.Errorf("display %q: XDG_RUNTIME_DIR %q is not an absolute path", display, runtimeDir)
		}
		path = filepath.Join(runtimeDir, display)
	}
	dialer := net.Dialer{Timeout: waylandDialTimeout}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("%s is not a unix socket", path)
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if credErr != nil {
		return 0, fmt.Errorf("peer of %s: %w", path, credErr)
	}
	if cred.Pid <= 0 {
		return 0, fmt.Errorf("peer of %s is outside this pid namespace", path)
	}
	return int(cred.Pid), nil
}

// sessionPids are the given processes and everything that started them:
// ending any of them ends the session or the panel, so they are not
// offered. A process gone while walking ends its chain.
func sessionPids(root string, pids ...int) map[int]bool {
	session := make(map[int]bool, len(pids)*4)
	for _, pid := range pids {
		for pid > 1 && !session[pid] {
			session[pid] = true
			stat, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "stat")) //nolint:gosec // under /proc
			if err != nil {
				break
			}
			parsed, err := parseProcStat(string(stat))
			if err != nil {
				break
			}
			pid = parsed.parent
		}
	}
	return session
}

// listProcesses lists the user's processes under root but those of
// hidden, the largest first. A process that exits while being read is
// simply not listed.
func listProcesses(root string, uid uint32, hidden map[int]bool) ([]process, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	pageSize := uint64(os.Getpagesize()) //nolint:gosec // a page size is positive
	out := make([]process, 0, len(entries))
	for _, e := range entries {
		p, ok, err := readProcess(filepath.Join(root, e.Name()), uid, pageSize)
		switch {
		case errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ESRCH):
			continue
		case err != nil:
			log.Printf("launcher: reading process %s: %v", e.Name(), err)
			continue
		case ok && !hidden[p.pid]:
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b process) int {
		return cmp.Or(cmp.Compare(b.rss, a.rss), cmp.Compare(a.pid, b.pid))
	})
	return out, nil
}

// signalProcess signals p through a pidfd, which cannot reach another
// process once it is open, after checking this pid is still the process
// that was listed: the list may be minutes old.
func signalProcess(root string, p process, sig unix.Signal) (err error) {
	fd, err := unix.PidfdOpen(p.pid, 0)
	if err != nil {
		return fmt.Errorf("process %d: %w", p.pid, err)
	}
	defer func() { err = errors.Join(err, unix.Close(fd)) }()
	stat, err := os.ReadFile(filepath.Join(root, strconv.Itoa(p.pid), "stat")) //nolint:gosec // under /proc
	if err != nil {
		return fmt.Errorf("process %d: %w", p.pid, err)
	}
	parsed, err := parseProcStat(string(stat))
	if err != nil {
		return fmt.Errorf("process %d: %w", p.pid, err)
	}
	if parsed.startTime != p.startTime {
		return fmt.Errorf("process %d has already exited", p.pid)
	}
	if err := unix.PidfdSendSignal(fd, sig, nil, 0); err != nil {
		return fmt.Errorf("process %d: %w", p.pid, err)
	}
	return nil
}

// Processes lists the user's running processes to end one. It is only
// ever a picker's level: a search for an app should not offer to kill it.
type Processes struct {
	root string
	// compositor names the session's compositor.
	compositor func(ctx context.Context) (int, error)
}

// Spec implements Provider.
func (*Processes) Spec() Spec { return Spec{Section: SectionNone} }

// Load implements Provider.
func (p *Processes) Load(ctx context.Context, publish func([]Entry)) {
	session := []int{os.Getpid()}
	if pid, err := p.compositor(ctx); err != nil {
		log.Printf("launcher: finding the compositor, which the process list may now show: %v", err)
	} else {
		session = append(session, pid)
	}
	list, err := listProcesses(p.root, uint32(os.Getuid()), sessionPids(p.root, session...)) //nolint:gosec // a uid fits
	if err != nil {
		log.Printf("launcher: listing processes: %v", err)
		return
	}
	entries := make([]Entry, len(list))
	for i, proc := range list {
		pid := strconv.Itoa(proc.pid)
		entries[i] = Entry{
			ID: "process:" + pid, Title: proc.name,
			Subtitle: i18n.T("launcher-process-subtitle", i18n.Str("memory", sysinfo.AutoBytes(proc.rss)), i18n.Str("pid", pid)),
			Keywords: pid + " " + proc.cmdline, Icon: launcher.IconName("ld-activity-symbolic"), Data: proc,
		}
	}
	publish(entries)
}

// Actions implements Provider.
func (p *Processes) Actions(e Entry) []Action {
	proc, ok := e.Data.(process)
	if !ok {
		return nil
	}
	signal := func(title string, sig unix.Signal) Action {
		return confirmed(Action{Title: title, Icon: "ld-x-symbolic", Run: func(context.Context) Outcome {
			if err := signalProcess(p.root, proc, sig); err != nil {
				return Stay{Message: i18n.T("launcher-failed", i18n.Str("what", title), i18n.Str("error", err.Error()))}
			}
			return Close{}
		}})
	}
	return []Action{
		signal(i18n.T("launcher-process-end", i18n.Str("name", proc.name)), unix.SIGTERM),
		signal(i18n.T("launcher-process-force", i18n.Str("name", proc.name)), unix.SIGKILL),
	}
}

// processesLevel lists what can be ended.
func processesLevel() *Level {
	return &Level{Title: i18n.T("launcher-picker-processes"), Placeholder: i18n.T("launcher-placeholder-filter"), Providers: []Provider{&Processes{
		root: procDir,
		compositor: func(ctx context.Context) (int, error) {
			return waylandCompositorPid(ctx, os.Getenv("XDG_RUNTIME_DIR"), os.Getenv("WAYLAND_DISPLAY"))
		},
	}}}
}
