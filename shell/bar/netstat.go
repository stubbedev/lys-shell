package bar

import (
	"errors"
	"time"

	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/jinja"
	"github.com/stubbedev/lys-shell/service/sysinfo"
)

// netstatLabel is helpers.rs's format_label over one interface's
// rates (kib "{:.0}", mib "{:.1}", gib "{:.2}", and the bytesize display).
func netstatLabel(format string, rate sysinfo.Rate) string {
	return jinja.RenderOr(format, netstatVars(rate))
}

func netstatVars(rate sysinfo.Rate) map[string]any {
	return map[string]any{
		"down_kib": bytesIn(rate.RxPerSec, kib, 0), "down_mib": bytesIn(rate.RxPerSec, mib, 1),
		"down_gib": bytesIn(rate.RxPerSec, gib, 2), "down_auto": sysinfo.AutoBytes(rate.RxPerSec),
		"up_kib": bytesIn(rate.TxPerSec, kib, 0), "up_mib": bytesIn(rate.TxPerSec, mib, 1),
		"up_gib": bytesIn(rate.TxPerSec, gib, 2), "up_auto": sysinfo.AutoBytes(rate.TxPerSec),
		"interface": rate.Interface,
	}
}

// netstat is the module: per-interface traffic rates.
type netstatModule struct {
	btn *barButton
}

func newNetstat(ctx ModuleContext) (Module, error) {
	if ctx.App == nil {
		return nil, errors.New("netstat: requires the application loop")
	}
	cfg := ctx.Config.Netstat
	m := &netstatModule{}
	m.btn = newBarButton(ctx, moduleIcon(ctx, cfg.Icon()), "")
	if cfg.PollIntervalMs == 0 {
		return m, nil
	}
	sampling := &netstatSampling{cfg: cfg, last: time.Now()}
	spec := samplerSpec[string]{
		key:       configKey("netstat", ctx.Config),
		source:    pollGrid(intconv.Duration(cfg.PollIntervalMs, time.Millisecond)),
		sample:    sampling.sample,
		same:      func(a, b string) bool { return a == b },
		closeRead: sampling.reader.Close,
	}
	if err := joinSampler(ctx.Life(), ctx.Polls, spec, func(text string) { ctx.Invoke(func() { m.btn.SetLabel(text) }) }); err != nil {
		return nil, err
	}
	return m, nil
}

type netstatSampling struct {
	cfg    config.NetstatConfig
	reader sysinfo.NetReader
	prev   map[string]sysinfo.NetTotals
	last   time.Time
}

// sample reads the counters and renders the label; the first pass primes the
// previous snapshot without a rate.
func (s *netstatSampling) sample() (string, bool) {
	at := time.Now()
	elapsed := at.Sub(s.last)
	s.last = at
	totals, err := s.reader.Read()
	if err != nil {
		return "", false
	}
	name, ok := sysinfo.SelectInterface(totals, s.cfg.Interface)
	if !ok {
		return "", true
	}
	now := totals[name]
	prev, seen := s.prev[name]
	s.prev = totals
	if !seen {
		return "", true
	}
	rate, err := sysinfo.NetRate(name, prev, now, elapsed.Seconds())
	if err != nil {
		return "", false
	}
	return netstatLabel(s.cfg.Format, rate), true
}

func (m *netstatModule) Root() widget.Widget { return m.btn }
