package portal

import (
	"errors"

	"github.com/stubbedev/lys-lib/portaldialogs"

	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// virtualSize is the size of a virtual output a session streams, all a
// restore token keeps of it.
type virtualSize struct{ width, height int32 }

// defaultVirtual is the virtual output an app gets: the ScreenCast API
// carries no size of its own (the frontend drops options it does not
// know), so a full-HD screen, unless a restore token keeps another.
var defaultVirtual = virtualSize{1920, 1080}

// The virtual output's refresh rate and scale: a stream runs at most at
// defaultFPS, and the device showing it scales the picture itself.
const (
	virtualRefreshMHz = defaultFPS * 1000
	virtualScale      = 1
)

const virtualDisplayIcon = "ld-monitor-symbolic"

// virtualConsent asks before an app adds an output: windows can move
// onto it, and only the app shows it.
var virtualConsent = portaldialogs.AccessRequest{
	Title:      "Add a virtual display?",
	Body:       "It wants to add a virtual display: your desktop extends onto a new screen that only it shows, and windows can be moved there.",
	GrantLabel: "Add Display", DenyLabel: "Deny", Icon: virtualDisplayIcon,
}

// createLysWMOutput makes a lys-wm virtual output of the size.
func createLysWMOutput(size virtualSize) (string, error) {
	return lyswm.CreateOutput(lyswm.Mode{Width: int(size.width), Height: int(size.height), RefreshMHz: virtualRefreshMHz}, virtualScale)
}

// availableSources is what the session can share: monitors and windows,
// and virtual outputs while lys-wm makes them.
func (c *screenCast) availableSources() uint32 {
	offered, err := c.virtualOffered()
	if err != nil && !errors.Is(err, lyswm.ErrNotRunning) {
		warnf("screencast: virtual outputs are not offered: %v", err)
	}
	if offered {
		return pickedSources | sourceVirtual
	}
	return pickedSources
}

// virtualStream streams a virtual output the session made: closing it
// removes the output, whose workspaces move as on an unplug.
type virtualStream struct {
	screenStream
	name   string
	size   virtualSize
	remove func(name string) error
}

func (v *virtualStream) Close() {
	v.screenStream.Close()
	if err := v.remove(v.name); err != nil {
		warnf("screencast: remove virtual output %s: %v", v.name, err)
	}
}

// virtualOf is the virtual output a stream shows; false for a monitor or
// a window.
func virtualOf(st startedStream) (*virtualStream, bool) {
	v, ok := st.stream.(*virtualStream)
	return v, ok
}

// shareVirtual extends the desktop onto a new output and streams it: of
// the size a restore token kept, or, once the user consents, of the
// default size. A denied consent makes no output; one that does not
// stream is removed again.
func (c *screenCast) shareVirtual(call *call, s *session, cfg castState) (sharedScreens, uint32) {
	size := defaultVirtual
	if cfg.restoreVirtual != nil {
		size = *cfg.restoreVirtual
	} else {
		allowed, _, err := portaldialogs.NewClient(c.conn).Access(call.on(portaldialogs.ServiceName), call.caller, virtualConsent)
		if code := call.response("screencast virtual display", allowed, err); code != ResponseSuccess {
			return sharedScreens{}, code
		}
	}
	name, err := c.createVirtual(size)
	if err != nil {
		warnf("screencast: no virtual output of %dx%d: %v", size.width, size.height, err)
		return sharedScreens{}, ResponseOther
	}
	target := sharepicker.Target{Kind: sharepicker.TargetOutput, Name: name}
	streams, err := c.beginWith(s, []sharepicker.Target{target}, cfg.cursorMode, func(t sharepicker.Target, cursor bool, fps uint32) (screenStream, error) {
		st, err := c.start(t, cursor, fps)
		if err != nil {
			return nil, err
		}
		return &virtualStream{screenStream: st, name: name, size: size, remove: c.removeVirtual}, nil
	})
	switch {
	case errors.Is(err, errSessionClosed):
		return sharedScreens{}, ResponseCancelled
	case err != nil:
		warnf("screencast: virtual output %s does not stream: %v", name, err)
		if err := c.removeVirtual(name); err != nil {
			warnf("screencast: remove virtual output %s: %v", name, err)
		}
		return sharedScreens{}, ResponseOther
	}
	return c.reply(streams, nil, true), ResponseSuccess
}
