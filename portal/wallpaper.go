package portal

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/internal/fileuri"
	"github.com/stubbedev/lys-shell/service/wallpaper"
	"github.com/stubbedev/lys-shell/shell/portaldialogs"
)

// wallpaperPortal maps SetWallpaperURI onto the shell's
// dev.stubbe.lys.Wallpaper1 (every monitor) and the lock screen's
// background; only file:// URIs are supported.
type wallpaperPortal struct {
	conn *dbus.Conn
	cfg  *config.Service
	// dir is where the images are kept, "" without a data directory.
	dir string
}

func wallpaperIface(conn *dbus.Conn, cfg *config.Service, dataDir string) dbusx.Interface {
	w := wallpaperPortal{conn: conn, cfg: cfg}
	if dataDir != "" {
		w.dir = filepath.Join(dataDir, "wallpapers")
	}
	return dbusx.Interface{
		Name:    "org.freedesktop.impl.portal.Wallpaper",
		Methods: w,
	}
}

// wallpaperTarget is set-on: where the picture goes.
type wallpaperTarget struct{ desktop, lockScreen bool }

// parseSetOn reads set-on; absent, the picture goes on the desktop
// only, as the app did not ask for the lock screen.
func parseSetOn(options Vardict) (wallpaperTarget, error) {
	v, ok := options["set-on"]
	if !ok {
		return wallpaperTarget{desktop: true}, nil
	}
	switch s, _ := v.Value().(string); s {
	case "background":
		return wallpaperTarget{desktop: true}, nil
	case "lockscreen":
		return wallpaperTarget{lockScreen: true}, nil
	case "both":
		return wallpaperTarget{desktop: true, lockScreen: true}, nil
	}
	return wallpaperTarget{}, fmt.Errorf("set-on %v is not background, lockscreen or both", v)
}

// SetWallpaperURI sets the picture where set-on says, after the
// preview dialog when show-preview asks for one. The picture is copied
// into lys-shell's data directory first: the path an app passes may be
// a document-portal file that goes away with it.
func (w wallpaperPortal) SetWallpaperURI(handle dbus.ObjectPath, appID, parent, uri string, options Vardict) (uint32, *dbus.Error) {
	path, ok := fileuri.Path(uri)
	if !ok {
		warnf("wallpaper: only file:// URIs are supported: %s", uri)
		return ResponseOther, nil
	}
	target, err := parseSetOn(options)
	if err != nil {
		warnf("wallpaper: %s: %v", appID, err)
		return ResponseOther, nil
	}
	dialogs := portaldialogs.NewClient(w.conn)
	c, ok := beginOr(w.conn, handle, appID, parent, dialogs.Dismiss)
	if !ok {
		return ResponseOther, nil
	}
	defer c.end()
	if preview, _ := optBool(options, "show-preview"); preview {
		set, err := dialogs.ConfirmWallpaper(c.ctx, c.caller, uri)
		if code := c.response("wallpaper preview", set, err); code != ResponseSuccess {
			return code, nil
		}
	}
	if w.dir == "" {
		warnf("wallpaper: no data directory to keep the picture in (XDG_DATA_HOME and HOME unset)")
		return ResponseOther, nil
	}
	kept, err := keepPicture(w.dir, path)
	if err != nil {
		warnf("wallpaper: %v", err)
		return ResponseOther, nil
	}
	if target.desktop {
		if err := wallpaper.NewClient(w.conn).SetWallpaper(c.ctx, kept, ""); err != nil {
			warnf("wallpaper: set_wallpaper failed: %v", err)
			return ResponseOther, nil
		}
	}
	if target.lockScreen {
		if err := w.setLockScreen(kept); err != nil {
			warnf("wallpaper: %v", err)
			return ResponseOther, nil
		}
	}
	return ResponseSuccess, nil
}

// setLockScreen makes the picture the lock screen's background and
// saves it as a runtime override.
func (w wallpaperPortal) setLockScreen(path string) error {
	if err := w.cfg.SetByPath("login.background-image", path); err != nil {
		return fmt.Errorf("set the lock screen's picture: %w", err)
	}
	if err := w.cfg.SetByPath("login.background-mode", string(config.BackgroundImage)); err != nil {
		return fmt.Errorf("set the lock screen's background mode: %w", err)
	}
	if err := w.cfg.Save(); err != nil {
		return fmt.Errorf("save the lock screen's background: %w", err)
	}
	return nil
}

// keepPicture copies the picture at path into dir, named for its
// content (so setting the same picture twice keeps one copy) with its
// extension, through a temporary file renamed into place; it returns
// the copy's path.
func keepPicture(dir, path string) (string, error) {
	src, err := os.Open(path) //nolint:gosec // the picture the frontend resolved for the app
	if err != nil {
		return "", fmt.Errorf("open the picture: %w", err)
	}
	defer func() { _ = src.Close() }()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".picture.tmp.*")
	if err != nil {
		return "", fmt.Errorf("create a temporary file in %s: %w", dir, err)
	}
	sum := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, sum), src)
	err = errors.Join(err, tmp.Sync(), tmp.Close())
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("copy %s: %w", path, err)
	}
	kept := filepath.Join(dir, hex.EncodeToString(sum.Sum(nil))+strings.ToLower(filepath.Ext(path)))
	if err := os.Rename(tmp.Name(), kept); err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("move the copy of %s into place: %w", path, err)
	}
	return kept, nil
}
