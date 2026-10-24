package styling

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
)

// userStylesTemplate is the starter index.css.
const userStylesTemplate = "/* Custom lys-shell styles. Anything here overrides the built-in styling. */\n"

// UserStylesFile is the user's stylesheet under <configDir>/styles/.
const UserStylesFile = "index.css"

// LegacyUserStylesFile is the SCSS entry lys-shell read before its
// styles became plain CSS.
const LegacyUserStylesFile = "index.scss"

// UserCSS is the user's optional overrides at
// <configDir>/styles/index.css. A failure is logged and yields "", so
// a broken user stylesheet leaves the rest of the bundle intact.
func UserCSS(configDir string) string {
	css, err := TryUserCSS(configDir)
	if err != nil {
		log.Printf("styling: user styles: %v", err)
		return ""
	}
	return css
}

// TryUserCSS is UserCSS surfacing the failure. ("", nil) means the file
// does not exist. A leftover styles/index.scss is an error naming the
// CSS file that replaces it, so its rules never silently stop applying.
func TryUserCSS(configDir string) (string, error) {
	stylesDir := filepath.Join(configDir, "styles")
	legacy := filepath.Join(stylesDir, LegacyUserStylesFile)
	if _, err := os.Stat(legacy); err == nil {
		return "", fmt.Errorf("%s is SCSS, which lys-shell no longer reads: move its rules, as plain CSS, to %s",
			legacy, filepath.Join(stylesDir, UserStylesFile))
	}
	entry := filepath.Join(stylesDir, UserStylesFile)
	data, err := os.ReadFile(entry) //nolint:gosec // the user's own stylesheet
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", entry, err)
	}
	return string(data), nil
}

// UserStylesDir returns <configDir>/styles/ when it is a directory.
func UserStylesDir(configDir string) (string, bool) {
	dir := filepath.Join(configDir, "styles")
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir, true
	}
	return "", false
}

// EnsureUserStylesScaffold creates <configDir>/styles/ and a starter
// index.css when either is missing. It is a no-op once both exist, and
// never overwrites the user's file; failures are logged, not returned,
// so scaffolding never blocks startup.
func EnsureUserStylesScaffold(configDir string) {
	stylesDir := filepath.Join(configDir, "styles")
	if err := os.MkdirAll(stylesDir, 0o755); err != nil { //nolint:gosec // the config dir is the user's own
		log.Printf("styling: cannot create user styles directory %s: %v", stylesDir, err)
		return
	}
	entry := filepath.Join(stylesDir, UserStylesFile)
	if _, err := os.Stat(entry); err == nil {
		return
	}
	if err := os.WriteFile(entry, []byte(userStylesTemplate), 0o644); err != nil { //nolint:gosec // a user-editable stylesheet, readable like the rest of the config
		log.Printf("styling: cannot create user styles/%s %s: %v", UserStylesFile, entry, err)
	}
}
