// Package sysinfo reads the kernel's own counters: /proc/stat for CPU
// time, /proc/meminfo for memory, and statfs for storage. The pure-Go
// counterpart of crates/wayle-sysinfo's read path.
package sysinfo

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strconv"
)

// ProcRoot and SysRoot are the procfs and sysfs mounts; tests repoint
// them at fake trees.
var (
	ProcRoot = "/proc"
	SysRoot  = "/sys"
)

// Memory is one /proc/meminfo snapshot, in bytes.
type Memory struct {
	Total     uint64
	Available uint64
	SwapTotal uint64
	SwapFree  uint64
}

// memFields are the meminfo lines Memory holds.
var memFields = [...]struct {
	name  string
	field func(*Memory) *uint64
}{
	{"MemTotal", func(m *Memory) *uint64 { return &m.Total }},
	{"MemAvailable", func(m *Memory) *uint64 { return &m.Available }},
	{"SwapTotal", func(m *Memory) *uint64 { return &m.SwapTotal }},
	{"SwapFree", func(m *Memory) *uint64 { return &m.SwapFree }},
}

// ReadMemory parses the meminfo totals once.
func ReadMemory() (Memory, error) {
	var r MemoryReader
	defer r.Close()
	return r.Read()
}

// MemoryReader polls the meminfo totals through a kept descriptor.
type MemoryReader struct{ file keptFile }

// Close releases the descriptor; a later Read opens it again.
func (r *MemoryReader) Close() { r.file.Close() }

// meminfoLast is the last line Read needs; the rest is not read.
var meminfoLast = []byte("\nSwapFree:")

// Read parses the totals in place; a missing line is an error, not a
// zero.
func (r *MemoryReader) Read() (Memory, error) {
	if r.file.path == "" {
		r.file.path = filepath.Join(ProcRoot, "meminfo")
	}
	content, err := r.file.read(meminfoLast)
	if err != nil {
		return Memory{}, err
	}
	var mem Memory
	var seen [len(memFields)]bool
	for data := content; len(data) > 0; {
		var line []byte
		line, data, _ = bytes.Cut(data, []byte{'\n'})
		name, rest, found := bytes.Cut(line, []byte{':'})
		if !found {
			continue
		}
		for i := range memFields {
			if string(name) != memFields[i].name {
				continue
			}
			field, _ := nextField(rest)
			if len(field) == 0 {
				break
			}
			kb, err := parseDecimal(field)
			if err != nil {
				return Memory{}, fmt.Errorf("sysinfo: meminfo %s: %w", memFields[i].name, err)
			}
			*memFields[i].field(&mem) = kb * 1024
			seen[i] = true
			break
		}
	}
	for i := range memFields {
		if !seen[i] {
			return Memory{}, fmt.Errorf("sysinfo: meminfo misses %s", memFields[i].name)
		}
	}
	return mem, nil
}

// Used returns total minus available (sysinfo's definition).
func (m Memory) Used() uint64 { return m.Total - m.Available }

// UsagePercent maps used onto 0..100.
func (m Memory) UsagePercent() float64 {
	if m.Total == 0 {
		return 0
	}
	return float64(m.Used()) / float64(m.Total) * 100
}

// SwapPercent maps swap usage onto 0..100.
func (m Memory) SwapPercent() float64 {
	if m.SwapTotal == 0 {
		return 0
	}
	return float64(m.SwapTotal-m.SwapFree) / float64(m.SwapTotal) * 100
}

// GiB renders a byte count the way the Rust module does: one decimal.
func GiB(bytes uint64) string {
	const gib = 1024.0 * 1024.0 * 1024.0
	return strconv.FormatFloat(float64(bytes)/gib, 'f', 1, 64)
}
