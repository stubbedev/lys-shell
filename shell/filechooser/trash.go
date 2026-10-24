package filechooser

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// trashLocation is the Trash place's directory: not a path, the
// listing of the trashed files.
const trashLocation = "trash:"

// virtualLocation reports a place that lists files gathered from
// elsewhere (Recent, Trash) instead of a folder: it has no parent, is
// not followed on disk, and nothing is created in it.
func virtualLocation(dir string) bool { return dir == recentLocation || dir == trashLocation }

// TrashDirs are the trash directories of the freedesktop trash spec:
// the home trash under dataHome, then for each mount point its
// $topdir/.Trash/$uid when .Trash is a real, sticky directory, and its
// $topdir/.Trash-$uid.
func TrashDirs(dataHome string, mounts []string, uid int) []string {
	var dirs []string
	if dataHome != "" {
		dirs = append(dirs, filepath.Join(dataHome, "Trash"))
	}
	id := strconv.Itoa(uid)
	for _, top := range mounts {
		shared := filepath.Join(top, ".Trash")
		if info, err := os.Lstat(shared); err == nil && info.IsDir() && info.Mode()&os.ModeSticky != 0 {
			dirs = append(dirs, filepath.Join(shared, id))
		}
		dirs = append(dirs, filepath.Join(top, ".Trash-"+id))
	}
	return dirs
}

// ListTrash lists the files in each trash directory's files/, each
// named by the original basename its info/<name>.trashinfo records. A
// file without readable, well-formed info is left out (the spec's
// orphan), a trash directory that does not exist lists nothing, and
// one that cannot be read is an error naming it.
func ListTrash(dirs []string, mode Mode) ([]Entry, error) {
	var entries []Entry
	for _, dir := range dirs {
		files := filepath.Join(dir, "files")
		des, err := os.ReadDir(files)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read the trash %s: %w", files, err)
		}
		for _, de := range des {
			info, err := os.ReadFile(filepath.Join(dir, "info", de.Name()+".trashinfo")) //nolint:gosec // the user's trash
			if err != nil {
				continue
			}
			name, ok := trashedName(info)
			if !ok {
				continue
			}
			if e, ok := statEntry(filepath.Join(files, de.Name()), mode); ok {
				e.Label = name
				entries = append(entries, e)
			}
		}
	}
	return entries, nil
}

// trashedName is the original basename a .trashinfo records: the Path
// key of its [Trash Info] group, percent-decoded; false for info that
// is not that group first or has no usable Path.
func trashedName(info []byte) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(info))
	group := ""
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if group == "" && line != "[Trash Info]" {
				return "", false
			}
			group = line
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if group != "[Trash Info]" || !ok || strings.TrimSpace(key) != "Path" {
			continue
		}
		p, err := url.PathUnescape(strings.TrimSpace(value))
		if err != nil {
			return "", false
		}
		name := path.Base(p)
		if name == "/" || name == "." || name == ".." || name == "" {
			return "", false
		}
		return name, true
	}
	return "", false
}
