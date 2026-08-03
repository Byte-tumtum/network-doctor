//go:build darwin

package diagnostic

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Absolute so a PATH entry ahead of /usr/bin can't stand in for the system
// browser: this runs against whatever the LAN says, with no privileges to lose
// but a terminal to write to.
const dnssdPath = "/usr/bin/dns-sd"

// dnssdTypes are the service types worth asking about. dns-sd browses one type
// per process — there is no --all — so this is a fixed list rather than a
// `_services._dns-sd._udp` enumeration: these are the types that carry a label
// a person would recognize, and the rest cost a process to learn nothing.
var dnssdTypes = []string{
	"_googlecast._tcp",
	"_airplay._tcp",
	"_raop._tcp",
	"_companion-link._tcp",
	"_device-info._tcp",
	"_hap._tcp",
	"_ipp._tcp",
	"_printer._tcp",
	"_smb._tcp",
	"_ssh._tcp",
}

// AdvertisedNames returns the device names advertised through the platform's
// DNS-SD browser, keyed by IP. They win over reverse DNS because they are
// usually the user-facing label configured on the device — so callers should
// ask here first and only fall back to ReverseName for what's left, keeping a
// row's first name its final one.
//
// dns-sd prints no addresses, so each instance's SRV target has to be resolved
// back to an IP through the system resolver, which answers .local off
// mDNSResponder.
func AdvertisedNames(ctx context.Context, ips []string) map[string]string {
	if _, err := os.Stat(dnssdPath); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	// dns-sd stops itself at -t and flushes on the way out; this context is the
	// backstop for a process that somehow doesn't, and it has to outlast -t or
	// every browse dies holding its results.
	browseCtx, cancelBrowse := context.WithTimeout(ctx, 5*time.Second)
	defer cancelBrowse()

	outs := make([][]byte, len(dnssdTypes))
	var wg sync.WaitGroup
	for i, svc := range dnssdTypes {
		wg.Go(func() { outs[i] = browseZone(browseCtx, svc) })
	}
	wg.Wait()

	var entries []zoneEntry
	for _, out := range outs {
		entries = append(entries, parseDNSSDZone(out)...)
	}

	addrs := resolveTargets(ctx, entries)
	wanted := make(map[string]bool, len(ips))
	for _, ip := range ips {
		wanted[ip] = true
	}

	best := bestNames{}
	for _, entry := range entries {
		host := strings.TrimSuffix(strings.TrimSuffix(unescapeDNS(entry.host), "."), ".local")
		name, score := nameCandidate(host, entry.instance, entry.svc, entry.txt)
		for _, addr := range addrs[entry.host] {
			if wanted[addr] {
				best.put(addr, name, score)
			}
		}
	}
	return best.plain()
}

// resolveTargets maps each distinct SRV target to its addresses.
// ponytail: unbounded fan-out — one lookup per advertising device, and a home
// LAN runs out of devices long before the resolver runs out of patience.
func resolveTargets(ctx context.Context, entries []zoneEntry) map[string][]string {
	addrs := make(map[string][]string, len(entries))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, entry := range entries {
		mu.Lock()
		_, seen := addrs[entry.host]
		if !seen {
			addrs[entry.host] = nil
		}
		mu.Unlock()
		if seen {
			continue
		}
		wg.Go(func() {
			lctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			found, err := net.DefaultResolver.LookupHost(lctx, entry.host)
			if err != nil {
				return
			}
			mu.Lock()
			addrs[entry.host] = found
			mu.Unlock()
		})
	}
	wg.Wait()
	return addrs
}

func browseZone(ctx context.Context, svc string) []byte {
	cmd := exec.CommandContext(ctx, dnssdPath, "-t", "3", "-Z", svc, "local.")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil
	}
	var out bytes.Buffer
	_, _ = io.Copy(&out, io.LimitReader(stdout, 1<<18))
	_, _ = io.Copy(io.Discard, stdout)
	_ = cmd.Wait()
	return out.Bytes()
}
