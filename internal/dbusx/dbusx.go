// Package dbusx holds the session-bus plumbing lys-shell's daemons share:
// exporting an interface under a well-known name the way zbus's
// object_server().at + request_name does, and serving zbus-style
// #[zbus(property)] getters (computed on every read) over
// org.freedesktop.DBus.Properties.
package dbusx

import (
	"fmt"
	"os"
	"slices"
	"sort"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

// Error names the Rust daemons reply with (zbus fdo::Error variants).
const (
	ErrFailed          = "org.freedesktop.DBus.Error.Failed"
	ErrInvalidArgs     = "org.freedesktop.DBus.Error.InvalidArgs"
	ErrUnknownProperty = "org.freedesktop.DBus.Error.UnknownProperty"
	ErrUnknownIface    = "org.freedesktop.DBus.Error.UnknownInterface"
	ErrPropertyRO      = "org.freedesktop.DBus.Error.PropertyReadOnly"
)

// Failed is fdo::Error::Failed(msg).
func Failed(msg string) *dbus.Error { return dbus.NewError(ErrFailed, []any{msg}) }

// InvalidArgs is fdo::Error::InvalidArgs(msg).
func InvalidArgs(msg string) *dbus.Error { return dbus.NewError(ErrInvalidArgs, []any{msg}) }

// Getters maps property names to their read functions. Each returns a
// value godbus can marshal; the D-Bus signature is taken from it.
type Getters map[string]func() any

// Setters maps writable property names to their write functions, which
// decode the variant themselves and refuse a wrong type.
type Setters map[string]func(dbus.Variant) *dbus.Error

// properties is org.freedesktop.DBus.Properties for the interfaces of
// one object.
type properties struct {
	ifaces  map[string]Getters
	setters map[string]Setters
}

func (p *properties) of(iface string) (Getters, *dbus.Error) {
	getters, ok := p.ifaces[iface]
	if !ok {
		return nil, dbus.NewError(ErrUnknownIface, []any{"Unknown interface '" + iface + "'"})
	}
	return getters, nil
}

// Get returns one property.
func (p *properties) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	getters, derr := p.of(iface)
	if derr != nil {
		return dbus.Variant{}, derr
	}
	get, ok := getters[name]
	if !ok {
		return dbus.Variant{}, dbus.NewError(ErrUnknownProperty, []any{"Unknown property '" + name + "'"})
	}
	return dbus.MakeVariant(get()), nil
}

// GetAll returns every property.
func (p *properties) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	getters, derr := p.of(iface)
	if derr != nil {
		return nil, derr
	}
	out := make(map[string]dbus.Variant, len(getters))
	for name, get := range getters {
		out[name] = dbus.MakeVariant(get())
	}
	return out, nil
}

// Set writes a property that has a setter; the rest are read-only.
func (p *properties) Set(iface, name string, value dbus.Variant) *dbus.Error {
	getters, derr := p.of(iface)
	if derr != nil {
		return derr
	}
	if _, ok := getters[name]; !ok {
		return dbus.NewError(ErrUnknownProperty, []any{"Unknown property '" + name + "'"})
	}
	set, ok := p.setters[iface][name]
	if !ok {
		return dbus.NewError(ErrPropertyRO, []any{"Property '" + name + "' is read-only"})
	}
	return set(value)
}

// Service is one exported interface.
type Service struct {
	// Name is the well-known bus name and Path the object path.
	Name string
	Path dbus.ObjectPath
	// Interface is the D-Bus interface the methods are exported as.
	Interface string
	// Methods is the object whose exported methods (each ending in a
	// *dbus.Error result) form the interface.
	Methods any
	// Properties are the read-only properties.
	Properties Getters
}

// Interface is one D-Bus interface of an object: its methods (an
// object whose exported methods each end in a *dbus.Error result), its
// properties, and the writers of those that are writable.
type Interface struct {
	Name       string
	Methods    any
	Properties Getters
	Setters    Setters
	// Ordered are the methods whose calls run one at a time in arrival
	// order (dbus.Conn.DispatchInOrder), for a caller that does not wait
	// for each reply.
	Ordered []string
}

// Export puts ifaces at path on conn, with one Properties object serving
// all of their properties and introspection listing them all, as zbus's
// object_server().at does for each interface it mounts at a path. The
// returned unexport removes them again (a Request or Session object
// ending).
func Export(conn *dbus.Conn, path dbus.ObjectPath, ifaces ...Interface) (unexport func(), err error) {
	props := &properties{ifaces: map[string]Getters{}, setters: map[string]Setters{}}
	node := introspect.Node{Name: string(path), Interfaces: []introspect.Interface{
		introspect.IntrospectData,
		{Name: "org.freedesktop.DBus.Properties", Methods: introspect.Methods(props)},
	}}
	names := []string{"org.freedesktop.DBus.Properties", "org.freedesktop.DBus.Introspectable"}
	for _, iface := range ifaces {
		if err := conn.Export(iface.Methods, path, iface.Name); err != nil {
			return nil, fmt.Errorf("export %s: %w", iface.Name, err)
		}
		if len(iface.Ordered) > 0 {
			conn.DispatchInOrder(iface.Name, iface.Ordered...)
		}
		names = append(names, iface.Name)
		for name := range iface.Setters {
			if _, ok := iface.Properties[name]; !ok {
				return nil, fmt.Errorf("export %s: setter for %s, which has no getter", iface.Name, name)
			}
		}
		props.ifaces[iface.Name] = iface.Properties
		props.setters[iface.Name] = iface.Setters
		node.Interfaces = append(node.Interfaces, introspect.Interface{Name: iface.Name, Methods: introspect.Methods(iface.Methods)})
	}
	if err := conn.Export(props, path, "org.freedesktop.DBus.Properties"); err != nil {
		return nil, fmt.Errorf("export %s properties: %w", path, err)
	}
	if err := conn.Export(lazyIntrospectable{node: node, ifaces: ifaces}, path, "org.freedesktop.DBus.Introspectable"); err != nil {
		return nil, fmt.Errorf("export %s introspection: %w", path, err)
	}
	return func() {
		for _, name := range names {
			_ = conn.Export(nil, path, name)
		}
	}, nil
}

// Serve exports s on conn and takes its well-known name; the returned
// release drops the name. A name already owned is an error, as zbus's
// request_name with DoNotQueue.
func Serve(conn *dbus.Conn, s Service) (func(), error) {
	if _, err := Export(conn, s.Path, Interface{Name: s.Interface, Methods: s.Methods, Properties: s.Properties}); err != nil {
		return nil, err
	}
	return Own(conn, s.Name)
}

// Own takes a well-known name; the returned release drops it. A name
// already owned is an error, as zbus's request_name with DoNotQueue.
func Own(conn *dbus.Conn, name string) (func(), error) {
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", name, err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return nil, fmt.Errorf("%s is already owned", name)
	}
	return func() { _, _ = conn.ReleaseName(name) }, nil
}

// lazyIntrospectable builds the introspection data per call: a
// property's type comes from reading it, which must not happen while the
// exporter may hold the lock its getters take.
type lazyIntrospectable struct {
	node   introspect.Node
	ifaces []Interface
}

func (l lazyIntrospectable) Introspect() (string, *dbus.Error) {
	node := l.node
	node.Interfaces = slices.Clone(l.node.Interfaces)
	for i, iface := range l.ifaces {
		node.Interfaces[2+i].Properties = introspectProps(iface.Properties, iface.Setters)
	}
	return string(introspect.NewIntrospectable(&node)), nil
}

func introspectProps(getters Getters, setters Setters) []introspect.Property {
	names := make([]string, 0, len(getters))
	for name := range getters {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]introspect.Property, 0, len(names))
	for _, name := range names {
		access := "read"
		if _, ok := setters[name]; ok {
			access = "readwrite"
		}
		out = append(out, introspect.Property{
			Name:   name,
			Type:   dbus.SignatureOf(getters[name]()).String(),
			Access: access,
		})
	}
	return out
}

// File wraps a descriptor received over D-Bus; a negative one is an
// error, not a file.
func File(fd dbus.UnixFD, name string) (*os.File, error) {
	if fd < 0 {
		return nil, fmt.Errorf("%s: invalid file descriptor %d", name, fd)
	}
	return os.NewFile(uintptr(fd), name), nil
}
