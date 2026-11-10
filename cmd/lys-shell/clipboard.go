package main

import "github.com/stubbedev/lys-shell/service/clipboardipc"

// copyToClipboard hands text to the running shell, whose clipboard then
// owns the selection; without a shell it fails naming the service.
func copyToClipboard(text string) error {
	p, err := connectDaemon("Clipboard", clipboardipc.ServiceName, clipboardipc.ServicePath, clipboardipc.ServiceName)
	if err != nil {
		return err
	}
	defer p.Close()
	return p.call("copy to the clipboard", "CopyText", nil, text)
}
