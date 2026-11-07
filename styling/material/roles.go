package material

// Role is a named color of a dynamic scheme.
type Role uint8

// The scheme roles, named as matugen's JSON names them.
const (
	RolePrimaryPaletteKeyColor Role = iota
	RoleSecondaryPaletteKeyColor
	RoleTertiaryPaletteKeyColor
	RoleNeutralPaletteKeyColor
	RoleNeutralVariantPaletteKeyColor
	RoleErrorPaletteKeyColor
	RoleBackground
	RoleOnBackground
	RoleSurface
	RoleSurfaceDim
	RoleSurfaceBright
	RoleSurfaceContainerLowest
	RoleSurfaceContainerLow
	RoleSurfaceContainer
	RoleSurfaceContainerHigh
	RoleSurfaceContainerHighest
	RoleOnSurface
	RoleSurfaceVariant
	RoleOnSurfaceVariant
	RoleInverseSurface
	RoleInverseOnSurface
	RoleOutline
	RoleOutlineVariant
	RoleShadow
	RoleScrim
	RoleSurfaceTint
	RolePrimary
	RoleOnPrimary
	RolePrimaryContainer
	RoleOnPrimaryContainer
	RoleInversePrimary
	RoleSecondary
	RoleOnSecondary
	RoleSecondaryContainer
	RoleOnSecondaryContainer
	RoleTertiary
	RoleOnTertiary
	RoleTertiaryContainer
	RoleOnTertiaryContainer
	RoleError
	RoleOnError
	RoleErrorContainer
	RoleOnErrorContainer
	RolePrimaryFixed
	RolePrimaryFixedDim
	RoleOnPrimaryFixed
	RoleOnPrimaryFixedVariant
	RoleSecondaryFixed
	RoleSecondaryFixedDim
	RoleOnSecondaryFixed
	RoleOnSecondaryFixedVariant
	RoleTertiaryFixed
	RoleTertiaryFixedDim
	RoleOnTertiaryFixed
	RoleOnTertiaryFixedVariant
	// RoleCount is the number of roles.
	RoleCount
)

// String is the role's snake_case name.
func (r Role) String() string { return roleSpecs[r].name }

type polarity uint8

const (
	nearer polarity = iota
	lighter
	darker
)

type toneDeltaPair struct {
	a, b         Role
	delta        float64
	polarity     polarity
	stayTogether bool
}

type roleSpec struct {
	name             string
	palette          paletteKind
	tone             func(*scheme) float64
	isBackground     bool
	background       func(*scheme) Role
	secondBackground func(*scheme) Role
	curve            contrastCurve
	pair             *toneDeltaPair
}

func fixed(r Role) func(*scheme) Role { return func(*scheme) Role { return r } }

func highestSurface(s *scheme) Role {
	if s.dark {
		return RoleSurfaceBright
	}
	return RoleSurfaceDim
}

func constTone(dark, light float64) func(*scheme) float64 {
	return func(s *scheme) float64 { return s.pick(dark, light) }
}

func monoTone(monoDark, monoLight, dark, light float64) func(*scheme) float64 {
	return func(s *scheme) float64 {
		if s.isMonochrome() {
			return s.pick(monoDark, monoLight)
		}
		return s.pick(dark, light)
	}
}

func monoFixedTone(mono, other float64) func(*scheme) float64 {
	return func(s *scheme) float64 {
		if s.isMonochrome() {
			return mono
		}
		return other
	}
}

func curveTone(dark, light contrastCurve) func(*scheme) float64 {
	return func(s *scheme) float64 {
		if s.dark {
			return dark.get(s.contrast)
		}
		return light.get(s.contrast)
	}
}

func darkCurveTone(dark contrastCurve, light float64) func(*scheme) float64 {
	return func(s *scheme) float64 {
		if s.dark {
			return dark.get(s.contrast)
		}
		return light
	}
}

func lightCurveTone(dark float64, light contrastCurve) func(*scheme) float64 {
	return func(s *scheme) float64 {
		if s.dark {
			return dark
		}
		return light.get(s.contrast)
	}
}

func keyColorTone(p paletteKind) func(*scheme) float64 {
	return func(s *scheme) float64 { return s.palettes[p].keyColor.Tone }
}

var (
	curveText        = contrastCurve{4.5, 7, 11, 21}
	curveSubtext     = contrastCurve{3, 4.5, 7, 11}
	curveAccent      = contrastCurve{3, 4.5, 7, 7}
	curveContainer   = contrastCurve{1, 1, 3, 4.5}
	curveBackground  = contrastCurve{3, 3, 4.5, 7}
	curveOutline     = contrastCurve{1.5, 3, 4.5, 7}
	curveOutlineVar  = contrastCurve{1, 1, 3, 4.5}
	pairPrimary      = &toneDeltaPair{RolePrimaryContainer, RolePrimary, 10, nearer, false}
	pairSecondary    = &toneDeltaPair{RoleSecondaryContainer, RoleSecondary, 10, nearer, false}
	pairTertiary     = &toneDeltaPair{RoleTertiaryContainer, RoleTertiary, 10, nearer, false}
	pairError        = &toneDeltaPair{RoleErrorContainer, RoleError, 10, nearer, false}
	pairPrimaryFix   = &toneDeltaPair{RolePrimaryFixed, RolePrimaryFixedDim, 10, lighter, true}
	pairSecondaryFix = &toneDeltaPair{RoleSecondaryFixed, RoleSecondaryFixedDim, 10, lighter, true}
	pairTertiaryFix  = &toneDeltaPair{RoleTertiaryFixed, RoleTertiaryFixedDim, 10, lighter, true}
)

func surfaceRole(name string, tone func(*scheme) float64) roleSpec {
	return roleSpec{name: name, palette: neutralPalette, tone: tone, isBackground: true}
}

func accentRole(name string, p paletteKind, tone func(*scheme) float64, curve contrastCurve, pair *toneDeltaPair) roleSpec {
	return roleSpec{name: name, palette: p, tone: tone, isBackground: true, background: highestSurface, curve: curve, pair: pair}
}

func onRole(name string, p paletteKind, tone func(*scheme) float64, bg Role, curve contrastCurve) roleSpec {
	return roleSpec{name: name, palette: p, tone: tone, background: fixed(bg), curve: curve}
}

func onFixedRole(name string, p paletteKind, tone func(*scheme) float64, dim, bright Role, curve contrastCurve) roleSpec {
	return roleSpec{name: name, palette: p, tone: tone, background: fixed(dim), secondBackground: fixed(bright), curve: curve}
}

var roleSpecs [RoleCount]roleSpec

func init() {
	roleSpecs = [RoleCount]roleSpec{
		RolePrimaryPaletteKeyColor:        {name: "primary_palette_key_color", palette: primaryPalette, tone: keyColorTone(primaryPalette)},
		RoleSecondaryPaletteKeyColor:      {name: "secondary_palette_key_color", palette: secondaryPalette, tone: keyColorTone(secondaryPalette)},
		RoleTertiaryPaletteKeyColor:       {name: "tertiary_palette_key_color", palette: tertiaryPalette, tone: keyColorTone(tertiaryPalette)},
		RoleNeutralPaletteKeyColor:        {name: "neutral_palette_key_color", palette: neutralPalette, tone: keyColorTone(neutralPalette)},
		RoleNeutralVariantPaletteKeyColor: {name: "neutral_variant_palette_key_color", palette: neutralVariantPalette, tone: keyColorTone(neutralVariantPalette)},
		RoleErrorPaletteKeyColor:          {name: "error_palette_key_color", palette: errorPalette, tone: keyColorTone(errorPalette)},

		RoleBackground:              surfaceRole("background", constTone(6, 98)),
		RoleOnBackground:            onRole("on_background", neutralPalette, constTone(90, 10), RoleBackground, curveBackground),
		RoleSurface:                 surfaceRole("surface", constTone(6, 98)),
		RoleSurfaceDim:              surfaceRole("surface_dim", lightCurveTone(6, contrastCurve{87, 87, 80, 75})),
		RoleSurfaceBright:           surfaceRole("surface_bright", darkCurveTone(contrastCurve{24, 24, 29, 34}, 98)),
		RoleSurfaceContainerLowest:  surfaceRole("surface_container_lowest", darkCurveTone(contrastCurve{4, 4, 2, 0}, 100)),
		RoleSurfaceContainerLow:     surfaceRole("surface_container_low", curveTone(contrastCurve{10, 10, 11, 12}, contrastCurve{96, 96, 96, 95})),
		RoleSurfaceContainer:        surfaceRole("surface_container", curveTone(contrastCurve{12, 12, 16, 20}, contrastCurve{94, 94, 92, 90})),
		RoleSurfaceContainerHigh:    surfaceRole("surface_container_high", curveTone(contrastCurve{17, 17, 21, 25}, contrastCurve{92, 92, 88, 85})),
		RoleSurfaceContainerHighest: surfaceRole("surface_container_highest", curveTone(contrastCurve{22, 22, 26, 30}, contrastCurve{90, 90, 84, 80})),
		RoleOnSurface:               {name: "on_surface", palette: neutralPalette, tone: constTone(90, 10), background: highestSurface, curve: curveText},
		RoleSurfaceVariant:          {name: "surface_variant", palette: neutralVariantPalette, tone: constTone(30, 90), isBackground: true},
		RoleOnSurfaceVariant:        {name: "on_surface_variant", palette: neutralVariantPalette, tone: constTone(80, 30), background: highestSurface, curve: curveSubtext},
		RoleInverseSurface:          surfaceRole("inverse_surface", constTone(90, 20)),
		RoleInverseOnSurface:        onRole("inverse_on_surface", neutralPalette, constTone(20, 95), RoleInverseSurface, curveText),
		RoleOutline:                 {name: "outline", palette: neutralVariantPalette, tone: constTone(60, 50), background: highestSurface, curve: curveOutline},
		RoleOutlineVariant:          {name: "outline_variant", palette: neutralVariantPalette, tone: constTone(30, 80), background: highestSurface, curve: curveOutlineVar},
		RoleShadow:                  {name: "shadow", palette: neutralPalette, tone: constTone(0, 0)},
		RoleScrim:                   {name: "scrim", palette: neutralPalette, tone: constTone(0, 0)},
		RoleSurfaceTint:             {name: "surface_tint", palette: primaryPalette, tone: constTone(80, 40), isBackground: true},

		RolePrimary:   accentRole("primary", primaryPalette, monoTone(100, 0, 80, 40), curveAccent, pairPrimary),
		RoleOnPrimary: onRole("on_primary", primaryPalette, monoTone(10, 90, 20, 100), RolePrimary, curveText),
		RolePrimaryContainer: accentRole("primary_container", primaryPalette, func(s *scheme) float64 {
			if s.isFidelity() {
				return s.source.Tone
			}
			return monoTone(85, 25, 30, 90)(s)
		}, curveContainer, pairPrimary),
		RoleOnPrimaryContainer: onRole("on_primary_container", primaryPalette, func(s *scheme) float64 {
			if s.isFidelity() {
				return foregroundTone(roleSpecs[RolePrimaryContainer].tone(s), 4.5)
			}
			return monoTone(0, 100, 90, 30)(s)
		}, RolePrimaryContainer, curveSubtext),
		RoleInversePrimary: onRole("inverse_primary", primaryPalette, constTone(40, 80), RoleInverseSurface, curveAccent),

		RoleSecondary:   accentRole("secondary", secondaryPalette, constTone(80, 40), curveAccent, pairSecondary),
		RoleOnSecondary: onRole("on_secondary", secondaryPalette, monoTone(10, 100, 20, 100), RoleSecondary, curveText),
		RoleSecondaryContainer: accentRole("secondary_container", secondaryPalette, func(s *scheme) float64 {
			initial := s.pick(30, 90)
			switch {
			case s.isMonochrome():
				return s.pick(30, 85)
			case !s.isFidelity():
				return initial
			}
			p := s.palettes[secondaryPalette]
			return findDesiredChromaByTone(p.hue, p.chroma, initial, !s.dark)
		}, curveContainer, pairSecondary),
		RoleOnSecondaryContainer: onRole("on_secondary_container", secondaryPalette, func(s *scheme) float64 {
			switch {
			case s.isMonochrome():
				return s.pick(90, 10)
			case !s.isFidelity():
				return s.pick(90, 30)
			}
			return foregroundTone(roleSpecs[RoleSecondaryContainer].tone(s), 4.5)
		}, RoleSecondaryContainer, curveSubtext),

		RoleTertiary:   accentRole("tertiary", tertiaryPalette, monoTone(90, 25, 80, 40), curveAccent, pairTertiary),
		RoleOnTertiary: onRole("on_tertiary", tertiaryPalette, monoTone(10, 90, 20, 100), RoleTertiary, curveText),
		RoleTertiaryContainer: accentRole("tertiary_container", tertiaryPalette, func(s *scheme) float64 {
			switch {
			case s.isMonochrome():
				return s.pick(60, 49)
			case !s.isFidelity():
				return s.pick(30, 90)
			}
			return fixIfDisliked(s.palettes[tertiaryPalette].hct(s.source.Tone)).Tone
		}, curveContainer, pairTertiary),
		RoleOnTertiaryContainer: onRole("on_tertiary_container", tertiaryPalette, func(s *scheme) float64 {
			switch {
			case s.isMonochrome():
				return s.pick(0, 100)
			case !s.isFidelity():
				return s.pick(90, 30)
			}
			return foregroundTone(roleSpecs[RoleTertiaryContainer].tone(s), 4.5)
		}, RoleTertiaryContainer, curveSubtext),

		RoleError:            accentRole("error", errorPalette, constTone(80, 40), curveAccent, pairError),
		RoleOnError:          onRole("on_error", errorPalette, constTone(20, 100), RoleError, curveText),
		RoleErrorContainer:   accentRole("error_container", errorPalette, constTone(30, 90), curveContainer, pairError),
		RoleOnErrorContainer: onRole("on_error_container", errorPalette, monoTone(90, 10, 90, 30), RoleErrorContainer, curveSubtext),

		RolePrimaryFixed:            accentRole("primary_fixed", primaryPalette, monoFixedTone(40, 90), curveContainer, pairPrimaryFix),
		RolePrimaryFixedDim:         accentRole("primary_fixed_dim", primaryPalette, monoFixedTone(30, 80), curveContainer, pairPrimaryFix),
		RoleOnPrimaryFixed:          onFixedRole("on_primary_fixed", primaryPalette, monoFixedTone(100, 10), RolePrimaryFixedDim, RolePrimaryFixed, curveText),
		RoleOnPrimaryFixedVariant:   onFixedRole("on_primary_fixed_variant", primaryPalette, monoFixedTone(90, 30), RolePrimaryFixedDim, RolePrimaryFixed, curveSubtext),
		RoleSecondaryFixed:          accentRole("secondary_fixed", secondaryPalette, monoFixedTone(80, 90), curveContainer, pairSecondaryFix),
		RoleSecondaryFixedDim:       accentRole("secondary_fixed_dim", secondaryPalette, monoFixedTone(70, 80), curveContainer, pairSecondaryFix),
		RoleOnSecondaryFixed:        onFixedRole("on_secondary_fixed", secondaryPalette, monoFixedTone(10, 10), RoleSecondaryFixedDim, RoleSecondaryFixed, curveText),
		RoleOnSecondaryFixedVariant: onFixedRole("on_secondary_fixed_variant", secondaryPalette, monoFixedTone(25, 30), RoleSecondaryFixedDim, RoleSecondaryFixed, curveSubtext),
		RoleTertiaryFixed:           accentRole("tertiary_fixed", tertiaryPalette, monoFixedTone(40, 90), curveContainer, pairTertiaryFix),
		RoleTertiaryFixedDim:        accentRole("tertiary_fixed_dim", tertiaryPalette, monoFixedTone(30, 80), curveContainer, pairTertiaryFix),
		RoleOnTertiaryFixed:         onFixedRole("on_tertiary_fixed", tertiaryPalette, monoFixedTone(100, 10), RoleTertiaryFixedDim, RoleTertiaryFixed, curveText),
		RoleOnTertiaryFixedVariant:  onFixedRole("on_tertiary_fixed_variant", tertiaryPalette, monoFixedTone(90, 30), RoleTertiaryFixedDim, RoleTertiaryFixed, curveSubtext),
	}
}
