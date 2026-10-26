package pipewire

import (
	"testing"
	"unsafe"
)

// The shared-memory mirrors sit where gcc lays out the C structs of
// PipeWire 1.6.9 (offsetof and sizeof over private.h's
// pw_node_activation and the spa headers), which 1.2.0 shares; the
// transport of a 1.6.9 server is 2312 bytes, of which the mirror is
// the head.
func TestSharedStructsMatchTheCLayout(t *testing.T) {
	var a nodeActivation
	var c ioClock
	for name, tc := range map[string][2]uintptr{
		"activation.state":            {unsafe.Offsetof(a.state), 8},
		"activation.signal_time":      {unsafe.Offsetof(a.signalTime), 32},
		"activation.awake_time":       {unsafe.Offsetof(a.awakeTime), 40},
		"activation.finish_time":      {unsafe.Offsetof(a.finishTime), 48},
		"activation.client_version":   {unsafe.Offsetof(a.clientVersion), 540},
		"activation.server_version":   {unsafe.Offsetof(a.serverVersion), 544},
		"activation.active_driver_id": {unsafe.Offsetof(a.activeDriverID), 548},
		"activation.flags":            {unsafe.Offsetof(a.flags), 556},
		"activation.position":         {unsafe.Offsetof(a.position), 560},
		"activation head":             {unsafe.Sizeof(a), 560 + 1688},
		"spa_io_position":             {unsafe.Sizeof(ioPosition{}), 1688},
		"spa_io_clock":                {unsafe.Sizeof(c), 160},
		"spa_io_clock.id":             {unsafe.Offsetof(c.id), 4},
		"spa_io_clock.cycle":          {unsafe.Offsetof(c.cycle), 148},
		"spa_io_buffers":              {unsafe.Sizeof(cIOBuffers{}), 8},
		"spa_meta_header":             {unsafe.Sizeof(cMetaHeader{}), 32},
		"spa_meta_header.pts":         {unsafe.Offsetof(cMetaHeader{}.pts), 8},
		"spa_meta_header.seq":         {unsafe.Offsetof(cMetaHeader{}.seq), 24},
		"spa_meta_region":             {unsafe.Sizeof(cMetaRegion{}), 16},
		"spa_meta_busy":               {unsafe.Sizeof(cMetaBusy{}), 8},
		"spa_meta_busy.count":         {unsafe.Offsetof(cMetaBusy{}.count), 4},
		"spa_chunk":                   {unsafe.Sizeof(cSpaChunk{}), 16},
		"spa_chunk.stride":            {unsafe.Offsetof(cSpaChunk{}.stride), 8},
	} {
		if tc[0] != tc[1] {
			t.Errorf("%s at %d, C has %d", name, tc[0], tc[1])
		}
	}
	if metaHeaderSize != unsafe.Sizeof(cMetaHeader{}) || metaRegionSize != unsafe.Sizeof(cMetaRegion{}) || metaBusySize != unsafe.Sizeof(cMetaBusy{}) {
		t.Error("a meta size param disagrees with its struct")
	}
}
