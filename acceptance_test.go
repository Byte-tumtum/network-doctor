//go:build acceptance && (darwin || windows)

// Native acceptance: the release-shaped netdoc binary run against the real
// Windows or macOS network stack, checked against an observation of that stack
// netdoc did not produce.
//
// The Linux namespace simulator proves the diagnosis engine against controlled
// topologies. It cannot prove that the Windows or macOS adapter reads its own
// operating system correctly, and a wrong reading there is not hypothetical:
// interface enumeration once named the Npcap loopback adapter as the machine's
// interface while routing was leaving through a real NIC, with every piece of
// generic logic behaving perfectly. So the oracle here is never netdoc's own
// parser or a fixture of its output. It is the kernel's own source-address
// selection and the platform's own documented route tool, both asked the same
// question netdoc asked and both answering through machinery netdoc does not
// use.
//
// Nothing here sends traffic. A route lookup and a connected datagram socket
// are decisions the stack makes locally, so these tests neither depend on
// reaching any third party nor put anything on the wire.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/heymaikol/network-doctor/internal/report"
)

// hostRoute is what the platform's own route tool answered about one
// destination. A field the tool does not report stays zero, and the tests
// assert only what it was actually told.
type hostRoute struct {
	Iface   string
	Gateway netip.Addr
	Source  netip.Addr
	Prefix  netip.Prefix
}

// TestNativeBinaryUsesLoopbackInterface proves the release-shaped binary can
// resolve and inspect a real host interface, then run the host's SSID fallback.
func TestNativeBinaryUsesLoopbackInterface(t *testing.T) {
	got, code := runNetdoc(t, "--json", "--check", "iface,ssid", "--iface", "127.0.0.1", "--public-dns=")
	if code != 0 {
		t.Fatalf("netdoc exited %d, want 0 for a run whose checks all pass: %+v", code, got)
	}
	if len(got.Checks) != 2 {
		t.Fatalf("checks = %+v, want interface and SSID only", got.Checks)
	}
	checks := map[string]report.Check{}
	for _, check := range got.Checks {
		checks[check.ID] = check
	}
	iface := checks["iface"]
	if iface.Status != "PASS" || iface.Source != "127.0.0.1" || iface.Iface == "" {
		t.Errorf("interface check = %+v, want loopback source on a named host interface", iface)
	}
	if ssid := checks["ssid"]; ssid.Status != "N/A" || ssid.Network != "" {
		t.Errorf("SSID check on loopback = %+v, want N/A with no network", ssid)
	}
}

// TestNativeSelectedInterfaceIsTheOneTheKernelRoutesThrough checks netdoc's
// route evidence against the stack's own egress decision.
//
// The independent observation is a connected datagram socket. connect() on a
// UDP socket makes the kernel perform its route lookup and pick the local
// address it would send from, and it puts nothing on the wire. On Windows that
// selection happens in the transport stack and netdoc's answer comes from the
// IP Helper API; on macOS it happens in the socket layer and netdoc's answer
// comes from a PF_ROUTE query. Neither platform can produce a matching pair by
// echoing netdoc back at itself, which is what makes disagreement here evidence
// about netdoc.
//
// This is the regression gate for the Npcap-adapter class of defect: naming an
// interface that routing did not choose fails here even when every generic
// decision in the run is correct.
func TestNativeSelectedInterfaceIsTheOneTheKernelRoutesThrough(t *testing.T) {
	check := nativeIfaceCheck(t)
	if len(check.Routes) == 0 {
		t.Fatalf("the interface row carried no route evidence: %+v; %s must answer a route lookup for the reference endpoints", check, runtime.GOOS)
	}

	var routed *report.Route
	for i := range check.Routes {
		r := check.Routes[i]
		if !r.Unreachable && routed == nil {
			routed = &check.Routes[i]
		}
		t.Run(r.Destination, func(t *testing.T) {
			dst := mustAddr(t, r.Destination)
			source, err := kernelEgressSource(t, dst)
			if r.Unreachable {
				if err == nil {
					t.Fatalf("netdoc reported no route to %s, but the kernel selected source %s for it", dst, source)
				}
				return
			}
			if err != nil {
				t.Fatalf("netdoc reported a route to %s through %q, but the kernel selected no path for it: %v", dst, r.Interface, err)
			}
			owners := interfacesHolding(t, source)
			if !slices.Contains(owners, r.Interface) {
				t.Fatalf("netdoc routed %s through %q, but the kernel sources that destination from %s, which is assigned to %v",
					dst, r.Interface, source, owners)
			}
			assertReportedSource(t, r, source, owners)
			assertLinkKind(t, r)
		})
	}

	if routed == nil {
		t.Fatalf("every reference route was unreachable: %+v; this host has no routed path to check", check.Routes)
	}
	// The headline interface, checked against the same independent source
	// selection rather than against the route row beside it. An enumeration
	// order that names a pseudo-adapter fails here.
	source, err := kernelEgressSource(t, mustAddr(t, routed.Destination))
	if err != nil {
		t.Fatalf("the kernel selected no path to %s: %v", routed.Destination, err)
	}
	if owners := interfacesHolding(t, source); !slices.Contains(owners, check.Iface) {
		t.Errorf("the interface row names %q, but traffic to %s leaves from %s, which is assigned to %v",
			check.Iface, routed.Destination, source, owners)
	}
	if check.Iface != routed.Interface {
		t.Errorf("the interface row names %q while its own route evidence selected %q; the row must report the interface routing chose",
			check.Iface, routed.Interface)
	}
}

// TestNativeRouteEvidenceMatchesTheHostRouteTool checks the same decisions
// against the platform's documented route tool, which reaches the routing
// state through a different subsystem than netdoc does: the NetTCPIP CIM
// provider on Windows, and /sbin/route on macOS. It covers the parts of a
// decision a socket cannot show, above all the next hop and the route entry
// that matched.
func TestNativeRouteEvidenceMatchesTheHostRouteTool(t *testing.T) {
	check := nativeIfaceCheck(t)
	checked := 0
	for _, r := range check.Routes {
		if r.Unreachable {
			continue
		}
		checked++
		t.Run(r.Destination, func(t *testing.T) {
			dst := mustAddr(t, r.Destination)
			host, found := hostRouteLookup(t, dst)
			if !found {
				t.Fatalf("%s found no route to %s, but netdoc reported one through %q", hostRouteTool, dst, r.Interface)
			}
			if host.Iface != r.Interface {
				t.Errorf("%s routes %s through %q, netdoc reported %q", hostRouteTool, dst, host.Iface, r.Interface)
			}
			if got := parseAddr(r.Gateway); got != host.Gateway {
				t.Errorf("%s reports next hop %v for %s, netdoc reported %q", hostRouteTool, host.Gateway, dst, r.Gateway)
			}
			if host.Source.IsValid() && parseAddr(r.Source) != host.Source {
				t.Errorf("%s selects source %v for %s, netdoc reported %q", hostRouteTool, host.Source, dst, r.Source)
			}
			if host.Prefix.IsValid() && r.Prefix != "" && mustPrefix(t, r.Prefix) != host.Prefix {
				t.Errorf("%s matched route entry %v for %s, netdoc reported %q", hostRouteTool, host.Prefix, dst, r.Prefix)
			}
		})
	}
	if checked == 0 {
		t.Fatalf("no reachable reference route to check against %s: %+v", hostRouteTool, check.Routes)
	}
}

// assertReportedSource holds netdoc's reported source address to the one the
// kernel picked.
//
// IPv4 is asserted exactly. IPv6 is asserted one step weaker on purpose: a host
// with privacy extensions sends from a temporary address while the route entry
// names the interface's own address, and both answers are correct, so the claim
// there is that netdoc named an address on the interface the kernel chose.
func assertReportedSource(t *testing.T, r report.Route, kernel netip.Addr, owners []string) {
	t.Helper()
	if r.Source == "" {
		// macOS omits RTAX_IFA from some replies, and netdoc reports unknown
		// rather than inventing one. Nothing to hold it to.
		t.Logf("netdoc reported no source address for %s; the kernel would use %s on %v", r.Destination, kernel, owners)
		return
	}
	source := mustAddr(t, r.Source)
	if kernel.Is4() {
		if source != kernel {
			t.Errorf("netdoc reported source %s for %s, the kernel selects %s", source, r.Destination, kernel)
		}
		return
	}
	if holders := interfacesHolding(t, source); !slices.Contains(holders, r.Interface) {
		t.Errorf("netdoc reported source %s for %s on %q, but that address is assigned to %v",
			source, r.Destination, r.Interface, holders)
	}
}

// assertLinkKind holds netdoc's tunnel classification of the selected link to
// the link's independently observed shape. Go's interface list is a different
// read of the adapter than the one netdoc's platform code makes, so a Windows
// interface type mapped to the wrong kind, or a macOS flag misread, surfaces as
// the runner's ordinary NIC being reported as a VPN.
func assertLinkKind(t *testing.T, r report.Route) {
	t.Helper()
	if r.Tunnel == "" {
		t.Errorf("netdoc left the link kind of %q unclassified; %s reports enough about an adapter to classify it", r.Interface, runtime.GOOS)
		return
	}
	link, err := net.InterfaceByName(r.Interface)
	if err != nil {
		t.Fatalf("netdoc named interface %q, which the host does not have: %v", r.Interface, err)
	}
	if len(link.HardwareAddr) > 0 && link.Flags&net.FlagPointToPoint == 0 && r.Tunnel != "direct" {
		t.Errorf("netdoc reported %q as tunnel=%q kind=%q; a link with hardware address %s that is not point to point carries no encapsulation of its own",
			r.Interface, r.Tunnel, r.TunnelKind, link.HardwareAddr)
	}
}

// kernelEgressSource asks the operating system which local address it would
// send to dst from. A datagram socket's connect() performs the stack's own
// route lookup and source-address selection and transmits nothing.
func kernelEgressSource(t *testing.T, dst netip.Addr) (netip.Addr, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "udp", netip.AddrPortFrom(dst, 443).String())
	if err != nil {
		return netip.Addr{}, err
	}
	defer conn.Close()
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return netip.Addr{}, fmt.Errorf("local address %v is not a UDP address", conn.LocalAddr())
	}
	addr, ok := netip.AddrFromSlice(local.IP)
	if !ok {
		return netip.Addr{}, fmt.Errorf("local address %v is not an IP", local.IP)
	}
	return addr.Unmap().WithZone(""), nil
}

// interfacesHolding names every interface the address is assigned to. More than
// one is not an error here: it is the ambiguity netdoc's own interface matching
// has to cope with, and the tests assert membership rather than a single name.
func interfacesHolding(t *testing.T, ip netip.Addr) []string {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("list interfaces: %v", err)
	}
	var names []string
	for _, ifi := range ifaces {
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if got, ok := netip.AddrFromSlice(ipnet.IP); ok && got.Unmap().WithZone("") == ip {
				names = append(names, ifi.Name)
				break
			}
		}
	}
	return names
}

// nativeIfaceCheck runs the release-shaped binary for the interface row alone
// and returns it. That row carries the run's reference route decisions, which
// the operating system answers out of its own routing state, so this reaches no
// network and no third party.
func nativeIfaceCheck(t *testing.T) report.Check {
	t.Helper()
	got, _ := runNetdoc(t, "--json", "--check", "iface")
	for _, check := range got.Checks {
		if check.ID != "iface" {
			continue
		}
		if check.Status != "PASS" {
			t.Fatalf("interface row = %+v, want PASS: this host has no usable interface to check", check)
		}
		return check
	}
	t.Fatalf("no interface row in %+v", got.Checks)
	return report.Check{}
}

// runNetdoc builds the release-shaped binary and runs it, returning the decoded
// report and the exit code. A run whose checks did not all pass exits non-zero
// and still prints a complete report, so the code is returned rather than
// treated as a failure: these tests read evidence, not verdicts.
func runNetdoc(t *testing.T, args ...string) (report.Report, int) {
	t.Helper()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	bin := filepath.Join(t.TempDir(), "netdoc"+suffix)
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build netdoc: %v\n%s", err, out)
	}

	runCtx, cancelRun := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelRun()
	cmd := exec.CommandContext(runCtx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("run netdoc %v: %v\n%s", args, err, stderr.String())
		}
		code = exit.ExitCode()
	}
	var got report.Report
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode report from netdoc %v: %v\nstdout: %s\nstderr: %s", args, err, stdout.String(), stderr.String())
	}
	return got, code
}

// mustAddr parses an address netdoc printed. A value that is not one is a
// report defect, not a test setup problem.
func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("netdoc reported %q, which is not an IP address: %v", s, err)
	}
	return addr.Unmap().WithZone("")
}

func mustPrefix(t *testing.T, s string) netip.Prefix {
	t.Helper()
	prefix, err := netip.ParsePrefix(s)
	if err != nil {
		t.Fatalf("netdoc reported route entry %q, which is not a prefix: %v", s, err)
	}
	return prefix
}

// parseAddr reads an optional address field, answering the zero Addr for the
// empty string netdoc uses to mean "none", and for anything unparseable, which
// a comparison then reports as a mismatch.
func parseAddr(s string) netip.Addr {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap().WithZone("")
}
