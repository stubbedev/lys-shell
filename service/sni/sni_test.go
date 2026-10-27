package sni

import (
	"slices"
	"testing"
)

func TestParseCategory(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want Category
	}{
		{"ApplicationStatus", CategoryApplicationStatus},
		{"Communications", CategoryCommunications},
		{"SystemServices", CategorySystemServices},
		{"Hardware", CategoryHardware},
		{"", CategoryApplicationStatus},
		{"Nonsense", CategoryApplicationStatus},
	} {
		if got := ParseCategory(tc.raw); got != tc.want {
			t.Errorf("ParseCategory(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestParseStatus(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want Status
	}{
		{"Passive", StatusPassive},
		{"Active", StatusActive},
		{"NeedsAttention", StatusNeedsAttention},
		{"", StatusPassive},
		{"Nonsense", StatusPassive},
	} {
		if got := ParseStatus(tc.raw); got != tc.want {
			t.Errorf("ParseStatus(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestParseAddress(t *testing.T) {
	for _, tc := range []struct {
		reg, bus, path string
	}{
		{":1.100/StatusNotifierItem", ":1.100", "/StatusNotifierItem"},
		{"org.kde.StatusNotifierItem-123-1/StatusNotifierItem", "org.kde.StatusNotifierItem-123-1", "/StatusNotifierItem"},
		{"com.canonical.indicator.application/MenuBar", "com.canonical.indicator.application", "/MenuBar"},
		{"org.example.Bare", "org.example.Bare", "/StatusNotifierItem"},
	} {
		bus, path := ParseAddress(tc.reg)
		if bus != tc.bus || path != tc.path {
			t.Errorf("ParseAddress(%q) = %q, %q; want %q, %q", tc.reg, bus, path, tc.bus, tc.path)
		}
	}
}

func TestStoreOrderAndChanges(t *testing.T) {
	s := NewStore()
	feed, _ := s.Subscribe()
	a := &Item{Bus: ":1.1", Path: "/StatusNotifierItem", ID: "a"}
	b := &Item{Bus: ":1.2", Path: "/StatusNotifierItem", ID: "b"}
	s.Put(a)
	<-feed
	s.Put(b)
	<-feed

	// Registration order holds.
	items := s.Items()
	if len(items) != 2 || items[0].ID != "a" || items[1].ID != "b" {
		t.Fatalf("items = %+v", items)
	}

	// Replacing keeps the slot.
	a2 := &Item{Bus: ":1.1", Path: "/StatusNotifierItem", ID: "a", Title: "replaced"}
	s.Put(a2)
	<-feed
	items = s.Items()
	if len(items) != 2 || items[0].Title != "replaced" {
		t.Fatalf("after replace = %+v", items)
	}

	// Removal drops the slot and ticks.
	if !s.Remove(":1.1", "/StatusNotifierItem") {
		t.Fatal("remove missed the item")
	}
	<-feed
	if !s.Remove(":1.1", "/StatusNotifierItem") {
		// A double remove is false but harmless.
	} else {
		t.Fatal("double remove reported success")
	}
	items = s.Items()
	if len(items) != 1 || items[0].ID != "b" {
		t.Fatalf("after remove = %+v", items)
	}
}

func TestStoreNoTickOnMiss(t *testing.T) {
	s := NewStore()
	feed, _ := s.Subscribe()
	// Drain the (empty) buffer without blocking: a remove of a missing
	// item must not enqueue.
	if s.Remove("gone", "/x") {
		t.Fatal("removed a ghost")
	}
	select {
	case <-feed:
		t.Fatal("a missed remove ticked the store")
	default:
	}
}

func TestBestPixmap(t *testing.T) {
	px := func(w, h int32) Pixmap { return Pixmap{Width: w, Height: h} }
	for _, tc := range []struct {
		in     []Pixmap
		target int
		want   int32
	}{
		{[]Pixmap{px(16, 16), px(24, 24), px(48, 48)}, 24, 24},
		{[]Pixmap{px(16, 16), px(28, 28), px(64, 64)}, 24, 28},
		{[]Pixmap{px(8, 8), px(20, 20)}, 24, 20},
		{[]Pixmap{px(128, 128)}, 24, 128},
		{[]Pixmap{px(32, 16), px(24, 24), px(16, 32)}, 24, 24},
		{[]Pixmap{px(16, 16), px(24, 24), px(48, 48)}, 48, 48},
		{[]Pixmap{px(32, 32), px(48, 48)}, 40, 32},
	} {
		if got, ok := BestPixmap(tc.in, tc.target); !ok || got.Width != tc.want {
			t.Errorf("%v for %d = %v, want width %d", tc.in, tc.target, got, tc.want)
		}
	}
	if _, ok := BestPixmap(nil, 24); ok {
		t.Error("no pixmaps selected one")
	}
}

// The kept subset holds one pixmap per distinct pick, in published
// order, and BestPixmap over it picks for every kept target what it
// picks over the whole set: what the tray draws is what it would draw
// had every size been kept.
func TestKeepPixmapsKeepsThePicks(t *testing.T) {
	var all []Pixmap
	for _, s := range []int32{256, 16, 22, 24, 32, 48, 64, 128} {
		all = append(all, Pixmap{Width: s, Height: s})
	}
	for _, tc := range []struct {
		targets []int
		want    []int32
	}{
		{nil, nil},
		{[]int{20}, []int32{22}},
		{[]int{20, 21, 22}, []int32{22}},
		{[]int{20, 40}, []int32{22, 32}},
		{[]int{30, 60, 200}, []int32{256, 32, 64}},
	} {
		kept := keepPixmaps(all, tc.targets)
		if got := pixmapWidths(kept); !slices.Equal(got, tc.want) {
			t.Errorf("targets %v kept %v, want %v", tc.targets, got, tc.want)
		}
		for _, target := range tc.targets {
			whole, _ := BestPixmap(all, target)
			sub, _ := BestPixmap(kept, target)
			if whole.Width != sub.Width {
				t.Errorf("target %d: the kept set picks %d, the whole set %d", target, sub.Width, whole.Width)
			}
		}
	}
}

func pixmapWidths(ps []Pixmap) []int32 {
	var out []int32
	for _, p := range ps {
		out = append(out, p.Width)
	}
	return out
}
