package pipewire

import (
	"encoding/binary"
	"slices"
	"strings"
	"testing"
)

// Every field type a message carries reads back as it was written, in
// order, nested structs and dicts included.
func TestStructFieldsReadWhatPodsWrite(t *testing.T) {
	format := formatPod(8, 4, 30, VideoBGRx, nil)
	msg := podStruct(
		podInt(-7), podID(42), podLong(-1<<40), podBool(true), podString("node.name"), podFd(3),
		podNone(), podRaw(format),
		podDict([]dictItem{{"a", "1"}, {"b", ""}}),
		podStruct(podInt(9)),
	).bytes()
	f := structFields(msg)
	gotInt, gotID, gotLong, gotBool, gotString, gotFd := f.int(), f.id(), f.long(), f.bool(), f.string(), f.fd()
	none, obj := f.object(), f.object()
	dict := f.dict()
	inner := f.strct()
	innerInt := inner.int()
	if f.err != nil || inner.err != nil {
		t.Fatalf("read: %v / %v", f.err, inner.err)
	}
	if gotInt != -7 || gotID != 42 || gotLong != -1<<40 || !gotBool || gotString != "node.name" || gotFd != 3 {
		t.Errorf("scalars = %d %d %d %v %q %d", gotInt, gotID, gotLong, gotBool, gotString, gotFd)
	}
	if none != nil || !slices.Equal(obj, format) {
		t.Errorf("objects = %x, %x", none, obj)
	}
	if !slices.Equal(dict, []dictItem{{"a", "1"}, {"b", ""}}) || innerInt != 9 {
		t.Errorf("dict %v, inner %d", dict, innerInt)
	}
}

// A field of the wrong type, a struct cut short, a string without its
// NUL, a dict claiming more items than it holds and a pod that is no
// Struct each fail with the field and what was expected, and every read
// after the first failure is zero.
func TestStructFieldsRefuseMalformed(t *testing.T) {
	for name, tc := range map[string]struct {
		msg  []byte
		read func(*podIter)
		want string
	}{
		"wrong type": {podStruct(podInt(1), podString("x")).bytes(), func(f *podIter) { f.int(); f.int() }, "field 2: String pod where Int was expected"},
		"cut short":  {podStruct(podInt(1)).bytes(), func(f *podIter) { f.int(); f.id() }, "field 2: struct ends where Id was expected"},
		"no NUL":     {podStruct(pod{typeString, []byte("abc")}).bytes(), func(f *podIter) { f.string() }, "String without its NUL"},
		"huge dict":  {podStruct(podStruct(podInt(1000))).bytes(), func(f *podIter) { f.dict() }, "dict of 1000 items"},
		"no struct":  {podInt(1).bytes(), func(f *podIter) { f.int() }, "Int pod where Struct was expected"},
		"truncated":  {podStruct(podInt(1)).bytes()[:10], func(f *podIter) { f.int() }, "truncated pod"},
		"not object": {podStruct(podInt(1)).bytes(), func(f *podIter) { f.object() }, "Int pod where Object was expected"},
	} {
		f := structFields(tc.msg)
		tc.read(&f)
		if f.err == nil || !strings.Contains(f.err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, f.err, tc.want)
		}
		if f.int() != 0 || f.string() != "" || f.object() != nil {
			t.Errorf("%s: a read after the failure returned a value", name)
		}
	}
}

// A property fixation left in a None choice reads as its value; any
// other choice is not a value.
func TestPropValueUnwrapsTheNoneChoice(t *testing.T) {
	none := pod{typeChoice, append(append(le32(choiceNone), le32(0)...), append(append(le32(4), le32(typeID)...), le32(uint32(VideoRGBA))...)...)}
	typ, v, ok := propValue(object(objectFormat, paramFormat, prop{key: formatVideoFormat, value: none}), formatVideoFormat)
	if !ok || typ != typeID || binary.LittleEndian.Uint32(v) != uint32(VideoRGBA) {
		t.Errorf("fixated value = %d %x %v", typ, v, ok)
	}
	if _, _, ok := propValue(object(objectFormat, paramFormat, prop{key: formatVideoSize, value: podIntRange(1, 0, 2)}), formatVideoSize); ok {
		t.Error("a range choice read as a value")
	}
	if _, _, ok := propValue(formatPod(1, 1, 30, VideoBGRx, nil), formatVideoModifier); ok {
		t.Error("an absent property read as a value")
	}
}
