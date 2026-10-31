package cava

import "testing"

func benchmarkExecute(b *testing.B, channels int) {
	p, err := NewPlan(20/channels, 44100, channels, 0.77, true, 50, 10000)
	if err != nil {
		b.Fatal(err)
	}
	frame := make([]float64, 735*channels)
	for i := range frame {
		frame[i] = oracleSignal(i%channels, int64(i/channels))
	}
	var out []float64
	b.ReportAllocs()
	for b.Loop() {
		out = p.Execute(out, frame)
	}
}

func BenchmarkExecuteMono(b *testing.B)   { benchmarkExecute(b, 1) }
func BenchmarkExecuteStereo(b *testing.B) { benchmarkExecute(b, 2) }

func BenchmarkDrain(b *testing.B) {
	s := newSamples(2, 16384)
	chunk := make([]float64, 735*2)
	var out []float64
	b.ReportAllocs()
	for b.Loop() {
		s.push(chunk)
		out = s.drained(out)
	}
}
