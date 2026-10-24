// Package locales embeds the greeter's Fluent file.
package locales

import "embed"

// FS holds every locale directory (en-US, fr, ...).
//
//go:embed all:*
var FS embed.FS
