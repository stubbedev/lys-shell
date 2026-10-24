// Package locales embeds the shell's Fluent partials: the bar, its
// dropdowns, the lock screen, the OSD and the notification surfaces.
package locales

import "embed"

// FS holds every locale directory (en-US, fr, ...).
//
//go:embed all:*
var FS embed.FS
