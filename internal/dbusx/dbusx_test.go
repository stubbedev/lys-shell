package dbusx_test

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbustest"
	"github.com/stubbedev/lys-shell/internal/dbusx"
)

type echo struct{}

func (echo) Echo(s string) (string, *dbus.Error) { return s, nil }

func (echo) Fail() *dbus.Error { return dbusx.Failed("it broke") }

const (
	name  = "dev.stubbe.lys.Test1"
	iface = "dev.stubbe.lys.Test1"
	path  = dbus.ObjectPath("/dev/stubbe/lys/Test")
)

func serve(t *testing.T, conn *dbus.Conn, count *dbustest.Var[uint32]) func() {
	t.Helper()
	release, err := dbusx.Serve(conn, dbusx.Service{
		Name: name, Path: path, Interface: iface, Methods: echo{},
		Properties: dbusx.Getters{"Count": func() any { return count.Load() }},
	})
	if err != nil {
		t.Fatal(err)
	}
	return release
}

func TestServeMethodsAndLiveProperties(t *testing.T) {
	dbustest.Session(t)
	var count dbustest.Var[uint32]
	count.Store(1)
	defer serve(t, dbustest.SessionConn(t), &count)()

	obj := dbustest.SessionConn(t).Object(name, path)
	var out string
	if err := obj.Call(iface+".Echo", 0, "hi").Store(&out); err != nil || out != "hi" {
		t.Fatalf("Echo = %q, %v", out, err)
	}
	err := obj.Call(iface+".Fail", 0).Err
	var de dbus.Error
	if !errors.As(err, &de) || de.Name != dbusx.ErrFailed || de.Body[0] != "it broke" {
		t.Fatalf("Fail = %#v", err)
	}

	// Getters are computed per read, like zbus property getters.
	for _, want := range []uint32{1, 5} {
		count.Store(want)
		v, err := obj.GetProperty(iface + ".Count")
		if err != nil || v.Value() != want {
			t.Fatalf("Count = %v, %v; want %d", v, err, want)
		}
	}
	var all map[string]dbus.Variant
	if err := obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, iface).Store(&all); err != nil || all["Count"].Value() != uint32(5) {
		t.Fatalf("GetAll = %v, %v", all, err)
	}
}

func TestServeRejectsUnknownAndWrites(t *testing.T) {
	dbustest.Session(t)
	var count dbustest.Var[uint32]
	defer serve(t, dbustest.SessionConn(t), &count)()
	obj := dbustest.SessionConn(t).Object(name, path)

	if _, err := obj.GetProperty(iface + ".Nope"); !isDBusError(err, dbusx.ErrUnknownProperty) {
		t.Errorf("unknown property: %v", err)
	}
	if err := obj.Call("org.freedesktop.DBus.Properties.Get", 0, "com.other", "Count").Err; !isDBusError(err, dbusx.ErrUnknownIface) {
		t.Errorf("unknown interface: %v", err)
	}
	if err := obj.SetProperty(iface+".Count", dbus.MakeVariant(uint32(3))); !isDBusError(err, dbusx.ErrPropertyRO) {
		t.Errorf("write: %v", err)
	}
}

func TestServeRefusesAnOwnedName(t *testing.T) {
	dbustest.Session(t)
	var count dbustest.Var[uint32]
	defer serve(t, dbustest.SessionConn(t), &count)()
	_, err := dbusx.Serve(dbustest.SessionConn(t), dbusx.Service{
		Name: name, Path: path, Interface: iface, Methods: echo{}, Properties: dbusx.Getters{},
	})
	if err == nil {
		t.Fatal("a second owner was accepted")
	}
}

func isDBusError(err error, name string) bool {
	var de dbus.Error
	return errors.As(err, &de) && de.Name == name
}

type other struct{}

func (other) Ping() (string, *dbus.Error) { return "pong", nil }

func TestExportServesSeveralInterfacesOnOnePath(t *testing.T) {
	bus := dbustest.Start(t)
	server, client := bus.Conn(t), bus.Conn(t)
	unexport, err := dbusx.Export(server, path,
		dbusx.Interface{Name: iface, Methods: echo{}, Properties: dbusx.Getters{"version": func() any { return uint32(2) }}},
		dbusx.Interface{Name: "dev.stubbe.lys.Other1", Methods: other{}, Properties: dbusx.Getters{"version": func() any { return uint32(1) }}},
	)
	if err != nil {
		t.Fatal(err)
	}
	obj := client.Object(server.Names()[0], path)
	var out string
	if err := obj.Call(iface+".Echo", 0, "hi").Store(&out); err != nil || out != "hi" {
		t.Fatalf("Echo = %q, %v", out, err)
	}
	if err := obj.Call("dev.stubbe.lys.Other1.Ping", 0).Store(&out); err != nil || out != "pong" {
		t.Fatalf("Ping = %q, %v", out, err)
	}
	// One Properties object answers for each interface with its own values.
	for name, want := range map[string]uint32{iface: 2, "dev.stubbe.lys.Other1": 1} {
		v, err := obj.GetProperty(name + ".version")
		if err != nil || v.Value() != want {
			t.Errorf("%s.version = %v, %v; want %d", name, v, err, want)
		}
	}
	var all map[string]dbus.Variant
	err = obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, "dev.stubbe.lys.Missing1").Store(&all)
	var de dbus.Error
	if !errors.As(err, &de) || de.Name != dbusx.ErrUnknownIface {
		t.Errorf("GetAll of an unexported interface = %v", err)
	}
	var xml string
	if err := obj.Call("org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{iface, "dev.stubbe.lys.Other1"} {
		if !strings.Contains(xml, `<interface name="`+name+`">`) {
			t.Errorf("introspection lacks %s:\n%s", name, xml)
		}
	}

	// Unexporting removes every interface again.
	unexport()
	if err := obj.Call("dev.stubbe.lys.Other1.Ping", 0).Err; err == nil {
		t.Error("Ping still answers after unexport")
	}
	if _, err := obj.GetProperty(iface + ".version"); err == nil {
		t.Error("properties still answer after unexport")
	}
}

func TestFileRefusesANegativeDescriptor(t *testing.T) {
	if f, err := dbusx.File(-1, "doc"); err == nil {
		_ = f.Close()
		t.Fatal("fd -1 became a file")
	}
}

func TestFileWrapsAReceivedDescriptor(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	fd, err := syscall.Dup(int(r.Fd()))
	_ = r.Close()
	if err != nil {
		t.Fatal(err)
	}
	f, err := dbusx.File(dbus.UnixFD(fd), "doc")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := w.WriteString("x"); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 1)
	if _, err := f.Read(b); err != nil || b[0] != 'x' {
		t.Fatalf("read %q %v", b, err)
	}
	if f.Name() != "doc" {
		t.Errorf("name %q", f.Name())
	}
}
