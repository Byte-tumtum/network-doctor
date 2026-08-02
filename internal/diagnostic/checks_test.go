// Shape checks for the probe DAG that BuildProbesFrom returns per protocol.

package diagnostic

import (
	"context"
	"strings"
	"testing"
	"time"
)

func mustTarget(t *testing.T, s string) *Target {
	t.Helper()
	tg, err := ParseTarget(s)
	if err != nil {
		t.Fatalf("ParseTarget(%q): %v", s, err)
	}
	return tg
}

// A target adds iface, internet, proxy, system/public dns, target_tcp, path_mtu,
// ssid, plus whatever rows its protocol contributes.
func TestBuildProbesShape(t *testing.T) {
	cases := []struct {
		target string // empty means no target
		want   int
	}{
		{"", 6},                   // no target — no target_tcp/path_mtu/protocol rows
		{"github.com", 11},        // + tls, http, https
		{"http://example.com", 9}, // + http
		{"host:22", 9},            // + ssh banner
		{"ssh://host:2222", 9},    // + ssh banner
		{"host:25", 9},            // + smtp banner
		{"host:587", 9},           // + smtp banner
		{"smtp://host:2525", 9},   // + smtp banner
		{"host:9999", 8},          // ProtoNone — stops at path_mtu
	}
	for _, c := range cases {
		var tg *Target
		if c.target != "" {
			tg = mustTarget(t, c.target)
		}
		if got := len(BuildProbesFrom(tg, nil)); got != c.want {
			t.Errorf("BuildProbesFrom(%q) = %d probes, want %d", c.target, got, c.want)
		}
	}
}

func TestSSIDDoesNotGateNetworkProbes(t *testing.T) {
	probes := BuildProbesFrom(nil, nil)
	deps := make(map[ProbeID][]ProbeID, len(probes))
	for _, p := range probes {
		deps[p.ID] = p.Deps
	}
	if got := deps[ProbeSSID]; len(got) != 1 || got[0] != ProbeIface {
		t.Fatalf("ssid deps = %v, want [iface]", got)
	}
	for _, id := range []ProbeID{ProbeInternet, ProbeProxy, ProbeDNS, ProbeDNSPublic} {
		got := deps[id]
		if len(got) != 1 || got[0] != ProbeIface {
			t.Errorf("%s deps = %v, want [iface]", id, got)
		}
	}
}

func TestBuildProbesNamesProtocolApplicationRow(t *testing.T) {
	https := BuildProbesFrom(mustTarget(t, "https://example.com"), nil)
	want := []struct {
		id   ProbeID
		name string
		dep  ProbeID
	}{
		{id: ProbeTLS, name: "TLS example.com", dep: ProbeTargetTCP},
		{id: ProbeHTTP, name: "HTTP example.com", dep: ProbeDNS},
		{id: ProbeHTTPS, name: "HTTPS example.com", dep: ProbeTLS},
	}
	for i, tt := range want {
		got := https[len(https)-len(want)+i]
		if got.ID != tt.id || got.Name != tt.name {
			t.Errorf("probe %d = (%q, %q), want (%q, %q)", i, got.ID, got.Name, tt.id, tt.name)
		}
		if len(got.Deps) != 1 || got.Deps[0] != tt.dep {
			t.Errorf("probe %s deps = %v, want [%s]", got.ID, got.Deps, tt.dep)
		}
	}

	http := BuildProbesFrom(mustTarget(t, "http://example.com"), nil)
	got := http[len(http)-1]
	if got.ID != ProbeHTTP || got.Name != "HTTP example.com" || len(got.Deps) != 1 || got.Deps[0] != ProbeTargetTCP {
		t.Errorf("plain HTTP application probe = %+v, want HTTP depending on target TCP", got)
	}
}

// Every probe is timed by the same wrapper that sanitizes it, so both runners
// report durations without either one keeping its own stopwatch.
func TestWrapRunTimesAndCleans(t *testing.T) {
	run := wrapRun(func(context.Context, map[ProbeID]ProbeResult) ProbeResult {
		time.Sleep(2 * time.Millisecond)
		return ProbeResult{Detail: "hello\x1b[31mworld"}
	})
	r := run(context.Background(), nil)
	if r.Dur < 2*time.Millisecond {
		t.Errorf("Dur = %v, want at least 2ms", r.Dur)
	}
	if strings.Contains(r.Detail, "\x1b") {
		t.Errorf("Detail = %q, want the escape sanitized away", r.Detail)
	}
}
