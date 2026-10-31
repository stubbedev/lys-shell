package sysinfo

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// CPUTimes is one /proc/stat cpu line reduced the way the sysinfo
// crate does: work is user, nice, system, irq, and softirq (with guest
// time taken out of user and nice, where the kernel already counts
// it); total adds idle, iowait, steal, and the guest time back.
type CPUTimes struct {
	Work, Total uint64
}

// CPUStat is the aggregate line and one per core, in core order.
type CPUStat struct {
	Global CPUTimes
	Cores  []CPUTimes
}

// ReadCPUStat parses /proc/stat's cpu lines in place: the poll runs
// every couple of seconds for the life of the bar, so a line costs no
// allocation beyond the scanner's buffer.
func ReadCPUStat() (CPUStat, error) {
	file, err := os.Open(filepath.Join(ProcRoot, "stat"))
	if err != nil {
		return CPUStat{}, fmt.Errorf("sysinfo: %w", err)
	}
	defer file.Close()
	var stat CPUStat
	found := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		name, rest := nextField(scanner.Bytes())
		if !bytes.HasPrefix(name, []byte("cpu")) {
			continue
		}
		times, err := parseCPUTimes(rest)
		if err != nil {
			return CPUStat{}, err
		}
		if len(name) == len("cpu") {
			stat.Global, found = times, true
		} else {
			stat.Cores = append(stat.Cores, times)
		}
	}
	if err := scanner.Err(); err != nil {
		return CPUStat{}, fmt.Errorf("sysinfo: read /proc/stat: %w", err)
	}
	if !found {
		return CPUStat{}, errors.New("sysinfo: /proc/stat has no cpu line")
	}
	return stat, nil
}

// nextField splits the first space-separated field off line.
func nextField(line []byte) (field, rest []byte) {
	line = bytes.TrimLeft(line, " ")
	if i := bytes.IndexByte(line, ' '); i >= 0 {
		return line[:i], line[i:]
	}
	return line, nil
}

// parseCPUTimes reduces a cpu line's counters (after its name), the
// first ten of them, absent ones zero.
func parseCPUTimes(fields []byte) (CPUTimes, error) {
	var v [10]uint64
	for i := range v {
		var field []byte
		field, fields = nextField(fields)
		if len(field) == 0 {
			break
		}
		n, err := parseDecimal(field)
		if err != nil {
			return CPUTimes{}, fmt.Errorf("sysinfo: /proc/stat field %d: %w", i, err)
		}
		v[i] = n
	}
	user, nice, system, idle, iowait, irq, softirq, steal, guest, guestNice := v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7], v[8], v[9]
	user = satSub(user, guest)
	nice = satSub(nice, guestNice)
	work := user + nice + system + irq + softirq
	return CPUTimes{Work: work, Total: work + idle + iowait + guest + guestNice + steal}, nil
}

// parseDecimal is strconv.ParseUint(string(b), 10, 64) without the
// string.
func parseDecimal(b []byte) (uint64, error) {
	var n uint64
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%q is not a decimal number", b)
		}
		d := uint64(c - '0')
		if n > (math.MaxUint64-d)/10 {
			return 0, fmt.Errorf("%q overflows 64 bits", b)
		}
		n = n*10 + d
	}
	return n, nil
}

func satSub(a, b uint64) uint64 {
	if a > b {
		return a - b
	}
	return 0
}

// Usage is sysinfo's CpuUsage percent between two readings: the work
// delta over the total delta, capped at 100. Against a zero reading
// (the first refresh) it is the average since boot.
func Usage(prev, cur CPUTimes) float32 {
	work := float32(0)
	if cur.Work > prev.Work {
		work = float32(cur.Work - prev.Work)
	}
	total := float32(1)
	if cur.Total > prev.Total {
		total = float32(cur.Total - prev.Total)
	}
	return min(work/total*100, 100)
}

// CoreFrequencyMHz is get_cpu_frequency: the core's cpufreq
// scaling_cur_freq (kHz), else cpuinfo, the first MHz line of
// /proc/cpuinfo (cpuinfoMHz), else 0. Read passes one cpuinfo for all
// its cores, so a machine without cpufreq reads /proc/cpuinfo once per
// poll, not once per core.
func CoreFrequencyMHz(core int, cpuinfo func() uint64) uint64 {
	path := filepath.Join(SysRoot, "devices/system/cpu", "cpu"+strconv.Itoa(core), "cpufreq/scaling_cur_freq")
	if line, ok := readLineOK(path); ok {
		if khz, err := strconv.ParseUint(line, 10, 64); err == nil {
			return khz / 1000
		}
	}
	return cpuinfo()
}

// cpuinfoMHz is the first MHz line of /proc/cpuinfo, 0 without one.
func cpuinfoMHz() uint64 {
	data, err := os.ReadFile(filepath.Join(ProcRoot, "cpuinfo"))
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(line, "cpu MHz\t") && !strings.HasPrefix(line, "BogoMIPS") &&
			!strings.HasPrefix(line, "clock\t") && !strings.HasPrefix(line, "bogomips per cpu") {
			continue
		}
		i := strings.LastIndexByte(line, ':')
		value := strings.TrimSpace(strings.ReplaceAll(line[i+1:], "MHz", ""))
		if mhz, err := strconv.ParseFloat(value, 64); err == nil {
			return uint64(mhz)
		}
		return 0
	}
	return 0
}

// Core is one core's usage and frequency.
type Core struct {
	UsagePercent float32
	FrequencyMHz uint64
}

// CPU is polling/cpu.rs's CpuData.
type CPU struct {
	UsagePercent       float32
	AvgFrequencyMHz    uint64
	MaxFrequencyMHz    uint64
	BusiestCoreFreqMHz uint64
	// TemperatureC is the matched sensor's reading; ok is false
	// without one.
	TemperatureC   float32
	HasTemperature bool
	Cores          []Core
}

// CPUReader keeps the previous counters between polls, as the Rust
// poll loop keeps its System.
type CPUReader struct {
	prev CPUStat
	// Sensor is the temp-sensor key: "auto" or a label substring.
	Sensor string
}

// Read samples the counters, frequencies, and temperature.
func (r *CPUReader) Read() (CPU, error) {
	stat, err := ReadCPUStat()
	if err != nil {
		return CPU{}, err
	}
	out := CPU{UsagePercent: Usage(r.prev.Global, stat.Global), Cores: make([]Core, 0, len(stat.Cores))}
	cpuinfo := sync.OnceValue(cpuinfoMHz)
	var sum uint64
	busiest := -1
	for i, cur := range stat.Cores {
		var prev CPUTimes
		if i < len(r.prev.Cores) {
			prev = r.prev.Cores[i]
		}
		core := Core{UsagePercent: Usage(prev, cur), FrequencyMHz: CoreFrequencyMHz(i, cpuinfo)}
		out.Cores = append(out.Cores, core)
		sum += core.FrequencyMHz
		out.MaxFrequencyMHz = max(out.MaxFrequencyMHz, core.FrequencyMHz)
		// max_by keeps the last of equal maxima.
		if busiest < 0 || core.UsagePercent >= out.Cores[busiest].UsagePercent {
			busiest = i
		}
	}
	if n := len(out.Cores); n > 0 {
		out.AvgFrequencyMHz = sum / uint64(n)
		out.BusiestCoreFreqMHz = out.Cores[busiest].FrequencyMHz
	}
	r.prev = stat
	out.TemperatureC, out.HasTemperature = CPUTemperature(r.Sensor)
	return out, nil
}

// cpuTempPatterns are CPU_TEMP_PATTERNS, tried in order for "auto".
var cpuTempPatterns = []string{"tctl", "tdie", "tccd", "k10temp", "coretemp", "package id", "cpu"}

// sensorInput is one hwmon temperature input, labeled the way the
// sysinfo crate labels its components, not yet read.
type sensorInput struct {
	label string
	path  string
}

// sensorInputs lists the hwmon temperature inputs (sysinfo's
// Components): an input's label is its tempN_label, else "<chip>
// <model>" or "<chip> tempN". The order is by hwmon directory, then by
// input number. Only the labels are read: reading an input queries its
// device (an NVMe drive or a Wi-Fi card wakes to answer), so that waits
// for the one input a lookup settles on.
func sensorInputs() []sensorInput {
	var out []sensorInput
	dirs, _ := filepath.Glob(filepath.Join(SysRoot, "class/hwmon/hwmon*"))
	sort.Strings(dirs)
	for _, dir := range dirs {
		chip := readLine(filepath.Join(dir, "name"))
		model, hasModel := readLineOK(filepath.Join(dir, "device/model"))
		paths, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		type entry struct {
			id int
			in sensorInput
		}
		entries := make([]entry, 0, len(paths))
		for _, path := range paths {
			id, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "temp"), "_input"))
			if err != nil {
				continue
			}
			label := readLine(filepath.Join(dir, "temp"+strconv.Itoa(id)+"_label"))
			if label == "" {
				if hasModel {
					label = chip + " " + model
				} else {
					label = chip + " temp" + strconv.Itoa(id)
				}
			}
			entries = append(entries, entry{id, sensorInput{label: label, path: path}})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id })
		for _, e := range entries {
			out = append(out, e.in)
		}
	}
	return out
}

// CPUTemperature is find_cpu_temperature: a named sensor by label
// substring (case-insensitive), or for "auto" the first sensor matching
// each pattern in turn. A sensor is an input that reads; of inputs
// sharing a label the first that reads is the sensor. Without any
// hwmon sensor, thermal_zone0 stands in, unlabeled.
func CPUTemperature(sensor string) (float32, bool) {
	inputs := sensorInputs()
	find := func(pattern string) (float32, bool) {
		for _, in := range inputs {
			if !strings.Contains(strings.ToLower(in.label), pattern) {
				continue
			}
			if temp, ok := readMilliCelsius(in.path); ok {
				return temp, true
			}
		}
		if pattern == "" {
			return readMilliCelsius(filepath.Join(SysRoot, "class/thermal/thermal_zone0/temp"))
		}
		return 0, false
	}
	if sensor != "auto" {
		return find(strings.ToLower(sensor))
	}
	for _, p := range cpuTempPatterns {
		if t, ok := find(p); ok {
			return t, true
		}
	}
	return 0, false
}

func readMilliCelsius(path string) (float32, bool) {
	n, err := strconv.ParseInt(readLine(path), 10, 32)
	if err != nil {
		return 0, false
	}
	return float32(n) / 1000, true
}

func readLine(path string) string {
	s, _ := readLineOK(path)
	return s
}

// readLineOK reads a small sysfs or procfs attribute, trailing
// whitespace trimmed; false when it cannot be opened or read. A stack
// buffer takes the usual few bytes in one read: sysfs files stat as a
// page, so os.ReadFile would stat and allocate 4 KiB for each.
func readLineOK(path string) (string, bool) {
	f, err := os.Open(path) //nolint:gosec // a sysfs/procfs path under the roots
	if err != nil {
		return "", false
	}
	defer f.Close()
	var buf [128]byte
	n := 0
	for n < len(buf) {
		m, err := f.Read(buf[n:])
		n += m
		if errors.Is(err, io.EOF) {
			return trimLine(buf[:n]), true
		}
		if err != nil {
			return "", false
		}
	}
	rest, err := io.ReadAll(f)
	if err != nil {
		return "", false
	}
	return trimLine(append(buf[:n:n], rest...)), true
}

// trimLine is b as a string without its trailing whitespace.
func trimLine(b []byte) string {
	return string(bytes.TrimRightFunc(b, func(r rune) bool { return r == '\n' || r == ' ' || r == '\t' || r == '\r' }))
}
