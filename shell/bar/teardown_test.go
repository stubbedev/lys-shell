package bar

import "testing"

func TestTeardownRunsLastAcquiredFirst(t *testing.T) {
	var order []int
	var down teardown
	down.add(func() { order = append(order, 1) })
	down.add(func() { order = append(order, 2) })
	down.run()
	if len(order) != 2 || order[0] != 2 || order[1] != 1 {
		t.Errorf("ran in order %v, want [2 1]", order)
	}
}
