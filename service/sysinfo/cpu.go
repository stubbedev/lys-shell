package sysinfo

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
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

// ReadCPUStat parses /proc/stat's cpu lines.
func ReadCPUStat() (CPUStat, error) {
	file := keptFile{path: filepath.Join(ProcRoot, "stat")}
	defer file.Close()
	return readCPUStat(&file, nil)
}

// statPastCPUs is what follows the cpu lines in /proc/stat; the rest of
// the file is not read.
var statPastCPUs = []byte("\nintr ")

// readCPUStat is ReadCPUStat through file, with cores' storage to
// reuse: the poll runs every couple of seconds for the life of the bar,
// so it costs one pread and no allocation.
func readCPUStat(file *keptFile, cores []CPUTimes) (CPUStat, error) {
	data, err := file.read(statPastCPUs)
	if err != nil {
		return CPUStat{}, err
	}
	stat := CPUStat{Cores: cores[:0]}
	found := false
	for len(data) > 0 {
		var line []byte
		line, data, _ = bytes.Cut(data, []byte{'\n'})
		name, rest := nextField(line)
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
			return 0, fmt.Errorf("%q is not a decimal number", string(b))
		}
		d := uint64(c - '0')
		if n > (math.MaxUint64-d)/10 {
			return 0, fmt.Errorf("%q overflows 64 bits", string(b))
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
	return frequencyMHz(coreFrequencyPath(core), cpuinfo)
}

func coreFrequencyPath(core int) string {
	return filepath.Join(SysRoot, "devices/system/cpu", "cpu"+strconv.Itoa(core), "cpufreq/scaling_cur_freq")
}

func frequencyMHz(path string, cpuinfo func() uint64) uint64 {
	if khz, err := readUint(path); err == nil {
		return khz / 1000
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
	// spare is the older counters' storage, for the next read.
	spare []CPUTimes
	// frequencyPaths are the cores' cpufreq attributes, built once.
	frequencyPaths []string
	// sensorPath is the input the last lookup settled on: scanning hwmon
	// costs hundreds of syscalls, so only a failed read scans again.
	sensorPath string
	stat       keptFile
	// Sensor is the temp-sensor key: "auto" or a label substring.
	Sensor string
	// OmitFrequency and OmitTemperature leave out what the caller does
	// not show, which is most of a read's cost: a file per core and a
	// query of the sensor.
	OmitFrequency, OmitTemperature bool
}

// Close releases what the reader keeps open; a later Read opens it again.
func (r *CPUReader) Close() { r.stat.Close() }

// Read samples the counters, frequencies, and temperature.
func (r *CPUReader) Read() (CPU, error) {
	if r.stat.path == "" {
		r.stat.path = filepath.Join(ProcRoot, "stat")
	}
	stat, err := readCPUStat(&r.stat, r.spare)
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
		core := Core{UsagePercent: Usage(prev, cur)}
		if !r.OmitFrequency {
			core.FrequencyMHz = frequencyMHz(r.frequencyPath(i), cpuinfo)
		}
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
	r.spare, r.prev = r.prev.Cores, stat
	if !r.OmitTemperature {
		out.TemperatureC, out.HasTemperature = r.temperature()
	}
	return out, nil
}

func (r *CPUReader) frequencyPath(core int) string {
	for len(r.frequencyPaths) <= core {
		r.frequencyPaths = append(r.frequencyPaths, coreFrequencyPath(len(r.frequencyPaths)))
	}
	return r.frequencyPaths[core]
}

// temperature reads the input the last lookup settled on, and looks
// again when it no longer reads, or when no lookup has found one yet.
func (r *CPUReader) temperature() (float32, bool) {
	if r.sensorPath != "" {
		if temp, ok := readMilliCelsius(r.sensorPath); ok {
			return temp, true
		}
	}
	var temp float32
	var ok bool
	r.sensorPath, temp, ok = findSensor(r.Sensor)
	return temp, ok
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
	_, temp, ok := findSensor(sensor)
	return temp, ok
}

// findSensor is CPUTemperature, also naming the input that answered.
func findSensor(sensor string) (path string, temp float32, ok bool) {
	inputs := sensorInputs()
	find := func(pattern string) (string, float32, bool) {
		for _, in := range inputs {
			if !strings.Contains(strings.ToLower(in.label), pattern) {
				continue
			}
			if temp, ok := readMilliCelsius(in.path); ok {
				return in.path, temp, true
			}
		}
		if pattern == "" {
			zone := filepath.Join(SysRoot, "class/thermal/thermal_zone0/temp")
			if temp, ok := readMilliCelsius(zone); ok {
				return zone, temp, true
			}
		}
		return "", 0, false
	}
	if sensor != "auto" {
		return find(strings.ToLower(sensor))
	}
	for _, p := range cpuTempPatterns {
		if path, temp, ok := find(p); ok {
			return path, temp, true
		}
	}
	return "", 0, false
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

// readInto reads path until EOF or buf is full, with no *os.File: that
// would stat every attribute it opens. It is for procfs and sysfs files,
// which stat as a page whatever they hold.
func readInto(path string, buf []byte) (int, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer unix.Close(fd) //nolint:errcheck // a read-only attribute
	n := 0
	for n < len(buf) {
		m, err := unix.Read(fd, buf[n:])
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case err != nil:
			return n, fmt.Errorf("read %s: %w", path, err)
		case m == 0:
			return n, nil
		}
		n += m
	}
	return n, nil
}

// readUint reads a sysfs attribute holding one decimal number.
func readUint(path string) (uint64, error) {
	var buf [32]byte
	n, err := readInto(path, buf[:])
	if err != nil {
		return 0, err
	}
	line := bytes.TrimSpace(buf[:n])
	if len(line) == 0 {
		return 0, fmt.Errorf("%s is empty", path)
	}
	return parseDecimal(line)
}

// readLineOK reads a small sysfs or procfs attribute, trailing
// whitespace trimmed; false when it cannot be opened or read. A stack
// buffer takes the usual few bytes: only one that fills it is read again
// whole.
func readLineOK(path string) (string, bool) {
	var buf [128]byte
	n, err := readInto(path, buf[:])
	if err != nil {
		return "", false
	}
	if n < len(buf) {
		return trimLine(buf[:n]), true
	}
	whole, err := os.ReadFile(path) //nolint:gosec // a sysfs/procfs path under the roots
	if err != nil {
		return "", false
	}
	return trimLine(whole), true
}

// trimLine is b as a string without its trailing whitespace.
func trimLine(b []byte) string {
	return string(bytes.TrimRightFunc(b, func(r rune) bool { return r == '\n' || r == ' ' || r == '\t' || r == '\r' }))
}
