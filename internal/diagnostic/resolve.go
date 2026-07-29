package diagnostic

import (
	"context"
	"net"
	"strings"
	"time"
)

// AdvertisedNames returns the device names advertised through the platform's
// DNS-SD browser, keyed by IP. They win over reverse DNS because they are
// usually the user-facing label configured on the device — so callers should
// ask here first and only fall back to ReverseName for what's left, keeping a
// row's first name its final one.
func AdvertisedNames(ctx context.Context, ips []string) map[string]string {
	return advertisedNames(ctx, ips)
}

// ReverseName is one IP's reverse-DNS name, or "" when it has none worth
// showing. It uses the OS resolver, which honors /etc/hosts and the system's
// configured DNS server.
func ReverseName(ctx context.Context, ip string) string {
	return reverseName(ctx, ip, net.DefaultResolver.LookupAddr)
}

func reverseName(ctx context.Context, ip string, lookup func(context.Context, string) ([]string, error)) string {
	lctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	found, err := lookup(lctx, ip)
	if err != nil || len(found) == 0 {
		return ""
	}
	// The allowlist doubles as sanitization: a PTR record is attacker-controlled
	// text and this name goes straight to the terminal.
	name := strings.TrimSuffix(found[0], ".")
	if name == "" || len(name) > 253 || !hostnameRe.MatchString(name) {
		return ""
	}
	return name
}
