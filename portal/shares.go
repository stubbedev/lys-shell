package portal

import (
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/service/screenshare"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// shares is dev.stubbe.lys.ScreenShares1: the sessions streaming the
// screen, a signal when one starts or ends, and a way to stop one, for
// the shell's bar to show what is shared.
type shares struct {
	conn     *dbus.Conn
	sessions *sessions
	// titles are the windows' titles by their identifiers (lys-wm's; a
	// fake in tests).
	titles func() map[string]string
}

func newShares(conn *dbus.Conn, s *sessions) *shares {
	sh := &shares{conn: conn, sessions: s, titles: lysWMWindowTitles}
	s.sharesChanged = sh.changed
	return sh
}

// lysWMWindowTitles are lys-wm's windows' titles by the identifiers a
// window share names them by; none where lys-wm does not run.
func lysWMWindowTitles() map[string]string {
	if !lyswm.IsRunning() {
		return nil
	}
	windows, err := lyswm.Windows()
	if err != nil {
		warnf("screen shares: no window titles: %v", err)
		return nil
	}
	titles := make(map[string]string, len(windows))
	for _, w := range windows {
		titles[lyswm.ToplevelIdentifier(w.ID)] = w.Title
	}
	return titles
}

func (sh *shares) iface() dbusx.Interface {
	return dbusx.Interface{
		Name:    screenshare.Interface,
		Methods: sharesObject{sh},
		Signals: []dbusx.SignalDecl{screenshare.Changed},
	}
}

// changed tells the shell the shares changed.
func (sh *shares) changed() {
	if err := screenshare.Changed.Emit(sh.conn, ObjectPath, struct{}{}); err != nil {
		warnf("screen shares: %v", err)
	}
}

// streaming is whether s streams anything.
func streaming(s *session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cast != nil && len(s.cast.streams) > 0
}

// list is every share running now.
func (sh *shares) list() []screenshare.Share {
	live := sh.sessions.each(streaming)
	out := make([]screenshare.Share, 0, len(live))
	var titles map[string]string
	for _, s := range live {
		share := screenshare.Share{Session: s.path, App: s.appID, Kind: screenshare.KindScreenCast}
		if s.kind == kindRemoteDesktop {
			share.Kind = screenshare.KindRemoteDesktop
		}
		s.mu.Lock()
		streams := s.cast.streams
		s.mu.Unlock()
		for _, st := range streams {
			t := screenshare.Target{Name: st.target.Name}
			switch st.target.Kind {
			case sharepicker.TargetWindow:
				if titles == nil {
					titles = sh.titles()
				}
				t.Kind, t.Title = screenshare.TargetWindow, titles[st.target.Name]
			case sharepicker.TargetRegion:
				t.Kind = screenshare.TargetRegion
			case sharepicker.TargetDynamic:
				t.Kind = screenshare.TargetDynamic
			default:
				t.Kind = screenshare.TargetOutput
			}
			share.Targets = append(share.Targets, t)
		}
		out = append(out, share)
	}
	return out
}

// sharesObject carries the interface's D-Bus methods.
type sharesObject struct{ sh *shares }

// List is every share running now.
func (o sharesObject) List() ([]screenshare.Share, *dbus.Error) { return o.sh.list(), nil }

// Stop ends the share of session: its streams stop and the app hears
// its session closed.
func (o sharesObject) Stop(session dbus.ObjectPath) *dbus.Error {
	s, ok := o.sh.sessions.get(session)
	if !ok || !streaming(s) {
		return dbusx.InvalidArgs("no screen share at " + string(session))
	}
	o.sh.sessions.close(s)
	return nil
}
