package bar

import (
	"context"
	"fmt"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/network"
)

// startNetworkService brings up the NetworkManager service (the secret
// agent, VPNs, wifi) on its own system-bus connection; stop ends its
// watchers and closes the connection.
func startNetworkService() (*network.Service, func(), error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, nil, fmt.Errorf("system bus: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	svc, err := network.Start(ctx, conn)
	if err != nil {
		cancel()
		_ = conn.Close()
		return nil, nil, err
	}
	return svc, func() {
		cancel()
		_ = conn.Close()
	}, nil
}
