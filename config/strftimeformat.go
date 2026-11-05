package config

import (
	"time"

	"github.com/stubbedev/lys-shell/strftime"
)

// StrftimeFormat is a chrono strftime format string compiled at load,
// so an unsupported specifier is a diagnostic for its field instead of
// garbage at render time. In the schema it is a string of format
// strftime.
type StrftimeFormat struct {
	src    string
	layout *strftime.Layout
}

// ParseStrftimeFormat compiles one format string.
func ParseStrftimeFormat(s string) (StrftimeFormat, error) {
	l, err := strftime.Compile(s)
	if err != nil {
		return StrftimeFormat{}, err
	}
	return StrftimeFormat{src: s, layout: l}, nil
}

// mustStrftime builds a schema-default format; a bad literal panics
// at init.
func mustStrftime(s string) StrftimeFormat {
	f, err := ParseStrftimeFormat(s)
	if err != nil {
		panic(err)
	}
	return f
}

// String is the format as written.
func (f StrftimeFormat) String() string { return f.src }

// Layout is the compiled format.
func (f StrftimeFormat) Layout() *strftime.Layout { return f.layout }

// Format renders t through the format, the sample an editor previews.
func (f StrftimeFormat) Format(t time.Time) string { return f.layout.Format(t) }

// UnmarshalConfig implements Unmarshaler.
func (f *StrftimeFormat) UnmarshalConfig(v any) error {
	s, ok := v.(string)
	if !ok {
		return invalidType(v, "a string")
	}
	parsed, err := ParseStrftimeFormat(s)
	if err != nil {
		return err
	}
	*f = parsed
	return nil
}

// MarshalConfig implements Marshaler.
func (f StrftimeFormat) MarshalConfig() any { return f.src }

func (StrftimeFormat) configSchema(*schemaGen) Schema {
	return Schema{"type": "string", "format": "strftime", "description": "A strftime pattern (`%H:%M`)."}
}
