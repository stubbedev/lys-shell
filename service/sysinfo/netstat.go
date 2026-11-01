package sysinfo

import (
	"bytes"
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-lib/third_party/gomath"
)

// NetTotals is one interface's lifetime byte counters.
type NetTotals struct {
	Rx uint64
	Tx uint64
}

// SelectInterface is netstat's helpers.rs select_interface: "auto"
// picks the busiest non-loopback interface, anything else matches by
// exact name (ok=false when absent).
func SelectInterface(totals map[string]NetTotals, iface string) (string, bool) {
	if iface != "auto" {
		if _, ok := totals[iface]; ok {
			return iface, true
		}
		return "", false
	}
	best, bestLoad := "", uint64(0)
	for name, totals := range totals {
		if strings.HasPrefix(name, "lo") {
			continue
		}
		if load := totals.Rx + totals.Tx; load > bestLoad {
			best, bestLoad = name, load
		}
	}
	return best, best != ""
}

var errNetRate = errors.New("sysinfo: interface counters went backwards")

// Rate is one interface's bytes-per-second over the sample window.
type Rate struct {
	Interface string
	RxPerSec  uint64
	TxPerSec  uint64
}

// NetRate computes the per-second rate of one interface between two
// total snapshots (elapsed in seconds).
func NetRate(name string, prev, now NetTotals, elapsed float64) (Rate, error) {
	if elapsed <= 0 {
		return Rate{}, errNetRate
	}
	if now.Rx < prev.Rx || now.Tx < prev.Tx {
		// A counter reset (rebind, suspend) reads as no traffic for
		// this window.
		return Rate{Interface: name}, nil
	}
	return Rate{
		Interface: name,
		RxPerSec:  uint64(float64(now.Rx-prev.Rx) / elapsed),
		TxPerSec:  uint64(float64(now.Tx-prev.Tx) / elapsed),
	}, nil
}

// AutoBytes is bytesize 2's Display: IEC (1024-based) units with one
// decimal, "KiB" through "EiB", plain bytes below a kibibyte. The unit
// is picked from ln(size)/ln(1024), as its std build does.
func AutoBytes(bytes uint64) string {
	const kib = 1024
	if bytes < kib {
		return strconv.FormatUint(bytes, 10) + " B"
	}
	size := float64(bytes)
	exp := int(gomath.Log(size) / 6.931471805599453)
	prefix := "KMGTPE"[exp-1]
	return strconv.FormatFloat(size/gomath.Pow(kib, float64(exp)), 'f', 1, 64) + " " + string(prefix) + "iB"
}

// ReadNetTotals reads every interface's rx/tx byte totals once.
func ReadNetTotals() (map[string]NetTotals, error) {
	var r NetReader
	defer r.Close()
	return r.Read()
}

// NetReader polls /proc/net/dev through a kept descriptor: one file
// however many interfaces there are, rather than two per interface in
// sysfs.
type NetReader struct{ file keptFile }

// Close releases the descriptor; a later Read opens it again.
func (r *NetReader) Close() { r.file.Close() }

// Read parses the totals in place. A line that does not parse skips its
// interface.
func (r *NetReader) Read() (map[string]NetTotals, error) {
	if r.file.path == "" {
		r.file.path = filepath.Join(ProcRoot, "net/dev")
	}
	content, err := r.file.read(nil)
	if err != nil {
		return nil, err
	}
	out := map[string]NetTotals{}
	for len(content) > 0 {
		var line []byte
		line, content, _ = bytes.Cut(content, []byte{'\n'})
		name, counters, found := bytes.CutLast(line, []byte{':'})
		if !found {
			continue
		}
		if totals, ok := parseNetDevCounters(counters); ok {
			out[string(bytes.TrimSpace(name))] = totals
		}
	}
	return out, nil
}

// parseNetDevCounters reads the receive bytes and transmit bytes, the
// first and ninth of a /proc/net/dev line's counters.
func parseNetDevCounters(fields []byte) (NetTotals, bool) {
	const txField = 8
	var totals NetTotals
	for i := range txField + 1 {
		var field []byte
		field, fields = nextField(fields)
		if len(field) == 0 {
			return NetTotals{}, false
		}
		if i != 0 && i != txField {
			continue
		}
		n, err := parseDecimal(field)
		if err != nil {
			return NetTotals{}, false
		}
		if i == 0 {
			totals.Rx = n
		} else {
			totals.Tx = n
		}
	}
	return totals, true
}
