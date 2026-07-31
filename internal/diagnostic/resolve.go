package diagnostic

import (
	"context"
	"net"
	"strings"
	"time"
)

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
