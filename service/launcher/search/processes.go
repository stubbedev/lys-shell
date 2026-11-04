package search

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/sysinfo"
)

const (
	// procDir is where the kernel lists processes.
	procDir = "/proc"
	// procStartTimeField is starttime's place in /proc/pid/stat, counted
	// from the state field that follows the command name.
	procStartTimeField = 19
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

// parseProcStat reads the name, state and start time of a /proc/pid/stat.
// The name may hold spaces and parentheses, so it ends at the last ')'.
func parseProcStat(stat string) (name, state, startTime string, err error) {
	open, end := strings.IndexByte(stat, '('), strings.LastIndexByte(stat, ')')
	if open < 0 || end < open {
		return "", "", "", fmt.Errorf("no command name in %q", stat)
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) <= procStartTimeField {
		return "", "", "", fmt.Errorf("%d fields after the command name in %q", len(fields), stat)
	}
	return stat[open+1 : end], fields[0], fields[procStartTimeField], nil
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
	name, state, start, err := parseProcStat(string(stat))
	if err != nil {
		return process{}, false, err
	}
	cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline")) //nolint:gosec // under /proc
	if err != nil {
		return process{}, false, err
	}
	if len(cmdline) == 0 || state == "Z" {
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
		pid: pid, name: name, startTime: start, rss: rss * pageSize,
		cmdline: strings.TrimSpace(strings.ReplaceAll(string(cmdline), "\x00", " ")),
	}, true, nil
}

// listProcesses lists the user's processes under root, the largest first.
// The shell itself is left out: nothing the panel does should end the
// panel. A process that exits while being read is simply not listed.
func listProcesses(root string, uid uint32, self int) ([]process, error) {
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
		case ok && p.pid != self:
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
	_, _, start, err := parseProcStat(string(stat))
	if err != nil {
		return fmt.Errorf("process %d: %w", p.pid, err)
	}
	if start != p.startTime {
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
}

// Spec implements Provider.
func (*Processes) Spec() Spec { return Spec{Section: SectionNone} }

// Load implements Provider.
func (p *Processes) Load(_ context.Context, publish func([]Entry)) {
	list, err := listProcesses(p.root, uint32(os.Getuid()), os.Getpid()) //nolint:gosec // a uid fits
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
	return &Level{Title: i18n.T("launcher-picker-processes"), Placeholder: i18n.T("launcher-placeholder-filter"), Providers: []Provider{&Processes{root: procDir}}}
}
