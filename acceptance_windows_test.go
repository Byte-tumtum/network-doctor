//go:build acceptance

package main

import (
	"context"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// hostRouteTool on Windows is Find-NetRoute, the documented NetTCPIP cmdlet
// that answers "which route would this destination take" through the CIM
// provider. netdoc asks the IP Helper API's GetBestRoute2 instead, so the two
// answers travel different code from the same routing state, which is what
// makes a disagreement here evidence about netdoc's adapter rather than an echo
// of it.
const hostRouteTool = "Find-NetRoute"

// findNetRoute is run with the destination in the environment rather than
// interpolated into the script, so nothing netdoc printed is ever parsed as
// PowerShell. The two returned objects are told apart by the properties they
// carry rather than by class name: the address object holds IPAddress, the
// route object holds NextHop.
const findNetRoute = `$ErrorActionPreference = 'Stop'
try {
  $found = Find-NetRoute -RemoteIPAddress $env:NETDOC_ACCEPTANCE_DST -ErrorAction Stop
} catch {
  'noroute=' + $_.Exception.Message
  exit 0
}
$address = $found | Where-Object { $_.PSObject.Properties.Name -contains 'IPAddress' } | Select-Object -First 1
$route = $found | Where-Object { $_.PSObject.Properties.Name -contains 'NextHop' } | Select-Object -First 1
'source=' + $address.IPAddress
'ifindex=' + $route.InterfaceIndex
'ifalias=' + $route.InterfaceAlias
'nexthop=' + $route.NextHop
'prefix=' + $route.DestinationPrefix
`

// hostRouteLookup asks Find-NetRoute about one destination. It reports
// found=false only when the cmdlet says there is no route, and fails the test
// when PowerShell could not run it or answered something unreadable.
func hostRouteLookup(t *testing.T, dst netip.Addr) (hostRoute, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", findNetRoute)
	cmd.Env = append(os.Environ(), "NETDOC_ACCEPTANCE_DST="+dst.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Find-NetRoute for %s did not run: %v\n%s", dst, err, out)
	}

	fields := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	if reason, absent := fields["noroute"]; absent {
		t.Logf("Find-NetRoute found no route to %s: %s", dst, reason)
		return hostRoute{}, false
	}

	index, err := strconv.Atoi(fields["ifindex"])
	if err != nil {
		t.Fatalf("Find-NetRoute for %s named no interface index:\n%s", dst, out)
	}
	// The index is the join, not the alias: it is what the cmdlet and the IP
	// Helper API both name the interface by, so comparing through it also holds
	// netdoc's index-to-name mapping to the same adapter Windows chose. The
	// alias goes into the failure text so a mismatch is readable.
	link, err := net.InterfaceByIndex(index)
	if err != nil {
		t.Fatalf("Find-NetRoute routes %s through interface index %d (%q), which the host does not have: %v",
			dst, index, fields["ifalias"], err)
	}
	got := hostRoute{Iface: link.Name, Source: parseAddr(fields["source"])}
	// Windows writes an on-link next hop as the unspecified address, which is
	// netdoc's absent gateway.
	if nexthop := parseAddr(fields["nexthop"]); !nexthop.IsUnspecified() {
		got.Gateway = nexthop
	}
	if prefix, err := netip.ParsePrefix(fields["prefix"]); err == nil {
		got.Prefix = prefix
	}
	return got, true
}
