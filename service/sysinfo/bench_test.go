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
	for b.Loop() {
		if _, err := ReadMemory(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadNetTotals(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := ReadNetTotals(); err != nil {
			b.Fatal(err)
		}
	}
}
