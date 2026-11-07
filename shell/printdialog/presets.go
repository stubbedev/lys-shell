package printdialog

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"slices"
	"sync"

	"github.com/stubbedev/lys-shell/internal/statefile"
)

// maxPresets is how many (app, printer) pairs' last forms are kept.
const maxPresets = 64

// documentKeys are the settings that belong to one document and are
// not carried to the next: the printer, which pages, the output file.
var documentKeys = []string{"printer", "print-pages", "page-ranges", "output-uri", "output-file-format"}

// preset is the last form an app confirmed for a printer, as its
// settings.
type preset struct {
	App      string    `json:"app"`
	Printer  string    `json:"printer"`
	Settings []Setting `json:"settings"`
}

// presets are the last forms confirmed, the most recent first, kept in
// a file; read and written under its lock from the loop and the
// requests' goroutines.
type presets struct {
	mu   sync.Mutex
	list []preset
	file statefile.File
}

// loadPresets reads the presets kept in path; a file that is not there
// keeps none, and one that does not parse is logged and starts empty.
func loadPresets(path string) *presets {
	p := &presets{file: statefile.File{Path: path}}
	if path == "" {
		return p
	}
	data, err := os.ReadFile(path) //nolint:gosec // the dialog's own state file
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("print: reading %s: %v", path, err)
		}
		return p
	}
	if err := json.Unmarshal(data, &p.list); err != nil {
		log.Printf("print: reading %s: %v", path, err)
		p.list = nil
	}
	return p
}

// settings are the settings app last confirmed for printer, none for a
// pair never seen.
func (p *presets) settings(app, printer string) []Setting {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.list {
		if e.App == app && e.Printer == printer {
			return slices.Clone(e.Settings)
		}
	}
	return nil
}

// remember keeps settings, the document's own left out, as app's last
// form for printer, and saves the presets.
func (p *presets) remember(app, printer string, settings []Setting) {
	kept := slices.DeleteFunc(slices.Clone(settings), func(s Setting) bool { return slices.Contains(documentKeys, s.Key) })
	p.mu.Lock()
	list := make([]preset, 0, maxPresets)
	list = append(list, preset{App: app, Printer: printer, Settings: kept})
	for _, e := range p.list {
		if (e.App != app || e.Printer != printer) && len(list) < maxPresets {
			list = append(list, e)
		}
	}
	p.list = list
	data, err := json.Marshal(list)
	p.mu.Unlock()
	if err != nil {
		log.Printf("print: encoding the presets: %v", err)
		return
	}
	p.file.Write(data)
}

// withPreset is r with app's last form for printer under its own
// settings, which win where both name a key.
func (p *presets) withPreset(r PrepareRequest, app, printer string) PrepareRequest {
	saved := p.settings(app, printer)
	if len(saved) == 0 {
		return r
	}
	merged := slices.DeleteFunc(saved, func(s Setting) bool {
		return slices.ContainsFunc(r.Settings, func(o Setting) bool { return o.Key == s.Key })
	})
	r.Settings = append(merged, r.Settings...)
	return r
}
