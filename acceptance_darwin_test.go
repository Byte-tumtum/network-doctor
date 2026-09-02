//go:build acceptance

package main

import (
	"context"
	"net/netip"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// hostRouteTool on macOS is /sbin/route, the documented user space interface to
// the routing socket. netdoc writes its own RTM_GET and parses the reply; route
// builds and reads its own. A mistake in either the request netdoc sends or the
// reply it decodes shows up here as a disagreement rather than as two copies of
// the same answer.
const hostRouteTool = "/sbin/route -n get"

// hostRouteLookup asks route about one destination. It reports found=false only
// when route says there is no route, and fails the test when route itself could
// not be run or produced something unreadable.
func hostRouteLookup(t *testing.T, dst netip.Addr) (hostRoute, bool) {
	t.Helper()
	family := "-inet"
	if dst.Is6() {
		family = "-inet6"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/sbin/route", "-n", "get", family, dst.String()).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			t.Fatalf("route -n get %s did not finish: %v\n%s", dst, err, out)
		}
		// route exits non-zero for a destination it has no route to, which is
		// an answer, and it exits non-zero when it cannot run at all, which is
		// not. It prints its reason either way, and the caller turns a missing
		// route into a failure, so the reason is logged rather than guessed at.
		t.Logf("route -n get %s exited %v:\n%s", dst, err, out)
		return hostRoute{}, false
	}

	// Every field route prints is "name: value" with the first colon as the
	// separator, which leaves an IPv6 value intact.
	fields := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	got := hostRoute{Iface: fields["interface"]}
	if got.Iface == "" {
		t.Fatalf("route -n get %s named no interface:\n%s", dst, out)
	}
	// An on-link route's next hop is a link layer address such as link#5, which
	// is not an IP and is netdoc's absent gateway. Everything else is parsed,
	// and parseAddr drops the zone a link-local next hop carries, because
	// netdoc's gateway field never carries one.
	got.Gateway = parseAddr(fields["gateway"])
	// route reports neither the source address nor the matched prefix in a form
	// that can be compared without rebuilding netdoc's own netmask arithmetic
	// here, so both stay unset. The kernel's source selection covers the first
	// in TestNativeSelectedInterfaceIsTheOneTheKernelRoutesThrough.
	return got, true
}
