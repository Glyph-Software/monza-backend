//go:build !linux

package main

import (
	"fmt"
	"net"
	"runtime"
)

func listenVsock(port uint32) (net.Listener, error) {
	return nil, fmt.Errorf("vsock is not supported on %s; the agent only runs inside Linux microVMs", runtime.GOOS)
}
