package filechooser

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/stubbedev/lys-shell/imagedecode"
	"github.com/stubbedev/lys-shell/internal/desktopentry"
	"github.com/stubbedev/lys-shell/shlex"
)

// thumbnailerTimeout is how long an installed thumbnailer may run, and
// thumbnailerMaxBytes how large its PNG may be, before it counts as
// failed.
const (
	thumbnailerTimeout  = 10 * time.Second
	thumbnailerMaxBytes = 16 << 20
)

// thumbnailerGroup is a .thumbnailer file's group.
const thumbnailerGroup = "Thumbnailer Entry"

// thumbnailerEntry is an installed thumbnailer: the command line its
// Exec names, with the %i %o %s %u field codes still in it, and the
// file that installed it.
type thumbnailerEntry struct {
	argv []string
	file string
}

// loadThumbnailers reads the thumbnailers/*.thumbnailer files of the
// data directories into a thumbnailer per MIME type: an earlier
// directory wins, as does the earlier file within one, and a
// thumbnailer whose TryExec is not installed or whose Exec does not
// parse is left out.
func loadThumbnailers(dataDirs []string) map[string]thumbnailerEntry {
	byType := map[string]thumbnailerEntry{}
	for _, dir := range dataDirs {
		files, err := filepath.Glob(filepath.Join(dir, "thumbnailers", "*.thumbnailer"))
		if err != nil {
			continue
		}
		slices.Sort(files)
		for _, file := range files {
			kf, err := desktopentry.LoadKeyFile(file)
			if err != nil || !kf.HasGroup(thumbnailerGroup) {
				continue
			}
			if try, ok := kf.String(thumbnailerGroup, "TryExec"); ok {
				if _, err := exec.LookPath(try); err != nil {
					continue
				}
			}
			line, _ := kf.String(thumbnailerGroup, "Exec")
			argv, ok := shlex.Split(line)
			if !ok || len(argv) == 0 {
				continue
			}
			for _, t := range kf.List(thumbnailerGroup, "MimeType") {
				if _, taken := byType[t]; !taken {
					byType[t] = thumbnailerEntry{argv: argv, file: file}
				}
			}
		}
	}
	return byType
}

// expand fills the command line's field codes: %i the input path, %u
// its URI, %o the output path, %s the size and %% a percent sign.
func (t thumbnailerEntry) expand(input, uri, output string, size int) []string {
	r := strings.NewReplacer("%%", "%", "%i", input, "%u", uri, "%o", output, "%s", strconv.Itoa(size))
	argv := make([]string, len(t.argv))
	for i, a := range t.argv {
		argv[i] = r.Replace(a)
	}
	return argv
}

// run makes input's thumbnail at size with the thumbnailer: the
// command runs in its own process group with no input or output, and
// the whole group is killed at timeout or when ctx ends. A command that
// fails, writes nothing, or writes more than thumbnailerMaxBytes or an
// image that does not decode, fails.
func (t thumbnailerEntry) run(ctx context.Context, input, uri string, size int, timeout time.Duration) (image.Image, error) {
	dir, err := os.MkdirTemp("", "lys-thumbnailer-")
	if err != nil {
		return nil, fmt.Errorf("thumbnailer %s: %w", t.file, err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	output := filepath.Join(dir, "thumbnail.png")
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	argv := t.expand(input, uri, output, size)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // an installed thumbnailer, as every file manager runs it
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = errors.Join(err, ctx.Err())
		}
		return nil, fmt.Errorf("thumbnailer %s on %s: %w", t.file, input, err)
	}
	info, err := os.Stat(output)
	if err != nil {
		return nil, fmt.Errorf("thumbnailer %s on %s wrote nothing: %w", t.file, input, err)
	}
	if info.Size() > thumbnailerMaxBytes {
		return nil, fmt.Errorf("thumbnailer %s on %s wrote %d bytes, more than %d", t.file, input, info.Size(), thumbnailerMaxBytes)
	}
	img, err := imagedecode.Open(output)
	if err != nil {
		return nil, fmt.Errorf("thumbnailer %s on %s: %w", t.file, input, err)
	}
	return img, nil
}
