package search

import (
	"bufio"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unsafe"

	"github.com/stubbedev/lys-lib/atomicfile"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/launcher"
)

// The file index's bounds. It is a breadth-first walk, so what falls off
// the end of a tree too large to index is what lies deepest; it runs
// while the launcher is open and never otherwise, with no daemon.
const (
	fileIndexMax     = 30000
	fileIndexDepth   = 8
	fileIndexBudget  = 3 * time.Second
	fileIndexMaxAge  = 10 * time.Minute
	recentFilesMax   = 200
	recentFilesBytes = 4 << 20
	// recentRank lifts a recent file over one merely indexed.
	recentRank = 24
	// pathRowsMax bounds a directory listing answered for a typed path.
	pathRowsMax = 30
)

// skippedDirs are trees full of files nobody searches for by name.
var skippedDirs = map[string]bool{
	"node_modules": true, "vendor": true, "target": true, "__pycache__": true,
	"venv": true, "dist": true, "build": true, "result": true,
}

// FilesConfig bounds what Files reads.
type FilesConfig struct {
	// Roots are walked; a walk stays on each root's filesystem.
	Roots []string
	// Exclude are directory names to skip, on top of skippedDirs.
	Exclude []string
	// CachePath is the index's file.
	CachePath string
	// RecentPath is the desktop's recently-used.xbel.
	RecentPath string
	// Home abbreviates paths and resolves a leading ~.
	Home string
	// Previews asks for the side pane.
	Previews bool
	// Now is the clock (the index's age).
	Now func() time.Time
}

// Files searches recent files and an index of the home directory, and
// answers a typed path with its directory's contents.
type Files struct {
	env Env
	cfg FilesConfig
}

// NewFiles builds the provider.
func NewFiles(env Env, cfg FilesConfig) *Files { return &Files{env: env, cfg: cfg} }

// Spec implements Provider.
func (*Files) Spec() Spec {
	return Spec{Section: SectionFiles, MinQuery: 2, Weight: -8}
}

// Load implements Provider: what it can have at once first, the walk
// behind it if the index has gone stale.
func (f *Files) Load(ctx context.Context, publish func([]Entry)) {
	recent := readRecentFiles(f.cfg.RecentPath, recentFilesMax)
	indexed, age, err := readFileIndex(f.cfg.CachePath, f.cfg.Now)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("launcher: file index: %v", err)
	}
	publish(f.entries(recent, indexed))
	if indexed != nil && age < fileIndexMaxAge {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, fileIndexBudget)
	defer cancel()
	walked, complete := walkFiles(ctx, f.cfg.Roots, f.cfg.Exclude, fileIndexMax)
	if ctx.Err() != nil && !complete && len(walked) < len(indexed) {
		return
	}
	publish(f.entries(recent, walked))
	if err := writeFileIndex(f.cfg.CachePath, walked); err != nil {
		log.Printf("launcher: file index: %v", err)
	}
}

// entries are the rows: recents first and only once. A row shares its
// path's bytes for its id and title, and the rows share one block for
// what they hold besides, so thirty thousand rows are a few allocations
// rather than a few per row.
func (f *Files) entries(recent, indexed []string) []Entry {
	total := len(recent) + len(indexed)
	seen := make(map[string]struct{}, len(recent))
	out := make([]Entry, 0, total)
	data := make([]fileData, total)
	var dirs strings.Builder
	size := 0
	for _, p := range indexed {
		size += len(p)
	}
	for _, p := range recent {
		size += len(p)
	}
	dirs.Grow(size)
	icons := map[string]launcher.Icon{}
	add := func(path string, dir bool, rank int) {
		d := &data[len(out)]
		*d = fileData{path: path, dir: dir}
		out = append(out, f.entry(d, &dirs, icons, rank))
	}
	for _, p := range recent {
		seen[p] = struct{}{}
		add(p, false, recentRank)
	}
	for _, p := range indexed {
		if _, dup := seen[p]; dup {
			continue
		}
		dir := strings.HasSuffix(p, "/")
		add(strings.TrimSuffix(p, "/"), dir, 0)
	}
	return out
}

// displayDir is path's folder as shown: under the home directory, ~/.
// Outside it the path's own bytes serve, and within it the shortened form
// goes in the shared block.
func (f *Files) displayDir(path string, block *strings.Builder) string {
	dir := path[:max(strings.LastIndexByte(path, '/'), 1)]
	if f.cfg.Home == "" || !strings.HasPrefix(dir, f.cfg.Home) {
		return dir
	}
	rest := dir[len(f.cfg.Home):]
	if rest != "" && rest[0] != '/' {
		return dir
	}
	start := block.Len()
	block.WriteByte('~')
	block.WriteString(rest)
	return block.String()[start:]
}

func (f *Files) entry(d *fileData, block *strings.Builder, icons map[string]launcher.Icon, rank int) Entry {
	path := d.path
	e := Entry{
		ID: path, Learn: path, Title: path[strings.LastIndexByte(path, '/')+1:], Subtitle: f.displayDir(path, block),
		Data: d, Rank: rank,
	}
	if d.dir {
		e.Icon = iconFolder
		e.Completion = strings.TrimSuffix(e.Subtitle, "/") + "/" + e.Title + "/"
		return e
	}
	ext := e.Title[max(strings.LastIndexByte(e.Title, '.'), 0):]
	icon, ok := icons[ext]
	if !ok {
		icon = launcher.IconName(launcher.MimeIcon(path))
		if icons != nil && strings.HasPrefix(ext, ".") {
			icons[ext] = icon
		}
	}
	e.Icon = icon
	if f.cfg.Previews {
		e.Preview = Preview{Path: path}
	}
	return e
}

type fileData struct {
	path string
	dir  bool
}

// expandPath resolves a typed ~.
func (f *Files) expandPath(q string) string {
	if q == "~" || strings.HasPrefix(q, "~/") {
		return f.cfg.Home + q[1:]
	}
	return q
}

// Answer implements Answerer: a query that is a path lists its directory
// filtered by the name being typed. It is one directory read, on the
// engine goroutine.
func (f *Files) Answer(query string) []Entry {
	q := strings.TrimSpace(query)
	if !strings.HasPrefix(q, "/") && q != "~" && !strings.HasPrefix(q, "~/") {
		return nil
	}
	full := f.expandPath(q)
	dir, prefix := full, ""
	if !strings.HasSuffix(full, "/") {
		dir, prefix = filepath.Dir(full), filepath.Base(full)
	}
	if q == "~" {
		dir, prefix = f.cfg.Home, ""
	}
	listing, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	folded := strings.ToLower(prefix)
	var block strings.Builder
	out := make([]Entry, 0, min(len(listing), pathRowsMax))
	for _, d := range listing {
		name := d.Name()
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(name), folded) {
			continue
		}
		data := &fileData{path: filepath.Join(dir, name), dir: d.IsDir()}
		out = append(out, f.entry(data, &block, nil, 0))
		if len(out) == pathRowsMax {
			break
		}
	}
	return out
}

// Actions implements Provider.
func (f *Files) Actions(e Entry) []Action {
	d, ok := e.Data.(*fileData)
	if !ok {
		return nil
	}
	parent := filepath.Dir(d.path)
	open := Action{Title: i18n.T("launcher-action-open"), Icon: "ld-external-link-symbolic", Run: func(context.Context) Outcome {
		f.env.openURI(d.path)
		return Close{}
	}}
	reveal := Action{Title: i18n.T("launcher-action-reveal"), Icon: "ld-folder-open-symbolic", Run: func(context.Context) Outcome {
		f.env.openURI(parent)
		return Close{}
	}}
	terminalAt := parent
	if d.dir {
		terminalAt = d.path
	}
	terminal := Action{Title: i18n.T("launcher-action-open-terminal-here"), Icon: "ld-terminal-symbolic", Run: func(context.Context) Outcome {
		f.env.inTerminal("sh", "-c", `cd "$1" && exec "${SHELL:-sh}"`, "sh", terminalAt)
		return Close{}
	}}
	return []Action{
		open, reveal, terminal,
		copyAction(i18n.T("launcher-action-copy-path"), d.path),
		copyAction(i18n.T("launcher-action-copy-name"), filepath.Base(d.path)),
	}
}

// readRecentFiles reads the most recent local files of an xbel, the
// format every toolkit writes. Missing or unreadable is nothing: the
// file belongs to whichever toolkit made it, and may not exist.
func readRecentFiles(path string, limit int) []string {
	file, err := os.Open(path) //nolint:gosec // the desktop's recently-used list
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	type recent struct {
		path string
		when string
	}
	var found []recent
	dec := xml.NewDecoder(io.LimitReader(bufio.NewReader(file), recentFilesBytes))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "bookmark" {
			continue
		}
		var href, when string
		for _, a := range start.Attr {
			switch a.Name.Local {
			case "href":
				href = a.Value
			case "modified":
				when = a.Value
			case "visited":
				if when == "" {
					when = a.Value
				}
			}
		}
		u, err := url.Parse(href)
		if err != nil || u.Scheme != "file" || u.Path == "" {
			continue
		}
		found = append(found, recent{path: u.Path, when: when})
	}
	// RFC 3339 timestamps in UTC order as text.
	slices.SortStableFunc(found, func(a, b recent) int { return strings.Compare(b.when, a.when) })
	out := make([]string, 0, min(limit, len(found)))
	for _, r := range found {
		if len(out) == limit {
			break
		}
		if _, err := os.Stat(r.path); err == nil && !slices.Contains(out, r.path) {
			out = append(out, r.path)
		}
	}
	return out
}

// readFileIndex reads the cached paths and how old they are.
func readFileIndex(path string, now func() time.Time) ([]string, time.Duration, error) {
	file, err := os.Open(path) //nolint:gosec // the launcher's own cache
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	// One read of the known size; the lines are slices of one string.
	data := make([]byte, info.Size())
	if _, err := io.ReadFull(file, data); err != nil {
		return nil, 0, err
	}
	if len(data) == 0 {
		return nil, now().Sub(info.ModTime()), nil
	}
	text := unsafe.String(&data[0], len(data)) //nolint:gosec // data is never written again, and copying the index is what this avoids
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n"), now().Sub(info.ModTime()), nil
}

func writeFileIndex(path string, paths []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(path, 0o600, func(w io.Writer) error {
		buf := bufio.NewWriter(w)
		for _, p := range paths {
			if strings.ContainsAny(p, "\n") {
				continue
			}
			if _, err := buf.WriteString(p + "\n"); err != nil {
				return err
			}
		}
		return buf.Flush()
	})
}

// walkFiles lists files and directories under roots breadth first, up to
// limit paths, directories marked by a trailing slash; complete is false
// when a bound stopped it. It skips hidden names and the excluded trees,
// follows no symlink, and stays on each root's filesystem so a network
// mount cannot hang it.
func walkFiles(ctx context.Context, roots, exclude []string, limit int) (paths []string, complete bool) {
	skip := func(name string) bool {
		return strings.HasPrefix(name, ".") || skippedDirs[name] || slices.Contains(exclude, name)
	}
	type dir struct {
		path  string
		depth int
		dev   uint64
	}
	var queue []dir
	for _, r := range roots {
		dev, ok := deviceOf(r)
		if ok {
			queue = append(queue, dir{path: r, dev: dev})
		}
	}
	for len(queue) > 0 {
		if ctx.Err() != nil {
			return paths, false
		}
		d := queue[0]
		queue = queue[1:]
		listing, err := os.ReadDir(d.path)
		if err != nil {
			continue
		}
		for _, entry := range listing {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			full := d.path + "/" + name
			if !entry.IsDir() {
				if entry.Type()&os.ModeSymlink == 0 {
					paths = append(paths, full)
				}
			} else {
				if skip(name) {
					continue
				}
				paths = append(paths, full+"/")
				if d.depth+1 < fileIndexDepth {
					if dev, ok := deviceOf(full); ok && dev == d.dev {
						queue = append(queue, dir{path: full, depth: d.depth + 1, dev: d.dev})
					}
				}
			}
			if len(paths) >= limit {
				return paths, false
			}
		}
	}
	return paths, true
}
