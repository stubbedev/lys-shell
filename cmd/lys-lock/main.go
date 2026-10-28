// Command lys-lock is a single-token `lys-shell lock` (lys-lock.rs):
// the same dispatch as its own executable, for contexts that exec one
// token without splitting on whitespace - a systemd ExecStart=, an idle
// daemon, a compositor spawn - so locking needs no wrapper script.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/shellipc"
)

func main() {
	conn, err := dbus.ConnectSessionBus()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	err = shellipc.LockCommand(ctx, conn, err, os.Stdout)
	cancel()
	if conn != nil {
		_ = conn.Close()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
