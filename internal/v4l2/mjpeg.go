package v4l2

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"slices"
	"sync"
)

// JPEG markers the MJPEG repair looks for.
const (
	jpegMarker = 0xff
	jpegSOI    = 0xd8
	jpegDHT    = 0xc4
	jpegSOS    = 0xda
)

// standardDHT is a DHT segment holding the four Huffman tables of the
// JPEG standard (Annex K), the ones image/jpeg encodes with: taken from
// an encoded picture rather than restated.
var standardDHT = sync.OnceValues(func() ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		return nil, err
	}
	seg, ok := dhtSegment(buf.Bytes())
	if !ok {
		return nil, errors.New("image/jpeg wrote no DHT segment")
	}
	return seg, nil
})

// dhtSegment is the first DHT segment before the scan, marker and
// length included.
func dhtSegment(b []byte) ([]byte, bool) {
	i := 2
	for i+4 <= len(b) && b[i] == jpegMarker {
		m := b[i+1]
		if m == jpegSOS {
			return nil, false
		}
		end := i + 2 + int(binary.BigEndian.Uint16(b[i+2:]))
		if end > len(b) {
			return nil, false
		}
		if m == jpegDHT {
			return b[i:end], true
		}
		i = end
	}
	return nil, false
}

// withHuffmanTables is an MJPEG frame decodable on its own: UVC cameras
// commonly leave the Huffman tables out, meaning the standard ones,
// which a JPEG decoder needs spelled out. A frame that has its own is
// returned as it is.
func withHuffmanTables(frame []byte) ([]byte, error) {
	if len(frame) < 4 || frame[0] != jpegMarker || frame[1] != jpegSOI {
		return nil, errors.New("an MJPEG camera frame does not start with a JPEG SOI marker")
	}
	if _, ok := dhtSegment(frame); ok {
		return frame, nil
	}
	dht, err := standardDHT()
	if err != nil {
		return nil, err
	}
	return slices.Concat(frame[:2], dht, frame[2:]), nil
}
