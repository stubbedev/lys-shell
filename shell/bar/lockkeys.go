package bar

import (
	"github.com/stubbedev/lys-shell/service/keytoggles"
	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/shell/osd"
)

func lockKeySource() osd.LockKeys {
	if detectCompositor() == compositorLysWM {
		return lysWMLockKeys
	}
	return evdevLockKeys
}

func evdevLockKeys(fn func(keytoggles.Event)) (func(), error) {
	return keytoggles.Watch(fn), nil
}

func lysWMLockKeys(fn func(keytoggles.Event)) (func(), error) {
	w, err := lyswm.Watch(lyswm.EventLocks)
	if err != nil {
		return nil, err
	}
	last := w.Locks()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range w.Ticks() {
			next := w.Locks()
			for _, e := range lockChanges(last, next) {
				fn(e)
			}
			last = next
		}
	}()
	return func() {
		w.Close()
		<-done
	}, nil
}

func lockChanges(prev, next lyswm.Locks) []keytoggles.Event {
	var events []keytoggles.Event
	for _, k := range []struct {
		key        keytoggles.Key
		prev, next bool
	}{
		{keytoggles.CapsLock, prev.Caps, next.Caps},
		{keytoggles.NumLock, prev.Num, next.Num},
		{keytoggles.ScrollLock, prev.Scroll, next.Scroll},
	} {
		if k.prev != k.next {
			events = append(events, keytoggles.Event{Key: k.key, Active: k.next})
		}
	}
	return events
}
