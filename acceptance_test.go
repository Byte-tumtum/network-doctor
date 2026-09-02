//go:build acceptance && (darwin || windows)

// Native acceptance runs the release-shaped netdoc binary against the real
// Windows or macOS network stack and checks its platform route evidence against
// observations the binary did not produce.

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
	"reflect"
	"runtime"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/heymaikol/network-doctor/internal/report"
)

// These are duplicated deliberately. They are the independently known first
// IPv4 and IPv6 reference destinations the interface row is required to
// describe. Reading destinations out of that row would let a wrong or missing
// production reference select the question its oracle is asked.
var nativeReferenceDestinations = []netip.Addr{
	netip.MustParseAddr("1.1.1.1"),
	netip.MustParseAddr("2606:4700:4700::1111"),
}

const nativeObservationAttempts = 3

// hostRoute is what the platform's own route tool answered about one
// destination. A field the tool does not report stays zero.
type hostRoute struct {
	Iface   string
	Gateway netip.Addr
	Source  netip.Addr
	Prefix  netip.Prefix
}

type hostRouteObservation struct {
	Route hostRoute
	Found bool
}

type kernelRouteObservation struct {
	Source netip.Addr
	Owners []string
	Err    string
}

// TestNativeBinaryUsesLoopbackInterface proves the release-shaped binary can
// resolve and inspect a real host interface, then run the host's SSID fallback.
func TestNativeBinaryUsesLoopbackInterface(t *testing.T) {
	bin := buildNetdoc(t)
	got, code := runNetdoc(t, bin, "--json", "--check", "iface,ssid", "--iface", "127.0.0.1", "--public-dns=")
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
// route evidence against the stack's source and interface selection. Connecting
// a UDP socket performs that selection locally and sends no datagram.
func TestNativeSelectedInterfaceIsTheOneTheKernelRoutesThrough(t *testing.T) {
	bin := buildNetdoc(t)
	var check report.Check
	var kernel map[netip.Addr]kernelRouteObservation
	for attempt := 1; attempt <= nativeObservationAttempts; attempt++ {
		before := kernelRouteObservations(t)
		check = nativeIfaceCheck(t, bin)
		after := kernelRouteObservations(t)
		if reflect.DeepEqual(before, after) {
			kernel = after
			break
		}
		t.Logf("kernel route/source state changed during observation %d: before=%+v after=%+v", attempt, before, after)
	}
	if kernel == nil {
		t.Fatalf("kernel route/source state did not stay stable across %d bounded observations", nativeObservationAttempts)
	}

	routes := referenceRoutes(t, check)
	var headlineDst netip.Addr
	for _, dst := range nativeReferenceDestinations {
		r := routes[dst]
		if !r.Unreachable && !headlineDst.IsValid() {
			headlineDst = dst
		}
		t.Run(dst.String(), func(t *testing.T) {
			observed := kernel[dst]
			if r.Unreachable {
				if observed.Err == "" {
					t.Fatalf("netdoc reported no route to %s, but the kernel selected source %s on %v", dst, observed.Source, observed.Owners)
				}
				return
			}
			if observed.Err != "" {
				t.Fatalf("netdoc reported a route to %s through %q, but a connected UDP socket selected no path: %s", dst, r.Interface, observed.Err)
			}
			if !slices.Contains(observed.Owners, r.Interface) {
				t.Fatalf("netdoc routed %s through %q, but the kernel sources it from %s, which is assigned to %v",
					dst, r.Interface, observed.Source, observed.Owners)
			}
			assertReportedSource(t, r, observed.Source, observed.Owners)
			assertLinkClassificationPresent(t, r)
		})
	}

	if !headlineDst.IsValid() {
		t.Fatalf("both independently selected reference destinations were unreachable: %+v", check.Routes)
	}
	routed, observed := routes[headlineDst], kernel[headlineDst]
	if observed.Err == "" && !slices.Contains(observed.Owners, check.Iface) {
		t.Errorf("the interface row names %q, but traffic to %s leaves from %s, which is assigned to %v",
			check.Iface, headlineDst, observed.Source, observed.Owners)
	}
	if check.Iface != routed.Interface {
		t.Errorf("the interface row names %q while its route evidence selected %q; the row must report the interface routing chose",
			check.Iface, routed.Interface)
	}
}

// TestNativeRouteEvidenceMatchesTheHostRouteTool checks route existence,
// interface, next hop, source where available, and matched prefix against the
// platform's route tool. The observation is made before and after netdoc so a
// legitimate route change is never mistaken for an adapter defect.
func TestNativeRouteEvidenceMatchesTheHostRouteTool(t *testing.T) {
	bin := buildNetdoc(t)
	var check report.Check
	var observed map[netip.Addr]hostRouteObservation
	for attempt := 1; attempt <= nativeObservationAttempts; attempt++ {
		before := hostRouteObservations(t)
		check = nativeIfaceCheck(t, bin)
		after := hostRouteObservations(t)
		if reflect.DeepEqual(before, after) {
			observed = after
			break
		}
		t.Logf("%s route state changed during observation %d: before=%+v after=%+v", hostRouteTool, attempt, before, after)
	}
	if observed == nil {
		t.Fatalf("%s route state did not stay stable across %d bounded observations", hostRouteTool, nativeObservationAttempts)
	}

	routes := referenceRoutes(t, check)
	reachable := 0
	for _, dst := range nativeReferenceDestinations {
		r, host := routes[dst], observed[dst]
		t.Run(dst.String(), func(t *testing.T) {
			if host.Found == r.Unreachable {
				if host.Found {
					t.Fatalf("%s found a route to %s through %q, but netdoc reported no route", hostRouteTool, dst, host.Route.Iface)
				}
				t.Fatalf("%s found no route to %s, but netdoc reported one through %q", hostRouteTool, dst, r.Interface)
			}
			if !host.Found {
				return
			}
			reachable++
			if host.Route.Iface != r.Interface {
				t.Errorf("%s routes %s through %q, netdoc reported %q", hostRouteTool, dst, host.Route.Iface, r.Interface)
			}
			if got := optionalReportedAddr(t, r.Gateway); got != host.Route.Gateway {
				t.Errorf("%s reports next hop %v for %s, netdoc reported %q", hostRouteTool, host.Route.Gateway, dst, r.Gateway)
			}
			if host.Route.Source.IsValid() {
				assertRouteToolSource(t, dst, r, host.Route.Source)
			}
			if !host.Route.Prefix.IsValid() {
				t.Fatalf("%s returned no matched prefix for reachable destination %s", hostRouteTool, dst)
			}
			if got := mustPrefix(t, r.Prefix); got != host.Route.Prefix {
				t.Errorf("%s matched route entry %v for %s, netdoc reported %q", hostRouteTool, host.Route.Prefix, dst, r.Prefix)
			}
		})
	}
	if reachable == 0 {
		t.Fatalf("no reachable reference route to check against %s: %+v", hostRouteTool, check.Routes)
	}
}

func assertRouteToolSource(t *testing.T, dst netip.Addr, r report.Route, hostSource netip.Addr) {
	t.Helper()
	reported := mustAddr(t, r.Source)
	if dst.Is4() {
		if reported != hostSource {
			t.Errorf("%s selects source %v for %s, netdoc reported %q", hostRouteTool, hostSource, dst, r.Source)
		}
		return
	}
	if holders := interfacesHolding(t, reported); !slices.Contains(holders, r.Interface) {
		t.Errorf("netdoc reported IPv6 source %s for %s on %q, but that address is assigned to %v; %s selected %s",
			reported, dst, r.Interface, holders, hostRouteTool, hostSource)
	}
}

// IPv4 is asserted exactly. IPv6 is deliberately weaker because privacy
// addressing can make a per-flow source differ from the stable interface
// address a route entry reports; both must still belong to the selected link.
func assertReportedSource(t *testing.T, r report.Route, kernel netip.Addr, owners []string) {
	t.Helper()
	if r.Source == "" {
		if runtime.GOOS == "windows" {
			t.Errorf("netdoc omitted the source address GetBestRoute2 supplies for %s", r.Destination)
		}
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
		t.Errorf("netdoc reported source %s for %s on %q, but that address is assigned to %v; kernel source %s is assigned to %v",
			source, r.Destination, r.Interface, holders, kernel, owners)
	}
}

// The runner does not prove whether an Ethernet-shaped virtual adapter carries
// a VPN, so this checks only the adapter contract: a selected real interface is
// classified, and a claimed known tunnel names its operating-system kind.
func assertLinkClassificationPresent(t *testing.T, r report.Route) {
	t.Helper()
	switch r.Tunnel {
	case "direct", "likely":
		if r.TunnelKind != "" {
			t.Errorf("netdoc reported tunnel=%q with unexpected kind %q on %q", r.Tunnel, r.TunnelKind, r.Interface)
		}
	case "tunnel":
		if r.TunnelKind == "" {
			t.Errorf("netdoc reported a known tunnel on %q without its operating-system kind", r.Interface)
		}
	default:
		t.Errorf("netdoc left the selected link %q unclassified: tunnel=%q kind=%q", r.Interface, r.Tunnel, r.TunnelKind)
	}
}

func kernelRouteObservations(t *testing.T) map[netip.Addr]kernelRouteObservation {
	t.Helper()
	out := make(map[netip.Addr]kernelRouteObservation, len(nativeReferenceDestinations))
	for _, dst := range nativeReferenceDestinations {
		source, err := kernelEgressSource(t, dst)
		if err != nil {
			out[dst] = kernelRouteObservation{Err: err.Error()}
			continue
		}
		owners := interfacesHolding(t, source)
		sort.Strings(owners)
		out[dst] = kernelRouteObservation{Source: source, Owners: owners}
	}
	return out
}

func hostRouteObservations(t *testing.T) map[netip.Addr]hostRouteObservation {
	t.Helper()
	out := make(map[netip.Addr]hostRouteObservation, len(nativeReferenceDestinations))
	for _, dst := range nativeReferenceDestinations {
		route, found := hostRouteLookup(t, dst)
		out[dst] = hostRouteObservation{Route: route, Found: found}
	}
	return out
}

// kernelEgressSource asks the operating system which local address it would
// use for dst. UDP connect performs route and source selection without writing
// application data.
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
	if !ok || addr.IsUnspecified() {
		return netip.Addr{}, fmt.Errorf("connected socket selected invalid local address %v", local.IP)
	}
	return addr.Unmap().WithZone(""), nil
}

// interfacesHolding names every interface the address is assigned to. More
// than one is kept as an explicit ambiguity rather than resolved by order.
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

func referenceRoutes(t *testing.T, check report.Check) map[netip.Addr]report.Route {
	t.Helper()
	if len(check.Routes) != len(nativeReferenceDestinations) {
		t.Fatalf("interface routes = %+v, want exactly the independently known reference destinations %v", check.Routes, nativeReferenceDestinations)
	}
	want := map[netip.Addr]bool{}
	for _, dst := range nativeReferenceDestinations {
		want[dst] = true
	}
	got := make(map[netip.Addr]report.Route, len(check.Routes))
	for _, r := range check.Routes {
		dst := mustAddr(t, r.Destination)
		if !want[dst] {
			t.Fatalf("interface row reported unexpected reference destination %s; want %v", dst, nativeReferenceDestinations)
		}
		if _, duplicate := got[dst]; duplicate {
			t.Fatalf("interface row reported reference destination %s more than once", dst)
		}
		family := "ipv6"
		if dst.Is4() {
			family = "ipv4"
		}
		if r.Family != family {
			t.Errorf("route to %s reports family %q, want %q", dst, r.Family, family)
		}
		got[dst] = r
	}
	for _, dst := range nativeReferenceDestinations {
		if _, ok := got[dst]; !ok {
			t.Fatalf("interface row omitted independently selected reference destination %s", dst)
		}
	}
	return got
}

func nativeIfaceCheck(t *testing.T, bin string) report.Check {
	t.Helper()
	got, code := runNetdoc(t, bin, "--json", "--check", "iface")
	if code != 0 {
		t.Fatalf("netdoc exited %d for its interface-only run: %+v", code, got)
	}
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

func buildNetdoc(t *testing.T) string {
	t.Helper()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	bin := filepath.Join(t.TempDir(), "netdoc"+suffix)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build netdoc: %v\n%s", err, out)
	}
	return bin
}

// runNetdoc returns a complete JSON report even when a diagnosis exits 1.
func runNetdoc(t *testing.T, bin string, args ...string) (report.Report, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("run netdoc %v: %v\n%s", args, err, stderr.String())
		}
		if ctx.Err() != nil {
			t.Fatalf("run netdoc %v did not finish: %v\nstdout: %s\nstderr: %s", args, ctx.Err(), stdout.String(), stderr.String())
		}
		code = exit.ExitCode()
	}
	var got report.Report
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode report from netdoc %v: %v\nstdout: %s\nstderr: %s", args, err, stdout.String(), stderr.String())
	}
	return got, code
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("%q is not an IP address: %v", s, err)
	}
	return addr.Unmap().WithZone("")
}

func optionalReportedAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	if s == "" {
		return netip.Addr{}
	}
	return mustAddr(t, s)
}

func mustPrefix(t *testing.T, s string) netip.Prefix {
	t.Helper()
	prefix, err := netip.ParsePrefix(s)
	if err != nil {
		t.Fatalf("%q is not an IP prefix: %v", s, err)
	}
	return prefix
}
