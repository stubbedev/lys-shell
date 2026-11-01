package bar

import (
	"context"
	"errors"
	"time"

	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/jinja"
	"github.com/stubbedev/lys-shell/service/sysinfo"
)

// netstatLabel is helpers.rs's format_label over one interface's
// rates (kib "{:.0}", mib "{:.1}", gib "{:.2}", and the bytesize display).
func netstatLabel(format string, rate sysinfo.Rate) string {
	return jinja.RenderOr(format, map[string]any{
		"down_kib": bytesIn(rate.RxPerSec, kib, 0), "down_mib": bytesIn(rate.RxPerSec, mib, 1),
		"down_gib": bytesIn(rate.RxPerSec, gib, 2), "down_auto": sysinfo.AutoBytes(rate.RxPerSec),
		"up_kib": bytesIn(rate.TxPerSec, kib, 0), "up_mib": bytesIn(rate.TxPerSec, mib, 1),
		"up_gib": bytesIn(rate.TxPerSec, gib, 2), "up_auto": sysinfo.AutoBytes(rate.TxPerSec),
		"interface": rate.Interface,
	})
}

// netstat is the module: per-interface traffic rates.
type netstatModule struct {
	ctx     ModuleContext
	btn     *barButton
	cancel  context.CancelFunc
	prev    map[string]sysinfo.NetTotals
	current sysinfo.Rate
	reader  sysinfo.NetReader
	// shownLabel is the last text sent to the loop, so an unchanged rate
	// does not wake it; shownOnce marks that one was sent.
	shownLabel string
	shownOnce  bool
}

func newNetstat(ctx ModuleContext) (Module, error) {
	if ctx.App == nil {
		return nil, errors.New("netstat: requires the application loop")
	}
	cfg := ctx.Config.Netstat
	m := &netstatModule{ctx: ctx}
	m.btn = newBarButton(ctx, moduleIcon(ctx, cfg.Icon()), "")
	runCtx, cancel := context.WithCancel(ctx.Life())
	m.cancel = cancel
	if cfg.PollIntervalMs > 0 {
		go func() {
			interval := intconv.Duration(cfg.PollIntervalMs, time.Millisecond)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			defer m.reader.Close()
			for {
				select {
				case <-runCtx.Done():
					return
				case <-ticker.C:
				}
				m.sample(interval)
			}
		}()
	}
	return m, nil
}

// sample reads the counters and re-renders; the first pass primes the
// previous snapshot without a rate.
func (m *netstatModule) sample(elapsed time.Duration) {
	cfg := m.ctx.Config.Netstat
	totals, err := m.reader.Read()
	if err != nil {
		return
	}
	name, ok := sysinfo.SelectInterface(totals, cfg.Interface)
	if !ok {
		m.show("")
		return
	}
	now := totals[name]
	prev, seen := m.prev[name]
	m.prev = totals
	if !seen {
		m.show("")
		return
	}
	rate, err := sysinfo.NetRate(name, prev, now, elapsed.Seconds())
	if err != nil {
		return
	}
	m.current = rate
	text := ""
	if cfg.LabelShow {
		text = netstatLabel(cfg.Format, rate)
	}
	m.show(text)
}

// show sends a text that differs from the last one to the button.
func (m *netstatModule) show(text string) {
	if m.shownOnce && text == m.shownLabel {
		return
	}
	m.shownOnce, m.shownLabel = true, text
	m.ctx.Invoke(func() { m.btn.SetLabel(text) })
}

func (m *netstatModule) Root() widget.Widget { return m.btn }

// Stop ends the sampling loop.
func (m *netstatModule) Stop() { m.cancel() }
