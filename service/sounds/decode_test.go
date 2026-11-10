package sounds

import (
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stubbedev/lys-lib/pulse/native"
)

func wav(format, channels, bits uint16, rate uint32, data []byte) []byte {
	b := []byte("RIFF\x00\x00\x00\x00WAVEfmt ")
	b = binary.LittleEndian.AppendUint32(b, 16)
	b = binary.LittleEndian.AppendUint16(b, format)
	b = binary.LittleEndian.AppendUint16(b, channels)
	b = binary.LittleEndian.AppendUint32(b, rate)
	b = binary.LittleEndian.AppendUint32(b, rate*uint32(channels)*uint32(bits/8))
	b = binary.LittleEndian.AppendUint16(b, channels*bits/8)
	b = binary.LittleEndian.AppendUint16(b, bits)
	b = append(b, "data"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(data)))
	return append(b, data...)
}

func decodeBytes(t *testing.T, name string, data []byte) (native.Sample, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return Decode(path)
}

func s16(s native.Sample) []int16 {
	out := make([]int16, len(s.Data)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(s.Data[2*i:]))
	}
	return out
}

func TestWAVDecodesToSigned16(t *testing.T) {
	f32 := func(vs ...float32) []byte {
		var b []byte
		for _, v := range vs {
			b = binary.LittleEndian.AppendUint32(b, math.Float32bits(v))
		}
		return b
	}
	for name, tc := range map[string]struct {
		file []byte
		want []int16
		ch   uint8
	}{
		"s16 stereo": {wav(1, 2, 16, 44100, []byte{0x00, 0x80, 0xff, 0x7f}), []int16{-32768, 32767}, 2},
		"u8 mono":    {wav(1, 1, 8, 8000, []byte{0, 128, 255}), []int16{-32768, 0, 32512}, 1},
		"f32 mono":   {wav(3, 1, 32, 48000, f32(-1, 0, 2)), []int16{-32767, 0, 32767}, 1},
	} {
		s, err := decodeBytes(t, "x.wav", tc.file)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s.SampleSpec.Format != native.SampleS16LE || s.SampleSpec.Channels != tc.ch || len(s.ChannelMap) != int(tc.ch) {
			t.Errorf("%s: spec %+v map %v", name, s.SampleSpec, s.ChannelMap)
		}
		got := s16(s)
		for i := range tc.want {
			if i >= len(got) || got[i] != tc.want[i] {
				t.Errorf("%s: samples %v, want %v", name, got, tc.want)
				break
			}
		}
	}
}

func TestAnOggVorbisFileDecodes(t *testing.T) {
	s, err := Decode("testdata/chirp.oga")
	if err != nil {
		t.Fatal(err)
	}
	if s.SampleSpec.Channels != 2 || s.SampleSpec.Rate == 0 || len(s.Data) == 0 || len(s.Data)%4 != 0 {
		t.Errorf("chirp = %+v, %d bytes", s.SampleSpec, len(s.Data))
	}
}

func TestWhatIsNotASoundIsRefused(t *testing.T) {
	for name, data := range map[string][]byte{
		"text.oga":   []byte("hello"),
		"24bit.wav":  wav(1, 1, 12, 8000, []byte{1, 2, 3}),
		"short.wav":  []byte("RIFF\x00\x00\x00\x00WAVE"),
		"6ch.wav":    wav(1, 6, 16, 8000, make([]byte, 12)),
		"broken.oga": []byte("OggS garbage that is not a page"),
	} {
		if _, err := decodeBytes(t, name, data); err == nil {
			t.Errorf("%s decoded", name)
		} else if name != "broken.oga" && !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: %v, want ErrUnsupported", name, err)
		}
	}
	dir := t.TempDir()
	if _, err := Decode(dir); err == nil {
		t.Error("a directory decoded")
	}
}
