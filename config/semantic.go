package config

import (
	"reflect"
	"slices"
)

type semanticString struct {
	doc      string
	variants []string
}

var semanticStrings = map[string]semanticString{
	"Monitor":      {doc: "An output connector name (`DP-1`).", variants: []string{""}},
	"IconTheme":    {doc: "An icon theme name.", variants: []string{""}},
	"CursorTheme":  {doc: "An Xcursor theme name.", variants: []string{""}},
	"Directory":    {doc: "A directory path.", variants: []string{""}},
	"Program":      {doc: "A program name on the path, or its path.", variants: []string{""}},
	"Command":      {doc: "A shell command line."},
	"NetInterface": {doc: "A network interface name (`wlan0`).", variants: []string{"auto"}},
	"TempSensor":   {doc: "A temperature sensor label as `sensors` prints it.", variants: []string{"auto"}},
	"AudioSource":  {doc: "An audio source name.", variants: []string{"auto"}},
	"PamService":   {doc: "A PAM service name, a file under `/etc/pam.d`."},
	"Secret":       {doc: "A secret, or `$NAME` to read it from the environment."},
}

var stringType = reflect.TypeFor[string]()

func isPlainString(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t == stringType
}

type textVariants interface {
	configVariants() []string
}

var textVariantsType = reflect.TypeFor[textVariants]()

func variantsOf(t reflect.Type) []string {
	if !implements(t, textVariantsType) {
		return nil
	}
	v, _ := reflect.TypeAssert[textVariants](zeroOf(t).Addr())
	return v.configVariants()
}

func describeField(f fieldInfo) FieldMeta {
	m := describe(f.typ)
	if s, ok := semanticStrings[f.semantic]; ok {
		m.Type, m.Variants = f.semantic, slices.Clone(s.variants)
	}
	return m
}

func (g *schemaGen) semanticRef(t reflect.Type, name string) Schema {
	if t.Kind() == reflect.Pointer {
		return g.define("prop:Nullable_"+name, "Nullable_"+name, func() Schema { return nullable(g.semanticRef(t.Elem(), name)) })
	}
	return g.define("prop:"+name, name, func() Schema {
		s := semanticStrings[name]
		out := Schema{"type": "string", "description": s.doc}
		if len(s.variants) > 0 {
			out["x-variants"] = anySlice(s.variants)
		}
		return out
	})
}

func anySlice(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}
