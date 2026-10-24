package main

import "testing"

func TestParseReadsThePointAndTheLayout(t *testing.T) {
	if p, err := parse([]string{"640", "22", "2304", "800"}); err != nil || p != (point{640, 22, 2304, 800}) {
		t.Errorf("parse = %+v, %v", p, err)
	}
	for _, bad := range [][]string{
		nil,
		{"1", "2", "3"},
		{"1", "2", "3", "4", "5"},
		{"-1", "2", "10", "10"},
		{"a", "2", "10", "10"},
		{"10", "2", "10", "10"},
		{"2", "10", "10", "10"},
	} {
		if _, err := parse(bad); err == nil {
			t.Errorf("parse(%q) accepted", bad)
		}
	}
}
