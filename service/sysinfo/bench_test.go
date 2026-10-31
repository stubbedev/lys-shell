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
