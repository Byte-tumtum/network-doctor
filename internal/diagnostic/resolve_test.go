package diagnostic

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestResolveNames(t *testing.T) {
	lookup := func(_ context.Context, ip string) ([]string, error) {
		switch ip {
		case "192.168.1.1":
			return []string{"pihole.lan."}, nil
		case "192.168.1.2":
			return nil, errors.New("nxdomain")
		case "192.168.1.3":
			return []string{"evil name) \x1b[31m"}, nil
		case "192.168.1.4":
			return []string{strings.Repeat("a", 260)}, nil
		case "192.168.1.5":
			return []string{"."}, nil
		}
		t.Errorf("unexpected lookup %q", ip)
		return nil, nil
	}
	ips := []string{"192.168.1.1", "192.168.1.2", "192.168.1.3", "192.168.1.4", "192.168.1.5"}
	got := resolveNames(context.Background(), ips, lookup)
	if len(got) != 1 || got["192.168.1.1"] != "pihole.lan" {
		t.Fatalf("resolveNames = %v, want only the valid PTR (trailing dot stripped)", got)
	}
}
