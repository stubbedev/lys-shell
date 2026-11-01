package sysinfo

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

func TestSelectInterface(t *testing.T) {
	totals := map[string]NetTotals{
		"lo":    {Rx: 999999, Tx: 999999},
		"wlan0": {Rx: 100, Tx: 50},
		"eth0":  {Rx: 10, Tx: 20},
	}
	if got, ok := SelectInterface(totals, "auto"); !ok || got != "wlan0" {
		t.Errorf("auto = %q ok=%v, want wlan0", got, ok)
	}
	if got, ok := SelectInterface(totals, "eth0"); !ok || got != "eth0" {
		t.Errorf("exact = %q ok=%v", got, ok)
	}
	if _, ok := SelectInterface(totals, "missing"); ok {
		t.Error("unknown interface matched")
	}
	// Loopback never wins auto even at full traffic.
	onlyLo := map[string]NetTotals{"lo": {Rx: 1, Tx: 1}}
	if _, ok := SelectInterface(onlyLo, "auto"); ok {
		t.Error("loopback won auto")
	}
}

func TestNetRate(t *testing.T) {
	rate, err := NetRate("wlan0", NetTotals{Rx: 1000, Tx: 500}, NetTotals{Rx: 3000, Tx: 1500}, 2)
	if err != nil {
		t.Fatalf("NetRate: %v", err)
	}
	if rate.RxPerSec != 1000 || rate.TxPerSec != 500 {
		t.Errorf("rate = %+v, want 1000/500", rate)
	}
	// A counter reset (reboot) reads as zero traffic, not negative.
	rate, err = NetRate("wlan0", NetTotals{Rx: 5000}, NetTotals{Rx: 10}, 1)
	if err != nil {
		t.Fatalf("reset NetRate: %v", err)
	}
	if rate.RxPerSec != 0 {
		t.Errorf("reset rate = %d, want 0", rate.RxPerSec)
	}
	// No elapsed time is an error, not a divide by zero.
	if _, err := NetRate("wlan0", NetTotals{}, NetTotals{}, 0); err == nil {
		t.Error("zero elapsed: want an error")
	}
}

func TestAutoBytes(t *testing.T) {
	for _, tc := range []struct {
		bytes uint64
		want  string
	}{
		// bytesize 2.3.1's own output for these values.
		{0, "0 B"},
		{999, "999 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1048575, "1024.0 KiB"},
		{1048576, "1.0 MiB"},
		{1073741824, "1.0 GiB"},
		{1099511627776, "1.0 TiB"},
		{1125899906842624, "1.0 PiB"},
		{1152921504606846976, "1.0 EiB"},
		{5000, "4.9 KiB"},
		{123456789, "117.7 MiB"},
		{999999999999, "931.3 GiB"},
		{18446744073709551615, "16.0 EiB"},
	} {
		if got := AutoBytes(tc.bytes); got != tc.want {
			t.Errorf("AutoBytes(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

func fakeNetDev(t *testing.T, body string) {
	t.Helper()
	fakeProc(t, "", "")
	path := filepath.Join(ProcRoot, "net/dev")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadNetTotals(t *testing.T) {
	fakeNetDev(t, "Inter-|   Receive\n face |bytes    packets\n"+
		"    lo: 100 1 0 0 0 0 0 0 10 2 0 0 0 0 0 0\n"+
		" wlan0:2048 5 0 0 0 0 0 0 4096 6 0 0 0 0 0 0\n"+
		"eth0:1: 7 0 0 0 0 0 0 0 8 0 0 0 0 0 0 0\n"+
		" broken: nope 5 0 0 0 0 0 0 4096 6 0 0 0 0 0 0\n"+
		" badtx: 1 5 0 0 0 0 0 0 nope 6 0 0 0 0 0 0\n"+
		" short: 1 2 3\n")
	totals, err := ReadNetTotals()
	if err != nil {
		t.Fatalf("ReadNetTotals: %v", err)
	}
	want := map[string]NetTotals{
		"lo":     {Rx: 100, Tx: 10},
		"wlan0":  {Rx: 2048, Tx: 4096},
		"eth0:1": {Rx: 7, Tx: 8},
	}
	if !maps.Equal(totals, want) {
		t.Errorf("totals = %+v, want %+v; a line that does not parse skips its interface", totals, want)
	}
}

func TestReadNetTotalsWithoutTheFile(t *testing.T) {
	fakeProc(t, "", "")
	if _, err := ReadNetTotals(); err == nil {
		t.Error("missing net/dev: want an error")
	}
}

func TestParseNetDevCountersDoesNotAllocate(t *testing.T) {
	line := []byte(" 2048 5 0 0 0 0 0 0 4096 6 0 0 0 0 0 0")
	if got := testing.AllocsPerRun(100, func() {
		if totals, ok := parseNetDevCounters(line); !ok || totals.Tx != 4096 {
			t.Fatal(totals, ok)
		}
	}); got != 0 {
		t.Errorf("%v allocations per line", got)
	}
}
