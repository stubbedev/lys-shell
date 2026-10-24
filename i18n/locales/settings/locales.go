// Package locales embeds the settings app's and the config schema's
// Fluent partials.
package locales

import "embed"

// FS holds every locale directory (en-US, fr, ...).
//
//go:embed all:*
var FS embed.FS
