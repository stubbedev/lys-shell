package main

import (
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"

	"github.com/stubbedev/lys-shell/service/clipboardipc"
)

func TestCopyToClipboardGoesThroughTheRunningShell(t *testing.T) {
	bus := dbustest.Session(t)
	copied := make(chan string, 1)
	release, err := clipboardipc.NewDaemon(func(text string) error {
		copied <- text
		return nil
	}).Export(bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	if err := copyToClipboard("ld-folder-symbolic"); err != nil {
		t.Fatal(err)
	}
	if got := <-copied; got != "ld-folder-symbolic" {
		t.Errorf("the shell was handed %q", got)
	}
}

func TestCopyToClipboardWithoutTheShellSaysSo(t *testing.T) {
	dbustest.Session(t)
	err := copyToClipboard("ld-folder-symbolic")
	if err == nil || err.Error() != "Clipboard service not running. Start lys-shell shell first." {
		t.Errorf("no shell = %v, want the service-not-running message", err)
	}
}
