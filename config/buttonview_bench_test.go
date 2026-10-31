package config

import "testing"

func BenchmarkModuleButton(b *testing.B) {
	m := DefaultsModules()
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := m.ModuleButton(string(ModuleClock)); !ok {
			b.Fatal("no clock section")
		}
	}
}
