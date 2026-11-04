// Package locales embeds the config schema's Fluent partials, which
// lys-settings reads through i18n.ConfigAssets.
package locales

import "embed"

// FS holds every locale directory (en-US, fr, ...).
//
//go:embed all:*
var FS embed.FS
