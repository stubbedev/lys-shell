package sounds

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/media/vorbis"

	"github.com/stubbedev/lys-shell/service/pulse/native"
)

// maxSoundBytes bounds a sound file: a theme's chirps are a few hundred
// kilobytes, and a hostile sound-file hint must not pull in a movie.
const maxSoundBytes = 8 << 20

// ErrUnsupported is a sound file that is neither Ogg Vorbis nor PCM WAV.
var ErrUnsupported = errors.New("unsupported sound file")

// Decode reads the sound file at path into interleaved signed 16-bit PCM
// for the sample cache.
func Decode(path string) (native.Sample, error) {
	data, err := readBounded(path)
	if err != nil {
		return native.Sample{}, err
	}
	var s native.Sample
	switch {
	case bytes.HasPrefix(data, []byte("OggS")):
		s, err = decodeVorbis(data)
	case bytes.HasPrefix(data, []byte("RIFF")):
		s, err = decodeWAV(data)
	default:
		err = fmt.Errorf("%w: %s", ErrUnsupported, filepath.Ext(path))
	}
	if err != nil {
		return native.Sample{}, fmt.Errorf("sounds: decode %s: %w", path, err)
	}
	return s, nil
}

func readBounded(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // a sound theme file or a notification's sound-file
	if err != nil {
		return nil, fmt.Errorf("sounds: open %s: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("sounds: stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxSoundBytes {
		return nil, fmt.Errorf("sounds: %s: not a regular file of at most %d bytes", path, maxSoundBytes)
	}
	data := make([]byte, info.Size())
	if _, err := f.ReadAt(data, 0); err != nil {
		return nil, fmt.Errorf("sounds: read %s: %w", path, err)
	}
	return data, nil
}

func sample(rate, channels int, pcm []int16) (native.Sample, error) {
	r, err := intconv.Exact[uint32](rate)
	if err != nil || r == 0 {
		return native.Sample{}, fmt.Errorf("sample rate %d", rate)
	}
	cmap, err := channelMap(channels)
	if err != nil {
		return native.Sample{}, err
	}
	data := make([]byte, 2*len(pcm))
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(data[2*i:], uint16(v)) //nolint:gosec // the s16 sample's bits
	}
	chans := uint8(len(cmap)) //nolint:gosec // channelMap lays out one or two channels
	return native.Sample{
		SampleSpec: native.SampleSpec{Format: native.SampleS16LE, Channels: chans, Rate: r},
		ChannelMap: cmap,
		Data:       data,
	}, nil
}

// channelMap lays out mono and stereo, what sound themes ship; a sound
// with more channels is refused rather than guessed at.
func channelMap(channels int) (native.ChannelMap, error) {
	switch channels {
	case 1:
		return native.ChannelMap{native.ChannelMono}, nil
	case 2:
		return native.ChannelMap{native.ChannelFrontLeft, native.ChannelFrontRight}, nil
	}
	return nil, fmt.Errorf("%w: %d channels", ErrUnsupported, channels)
}

func decodeVorbis(data []byte) (native.Sample, error) {
	snd, err := vorbis.Decode(data)
	if err != nil {
		return native.Sample{}, err
	}
	return sample(snd.SampleRate, snd.Channels, snd.Samples)
}

const (
	wavPCM   = 1
	wavFloat = 3
)

func decodeWAV(data []byte) (native.Sample, error) {
	if len(data) < 12 || string(data[8:12]) != "WAVE" {
		return native.Sample{}, fmt.Errorf("%w: RIFF without WAVE", ErrUnsupported)
	}
	var (
		format, channels, bits uint16
		rate                   uint32
		pcm                    []byte
		haveFmt                bool
	)
	for rest := data[12:]; len(rest) >= 8; {
		id, size := string(rest[:4]), binary.LittleEndian.Uint32(rest[4:8])
		rest = rest[8:]
		if uint64(size) > uint64(len(rest)) {
			return native.Sample{}, fmt.Errorf("%w: chunk %q runs past the file", ErrUnsupported, strings.TrimSpace(id))
		}
		body := rest[:size]
		rest = rest[min(len(rest), int(size)+int(size%2)):]
		switch id {
		case "fmt ":
			if len(body) < 16 {
				return native.Sample{}, fmt.Errorf("%w: short fmt chunk", ErrUnsupported)
			}
			format = binary.LittleEndian.Uint16(body[0:])
			channels = binary.LittleEndian.Uint16(body[2:])
			rate = binary.LittleEndian.Uint32(body[4:])
			bits = binary.LittleEndian.Uint16(body[14:])
			if format == 0xFFFE && len(body) >= 26 {
				format = binary.LittleEndian.Uint16(body[24:])
			}
			haveFmt = true
		case "data":
			pcm = body
		}
		if haveFmt && pcm != nil {
			break
		}
	}
	if !haveFmt || pcm == nil {
		return native.Sample{}, fmt.Errorf("%w: WAV without fmt or data", ErrUnsupported)
	}
	samples, err := wavSamples(format, bits, pcm)
	if err != nil {
		return native.Sample{}, err
	}
	if channels == 0 || len(samples)%int(channels) != 0 {
		return native.Sample{}, fmt.Errorf("%w: %d samples over %d channels", ErrUnsupported, len(samples), channels)
	}
	return sample(int(rate), int(channels), samples)
}

func wavSamples(format, bits uint16, pcm []byte) ([]int16, error) {
	width := int(bits) / 8
	if width == 0 || int(bits)%8 != 0 {
		return nil, fmt.Errorf("%w: %d-bit samples", ErrUnsupported, bits)
	}
	out := make([]int16, len(pcm)/width)
	for i := range out {
		b := pcm[i*width : (i+1)*width]
		switch {
		case format == wavPCM && width == 1:
			out[i] = int16(b[0]^0x80) << 8
		case format == wavPCM && width == 2:
			out[i] = int16(binary.LittleEndian.Uint16(b)) //nolint:gosec // the s16 sample's bits
		case format == wavPCM && width == 3:
			out[i] = int16(uint16(b[1]) | uint16(b[2])<<8) //nolint:gosec // the top 16 bits of an s24 sample
		case format == wavPCM && width == 4:
			out[i] = int16(binary.LittleEndian.Uint32(b) >> 16) //nolint:gosec // the top 16 bits of an s32 sample
		case format == wavFloat && width == 4:
			f := math.Float32frombits(binary.LittleEndian.Uint32(b))
			out[i] = int16(max(-1, min(1, f)) * math.MaxInt16)
		default:
			return nil, fmt.Errorf("%w: WAV format %d with %d-bit samples", ErrUnsupported, format, bits)
		}
	}
	return out, nil
}
