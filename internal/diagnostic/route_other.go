//go:build !linux && !darwin && !windows

package diagnostic

import "net"

// A platform whose routing table this package cannot read answers nothing at
// all, rather than reporting a path it inferred from the interface list. An
// absent decision reads as "not known here" everywhere downstream, which is
// true; a guessed one would not be.
func lookupRouteDecision(net.IP) (RouteDecision, bool) { return RouteDecision{}, false }

func defaultRoutesFor(string) []defaultRouteState { return nil }
