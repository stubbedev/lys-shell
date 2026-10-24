package filechooser

import (
	"bufio"
	"cmp"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/stubbedev/lys-shell/internal/fileuri"
	"github.com/stubbedev/lys-shell/internal/glob"
	"github.com/stubbedev/lys-shell/internal/mime"
	"github.com/stubbedev/lys-shell/internal/xdg"
)

// Mode is what the chooser is doing.
type Mode uint8

// The chooser's modes.
const (
	ModeOpenFile Mode = iota
	ModeOpenMultiple
	ModeFolder
	ModeFolders
	ModeSave
)

// openMode maps an open request to its mode.
func openMode(r OpenRequest) Mode {
	switch {
	case r.Directory && r.Multiple:
		return ModeFolders
	case r.Directory:
		return ModeFolder
	case r.Multiple:
		return ModeOpenMultiple
	}
	return ModeOpenFile
}

// folders reports a folder pick.
func (m Mode) folders() bool { return m == ModeFolder || m == ModeFolders }

// multiple reports a pick of several.
func (m Mode) multiple() bool { return m == ModeOpenMultiple || m == ModeFolders }

// confirmLabel is the confirm button's text.
func (m Mode) confirmLabel() string {
	switch {
	case m == ModeSave:
		return "Save"
	case m.folders():
		return "Select"
	}
	return "Open"
}

// Entry is one listed file or directory.
type Entry struct {
	Path     string
	IsDir    bool
	Size     int64
	Modified time.Time
	// Label is the name shown in place of the basename: a trashed
	// file's original name; "" shows the basename.
	Label string
}

// Name is the entry's shown name: its label, else its basename.
func (e Entry) Name() string {
	if e.Label != "" {
		return e.Label
	}
	return filepath.Base(e.Path)
}

// Kind is the displayed type: Folder, the uppercased extension (PNG),
// or File without one.
func (e Entry) Kind() string {
	if e.IsDir {
		return "Folder"
	}
	if ext := strings.TrimPrefix(filepath.Ext(e.Name()), "."); ext != "" {
		return strings.ToUpper(ext)
	}
	return "File"
}

// statEntry stats path (following symlinks) into an Entry; a folder
// pick has no use for files.
func statEntry(path string, mode Mode) (Entry, bool) {
	e := Entry{Path: path}
	if info, err := os.Stat(path); err == nil {
		e.IsDir, e.Size, e.Modified = info.IsDir(), info.Size(), info.ModTime()
	}
	if !e.IsDir && mode.folders() {
		return Entry{}, false
	}
	return e, true
}

// ListDir lists dir in full. Only the mode rule applies here, because
// it cannot change while the listing is cached; the hidden toggle, the
// type filter and the search text partition the cached listing. A
// directory that cannot be read is an error.
func ListDir(dir string, mode Mode) ([]Entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(des))
	for _, de := range des {
		if e, ok := statEntry(filepath.Join(dir, de.Name()), mode); ok {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

// hiddenNames are the names dir's .hidden file lists, one a line, which
// GTK hides with the dot files; none without one.
func hiddenNames(dir string) map[string]bool {
	data, err := os.ReadFile(filepath.Join(dir, ".hidden")) //nolint:gosec // a folder's own .hidden list
	if err != nil {
		return nil
	}
	names := map[string]bool{}
	for line := range strings.SplitSeq(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names[line] = true
		}
	}
	return names
}

// hiddenName is GTK's rule for a name in a folder whose .hidden lists
// listed: a dot name, a ~ backup, or one listed.
func hiddenName(name string, listed map[string]bool) bool {
	return strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") || listed[name]
}

// searchEntries stats a recursive search's matches.
func searchEntries(paths []string, mode Mode) []Entry {
	var entries []Entry
	for _, p := range paths {
		if e, ok := statEntry(p, mode); ok {
			entries = append(entries, e)
		}
	}
	return entries
}

// The recursive search's bounds: at most searchCap matches, handed
// over searchBatch at a time so the first rows appear at once without
// a message per file.
const (
	searchCap   = 1000
	searchBatch = 64
)

// WalkSearch walks root depth first, handing every path whose name
// matches query to emit in batches as they are found, and returns the
// match count. Hidden entries (GTK's rule) are skipped, and their
// subtrees not entered, unless showHidden; symlinked directories are
// not entered, which keeps the walk out of loops. A cancelled ctx
// stops the walk, mid-directory too.
func WalkSearch(ctx context.Context, root, query string, showHidden bool, emit func([]string)) int {
	match := newTextFilter(query)
	var batch []string
	found := 0
	flush := func() {
		if len(batch) > 0 {
			emit(batch)
			batch = nil
		}
	}
	stack := []string{root}
walk:
	for len(stack) > 0 && found < searchCap {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		des, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		var listed map[string]bool
		if !showHidden {
			listed = hiddenNames(dir)
		}
		for _, de := range des {
			if ctx.Err() != nil {
				return found
			}
			name := de.Name()
			if !showHidden && hiddenName(name, listed) {
				continue
			}
			path := filepath.Join(dir, name)
			if match.matches(name) {
				batch = append(batch, path)
				found++
				if len(batch) >= searchBatch {
					flush()
				}
				if found >= searchCap {
					break walk
				}
			}
			if de.IsDir() {
				stack = append(stack, path)
			}
		}
	}
	if ctx.Err() == nil {
		flush()
	}
	return found
}

// SortColumn is a list view column.
type SortColumn uint8

// The sortable columns.
const (
	SortName SortColumn = iota
	SortSize
	SortModified
	SortKind
)

var sortNames = [...]string{"name", "size", "modified", "kind"}

// SortEntries orders directories first, then by the column.
func SortEntries(entries []Entry, key SortColumn, asc bool) {
	byName := func(a, b Entry) int { return strings.Compare(strings.ToLower(a.Name()), strings.ToLower(b.Name())) }
	slices.SortStableFunc(entries, func(a, b Entry) int {
		if a.IsDir != b.IsDir {
			if a.IsDir {
				return -1
			}
			return 1
		}
		var c int
		switch key {
		case SortSize:
			c = cmp.Compare(a.Size, b.Size)
		case SortModified:
			c = a.Modified.Compare(b.Modified)
		case SortKind:
			c = cmp.Or(strings.Compare(strings.ToLower(a.Kind()), strings.ToLower(b.Kind())), byName(a, b))
		default:
			c = byName(a, b)
		}
		if !asc {
			c = -c
		}
		return c
	})
}

// searchRank scores a recursive hit, lower first: an exact name before
// a prefix before a substring, then shallower under root, then the
// earlier the match falls in the name.
func searchRank(e Entry, root, query string) [3]int {
	name := strings.ToLower(e.Name())
	class := 2
	switch {
	case name == query:
		class = 0
	case strings.HasPrefix(name, query):
		class = 1
	}
	rel, err := filepath.Rel(root, e.Path)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = e.Path
	}
	depth := len(strings.Split(strings.Trim(rel, "/"), "/"))
	pos := strings.Index(name, query)
	if pos < 0 {
		pos = 1 << 30
	}
	return [3]int{class, depth, pos}
}

// SortSearch orders recursive hits by relevance, then by path so the
// order is stable.
func SortSearch(entries []Entry, root, query string) {
	q := strings.ToLower(query)
	slices.SortFunc(entries, func(a, b Entry) int {
		ra, rb := searchRank(a, root, q), searchRank(b, root, q)
		return cmp.Or(slices.Compare(ra[:], rb[:]), strings.Compare(a.Path, b.Path))
	})
}

// FilterLabel is a filter's name. Apps that want the patterns seen put
// them in the name; GTK's case-insensitive suffix globs
// (*.[pP][nN][gG]) would read as noise. Only a nameless filter shows
// its patterns, MIME types as their *.ext.
func FilterLabel(f Filter) string {
	if strings.TrimSpace(f.Name) != "" {
		return f.Name
	}
	var tokens []string
	for _, r := range f.Rules {
		token := r.Value
		if r.Kind == RuleMIME {
			if ext := mimeExt(r.Value); ext != "" {
				token = "*." + ext
			}
		}
		if !slices.Contains(tokens, token) {
			tokens = append(tokens, token)
		}
	}
	return strings.Join(tokens, ", ")
}

// mimeExts are the canonical extensions of the types apps put in
// file chooser filters (the Rust host's mime2ext table, with its media
// overrides); shared-mime-info lists extensions in no useful order.
var mimeExts = map[string]string{
	"application/epub+zip":                            "epub",
	"application/gzip":                                "gz",
	"application/javascript":                          "js",
	"application/json":                                "json",
	"application/msword":                              "doc",
	"application/ogg":                                 "ogx",
	"application/pdf":                                 "pdf",
	"application/rtf":                                 "rtf",
	"application/toml":                                "toml",
	"application/vnd.ms-excel":                        "xls",
	"application/vnd.ms-powerpoint":                   "ppt",
	"application/vnd.oasis.opendocument.presentation": "odp",
	"application/vnd.oasis.opendocument.spreadsheet":  "ods",
	"application/vnd.oasis.opendocument.text":         "odt",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": "pptx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         "xlsx",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   "docx",
	"application/x-7z-compressed":                                               "7z",
	"application/x-bzip2":                                                       "bz2",
	"application/x-tar":                                                         "tar",
	"application/x-xz":                                                          "xz",
	"application/xml":                                                           "xml",
	"application/yaml":                                                          "yaml",
	"application/zip":                                                           "zip",
	"application/zstd":                                                          "zst",
	"audio/aac":                                                                 "aac",
	"audio/flac":                                                                "flac",
	"audio/mp3":                                                                 "mp3",
	"audio/mp4":                                                                 "m4a",
	"audio/mpeg":                                                                "mp3",
	"audio/ogg":                                                                 "oga",
	"audio/opus":                                                                "opus",
	"audio/wav":                                                                 "wav",
	"audio/webm":                                                                "weba",
	"audio/x-flac":                                                              "flac",
	"audio/x-wav":                                                               "wav",
	"font/otf":                                                                  "otf",
	"font/ttf":                                                                  "ttf",
	"font/woff":                                                                 "woff",
	"font/woff2":                                                                "woff2",
	"image/avif":                                                                "avif",
	"image/bmp":                                                                 "bmp",
	"image/gif":                                                                 "gif",
	"image/heic":                                                                "heic",
	"image/jpeg":                                                                "jpg",
	"image/jxl":                                                                 "jxl",
	"image/png":                                                                 "png",
	"image/svg+xml":                                                             "svg",
	"image/tiff":                                                                "tiff",
	"image/vnd.microsoft.icon":                                                  "ico",
	"image/webp":                                                                "webp",
	"image/x-icon":                                                              "ico",
	"text/calendar":                                                             "ics",
	"text/css":                                                                  "css",
	"text/csv":                                                                  "csv",
	"text/html":                                                                 "html",
	"text/javascript":                                                           "js",
	"text/markdown":                                                             "md",
	"text/plain":                                                                "txt",
	"text/vcard":                                                                "vcf",
	"text/xml":                                                                  "xml",
	"video/mp2t":                                                                "ts",
	"video/mp4":                                                                 "mp4",
	"video/mpeg":                                                                "mpeg",
	"video/ogg":                                                                 "ogv",
	"video/quicktime":                                                           "mov",
	"video/webm":                                                                "webm",
	"video/x-matroska":                                                          "mkv",
	"video/x-msvideo":                                                           "avi",
}

// mimeExt is a type's canonical extension, "" for a wildcard or an
// unknown type (the label then shows the type itself).
func mimeExt(mimeType string) string { return mimeExts[mimeType] }

// matchesEverything reports a * or *.* glob, so no redundant "All
// Files" is added.
func matchesEverything(f Filter) bool {
	return slices.ContainsFunc(f.Rules, func(r Rule) bool {
		return r.Kind == RuleGlob && (r.Value == "*" || r.Value == "*.*")
	})
}

// filterExt is the extension a filter names when it names one: a
// single *.ext glob (GTK's case classes read back as lower case), or a
// single MIME type with a canonical one; "" otherwise.
func filterExt(f Filter) string {
	if len(f.Rules) != 1 {
		return ""
	}
	r := f.Rules[0]
	if r.Kind == RuleMIME {
		return mimeExt(r.Value)
	}
	ext, ok := strings.CutPrefix(r.Value, "*.")
	if !ok || ext == "" {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(ext); i++ {
		switch {
		case ext[i] == '[' && i+3 < len(ext) && ext[i+3] == ']':
			b.WriteByte(strings.ToLower(ext[i+1 : i+2])[0])
			i += 3
		case strings.ContainsRune("*?[]", rune(ext[i])):
			return ""
		default:
			b.WriteByte(ext[i])
		}
	}
	return b.String()
}

// withExtension gives name the filter's extension in place of its own;
// a name or filter without one is left as it is.
func withExtension(name string, f Filter) string {
	ext := filterExt(f)
	if name == "" || ext == "" {
		return name
	}
	return strings.TrimSuffix(name, filepath.Ext(name)) + "." + ext
}

// allFiles is the escape hatch appended after a request's filters.
var allFiles = Filter{Name: "All Files", Rules: []Rule{{RuleGlob, "*"}}}

// withAllFiles appends All Files to a non-empty filter list that has
// no catch-all, so a restrictive app filter can always be bypassed.
func withAllFiles(filters []Filter) []Filter {
	if len(filters) == 0 || slices.ContainsFunc(filters, matchesEverything) {
		return filters
	}
	return append(slices.Clip(filters), allFiles)
}

// MatchesFilter reports whether name passes the active filter: no
// filters, an out-of-range index or an empty rule set pass everything;
// otherwise any one rule must match, a glob by pattern, a MIME rule
// against the type guessed from the name.
func MatchesFilter(db *mime.Database, name string, filters []Filter, active int) bool {
	if active < 0 || active >= len(filters) || len(filters[active].Rules) == 0 {
		return true
	}
	return slices.ContainsFunc(filters[active].Rules, func(r Rule) bool {
		if r.Kind == RuleMIME {
			return matchesMIME(db, name, r.Value)
		}
		return matchGlob(name, r.Value)
	})
}

// matchGlob matches a glob rule case-insensitively with full * ? [..]
// semantics: GTK 4's add_suffix arrives as a class glob
// (*.[pP][nN][gG]), so classes are not optional. An unparseable
// pattern compares as an exact name, never matching everything.
func matchGlob(name, pattern string) bool {
	p, ok := glob.Compile(pattern)
	if !ok {
		return strings.EqualFold(name, pattern)
	}
	return p.Matches(name, false)
}

// matchesMIME checks the type guessed from name: a type/* rule by
// prefix, else exact or a subtype.
func matchesMIME(db *mime.Database, name, rule string) bool {
	guessed := db.TypeByName(name)
	if prefix, ok := strings.CutSuffix(rule, "*"); ok {
		return strings.HasPrefix(guessed, prefix)
	}
	return db.IsA(guessed, rule)
}

// textFilter is the search box's predicate over a basename: a
// case-insensitive substring, or a glob when the query carries * ? or [.
type textFilter struct {
	needle  string
	pattern *glob.Pattern
}

func newTextFilter(query string) textFilter {
	f := textFilter{needle: strings.ToLower(query)}
	if strings.ContainsAny(query, "*?[") {
		if p, ok := glob.Compile(query); ok {
			f.pattern = &p
		}
	}
	return f
}

func (f textFilter) matches(name string) bool {
	switch {
	case f.needle == "":
		return true
	case f.pattern != nil:
		return f.pattern.Matches(name, false)
	}
	return strings.Contains(strings.ToLower(name), f.needle)
}

// HumanSize is a byte count in binary units (1.5 KB).
func HumanSize(n int64) string {
	units := [...]string{"B", "KB", "MB", "GB", "TB"}
	size, unit := float64(n), 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", size, units[unit])
}

// FormatTime is a modification time in local time, "" for none.
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

// rasterExts are the image types thumbnailed (imagedecode's formats).
var rasterExts = []string{"png", "jpg", "jpeg", "gif", "webp", "bmp", "tiff", "tif"}

// isRaster reports a thumbnailable image by extension.
func isRaster(path string) bool {
	return slices.Contains(rasterExts, strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")))
}

// previewInfo is the preview card's detail line.
func previewInfo(e Entry) string {
	if e.IsDir {
		return "Folder · " + FormatTime(e.Modified)
	}
	kind := "File"
	if k := e.Kind(); k != "File" {
		kind = k + " file"
	}
	return kind + " · " + HumanSize(e.Size) + " · " + FormatTime(e.Modified)
}

// Place is a sidebar shortcut.
type Place struct {
	Label, Path, Icon string
}

// userPlaceDirs are the user directories offered when present: the
// user-dirs.dirs key, the name under home when it sets none, the icon.
var userPlaceDirs = []struct{ key, name, icon string }{
	{"DESKTOP", "Desktop", "user-desktop-symbolic"},
	{"DOCUMENTS", "Documents", "folder-documents-symbolic"},
	{"DOWNLOAD", "Downloads", "folder-download-symbolic"},
	{"MUSIC", "Music", "folder-music-symbolic"},
	{"PICTURES", "Pictures", "folder-pictures-symbolic"},
	{"VIDEOS", "Videos", "folder-videos-symbolic"},
}

// UserPlaces is Home, the user directories that exist (where
// user-dirs.dirs puts them, under their localized names), then the GTK
// bookmarks.
func UserPlaces(home string) []Place {
	if home == "" {
		return nil
	}
	places := []Place{{"Home", home, "user-home-symbolic"}}
	for _, d := range userPlaceDirs {
		path, ok := xdg.UserDir(d.key)
		if !ok {
			path = filepath.Join(home, d.name)
		}
		// user-dirs.dirs points a directory it disables at home.
		if path == home {
			continue
		}
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			places = append(places, Place{filepath.Base(path), path, d.icon})
		}
	}
	if f, err := os.Open(gtkBookmarksPath(home)); err == nil {
		defer func() { _ = f.Close() }()
		places = append(places, Bookmarks(f)...)
	}
	return places
}

// gtkBookmarksPath is GTK 3's bookmarks file, which GTK 4 reads too.
func gtkBookmarksPath(home string) string {
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = filepath.Join(home, ".config")
	}
	return filepath.Join(config, "gtk-3.0", "bookmarks")
}

// Bookmarks are the local folders a GTK bookmarks file lists, one
// "<uri> [label]" a line, labelled by their label else their name;
// remote ones and folders that are gone are left out.
func Bookmarks(r io.Reader) []Place {
	var places []Place
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		uri, label, _ := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		path, ok := fileuri.Path(uri)
		if !ok {
			continue
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			continue
		}
		if label = strings.TrimSpace(label); label == "" {
			label = filepath.Base(path)
		}
		places = append(places, Place{label, path, "folder-symbolic"})
	}
	return places
}

// recentLocation is the Recent place's directory: not a path, the
// listing of the recently used files.
const recentLocation = "recent:"

// maxRecent bounds the Recent listing to the newest files.
const maxRecent = 100

// RecentFile is the recently-used list GTK writes,
// $XDG_DATA_HOME/recently-used.xbel; "" without a data home.
func RecentFile() string {
	data, ok := xdg.DataHome()
	if !ok {
		return ""
	}
	return filepath.Join(data, "recently-used.xbel")
}

// xbel is the part of a recently-used.xbel the listing reads.
type xbel struct {
	Bookmarks []xbelBookmark `xml:"bookmark"`
}

// xbelBookmark is one used file: its URI and when it was last used
// (an ISO 8601 UTC stamp, which sorts as text).
type xbelBookmark struct {
	Href     string `xml:"href,attr"`
	Modified string `xml:"modified,attr"`
}

// ListRecent lists the recently used local files that still exist,
// the newest maxRecent of them; an unreadable list lists none.
func ListRecent(path string, mode Mode) ([]Entry, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the user's recently-used list
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var doc xbel
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	slices.SortStableFunc(doc.Bookmarks, func(a, b xbelBookmark) int { return strings.Compare(b.Modified, a.Modified) })
	var entries []Entry
	for _, b := range doc.Bookmarks {
		p, ok := fileuri.Path(b.Href)
		if !ok {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if e, ok := statEntry(p, mode); ok && !e.IsDir {
			entries = append(entries, e)
			if len(entries) == maxRecent {
				break
			}
		}
	}
	return entries, nil
}

// OtherLocations is Computer (/) plus the user-visible mounts in a
// /proc/self/mounts listing: what gio's volume monitor shows for unix
// mounts, those under /media, /run/media/<user> and the home
// directory (not hidden ones).
func OtherLocations(mounts io.Reader, home, user string) []Place {
	locations := []Place{{"Computer", "/", "drive-harddisk-symbolic"}}
	for _, path := range VisibleMounts(mounts, home, user) {
		locations = append(locations, Place{filepath.Base(path), path, "drive-removable-media-symbolic"})
	}
	return locations
}

// VisibleMounts are the user-visible mount points in a
// /proc/self/mounts listing, each once, in its order; none for a nil
// listing.
func VisibleMounts(mounts io.Reader, home, user string) []string {
	if mounts == nil {
		return nil
	}
	var paths []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(mounts)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		path := unescapeMount(fields[1])
		if seen[path] || !userVisibleMount(path, home, user) {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	return paths
}

// NetworkLocations are the network shares gvfs mounted, as its FUSE
// daemon shows them under runtimeDir/gvfs, one folder each named
// "<type>:<key>=<value>,...": labelled by share and server, else by
// user and host; none without gvfsd-fuse running.
func NetworkLocations(runtimeDir string) []Place {
	if runtimeDir == "" {
		return nil
	}
	dir := filepath.Join(runtimeDir, "gvfs")
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var places []Place
	for _, de := range des {
		if de.IsDir() {
			places = append(places, Place{gvfsLabel(de.Name()), filepath.Join(dir, de.Name()), "folder-remote-symbolic"})
		}
	}
	return places
}

// gvfsLabel names a gvfs mount from its "<type>:<key>=<value>,..."
// folder name: "share on server", "user@host", "host", else the name.
func gvfsLabel(name string) string {
	_, params, ok := strings.Cut(name, ":")
	if !ok {
		return name
	}
	kv := map[string]string{}
	for pair := range strings.SplitSeq(params, ",") {
		if k, v, ok := strings.Cut(pair, "="); ok {
			if u, err := url.PathUnescape(v); err == nil {
				v = u
			}
			kv[k] = v
		}
	}
	switch {
	case kv["share"] != "" && kv["server"] != "":
		return kv["share"] + " on " + kv["server"]
	case kv["host"] != "" && kv["user"] != "":
		return kv["user"] + "@" + kv["host"]
	case kv["host"] != "":
		return kv["host"]
	case kv["server"] != "":
		return kv["server"]
	}
	return name
}

// userVisibleMount is g_unix_mount_guess_should_display's path rule.
func userVisibleMount(path, home, user string) bool {
	under := func(dir string) bool {
		rest, ok := strings.CutPrefix(path, dir+"/")
		return ok && rest != "" && !strings.HasPrefix(rest, ".") && !strings.Contains(rest, "/")
	}
	if under("/media") || (user != "" && under("/run/media/"+user)) {
		return true
	}
	if home != "" && home != "/" {
		rest, ok := strings.CutPrefix(path, home+"/")
		return ok && rest != "" && !strings.HasPrefix(rest, ".")
	}
	return false
}

// unescapeMount resolves /proc/mounts' octal escapes (\040 a space).
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			b.WriteByte((s[i+1]-'0')<<6 | (s[i+2]-'0')<<3 | (s[i+3] - '0'))
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

// Crumb is one breadcrumb button.
type Crumb struct {
	Label, Path string
	Current     bool
}

// maxCrumbTail is how many trailing segments stay visible; deeper
// ones collapse into one … crumb.
const maxCrumbTail = 3

// Crumbs is dir's breadcrumb trail: Home for the home directory (else
// /), its named segments, and past maxCrumbTail of them a … crumb
// standing for (and jumping to) the deepest collapsed one. Recent is
// one crumb.
func Crumbs(dir, home string) []Crumb {
	switch dir {
	case recentLocation:
		return []Crumb{{"Recent", recentLocation, true}}
	case trashLocation:
		return []Crumb{{"Trash", trashLocation, true}}
	}
	base, baseLabel := "/", "/"
	rest := strings.TrimPrefix(dir, "/")
	if home != "" && (dir == home || strings.HasPrefix(dir, strings.TrimSuffix(home, "/")+"/")) {
		base, baseLabel = home, "Home"
		rest = strings.TrimPrefix(strings.TrimPrefix(dir, home), "/")
	}
	var names []string
	for n := range strings.SplitSeq(rest, "/") {
		if n != "" {
			names = append(names, n)
		}
	}
	crumbs := []Crumb{{baseLabel, base, len(names) == 0}}
	acc := base
	collapse := max(0, len(names)-maxCrumbTail)
	for i, n := range names {
		acc = filepath.Join(acc, n)
		switch {
		case collapse > 0 && i+1 == collapse:
			crumbs = append(crumbs, Crumb{"…", acc, false})
		case i >= collapse:
			crumbs = append(crumbs, Crumb{n, acc, i+1 == len(names)})
		}
	}
	return crumbs
}

// view is the sticky view preference: the sort and the list or grid.
type view struct {
	sort SortColumn
	asc  bool
	grid bool
}

var defaultView = view{sort: SortName, asc: true}

// parseView reads "<sort> <asc|desc> <list|grid>", defaulting a
// missing or unknown token.
func parseView(text string) view {
	v := defaultView
	tokens := strings.Fields(text)
	tok := func(i int) string {
		if i < len(tokens) {
			return tokens[i]
		}
		return ""
	}
	if i := slices.Index(sortNames[:], tok(0)); i >= 0 {
		v.sort = SortColumn(i)
	}
	v.asc = tok(1) != "desc"
	v.grid = tok(2) == "grid"
	return v
}

// String is the persisted form parseView reads.
func (v view) String() string {
	dir, layout := "asc", "list"
	if !v.asc {
		dir = "desc"
	}
	if v.grid {
		layout = "grid"
	}
	return sortNames[v.sort] + " " + dir + " " + layout + "\n"
}
