package styling

import (
	"crypto/sha256"
	_ "embed" // the compiled stylesheet
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// StaticCSS is the compiled stylesheet: scss/main.scss through grass.
// It is a checked-in build artifact rather than a build step because
// the build is pure Go: `just css` regenerates it, and
// TestStaticCSSIsFresh fails when the SCSS moved on without it.
//
//go:embed static.css
var StaticCSS string

// scssDir is the SCSS source tree, relative to this package.
const scssDir = "scss"

// staticSumFile records the SCSS tree hash StaticCSS was compiled from.
const staticSumFile = "static.css.sum"

// scssTreeHash hashes every .scss file under dir: sorted relative paths
// and contents, so any edit, addition, removal, or rename changes it.
func scssTreeHash(dir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".scss") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	slices.Sort(files)
	h := sha256.New()
	for _, f := range files {
		rel, err := filepath.Rel(dir, f)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(f) //nolint:gosec // the repo's own SCSS tree
		if err != nil {
			return "", err
		}
		h.Write([]byte(filepath.ToSlash(rel)))
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
