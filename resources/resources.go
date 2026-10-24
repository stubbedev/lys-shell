// Package resources embeds the repository's bundled assets: the
// symbolic icons `wayle icons setup` installs.
package resources

import "embed"

// Icons holds icons/hicolor/scalable/actions/*.svg.
//
//go:embed icons/hicolor/scalable/actions/*.svg
var Icons embed.FS

// IconsDir is the directory inside Icons.
const IconsDir = "icons/hicolor/scalable/actions"
