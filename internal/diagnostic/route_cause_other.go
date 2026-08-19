//go:build !linux && !darwin && !windows

package diagnostic

import "net"

// Platforms with no routing table this package knows how to read stay silent
// rather than guessing at a cause.
func routeFailureCause(net.IP) string { return "" }
