package prop

import (
	"sync"
	"testing"
)

// A value Get handed out is not written again: Set replaces the stored
// value instead of storing into it, so a reply still being encoded from
// an earlier Get never sees a later Set. Run it under -race.
func TestSetLeavesAnEarlierGetsValueAlone(t *testing.T) {
	p := &Properties{m: copyProps(Map{"org.example.T": {
		"Paths": {Value: []string{"/a", "/b"}, Emit: EmitFalse},
	}})}
	held, err := p.Get("org.example.T", "Paths")
	if err != nil {
		t.Fatal(err)
	}
	first := held.Value().([]string)
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 1000 {
			for _, s := range first {
				_ = len(s)
			}
		}
	})
	for i := range 1000 {
		p.SetMust("org.example.T", "Paths", []string{"/c", string(rune('d' + i%20))})
	}
	wg.Wait()
	if first[0] != "/a" || first[1] != "/b" {
		t.Errorf("an earlier Get's value became %v", first)
	}
	if got := p.GetMust("org.example.T", "Paths").([]string); got[0] != "/c" {
		t.Errorf("the property after the sets is %v", got)
	}
}
