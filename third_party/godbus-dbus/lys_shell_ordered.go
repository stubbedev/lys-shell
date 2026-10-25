package dbus

import "sync"

// orderedLaneDepth bounds a lane's queue: past it the read loop waits
// for the lane, holding back the connection's input instead of growing.
const orderedLaneDepth = 256

// orderedKey names one method of one interface.
type orderedKey struct{ iface, member string }

// orderedLanes are the methods whose calls run in arrival order.
type orderedLanes struct {
	mu    sync.RWMutex
	lanes map[orderedKey]chan *Message
}

// DispatchInOrder runs the calls to iface's members one at a time, in
// the order they arrive, on one goroutine for the set; every other call
// still runs on a goroutine of its own. A caller that does not wait for
// each reply (xdg-desktop-portal forwarding input) relies on it: calls
// dispatched each on its own goroutine race, so a later one can take
// effect first. Naming a member that already has a lane joins that
// lane, so exporting an interface again starts no second one. The lane
// ends with the connection.
func (conn *Conn) DispatchInOrder(iface string, members ...string) {
	conn.ordered.mu.Lock()
	defer conn.ordered.mu.Unlock()
	if conn.ordered.lanes == nil {
		conn.ordered.lanes = map[orderedKey]chan *Message{}
	}
	var lane chan *Message
	for _, m := range members {
		if l, ok := conn.ordered.lanes[orderedKey{iface, m}]; ok {
			lane = l
		}
	}
	fresh := lane == nil
	if fresh {
		lane = make(chan *Message, orderedLaneDepth)
	}
	for _, m := range members {
		conn.ordered.lanes[orderedKey{iface, m}] = lane
	}
	if !fresh {
		return
	}
	go func() {
		for {
			select {
			case <-conn.ctx.Done():
				return
			case msg := <-lane:
				conn.handleCall(msg)
			}
		}
	}()
}

// dispatchCall hands a method call to its ordered lane, or to a
// goroutine of its own when it has none.
func (conn *Conn) dispatchCall(msg *Message) {
	iface, _ := msg.Headers[FieldInterface].value.(string)
	member, _ := msg.Headers[FieldMember].value.(string)
	conn.ordered.mu.RLock()
	lane := conn.ordered.lanes[orderedKey{iface, member}]
	conn.ordered.mu.RUnlock()
	if lane == nil {
		go conn.handleCall(msg)
		return
	}
	select {
	case lane <- msg:
	case <-conn.ctx.Done():
	}
}
