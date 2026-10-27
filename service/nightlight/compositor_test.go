package nightlight

import (
	"encoding/binary"
	"fmt"
	"maps"
	"net"
	"slices"
	"sync"
	"syscall"
	"testing"

	"github.com/stubbedev/lys-lib/wltest"
)

// compositor is a Wayland server on the wire, just large enough for the
// filter: wl_output globals, hyprland_ctm_control_manager_v1 with its
// one-owner rule, and zwlr_gamma_control_manager_v1 with its
// one-control-per-output rule. It records the matrix and ramps each
// output ends up with, as a compositor would program them.
type compositor struct {
	ln *net.UnixListener

	mu        sync.Mutex
	offerCTM  bool
	offerGam  bool
	gammaSize int
	nextName  uint32
	outputs   map[uint32]string
	clients   []*client
	ctmOwner  *ctmManager
	ctm       map[string][9]float64
	gammaOwns map[string]*gammaControl
	ramps     map[string][]uint16
}

func newCompositor(t *testing.T, offerCTM, offerGamma bool) *compositor {
	t.Helper()
	ln, _ := wltest.LoopbackDisplay(t, "wayland-nightlight")
	c := &compositor{
		ln: ln, offerCTM: offerCTM, offerGam: offerGamma, gammaSize: 256, nextName: 100,
		outputs: map[uint32]string{}, ctm: map[string][9]float64{},
		gammaOwns: map[string]*gammaControl{}, ramps: map[string][]uint16{},
	}
	go c.accept()
	t.Cleanup(func() {
		_ = ln.Close()
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, cl := range c.clients {
			_ = cl.conn.Close()
		}
	})
	return c
}

const (
	globalCTM   = 1
	globalGamma = 2
)

func (c *compositor) accept() {
	for {
		conn, err := c.ln.AcceptUnix()
		if err != nil {
			return
		}
		cl := &client{comp: c, conn: conn, objects: map[uint32]any{1: displayObj{}}}
		c.mu.Lock()
		c.clients = append(c.clients, cl)
		c.mu.Unlock()
		go cl.serve()
	}
}

// addOutput plugs in an output and announces it to every registry.
func (c *compositor) addOutput(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextName++
	c.outputs[c.nextName] = name
	for _, cl := range c.clients {
		for _, reg := range cl.registries {
			cl.sendGlobal(reg, c.nextName, "wl_output", 4)
		}
	}
}

// removeOutput unplugs an output.
func (c *compositor) removeOutput(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for gname, n := range c.outputs {
		if n != name {
			continue
		}
		delete(c.outputs, gname)
		delete(c.ctm, name)
		delete(c.ramps, name)
		delete(c.gammaOwns, name)
		for _, cl := range c.clients {
			for _, reg := range cl.registries {
				cl.send(reg, 1, u32(gname))
			}
		}
	}
}

func (c *compositor) matrix(output string) ([9]float64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.ctm[output]
	return m, ok
}

func (c *compositor) gammaRamps(output string) []uint16 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.ramps[output])
}

type (
	displayObj   struct{}
	registryObj  struct{}
	outputObj    struct{ name string }
	ctmManager   struct{ pending map[string][9]float64 }
	gammaManager struct{}
	gammaControl struct{ output string }
)

type client struct {
	comp       *compositor
	conn       *net.UnixConn
	writeMu    sync.Mutex
	objects    map[uint32]any
	registries []uint32
	fds        []int
}

func (cl *client) serve() {
	defer cl.disconnect()
	buf := make([]byte, 0, 4096)
	data := make([]byte, 4096)
	oob := make([]byte, syscall.CmsgSpace(4*28))
	for {
		n, oobn, _, _, err := cl.conn.ReadMsgUnix(data, oob)
		if err != nil {
			return
		}
		cl.collectFDs(oob[:oobn])
		buf = append(buf, data[:n]...)
		for len(buf) >= 8 {
			size := int(binary.NativeEndian.Uint32(buf[4:]) >> 16)
			if size < 8 || len(buf) < size {
				break
			}
			id := binary.NativeEndian.Uint32(buf)
			op := binary.NativeEndian.Uint32(buf[4:]) & 0xffff
			cl.request(id, op, &args{b: buf[8:size]})
			buf = buf[size:]
		}
	}
}

func (cl *client) collectFDs(oob []byte) {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return
	}
	for _, m := range msgs {
		fds, err := syscall.ParseUnixRights(&m)
		if err == nil {
			cl.fds = append(cl.fds, fds...)
		}
	}
}

// disconnect is the client going away: what it owned resets.
func (cl *client) disconnect() {
	c := cl.comp
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, obj := range cl.objects {
		cl.release(obj)
	}
	for _, fd := range cl.fds {
		_ = syscall.Close(fd)
	}
}

// release drops what obj holds; c.mu is held.
func (cl *client) release(obj any) {
	c := cl.comp
	switch o := obj.(type) {
	case *ctmManager:
		if c.ctmOwner == o {
			c.ctmOwner = nil
			clear(c.ctm)
		}
	case *gammaControl:
		if c.gammaOwns[o.output] == o {
			delete(c.gammaOwns, o.output)
			delete(c.ramps, o.output)
		}
	}
}

func (cl *client) request(id, op uint32, a *args) {
	c := cl.comp
	c.mu.Lock()
	defer c.mu.Unlock()
	switch obj := cl.objects[id].(type) {
	case displayObj:
		switch op {
		case 0:
			cb := a.u32()
			cl.send(cb, 0, u32(0))
			cl.send(1, 1, u32(cb))
		case 1:
			reg := a.u32()
			cl.objects[reg] = registryObj{}
			cl.registries = append(cl.registries, reg)
			if c.offerCTM {
				cl.sendGlobal(reg, globalCTM, ctmIface, 2)
			}
			if c.offerGam {
				cl.sendGlobal(reg, globalGamma, gammaIface, 1)
			}
			for _, name := range slices.Sorted(maps.Keys(c.outputs)) {
				cl.sendGlobal(reg, name, "wl_output", 4)
			}
		}
	case registryObj:
		name, _, _, newID := a.u32(), a.str(), a.u32(), a.u32()
		switch name {
		case globalCTM:
			m := &ctmManager{pending: map[string][9]float64{}}
			cl.objects[newID] = m
			if c.ctmOwner != nil {
				cl.send(newID, 0)
				return
			}
			c.ctmOwner = m
		case globalGamma:
			cl.objects[newID] = gammaManager{}
		default:
			out := c.outputs[name]
			cl.objects[newID] = outputObj{name: out}
			cl.send(newID, 4, str(out))
			cl.send(newID, 2)
		}
	case *ctmManager:
		switch op {
		case 0:
			out := cl.objects[a.u32()].(outputObj).name
			var m [9]float64
			for i := range m {
				m[i] = float64(int32(a.u32())) / 256
			}
			obj.pending[out] = m
		case 1:
			if c.ctmOwner != obj {
				return
			}
			clear(c.ctm)
			for _, out := range c.outputs {
				if m, ok := obj.pending[out]; ok {
					c.ctm[out] = m
				}
			}
			clear(obj.pending)
		case 2:
			cl.release(obj)
			delete(cl.objects, id)
		}
	case gammaManager:
		if op != 0 {
			delete(cl.objects, id)
			return
		}
		newID := a.u32()
		out := cl.objects[a.u32()].(outputObj).name
		ctl := &gammaControl{output: out}
		cl.objects[newID] = ctl
		if _, taken := c.gammaOwns[out]; taken {
			cl.send(newID, 1)
			return
		}
		c.gammaOwns[out] = ctl
		cl.send(newID, 0, u32(uint32(c.gammaSize)))
	case *gammaControl:
		switch op {
		case 0:
			fd := cl.fds[0]
			cl.fds = cl.fds[1:]
			ramps := make([]byte, 2*3*c.gammaSize)
			_, _ = syscall.Pread(fd, ramps, 0)
			_ = syscall.Close(fd)
			if c.gammaOwns[obj.output] != obj {
				return
			}
			vals := make([]uint16, 3*c.gammaSize)
			for i := range vals {
				vals[i] = binary.NativeEndian.Uint16(ramps[2*i:])
			}
			c.ramps[obj.output] = vals
		case 1:
			cl.release(obj)
			delete(cl.objects, id)
		}
	case outputObj:
		delete(cl.objects, id)
	default:
		if op == 1 && id != 1 {
			delete(cl.objects, id)
		}
	}
}

func (cl *client) sendGlobal(reg, name uint32, iface string, version uint32) {
	cl.send(reg, 0, u32(name), str(iface), u32(version))
}

func (cl *client) send(id, op uint32, parts ...[]byte) {
	var body []byte
	for _, p := range parts {
		body = append(body, p...)
	}
	msg := make([]byte, 8, 8+len(body))
	binary.NativeEndian.PutUint32(msg, id)
	binary.NativeEndian.PutUint32(msg[4:], uint32(8+len(body))<<16|op)
	msg = append(msg, body...)
	cl.writeMu.Lock()
	defer cl.writeMu.Unlock()
	_, _ = cl.conn.Write(msg)
}

type args struct{ b []byte }

func (a *args) u32() uint32 {
	v := binary.NativeEndian.Uint32(a.b)
	a.b = a.b[4:]
	return v
}

func (a *args) str() string {
	n := int(a.u32())
	s := string(a.b[:max(n-1, 0)])
	a.b = a.b[(n+3)&^3:]
	return s
}

func u32(v uint32) []byte { return binary.NativeEndian.AppendUint32(nil, v) }

func str(s string) []byte {
	out := u32(uint32(len(s) + 1))
	out = append(out, s...)
	out = append(out, 0)
	for len(out)%4 != 0 {
		out = append(out, 0)
	}
	return out
}

// eventually polls cond until it holds.
func eventually(t *testing.T, what string, cond func() error) {
	t.Helper()
	wltest.Eventually(t, what, cond)
}

func wantMatrix(c *compositor, output string, want Matrix) func() error {
	return func() error {
		got, ok := c.matrix(output)
		if !ok {
			return fmt.Errorf("output %s has the identity", output)
		}
		for i := range got {
			if diff := got[i] - want[i]; diff > 1.0/256 || diff < -1.0/256 {
				return fmt.Errorf("output %s matrix = %v, want %v", output, got, want)
			}
		}
		return nil
	}
}

func wantIdentity(c *compositor, output string) func() error {
	return func() error {
		if m, ok := c.matrix(output); ok {
			return fmt.Errorf("output %s matrix = %v, want the identity", output, m)
		}
		if r := c.gammaRamps(output); r != nil {
			return fmt.Errorf("output %s still has ramps", output)
		}
		return nil
	}
}

func wantRamps(c *compositor, output string, want []uint16) func() error {
	return func() error {
		if got := c.gammaRamps(output); !slices.Equal(got, want) {
			return fmt.Errorf("output %s ramps differ (got %d entries, want %d)", output, len(got), len(want))
		}
		return nil
	}
}
