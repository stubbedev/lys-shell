package jinja

import "testing"

func TestReplaceVarsSubstitutesExactSpellingsOnce(t *testing.T) {
	got := ReplaceVars("{{ a }}/{{b}}/{{ b }}/{{ c }}", map[string]string{"a": "{{ b }}", "b": "2"})
	if want := "{{ b }}/{{b}}/2/{{ c }}"; got != want {
		t.Errorf("ReplaceVars = %q, want %q", got, want)
	}
	if got := ReplaceVars("{{ a }}", nil); got != "{{ a }}" {
		t.Errorf("no vars = %q", got)
	}
}
