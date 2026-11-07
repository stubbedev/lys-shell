package config

import (
	"fmt"
	"slices"
)

// ColorPickerConfig is the colorpicker section: how the color picker
// writes what it picked.
//
// Color picker settings.
type ColorPickerConfig struct {
	// The format the picker's readout starts in, and the one a pick is
	// copied and printed in. Tab in the picker cycles the readout through
	// the formats; the one shown at the click is used, and kept for the
	// next pick.
	Format ColorFormat `cfg:"format"`
}

// DefaultsColorPicker returns the schema defaults.
func DefaultsColorPicker() ColorPickerConfig {
	return ColorPickerConfig{Format: ColorFormatHex}
}

// ColorFormat is how a picked color is written.
type ColorFormat string

// ColorFormat values.
const (
	// `#RRGGBB`.
	ColorFormatHex ColorFormat = "hex"
	// `rgb(r, g, b)`, each channel 0-255.
	ColorFormatRGB ColorFormat = "rgb"
	// `hsl(h, s%, l%)`.
	ColorFormatHSL ColorFormat = "hsl"
	// `hsv(h, s%, v%)`.
	ColorFormatHSV ColorFormat = "hsv"
	// `oklch(L% C h)`, as CSS Color 4 writes it.
	ColorFormatOKLCH ColorFormat = "oklch"
	// `cmyk(c%, m%, y%, k%)`, converted without a color profile.
	ColorFormatCMYK ColorFormat = "cmyk"
)

// ColorFormats are the formats in the order Tab cycles them.
var ColorFormats = []ColorFormat{ColorFormatHex, ColorFormatRGB, ColorFormatHSL, ColorFormatHSV, ColorFormatOKLCH, ColorFormatCMYK}

var _ = registerEnum(ColorFormats...)

// ParseColorFormat reads a format named on the command line or the bus.
func ParseColorFormat(s string) (ColorFormat, error) {
	f := ColorFormat(s)
	if !slices.Contains(ColorFormats, f) {
		names := make([]string, len(ColorFormats))
		for i, v := range ColorFormats {
			names[i] = string(v)
		}
		return "", fmt.Errorf("unknown color format %q, %s", s, expectedOneOf(names))
	}
	return f, nil
}

// Next is the format after f in ColorFormats, wrapping around.
func (f ColorFormat) Next() ColorFormat {
	return ColorFormats[(slices.Index(ColorFormats, f)+1)%len(ColorFormats)]
}
