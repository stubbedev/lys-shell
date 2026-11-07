package wallpaper

import (
	"fmt"
	"image"

	"github.com/stubbedev/lys-lib/xdg"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/imagedecode"
	engine "github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/wallpaper"
)

// Picker is what the launcher's wallpaper picker sets wallpapers
// through: the running service for previews and the live view, and the
// config for what is kept.
type Picker struct {
	svc       *wallpaper.Service
	config    func() *config.Config
	setConfig func(path string, value any)
	pictures  func() string
	thumbs    engine.ImageThumbnails
}

// NewPicker serves the picker from the shell's service; setConfig queues
// a config change to apply and save.
func (s *Shell) NewPicker(cfg func() *config.Config, setConfig func(path string, value any)) *Picker {
	return &Picker{
		svc: s.svc, config: cfg, setConfig: setConfig, pictures: picturesDir,
		thumbs: engine.ImageThumbnails{Dir: engine.LargeThumbnailDir(), Decode: imagedecode.Open},
	}
}

// picturesDir is XDG_PICTURES_DIR, "" when the user has none.
func picturesDir() string {
	dir, ok, err := xdg.OS.UserDir("XDG_PICTURES_DIR")
	if err != nil || !ok {
		return ""
	}
	return dir
}

// Images lists the pictures of the picker's directories.
func (p *Picker) Images() ([]string, error) {
	return wallpaper.PickerImages(p.config().Wallpaper.PickerDirs(p.pictures()))
}

// Shown is the image monitor shows, "" for none.
func (p *Picker) Shown(monitor string) string {
	path, _ := p.svc.Wallpaper(monitor)
	return path
}

// Preview shows path on monitor ("" every monitor) without setting it.
func (p *Picker) Preview(path, monitor string) error { return p.svc.SetPreview(path, monitor) }

// EndPreview shows the set images again.
func (p *Picker) EndPreview() { p.svc.EndPreview() }

// Commit sets path on monitor, or every monitor when monitor is empty,
// and saves it: the monitor's own entry, or the global image with every
// entry's image cleared, in one config change.
func (p *Picker) Commit(path, monitor string) error {
	if err := p.svc.SetWallpaper(path, monitor); err != nil {
		return fmt.Errorf("setting the wallpaper: %w", err)
	}
	key, value := commitChange(p.config().Wallpaper, path, monitor)
	p.setConfig(key, value)
	return nil
}

// LoadThumbnail is path's large thumbnail, from the thumbnail cache or
// made and saved there.
func (p *Picker) LoadThumbnail(path string) (image.Image, error) {
	img, _, err := p.thumbs.Load(path)
	return img, err
}

// commitChange is the config change that keeps path on monitor ("" every
// monitor), as a path and a plain value for SetByPath.
func commitChange(cfg config.WallpaperConfig, path, monitor string) (string, any) {
	entries := make([]any, 0, len(cfg.Monitors)+1)
	found := false
	for _, m := range cfg.Monitors {
		wall := m.Wallpaper
		switch {
		case monitor == "":
			wall = ""
		case m.Name == monitor:
			wall, found = path, true
		}
		entries = append(entries, map[string]any{"name": m.Name, "fit-mode": string(m.FitMode), "wallpaper": wall})
	}
	if monitor == "" {
		return "wallpaper", map[string]any{"wallpaper": path, "monitors": entries}
	}
	if !found {
		entries = append(entries, map[string]any{"name": monitor, "fit-mode": string(cfg.FitMode), "wallpaper": path})
	}
	return "wallpaper.monitors", entries
}
