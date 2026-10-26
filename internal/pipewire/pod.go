package pipewire

import (
	"encoding/binary"
	"fmt"
)

// SPA pod types (spa/utils/type.h).
const (
	typeNone      = 1
	typeBool      = 2
	typeID        = 3
	typeInt       = 4
	typeLong      = 5
	typeString    = 8
	typeRectangle = 10
	typeFraction  = 11
	typeStruct    = 14
	typeObject    = 15
	typeFd        = 18
	typeChoice    = 19
)

// podTypeNames names the pod types a message carries, for errors.
var podTypeNames = map[uint32]string{
	typeNone: "None", typeBool: "Bool", typeID: "Id", typeInt: "Int", typeLong: "Long",
	typeString: "String", typeRectangle: "Rectangle", typeFraction: "Fraction",
	typeStruct: "Struct", typeObject: "Object", typeFd: "Fd", typeChoice: "Choice",
}

func podTypeName(t uint32) string {
	if n, ok := podTypeNames[t]; ok {
		return n
	}
	return fmt.Sprintf("type %d", t)
}

// SPA object types and param ids (spa/utils/type.h, spa/param/param.h).
const (
	objectFormat       = 0x40003
	objectParamBuffers = 0x40004
	objectParamMeta    = 0x40005

	paramEnumFormat = 3
	paramFormat     = 4
	paramBuffers    = 5
	paramMeta       = 6
)

// Format keys and values (spa/param/format.h, video/raw.h).
const (
	formatMediaType     = 1
	formatMediaSubtype  = 2
	formatVideoFormat   = 0x20001
	formatVideoModifier = 0x20002
	formatVideoSize     = 0x20003
	formatVideoRate     = 0x20004

	mediaTypeVideo  = 2
	mediaSubtypeRaw = 1
)

// VideoFormat is an spa_video_format.
type VideoFormat uint32

// The packed 8-bit formats compositors hand out.
const (
	VideoRGBx VideoFormat = 7
	VideoBGRx VideoFormat = 8
	VideoRGBA VideoFormat = 11
	VideoBGRA VideoFormat = 12
	// The packed 24-bit formats GPU renderers offer over shm.
	VideoRGB VideoFormat = 15
	VideoBGR VideoFormat = 16
)

// Buffers and meta keys (spa/param/buffers.h).
const (
	buffersBuffers  = 1
	buffersBlocks   = 2
	buffersSize     = 3
	buffersStride   = 4
	buffersAlign    = 5
	buffersDataType = 6

	metaKeyType = 1
	metaKeySize = 2
)

// Data and meta types (spa/buffer/buffer.h, meta.h).
const (
	dataMemPtr = 1
	dataMemFd  = 2
	dataDmaBuf = 3
	dataMemID  = 4

	// spa_data flags.
	dataFlagReadable = 1 << 0
	dataFlagWritable = 1 << 1
	dataFlagMappable = 1 << 3

	metaHeader         = 1
	metaVideoDamage    = 3
	metaBusy           = 7
	metaVideoTransform = 8

	metaHeaderSize    = 32 // spa_meta_header
	metaTransformSize = 4  // spa_meta_videotransform
	metaRegionSize    = 16 // spa_meta_region
	metaBusySize      = 8  // spa_meta_busy
)

const (
	choiceNone        = 0
	choiceRange       = 1
	propFlagMandatory = 1 << 3
)

// pod is one serialized SPA pod value: its type and body.
type pod struct {
	typ  uint32
	body []byte
}

func le32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

func podID(v uint32) pod  { return pod{typeID, le32(v)} }
func podInt(v int32) pod  { return pod{typeInt, le32(uint32(v))} }
func podLong(v int64) pod { return pod{typeLong, binary.LittleEndian.AppendUint64(nil, uint64(v))} }

func podBool(v bool) pod {
	if v {
		return pod{typeBool, le32(1)}
	}
	return pod{typeBool, le32(0)}
}

// podFd is an Fd pod: the index of the fd in the message's fd array.
func podFd(index int) pod { return pod{typeFd, binary.LittleEndian.AppendUint64(nil, uint64(index))} }

func podNone() pod { return pod{typ: typeNone} }

// podString is a String pod: the bytes and their NUL.
func podString(s string) pod { return pod{typeString, append([]byte(s), 0)} }

func podRectangle(w, h uint32) pod { return pod{typeRectangle, append(le32(w), le32(h)...)} }

func podFraction(num, denom uint32) pod { return pod{typeFraction, append(le32(num), le32(denom)...)} }

// podStruct is a Struct of the fields, each padded as pods are laid out.
func podStruct(fields ...pod) pod {
	var body []byte
	for _, f := range fields {
		body = append(body, f.bytes()...)
	}
	return pod{typeStruct, body}
}

// podRaw is an already serialized pod, header and body, as a value.
func podRaw(b []byte) pod {
	return pod{binary.LittleEndian.Uint32(b[4:]), b[8 : 8+binary.LittleEndian.Uint32(b)]}
}

// dictItem is one property of a dictionary.
type dictItem struct{ key, value string }

// podDict is the protocol's spa_dict: a Struct of the item count, then
// each key and value.
func podDict(items []dictItem) pod {
	fields := make([]pod, 0, 1+2*len(items))
	fields = append(fields, podInt(int32(len(items))))
	for _, it := range items {
		fields = append(fields, podString(it.key), podString(it.value))
	}
	return podStruct(fields...)
}

// podIntRange is a Choice of Range over Ints: default, min, max.
func podIntRange(def, lo, hi int32) pod {
	body := append(le32(choiceRange), le32(0)...)             // choice type, flags
	body = append(body, append(le32(4), le32(typeInt)...)...) // child pod: size, type
	for _, v := range []int32{def, lo, hi} {
		body = append(body, le32(uint32(v))...)
	}
	return pod{typeChoice, body}
}

// bytes is the pod with its header, padded to 8 bytes as pods are laid
// out.
func (p pod) bytes() []byte {
	out := append(le32(uint32(len(p.body))), le32(p.typ)...)
	out = append(out, p.body...)
	for len(out)%8 != 0 {
		out = append(out, 0)
	}
	return out
}

// prop is one object property.
type prop struct {
	key, flags uint32
	value      pod
}

// object serializes an spa_pod_object.
func object(typ, id uint32, props ...prop) []byte {
	body := append(le32(typ), le32(id)...)
	for _, p := range props {
		body = append(body, le32(p.key)...)
		body = append(body, le32(p.flags)...)
		body = append(body, p.value.bytes()...)
	}
	return pod{typeObject, body}.bytes()
}

// formatPod is the EnumFormat object for one offered video format; a
// modifier marks a dmabuf format (mandatory), none the SHM one.
func formatPod(width, height, fps uint32, format VideoFormat, modifier *uint64) []byte {
	return formatObject(paramEnumFormat, width, height, fps, format, modifier)
}

// formatObject is formatPod as the param id (EnumFormat or Format).
func formatObject(id, width, height, fps uint32, format VideoFormat, modifier *uint64) []byte {
	props := []prop{
		{key: formatMediaType, value: podID(mediaTypeVideo)},
		{key: formatMediaSubtype, value: podID(mediaSubtypeRaw)},
		{key: formatVideoFormat, value: podID(uint32(format))},
	}
	if modifier != nil {
		props = append(props, prop{key: formatVideoModifier, flags: propFlagMandatory, value: podLong(int64(*modifier))})
	}
	props = append(props,
		prop{key: formatVideoSize, value: podRectangle(width, height)},
		prop{key: formatVideoRate, value: podFraction(fps, 1)},
	)
	return object(objectFormat, id, props...)
}

// maxDamageRegions is how many damage rectangles a buffer's meta has
// room for (xdph's MAX_DAMAGE); more collapse into one full frame.
const maxDamageRegions = 4

// bufferPods is the buffer layout and the Header, VideoTransform and
// VideoDamage metas, declared once a format is negotiated: without the
// layout the server maps empty buffers and consumers show nothing.
func bufferPods(stride, height uint32) [][]byte {
	return bufferPodsOf(stride, height, 1<<dataMemFd|1<<dataMemPtr)
}

// bufferPodsOf is bufferPods with the buffers' data types (a bit mask of
// the spa_data types the producer provides).
func bufferPodsOf(stride, height, dataTypes uint32) [][]byte {
	return [][]byte{
		object(objectParamBuffers, paramBuffers,
			prop{key: buffersBuffers, value: podIntRange(4, 2, 16)},
			prop{key: buffersBlocks, value: podInt(1)},
			prop{key: buffersSize, value: podInt(int32(stride * height))},
			prop{key: buffersStride, value: podInt(int32(stride))},
			prop{key: buffersAlign, value: podInt(16)},
			prop{key: buffersDataType, value: podInt(int32(dataTypes))},
		),
		object(objectParamMeta, paramMeta,
			prop{key: metaKeyType, value: podID(metaHeader)},
			prop{key: metaKeySize, value: podInt(metaHeaderSize)},
		),
		object(objectParamMeta, paramMeta,
			prop{key: metaKeyType, value: podID(metaVideoTransform)},
			prop{key: metaKeySize, value: podInt(metaTransformSize)},
		),
		object(objectParamMeta, paramMeta,
			prop{key: metaKeyType, value: podID(metaVideoDamage)},
			prop{key: metaKeySize, value: podIntRange(metaRegionSize*maxDamageRegions, metaRegionSize, metaRegionSize*maxDamageRegions)},
		),
	}
}

// busyMetaPod is the Busy meta: a consumer holding a buffer marks it
// busy, and the producer leaves it alone until it is released.
func busyMetaPod() []byte {
	return object(objectParamMeta, paramMeta,
		prop{key: metaKeyType, value: podID(metaBusy)},
		prop{key: metaKeySize, value: podInt(metaBusySize)},
	)
}

// podAt is the type and body of the pod at off in b; false when b holds
// no whole pod there.
func podAt(b []byte, off int) (typ uint32, body []byte, ok bool) {
	if off < 0 || off+8 > len(b) {
		return 0, nil, false
	}
	size := int(binary.LittleEndian.Uint32(b[off:]))
	if size > len(b)-off-8 {
		return 0, nil, false
	}
	return binary.LittleEndian.Uint32(b[off+4:]), b[off+8 : off+8+size], true
}

// podSpan is the bytes a pod of body size occupies, header and padding.
func podSpan(size int) int { return 8 + (size+7)&^7 }

// objectProp is the value pod (header and body) of the property key
// of the spa_pod_object in b, nil when it has none: a negotiated
// format with the modifier is a dmabuf one.
func objectProp(b []byte, key uint32) []byte {
	typ, body, ok := podAt(b, 0)
	if !ok || typ != typeObject || len(body) < 8 {
		return nil
	}
	for i := 8; i+16 <= len(body); {
		size := int(binary.LittleEndian.Uint32(body[i+8:]))
		if binary.LittleEndian.Uint32(body[i:]) == key {
			return body[i+8 : min(len(body), i+16+size)]
		}
		i += 8 + podSpan(size)
	}
	return nil
}

// propValue is the value of an object property with a None choice
// unwrapped (fixation leaves values in one): its type and body.
func propValue(b []byte, key uint32) (typ uint32, body []byte, ok bool) {
	v := objectProp(b, key)
	typ, body, ok = podAt(v, 0)
	if !ok || typ != typeChoice {
		return typ, body, ok
	}
	if len(body) < 16 || binary.LittleEndian.Uint32(body) != choiceNone {
		return 0, nil, false
	}
	childSize, childType := int(binary.LittleEndian.Uint32(body[8:])), binary.LittleEndian.Uint32(body[12:])
	if len(body) < 16+childSize {
		return 0, nil, false
	}
	return childType, body[16 : 16+childSize], true
}

// formatSize is a negotiated format's video size, a Rectangle or the
// None choice of one fixation leaves; false without one.
func formatSize(b []byte) (width, height uint32, ok bool) {
	typ, v, ok := propValue(b, formatVideoSize)
	if !ok || typ != typeRectangle || len(v) < 8 {
		return 0, 0, false
	}
	return binary.LittleEndian.Uint32(v), binary.LittleEndian.Uint32(v[4:]), true
}

// podIter reads a Struct's fields in order. A field of the wrong type
// or a truncated struct sets err, after which every read is zero.
type podIter struct {
	b     []byte
	off   int
	field int
	err   error
}

// structFields iterates the Struct pod at the start of b; a message
// carries one, optionally followed by a footer.
func structFields(b []byte) podIter {
	typ, body, ok := podAt(b, 0)
	switch {
	case !ok:
		return podIter{err: fmt.Errorf("truncated pod (%d bytes)", len(b))}
	case typ != typeStruct:
		return podIter{err: fmt.Errorf("%s pod where Struct was expected", podTypeName(typ))}
	}
	return podIter{b: body}
}

// next is the next field's type, body and whole pod.
func (it *podIter) next(want uint32) []byte {
	if it.err != nil {
		return nil
	}
	it.field++
	typ, body, ok := podAt(it.b, it.off)
	if !ok {
		it.err = fmt.Errorf("field %d: struct ends where %s was expected", it.field, podTypeName(want))
		return nil
	}
	if typ != want {
		it.err = fmt.Errorf("field %d: %s pod where %s was expected", it.field, podTypeName(typ), podTypeName(want))
		return nil
	}
	it.off += podSpan(len(body))
	return body
}

func (it *podIter) fixed(want uint32, size int) []byte {
	body := it.next(want)
	if it.err == nil && len(body) < size {
		it.err = fmt.Errorf("field %d: %s body of %d bytes", it.field, podTypeName(want), len(body))
		return nil
	}
	return body
}

func (it *podIter) int() int32 {
	if b := it.fixed(typeInt, 4); b != nil {
		return int32(binary.LittleEndian.Uint32(b))
	}
	return 0
}

func (it *podIter) uint() uint32 { return uint32(it.int()) }

func (it *podIter) id() uint32 {
	if b := it.fixed(typeID, 4); b != nil {
		return binary.LittleEndian.Uint32(b)
	}
	return 0
}

func (it *podIter) long() int64 {
	if b := it.fixed(typeLong, 8); b != nil {
		return int64(binary.LittleEndian.Uint64(b))
	}
	return 0
}

func (it *podIter) bool() bool {
	if b := it.fixed(typeBool, 4); b != nil {
		return binary.LittleEndian.Uint32(b) != 0
	}
	return false
}

// fd is an Fd field: the index into the message's fds, -1 for none.
func (it *podIter) fd() int64 {
	if b := it.fixed(typeFd, 8); b != nil {
		return int64(binary.LittleEndian.Uint64(b))
	}
	return -1
}

func (it *podIter) string() string {
	b := it.next(typeString)
	if it.err != nil {
		return ""
	}
	if len(b) == 0 || b[len(b)-1] != 0 {
		it.err = fmt.Errorf("field %d: String without its NUL", it.field)
		return ""
	}
	return string(b[:len(b)-1])
}

// object is an Object field as a whole pod, or nil for a None field.
func (it *podIter) object() []byte {
	if it.err != nil {
		return nil
	}
	typ, body, ok := podAt(it.b, it.off)
	if ok && typ == typeNone {
		it.field++
		it.off += podSpan(len(body))
		return nil
	}
	start := it.off
	body = it.next(typeObject)
	if it.err != nil {
		return nil
	}
	return it.b[start : start+8+len(body)]
}

// strct is a nested Struct field's iterator; errors in it are its own.
func (it *podIter) strct() podIter {
	start := it.off
	body := it.next(typeStruct)
	if it.err != nil {
		return podIter{err: it.err}
	}
	return structFields(it.b[start : start+8+len(body)])
}

// dict reads a spa_dict Struct field.
func (it *podIter) dict() []dictItem {
	d := it.strct()
	n := d.int()
	if d.err == nil && (n < 0 || int(n) > len(d.b)/8) {
		d.err = fmt.Errorf("dict of %d items in %d bytes", n, len(d.b))
	}
	var items []dictItem
	for i := int32(0); i < n && d.err == nil; i++ {
		items = append(items, dictItem{d.string(), d.string()})
	}
	if d.err != nil && it.err == nil {
		it.err = fmt.Errorf("field %d: %w", it.field, d.err)
	}
	return items
}
