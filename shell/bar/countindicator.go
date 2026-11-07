package bar

import (
	"context"
	"strconv"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
)

// subscription is a followed list's change feed: a tick per change and
// the func that unsubscribes.
type subscription func() (<-chan struct{}, func())

// countIndicator is a bar button for a list a service follows (screen
// shares, background apps): shown while the list holds anything, its
// length beside the icon under label-show.
type countIndicator struct {
	ctx       ModuleContext
	btn       *barButton
	labelShow bool
	chrome    moduleChrome
	count     int
	stop      context.CancelFunc
}

func newCountIndicator(ctx ModuleContext, icon config.IconConfig, labelShow bool, length func() int, subscribe subscription) *countIndicator {
	m := &countIndicator{ctx: ctx, labelShow: labelShow}
	m.btn = newBarButton(ctx, moduleIcon(ctx, icon), "")
	m.apply(length())
	life, stop := context.WithCancel(context.Background())
	m.stop = stop
	followTicks(life, subscribe, func() {
		count := length()
		m.ctx.Invoke(func() { m.apply(count) })
	})
	return m
}

// apply shows count entries, the indicator hidden at none.
func (m *countIndicator) apply(count int) {
	m.count = count
	text := ""
	if m.labelShow {
		text = strconv.Itoa(count)
	}
	m.btn.SetLabel(text)
	m.showChrome()
}

func (m *countIndicator) showChrome() {
	if m.chrome != nil {
		m.chrome.SetVisible(m.count > 0)
	}
}

func (m *countIndicator) setChrome(c moduleChrome) {
	m.chrome = c
	m.showChrome()
}

func (m *countIndicator) Root() widget.Widget { return m.btn }

// Stop unsubscribes from the list.
func (m *countIndicator) Stop() { m.stop() }

// followTicks runs changed, off the loop, after every tick of
// subscribe's feed until life ends or the feed closes.
func followTicks(life context.Context, subscribe subscription, changed func()) {
	ticks, unsubscribe := subscribe()
	go func() {
		defer unsubscribe()
		for {
			select {
			case <-life.Done():
				return
			case _, ok := <-ticks:
				if !ok {
					return
				}
				changed()
			}
		}
	}()
}
