package runtimesock

import (
	"strings"
	"testing"
)

func TestPathIsInTheRuntimeDir(t *testing.T) {
	got, err := Path(func(string) string { return "/run/user/1000" }, "widget")
	if err != nil || got != "/run/user/1000/lys-shell/widget.sock" {
		t.Fatalf("Path = %q, %v", got, err)
	}
}

func TestPathRefusesAMissingOrRelativeRuntimeDir(t *testing.T) {
	for _, dir := range []string{"", "run/user/1000"} {
		got, err := Path(func(name string) string {
			if name == "XDG_RUNTIME_DIR" {
				return dir
			}
			return "/home/u"
		}, "launcher")
		if err == nil || !strings.Contains(err.Error(), "XDG_RUNTIME_DIR") || !strings.Contains(err.Error(), "launcher socket") {
			t.Errorf("XDG_RUNTIME_DIR=%q: Path = %q, %v; want an error naming the variable and the socket", dir, got, err)
		}
	}
}
