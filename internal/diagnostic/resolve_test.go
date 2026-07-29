package diagnostic

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"
)

func TestReverseName(t *testing.T) {
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
	for ip, want := range map[string]string{
		"192.168.1.1": "pihole.lan", // trailing dot stripped
		"192.168.1.2": "",           // lookup error
		"192.168.1.3": "",           // fails the hostname allowlist
		"192.168.1.4": "",           // over 253 bytes
		"192.168.1.5": "",           // bare root
	} {
		if got := reverseName(context.Background(), ip, lookup); got != want {
			t.Errorf("reverseName(%s) = %q, want %q", ip, got, want)
		}
	}
}

func TestParseAvahiNames(t *testing.T) {
	out := []byte(strings.Join([]string{
		// An overlong TXT record must not cost us the entries behind it.
		`=;eth0;IPv4;giant;_http._tcp;local;giant.local;192.168.1.98;80;"fn=` + strings.Repeat("a", 70<<10) + `"`,
		`=;eth0;IPv4;BRAVIA-4K-GB;_googlecast._tcp;local;uuid.local;192.168.1.79;8009;"fn=Living\032Room\032TV"`,
		`=;eth0;IPv4;AS-BRAVIA4KGBATV3;_asrecv._tcp;local;uuid.local;192.168.1.79;59008;"fn=AS-BRAVIA4KGBATV3"`,
		`=;eth0;IPv4;pi-nas\032-\032SSH;_ssh._tcp;local;pi-nas.local;192.168.1.2;22;`,
		`=;eth0;IPv4;HP\032Envy\0326100e\032series\032\091AD2424\093;_ipp._tcp;local;printer.local;192.168.1.247;631;"ty=HP\032Envy\0326100e\032series"`,
		`=;eth0;IPv4;Not\032scanned;_http._tcp;local;other.local;192.168.1.99;80;`,
	}, "\n"))

	got := parseAvahiNames(out, []string{"192.168.1.2", "192.168.1.79", "192.168.1.247"})
	want := map[string]string{
		"192.168.1.2":   "pi-nas",
		"192.168.1.79":  "Living Room TV",
		"192.168.1.247": "HP Envy 6100e series",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("parseAvahiNames = %v, want %v", got, want)
	}
}
