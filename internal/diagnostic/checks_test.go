// Shape checks for the probe DAG that BuildProbes returns per protocol.

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

func TestBuildProbesShape(t *testing.T) {
	if got := len(BuildProbes(nil)); got != 6 {
		t.Errorf("generic probes = %d, want 6", got)
	}
	if got := len(BuildProbes(mustTarget(t, "github.com"))); got != 10 {
		t.Errorf("https target probes = %d, want 10", got)
	}
	if got := len(BuildProbes(mustTarget(t, "host:22"))); got != 8 {
		t.Errorf("ssh target probes = %d, want 8", got)
	}
}

func TestSSIDDoesNotGateNetworkProbes(t *testing.T) {
	probes := BuildProbes(nil)
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
	https := BuildProbes(mustTarget(t, "https://example.com"))
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

	http := BuildProbes(mustTarget(t, "http://example.com"))
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
