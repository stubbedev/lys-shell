package bar

import (
	"context"
	"errors"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/network"
)

// startNetworkService brings up the NetworkManager service (the secret
// agent, answering from vault first, VPNs, wifi) on the shell's system-bus
// connection; stop ends its watchers.
func startNetworkService(conn *dbus.Conn, vault network.Vault) (*network.Service, func(), error) {
	if conn == nil {
		return nil, nil, errors.New("no system bus")
	}
	ctx, cancel := context.WithCancel(context.Background())
	svc, err := network.Start(ctx, conn, vault)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return svc, cancel, nil
}
