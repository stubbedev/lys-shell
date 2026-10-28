package notifications

import (
	"fmt"
	"sync/atomic"

	"github.com/stubbedev/lys-lib/dbusx"
	"github.com/stubbedev/lys-lib/intconv"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// Server serves org.freedesktop.Notifications on the session bus and
// lys-shell's dev.stubbe.lys.Notifications1 extension beside it.
type Server struct {
	conn        *dbus.Conn
	svc         *Service
	releaseExts func()
}

// Serve requests the well-known names and exports the objects; Release
// drops them.
func Serve(conn *dbus.Conn, svc *Service) (*Server, error) {
	svc.SetEmitter(func(signal string, args ...any) {
		if err := conn.Emit(ObjPath, signal, args...); err != nil {
			return
		}
	})
	server := &Server{conn: conn, svc: svc}
	if err := conn.Export(server, ObjPath, Interface); err != nil {
		return nil, fmt.Errorf("notifications: export: %w", err)
	}
	reply, err := conn.RequestName(Interface, dbus.NameFlagDoNotQueue)
	if err != nil {
		return nil, fmt.Errorf("notifications: request name: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return nil, fmt.Errorf("notifications: %s is already owned", Interface)
	}
	release, err := ServeLysShell(conn, svc)
	if err != nil {
		_, _ = conn.ReleaseName(Interface)
		return nil, err
	}
	server.releaseExts = release
	return server, nil
}

// Release drops the names.
func (s *Server) Release() error {
	if s.releaseExts != nil {
		s.releaseExts()
	}
	_, err := s.conn.ReleaseName(Interface)
	return err
}

// Notify handles the spec's Notify call.
func (s *Server) Notify(appName string, replacesID uint32, appIcon, summary, body string, actions []string, hints map[string]dbus.Variant, expireTimeout int32) (uint32, *dbus.Error) {
	return s.svc.NotifyHints(appName, replacesID, appIcon, summary, body, actions, hints, expireTimeout), nil
}

// CloseNotification handles the spec's CloseNotification call; an
// unknown id is not an error (the spec leaves it to the server).
func (s *Server) CloseNotification(id uint32) *dbus.Error {
	s.svc.Close(id, ClosedCall)
	return nil
}

// GetCapabilities handles the spec's GetCapabilities call.
func (s *Server) GetCapabilities() ([]string, *dbus.Error) {
	return Capabilities, nil
}

// GetServerInformation handles the spec's information call.
func (s *Server) GetServerInformation() (string, string, string, string, *dbus.Error) {
	return ServerName, ServerVendor, ServerVersion, SpecVersion, nil
}

// lys-shell's control extension, which `lys-shell notify` drives
// (wayle-notification/src/lys_shell_daemon.rs).
const (
	LysShellName = "dev.stubbe.lys.Notifications1"
	LysShellPath = dbus.ObjectPath("/dev/stubbe/lys/Notifications")
)

// defaultPopupDuration is the service builder's popup_duration.
const defaultPopupDuration = 5000

// lysShellDaemon is the dev.stubbe.lys.Notifications1 object (LysShellDaemon).
type lysShellDaemon struct {
	svc           *Service
	popupDuration atomic.Uint32
}

// DismissAll empties the history.
func (d *lysShellDaemon) DismissAll() *dbus.Error {
	d.svc.DismissAll()
	return nil
}

// Dismiss removes one notification as dismissed by the user.
func (d *lysShellDaemon) Dismiss(id uint32) *dbus.Error {
	d.svc.Close(id, Dismissed)
	return nil
}

// SetDnd sets do-not-disturb.
func (d *lysShellDaemon) SetDnd(enabled bool) *dbus.Error {
	d.svc.SetDND(enabled)
	return nil
}

// ToggleDnd flips do-not-disturb.
func (d *lysShellDaemon) ToggleDnd() *dbus.Error {
	d.svc.ToggleDND()
	return nil
}

// SetPopupDuration stores the popup duration the property reports.
func (d *lysShellDaemon) SetPopupDuration(ms uint32) *dbus.Error {
	d.popupDuration.Store(ms)
	return nil
}

// ListRow is one List row: (id, arrival in Unix milliseconds, app,
// summary, body). The last row's (Stamp, ID) is the next page's cursor.
type ListRow struct {
	ID      uint32
	Stamp   int64
	App     string
	Summary string
	Body    string
}

// List is the history's first page, newest first: up to limit rows,
// fewer at the end of the history.
func (d *lysShellDaemon) List(limit uint32) ([]ListRow, *dbus.Error) {
	return d.page(nil, limit)
}

// ListBefore is the page after the row (stamp, id): up to limit rows
// strictly older, newest first.
func (d *lysShellDaemon) ListBefore(stamp int64, id, limit uint32) ([]ListRow, *dbus.Error) {
	return d.page(&Key{Stamp: stamp, ID: id}, limit)
}

func (d *lysShellDaemon) page(before *Key, limit uint32) ([]ListRow, *dbus.Error) {
	notifs, err := d.svc.Page(before, intconv.Clamp[int](limit))
	if err != nil {
		return nil, dbus.MakeFailedError(err)
	}
	rows := make([]ListRow, 0, len(notifs))
	for _, n := range notifs {
		rows = append(rows, ListRow{n.ID, n.Key().Stamp, n.AppName, n.Summary, n.Body})
	}
	return rows, nil
}

// ServeLysShell exports dev.stubbe.lys.Notifications1 over svc.
func ServeLysShell(conn *dbus.Conn, svc *Service) (func(), error) {
	d := &lysShellDaemon{svc: svc}
	d.popupDuration.Store(defaultPopupDuration)
	return dbusx.Serve(conn, dbusx.Service{
		Name:      LysShellName,
		Path:      LysShellPath,
		Interface: LysShellName,
		Methods:   d,
		Properties: dbusx.Getters{
			"Dnd":           func() any { return svc.DND() },
			"PopupDuration": func() any { return d.popupDuration.Load() },
			"Count":         func() any { return intconv.Clamp[uint32](svc.Count()) },
			"PopupCount":    func() any { return intconv.Clamp[uint32](len(svc.Popups())) },
		},
	})
}
