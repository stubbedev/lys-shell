package sysinfo

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeProc writes a minimal procfs tree and repoints ProcRoot at it.
func fakeProc(t *testing.T, stat, meminfo string) {
	t.Helper()
	dir := t.TempDir()
	if stat != "" {
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if meminfo != "" {
		if err := os.WriteFile(filepath.Join(dir, "meminfo"), []byte(meminfo), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := ProcRoot
	ProcRoot = dir
	t.Cleanup(func() { ProcRoot = old })
}

// The sysinfo crate's reduction: guest leaves user (and guest_nice
// leaves nice), work is user+nice+system+irq+softirq, and the total adds
// idle, iowait, steal, and the guest time once.
func TestReadCPUStatReducesLikeSysinfo(t *testing.T) {
	fakeProc(t, "cpu  100 20 200 1000 50 7 3 9 40 10\ncpu0 60 0 100 500 0 0 0 0 0 0\ncpu1 40 0 100 500 0 0 0 0 0\nintr 1\n", "")
	stat, err := ReadCPUStat()
	if err != nil {
		t.Fatal(err)
	}
	// work = (100-40) + (20-10) + 200 + 7 + 3 = 280
	// total = 280 + 1000 + 50 + 40 + 10 + 9 = 1389
	if stat.Global != (CPUTimes{Work: 280, Total: 1389}) {
		t.Errorf("global = %+v, want work 280 total 1389", stat.Global)
	}
	if len(stat.Cores) != 2 || stat.Cores[1] != (CPUTimes{Work: 140, Total: 640}) {
		t.Errorf("cores = %+v", stat.Cores)
	}
}

func TestReadCPUStatErrors(t *testing.T) {
	fakeProc(t, "", "")
	if _, err := ReadCPUStat(); err == nil {
		t.Error("missing stat file: want an error")
	}
	fakeProc(t, "intr 123\n", "")
	if _, err := ReadCPUStat(); err == nil {
		t.Error("no cpu line: want an error")
	}
	fakeProc(t, "cpu  1 x\n", "")
	if _, err := ReadCPUStat(); err == nil {
		t.Error("a non-numeric field: want an error")
	}
	// Missing trailing fields read as zero, as sysinfo does.
	fakeProc(t, "cpu  1 2\n", "")
	if stat, err := ReadCPUStat(); err != nil || stat.Global != (CPUTimes{Work: 3, Total: 3}) {
		t.Errorf("short line = %+v, %v", stat.Global, err)
	}
}

func TestUsageBetweenReadings(t *testing.T) {
	if got := Usage(CPUTimes{Work: 100, Total: 1000}, CPUTimes{Work: 190, Total: 1100}); got != 90 {
		t.Errorf("usage = %v, want 90", got)
	}
	// Against zero (the first refresh) it is the since-boot average.
	if got := Usage(CPUTimes{}, CPUTimes{Work: 25, Total: 100}); got != 25 {
		t.Errorf("first usage = %v, want 25", got)
	}
	// No elapsed time is no usage, and it never exceeds 100.
	if got := Usage(CPUTimes{Work: 5, Total: 5}, CPUTimes{Work: 5, Total: 5}); got != 0 {
		t.Errorf("idle interval = %v", got)
	}
	if got := Usage(CPUTimes{Work: 0, Total: 10}, CPUTimes{Work: 50, Total: 10}); got != 100 {
		t.Errorf("capped = %v", got)
	}
}

// fakeSys writes files under a fake sysfs root.
func fakeSys(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := SysRoot
	SysRoot = dir
	t.Cleanup(func() { SysRoot = old })
}

// A core's frequency is its cpufreq reading, and only a core without
// one asks the cpuinfo fallback, which is the first MHz line of
// /proc/cpuinfo, else 0.
func TestCoreFrequencyFromCpufreqThenCpuinfo(t *testing.T) {
	fakeSys(t, map[string]string{"devices/system/cpu/cpu0/cpufreq/scaling_cur_freq": "3400123\n"})
	fakeProc(t, "", "")
	if err := os.WriteFile(filepath.Join(ProcRoot, "cpuinfo"), []byte("processor\t: 0\ncpu MHz\t\t: 1999.7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	asked := 0
	fallback := func() uint64 { asked++; return cpuinfoMHz() }
	if got := CoreFrequencyMHz(0, fallback); got != 3400 || asked != 0 {
		t.Errorf("cpufreq = %d (cpuinfo asked %d times), want 3400 without asking", got, asked)
	}
	if got := CoreFrequencyMHz(1, fallback); got != 1999 || asked != 1 {
		t.Errorf("cpuinfo = %d (asked %d times), want 1999 asked once", got, asked)
	}
	if err := os.Remove(filepath.Join(ProcRoot, "cpuinfo")); err != nil {
		t.Fatal(err)
	}
	if got := cpuinfoMHz(); got != 0 {
		t.Errorf("no source = %d, want 0", got)
	}
}

// /proc/stat's cpu lines parse into the global and per-core counters,
// lines without the cpu prefix are skipped, and a counter that is no
// number is an error naming its field.
func TestReadCPUStatParsesTheCPULines(t *testing.T) {
	fakeSys(t, nil)
	fakeProc(t, "cpu  10 1 10 80 2 0 0 0 0 0\ncpu0 4 0 6 40 0 0 0 0\nintr 123 4 5\ncpu1 6 1 4 40 2 0 0 0 0 0\nctxt 99\n", "")
	stat, err := ReadCPUStat()
	if err != nil {
		t.Fatal(err)
	}
	want := CPUStat{Global: CPUTimes{Work: 21, Total: 103}, Cores: []CPUTimes{{Work: 10, Total: 50}, {Work: 11, Total: 53}}}
	if stat.Global != want.Global || !slices.Equal(stat.Cores, want.Cores) {
		t.Errorf("stat = %+v, want %+v", stat, want)
	}
	fakeProc(t, "cpu  10 x 10 80\n", "")
	if _, err := ReadCPUStat(); err == nil || !strings.Contains(err.Error(), "field 1") {
		t.Errorf("a non-numeric counter: %v, want an error naming field 1", err)
	}
	fakeProc(t, "intr 1\n", "")
	if _, err := ReadCPUStat(); err == nil {
		t.Error("a stat without a cpu line parsed")
	}
}

// A sysfs attribute reads trimmed, a long one whole, and a missing one
// reports false.
func TestReadLineOK(t *testing.T) {
	dir := t.TempDir()
	short, long := filepath.Join(dir, "short"), filepath.Join(dir, "long")
	longText := strings.Repeat("model-", 60)
	for path, text := range map[string]string{short: "coretemp\n", long: longText + " \n"} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got, ok := readLineOK(short); !ok || got != "coretemp" {
		t.Errorf("short = %q %v", got, ok)
	}
	if got, ok := readLineOK(long); !ok || got != longText {
		t.Errorf("long = %d bytes %v, want %d", len(got), ok, len(longText))
	}
	if _, ok := readLineOK(filepath.Join(dir, "missing")); ok {
		t.Error("a missing attribute read")
	}
}

func TestSensorsLabelLikeSysinfoAndAutoPicksByPattern(t *testing.T) {
	fakeSys(t, map[string]string{
		"class/hwmon/hwmon0/name":        "nvme\n",
		"class/hwmon/hwmon0/temp1_input": "41850\n",
		"class/hwmon/hwmon1/name":        "k10temp\n",
		"class/hwmon/hwmon1/temp1_input": "55125\n",
		"class/hwmon/hwmon1/temp1_label": "Tctl\n",
		"class/hwmon/hwmon1/temp3_input": "50000\n",
		"class/hwmon/hwmon1/temp3_label": "Tccd1\n",
		"class/hwmon/hwmon2/name":        "acpitz\n",
		"class/hwmon/hwmon2/temp2_label": "orphan\n",
	})
	inputs := sensorInputs()
	labels := make([]string, len(inputs))
	for i, in := range inputs {
		labels[i] = in.label
	}
	// A labeled input keeps its label; an unlabeled one is "<chip>
	// tempN"; a label without an input is no sensor.
	if strings.Join(labels, ",") != "nvme temp1,Tctl,Tccd1" {
		t.Fatalf("labels = %v", labels)
	}
	if temp, ok := CPUTemperature("auto"); !ok || temp != 55.125 {
		t.Errorf("auto = %v %v, want Tctl's 55.125", temp, ok)
	}
	if temp, ok := CPUTemperature("tccd"); !ok || temp != 50 {
		t.Errorf("named = %v %v, want 50", temp, ok)
	}
	if _, ok := CPUTemperature("nonexistent"); ok {
		t.Error("an unknown sensor matched")
	}
}

// The lookup reads only the input it settles on: an unmatched sensor's
// input, here a FIFO no one writes, which blocks whoever opens it as a
// device busy answering would stall, is never opened. An input that
// does not read is skipped for the next of its label, and with no hwmon
// sensor read thermal_zone0 answers the unlabeled lookup.
func TestCPUTemperatureReadsOnlyTheMatchedInput(t *testing.T) {
	fakeSys(t, map[string]string{
		"class/hwmon/hwmon0/name":        "nvme\n",
		"class/hwmon/hwmon1/name":        "coretemp\n",
		"class/hwmon/hwmon1/temp1_label": "Package id 0\n",
		"class/hwmon/hwmon1/temp1_input": "garbage\n",
		"class/hwmon/hwmon1/temp2_label": "Package id 0\n",
		"class/hwmon/hwmon1/temp2_input": "61000\n",
	})
	fifo := filepath.Join(SysRoot, "class/hwmon/hwmon0/temp1_input")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			f.Close()
		}
	})
	type result struct {
		temp float32
		ok   bool
	}
	got := make(chan result, 1)
	go func() {
		temp, ok := CPUTemperature("auto")
		got <- result{temp, ok}
	}()
	select {
	case r := <-got:
		if !r.ok || r.temp != 61 {
			t.Errorf("auto = %v %v, want the second Package id 0 input's 61", r.temp, r.ok)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CPUTemperature opened the unmatched nvme input")
	}

	fakeSys(t, map[string]string{
		"class/hwmon/hwmon0/name":          "nvme\n",
		"class/hwmon/hwmon0/temp1_input":   "unreadable\n",
		"class/thermal/thermal_zone0/temp": "45000\n",
	})
	if temp, ok := CPUTemperature(""); !ok || temp != 45 {
		t.Errorf("unlabeled lookup without a hwmon sensor = %v %v, want thermal_zone0's 45", temp, ok)
	}
	if _, ok := CPUTemperature("auto"); ok {
		t.Error("auto matched thermal_zone0, which carries no label")
	}
}

func TestCPUReaderFirstReadIsSinceBoot(t *testing.T) {
	fakeSys(t, nil)
	fakeProc(t, "cpu  10 0 10 80 0 0 0 0 0 0\ncpu0 10 0 10 80 0 0 0 0 0 0\ncpu1 0 0 0 100 0 0 0 0 0 0\n", "")
	r := &CPUReader{Sensor: "auto"}
	cpu, err := r.Read()
	if err != nil {
		t.Fatal(err)
	}
	if cpu.UsagePercent != 20 || len(cpu.Cores) != 2 || cpu.Cores[0].UsagePercent != 20 || cpu.HasTemperature {
		t.Errorf("first read = %+v", cpu)
	}
	if err := os.WriteFile(filepath.Join(ProcRoot, "stat"), []byte("cpu  60 0 10 80 0 0 0 0 0 0\ncpu0 10 0 10 80 0 0 0 0 0 0\ncpu1 50 0 0 100 0 0 0 0 0 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cpu, err = r.Read()
	if err != nil {
		t.Fatal(err)
	}
	if cpu.UsagePercent != 100 || cpu.Cores[1].UsagePercent != 100 || cpu.Cores[0].UsagePercent != 0 {
		t.Errorf("second read = %+v", cpu)
	}
}

func TestAggregateStorage(t *testing.T) {
	disks := []Disk{
		{MountPoint: "/", Filesystem: "ext4", TotalBytes: 100, AvailableBytes: 40, UsedBytes: 60},
		{MountPoint: "/home", Filesystem: "btrfs", TotalBytes: 300, AvailableBytes: 100, UsedBytes: 200},
	}
	one, ok := AggregateStorage(disks, []string{"/"})
	if !ok || math.Abs(float64(one.UsagePercent)-60) > 1e-4 || one.Filesystem != "ext4" || one.Multiple {
		t.Errorf("one = %+v %v", one, ok)
	}
	both, ok := AggregateStorage(disks, []string{"/", "/home/", "/nope"})
	if !ok || both.TotalBytes != 400 || both.UsedBytes != 260 || !both.Multiple || math.Abs(float64(both.UsagePercent)-65) > 1e-4 {
		t.Errorf("both = %+v %v", both, ok)
	}
	// A path that is not itself a mount point matches nothing (no
	// statfs of whatever contains it).
	if _, ok := AggregateStorage(disks, []string{"/home/user"}); ok {
		t.Error("a non-mount path matched")
	}
	if _, ok := AggregateStorage(disks, nil); ok {
		t.Error("no paths matched")
	}
}

func TestDisksSkipPseudoFilesystems(t *testing.T) {
	fakeProc(t, "", "")
	mounts := "/dev/nvme0n1p2 / ext4 rw 0 0\nproc /proc proc rw 0 0\ntmpfs /tmp tmpfs rw 0 0\n" +
		"/dev/sda1 /run/user/1000/x ext4 rw 0 0\n/dev/sdb1 /run/media/usb vfat rw 0 0\n/dev/sdc1 /mnt/my\\040disk xfs rw 0 0\n/dev/nvme0n1p2 / ext4 rw 0 0\n"
	if err := os.WriteFile(filepath.Join(ProcRoot, "mounts"), []byte(mounts), 0o600); err != nil {
		t.Fatal(err)
	}
	old := statvfs
	statvfs = func(path string) (uint64, uint64, bool) { return 1000, 250, path != "/mnt/empty" }
	t.Cleanup(func() { statvfs = old })
	var got []string
	for _, d := range Disks() {
		got = append(got, d.MountPoint+":"+d.Filesystem)
		if d.UsedBytes != 750 {
			t.Errorf("%s used = %d, want total-available", d.MountPoint, d.UsedBytes)
		}
	}
	if strings.Join(got, ",") != "/:ext4,/run/media/usb:vfat,/mnt/my disk:xfs" {
		t.Errorf("disks = %v", got)
	}
}

func TestReadMemory(t *testing.T) {
	fakeProc(t, "", "MemTotal:       16000000 kB\nMemAvailable:    8000000 kB\nSwapTotal:       4000000 kB\nSwapFree:        1000000 kB\n")
	mem, err := ReadMemory()
	if err != nil {
		t.Fatalf("ReadMemory: %v", err)
	}
	if mem.Total != 16000000*1024 || mem.Available != 8000000*1024 {
		t.Errorf("mem = %+v", mem)
	}
	if mem.UsagePercent() < 49.9 || mem.UsagePercent() > 50.1 {
		t.Errorf("usage = %f, want 50", mem.UsagePercent())
	}
	if mem.SwapPercent() < 74.9 || mem.SwapPercent() > 75.1 {
		t.Errorf("swap = %f, want 75", mem.SwapPercent())
	}
	// sysinfo's used = total - available.
	if mem.Used() != mem.Total-mem.Available {
		t.Errorf("used = %d", mem.Used())
	}
}

func TestReadMemoryErrors(t *testing.T) {
	fakeProc(t, "", "")
	if _, err := ReadMemory(); err == nil {
		t.Error("missing meminfo: want an error")
	}
	fakeProc(t, "", "MemTotal: 100 kB\n")
	if _, err := ReadMemory(); err == nil {
		t.Error("missing keys: want an error")
	}
	// A zero-total memory reads as 0%, not NaN.
	if got := (Memory{}).UsagePercent(); got != 0 {
		t.Errorf("empty memory usage = %f, want 0", got)
	}
}

func TestGiB(t *testing.T) {
	const gib = 1024 * 1024 * 1024
	if got := GiB(gib / 2); got != "0.5" {
		t.Errorf("GiB = %q, want 0.5", got)
	}
	if got := GiB(3 * gib); got != "3.0" {
		t.Errorf("GiB = %q, want 3.0", got)
	}
}
