//go:build acceptance

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
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

// findNetRoute emits one JSON object and passes the destination through the
// environment, so no report text is parsed as PowerShell and no formatted
// table or localized error message becomes part of the contract.
const findNetRoute = `$ErrorActionPreference = 'Stop'
$utf8 = [System.Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding = $utf8
$OutputEncoding = $utf8
try {
  $found = @(Find-NetRoute -RemoteIPAddress $env:NETDOC_ACCEPTANCE_DST -ErrorAction Stop)
} catch {
  [pscustomobject]@{
    found = $false
    error_id = $_.FullyQualifiedErrorId
  } | ConvertTo-Json -Compress
  exit 0
}
$addresses = @($found | Where-Object { $_.PSObject.Properties.Name -contains 'IPAddress' })
$routes = @($found | Where-Object { $_.PSObject.Properties.Name -contains 'NextHop' })
[pscustomobject]@{
  found = $true
  address_count = $addresses.Count
  route_count = $routes.Count
  source = [string]$addresses[0].IPAddress
  source_interface_index = [int]$addresses[0].InterfaceIndex
  source_interface_alias = [string]$addresses[0].InterfaceAlias
  interface_index = [int]$routes[0].InterfaceIndex
  interface_alias = [string]$routes[0].InterfaceAlias
  next_hop = [string]$routes[0].NextHop
  prefix = [string]$routes[0].DestinationPrefix
} | ConvertTo-Json -Compress
`

type findNetRouteResult struct {
	Found                bool   `json:"found"`
	ErrorID              string `json:"error_id"`
	AddressCount         int    `json:"address_count"`
	RouteCount           int    `json:"route_count"`
	Source               string `json:"source"`
	SourceInterfaceIndex int    `json:"source_interface_index"`
	SourceInterfaceAlias string `json:"source_interface_alias"`
	InterfaceIndex       int    `json:"interface_index"`
	InterfaceAlias       string `json:"interface_alias"`
	NextHop              string `json:"next_hop"`
	Prefix               string `json:"prefix"`
}

// hostRouteLookup asks Find-NetRoute about one destination. It reports
// found=false only when the cmdlet says there is no route, and fails the test
// when PowerShell could not run it or answered something unreadable.
func hostRouteLookup(t *testing.T, dst netip.Addr) (hostRoute, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", findNetRoute)
	cmd.Env = append(os.Environ(), "NETDOC_ACCEPTANCE_DST="+dst.String())
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			t.Fatalf("Find-NetRoute for %s did not finish: %v\n%s", dst, ctx.Err(), stderr.String())
		}
		t.Fatalf("Find-NetRoute for %s did not run: %v\n%s", dst, err, stderr.String())
	}
	var result findNetRouteResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode Find-NetRoute JSON for %s: %v\nstdout: %s\nstderr: %s", dst, err, stdout.String(), stderr.String())
	}
	if !result.Found {
		if !windowsNoRouteErrorID(result.ErrorID) {
			t.Fatalf("Find-NetRoute failed for %s instead of reporting a route decision: %q", dst, result.ErrorID)
		}
		t.Logf("Find-NetRoute found no route to %s: %s", dst, result.ErrorID)
		return hostRoute{}, false
	}
	got, err := routeFromFindNetRouteResult(dst, result)
	if err != nil {
		t.Fatalf("unreadable Find-NetRoute result for %s: %v\n%s", dst, err, stdout.String())
	}
	// The CIM alias is the independent name compared with netdoc. Checking the
	// same index through Go separately proves the index/name conversion agrees
	// rather than using that conversion as the oracle itself.
	link, err := net.InterfaceByIndex(result.InterfaceIndex)
	if err != nil {
		t.Fatalf("Find-NetRoute routes %s through interface index %d (%q), which the host does not have: %v",
			dst, result.InterfaceIndex, result.InterfaceAlias, err)
	}
	if link.Name != result.InterfaceAlias {
		t.Fatalf("Find-NetRoute names interface index %d as %q, but net.InterfaceByIndex names it %q",
			result.InterfaceIndex, result.InterfaceAlias, link.Name)
	}
	return got, true
}

func routeFromFindNetRouteResult(dst netip.Addr, result findNetRouteResult) (hostRoute, error) {
	if result.AddressCount != 1 || result.RouteCount != 1 {
		return hostRoute{}, fmt.Errorf("returned %d address objects and %d route objects, want one of each", result.AddressCount, result.RouteCount)
	}
	if result.InterfaceIndex <= 0 || result.SourceInterfaceIndex != result.InterfaceIndex {
		return hostRoute{}, fmt.Errorf("source interface index %d and route interface index %d do not name one selected path", result.SourceInterfaceIndex, result.InterfaceIndex)
	}
	if result.InterfaceAlias == "" || result.SourceInterfaceAlias != result.InterfaceAlias {
		return hostRoute{}, fmt.Errorf("source interface alias %q and route interface alias %q do not name one selected path", result.SourceInterfaceAlias, result.InterfaceAlias)
	}
	source, err := netip.ParseAddr(result.Source)
	if err != nil {
		return hostRoute{}, fmt.Errorf("invalid source %q: %w", result.Source, err)
	}
	source = source.Unmap().WithZone("")
	if source.Is4() != dst.Is4() || source.IsUnspecified() {
		return hostRoute{}, fmt.Errorf("source %s has the wrong family or is unspecified for %s", source, dst)
	}
	nextHop, err := netip.ParseAddr(result.NextHop)
	if err != nil {
		return hostRoute{}, fmt.Errorf("invalid next hop %q: %w", result.NextHop, err)
	}
	nextHop = nextHop.Unmap().WithZone("")
	if nextHop.Is4() != dst.Is4() {
		return hostRoute{}, fmt.Errorf("next hop %s has the wrong family for %s", nextHop, dst)
	}
	prefix, err := netip.ParsePrefix(result.Prefix)
	if err != nil {
		return hostRoute{}, fmt.Errorf("invalid destination prefix %q: %w", result.Prefix, err)
	}
	prefix = prefix.Masked()
	if !prefix.Contains(dst) {
		return hostRoute{}, fmt.Errorf("destination prefix %s does not contain %s", prefix, dst)
	}
	got := hostRoute{Iface: result.InterfaceAlias, Source: source, Prefix: prefix}
	if !nextHop.IsUnspecified() {
		got.Gateway = nextHop
	}
	return got, nil
}

func windowsNoRouteErrorID(id string) bool {
	heading, command, ok := strings.Cut(id, ",")
	if !ok || command != "Find-NetRoute" {
		return false
	}
	words := strings.Fields(heading)
	if len(words) == 0 {
		return false
	}
	switch words[len(words)-1] {
	case "1168", "1231", "1232":
		return true
	}
	return false
}

func TestNativeWindowsFindNetRouteJSONParser(t *testing.T) {
	dst := netip.MustParseAddr("1.1.1.1")
	result := findNetRouteResult{
		Found: true, AddressCount: 1, RouteCount: 1,
		Source: "192.0.2.10", SourceInterfaceIndex: 7, SourceInterfaceAlias: "Ethernet",
		InterfaceIndex: 7, InterfaceAlias: "Ethernet", NextHop: "192.0.2.1", Prefix: "0.0.0.0/0",
	}
	got, err := routeFromFindNetRouteResult(dst, result)
	if err != nil {
		t.Fatal(err)
	}
	if got.Iface != "Ethernet" || got.Source.String() != "192.0.2.10" || got.Gateway.String() != "192.0.2.1" || got.Prefix.String() != "0.0.0.0/0" {
		t.Errorf("parsed route = %+v", got)
	}
	result.RouteCount = 2
	if _, err := routeFromFindNetRouteResult(dst, result); err == nil {
		t.Error("multiple route objects were accepted")
	}
	result.RouteCount = 1
	result.Prefix = "not-a-prefix"
	if _, err := routeFromFindNetRouteResult(dst, result); err == nil {
		t.Error("an invalid destination prefix was accepted")
	}
	if !windowsNoRouteErrorID("Localized text 1231,Find-NetRoute") ||
		windowsNoRouteErrorID("Windows System Error 1231,Get-NetRoute") ||
		windowsNoRouteErrorID("CommandNotFoundException") {
		t.Error("no-route error IDs were not separated from command failures")
	}
}
