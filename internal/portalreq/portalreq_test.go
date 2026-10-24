package portalreq

import "testing"

func TestChoiceStart(t *testing.T) {
	opts := []Option{{"a", "A"}, {"b", "B"}}
	for _, tc := range []struct {
		c    Choice
		want string
	}{
		{Choice{Options: opts, Initial: "b"}, "b"},
		{Choice{Options: opts, Initial: ""}, "a"},
		{Choice{Options: opts, Initial: "zzz"}, "a"},
		{Choice{Initial: "true"}, "true"},
		{Choice{Initial: ""}, "false"},
		{Choice{Initial: "yes"}, "false"},
	} {
		if got := tc.c.Start(); got != tc.want {
			t.Errorf("%+v starts at %q, want %q", tc.c, got, tc.want)
		}
	}
}
