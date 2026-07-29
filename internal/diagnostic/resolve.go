package diagnostic

import (
	"context"
	"maps"
	"net"
	"strings"
	"sync"
	"time"
)

// ResolveNames combines reverse DNS with device names advertised through the
// platform's DNS-SD browser. Advertised names win because they are usually the
// user-facing label configured on the device.
func ResolveNames(ctx context.Context, ips []string) map[string]string {
	var names, advertised map[string]string
	var wg sync.WaitGroup
	wg.Go(func() { names = resolveNames(ctx, ips, net.DefaultResolver.LookupAddr) })
	wg.Go(func() { advertised = advertisedNames(ctx, ips) })
	wg.Wait()
	maps.Copy(names, advertised)
	return names
}

// resolveNames uses the OS resolver, which honors /etc/hosts and the system's
// configured DNS server. Names that fail the hostname allowlist are dropped;
// failed lookups simply have no entry.
func resolveNames(ctx context.Context, ips []string, lookup func(context.Context, string) ([]string, error)) map[string]string {
	names := make(map[string]string)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, ip := range ips {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			lctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			found, err := lookup(lctx, ip)
			if err != nil || len(found) == 0 {
				return
			}
			// The allowlist doubles as sanitization: a PTR record is
			// attacker-controlled text and this name goes straight to the
			// terminal.
			name := strings.TrimSuffix(found[0], ".")
			if name == "" || len(name) > 253 || !hostnameRe.MatchString(name) {
				return
			}
			mu.Lock()
			names[ip] = name
			mu.Unlock()
		})
	}
	wg.Wait()
	return names
}
