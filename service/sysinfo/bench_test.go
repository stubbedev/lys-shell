package sysinfo

import "testing"

func BenchmarkCPURead(b *testing.B) {
	r := &CPUReader{Sensor: "auto"}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := r.Read(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadMemory(b *testing.B) {
	b.ReportAllocs()
	var r MemoryReader
	defer r.Close()
	for b.Loop() {
		if _, err := r.Read(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadNetTotals(b *testing.B) {
	b.ReportAllocs()
	var r NetReader
	defer r.Close()
	for b.Loop() {
		if _, err := r.Read(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCPUReadUsageOnly(b *testing.B) {
	r := CPUReader{OmitFrequency: true, OmitTemperature: true}
	defer r.Close()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := r.Read(); err != nil {
			b.Fatal(err)
		}
	}
}
