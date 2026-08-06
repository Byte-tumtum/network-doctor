//go:build !linux

package diagnostic

import "net"

func routeFailureCause(net.IP) string { return "" }
