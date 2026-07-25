package diagnostic

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

// ResolveNames reverse-resolves ips through the OS resolver, which honors
// /etc/hosts and the system's configured DNS server — usually the box the
// user actually trusts to know device names. nmap's built-in lookup only
// fires raw PTR queries at every nameserver in parallel and keeps whichever
// answers first, so on multi-resolver setups it can report a name the user
// has never seen. Names that fail the hostname allowlist are dropped; failed
// lookups simply have no entry.
func ResolveNames(ctx context.Context, ips []string) map[string]string {
	return resolveNames(ctx, ips, net.DefaultResolver.LookupAddr)
}

func resolveNames(ctx context.Context, ips []string, lookup func(context.Context, string) ([]string, error)) map[string]string {
	names := make(map[string]string)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, ip := range ips {
		wg.Add(1)
		go func() {
			defer wg.Done()
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
		}()
	}
	wg.Wait()
	return names
}
