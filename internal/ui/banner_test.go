// The failure banner's remediation block: the verdict line, the "Fix:" line,
// and the "Next: press ..." drill-down hint, plus the rule that all of them
// describe the FIRST failing probe in probe order rather than the last one.

package ui

import (
	"testing"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

// TestBannerFailureGuidance pins the whole failure banner for several result
// maps. It is deliberately an exact comparison of a three-line block, not a
// full-screen snapshot: these three lines are the entire remediation contract,
// and a substring check for "Fix:" would let the wrong advice through.
//
// Ordering matters because several rungs fail together in every real outage:
// the first failing probe is the cause and the ones below it are symptoms, so
// remediation taken from the last failure contradicts the verdict above it.
//
// The path-MTU black hole is represented by its protocol rows (case 4). The
// summary that names path MTU additionally requires ProbeResult.timedOut,
// which is unexported to package diagnostic and unreachable from here; the
// contract this test protects, first failure wins over the later ones, is the
// same either way.
func TestBannerFailureGuidance(t *testing.T) {
	oldLookPath := toolLookPath
	toolLookPath = func(bin string) (string, error) { return bin, nil }
	t.Cleanup(func() { toolLookPath = oldLookPath })

	fail := func(fix string) diagnostic.ProbeResult {
		return diagnostic.ProbeResult{Status: diagnostic.StatusFail, Fix: fix}
	}
	// Fix strings are the real ones the probes emit, so a case reads like a
	// screen a user would actually see.
	const (
		egressFix = "no default route: nothing in the routing table leads off this network, check DHCP, the VPN, or a static route"
		proxyFix  = "proxy configured but unreachable: check HTTPS_PROXY/HTTP_PROXY/ALL_PROXY and the proxy host"
		dnsFix    = "name resolution failing: check /etc/resolv.conf / DNS"
		tcpFix    = "port 443 blocked/refused: firewall, wrong network, or VPN routing?"
		tlsFix    = "TLS timed out after TCP connected; read the Path MTU row: it says whether full-size packets are reaching the far end (VPN, PPPoE, or tunnel)"
		httpFix   = "HTTP blocked: proxy or firewall?"
		httpsFix  = "HTTPS blocked: proxy or firewall?"
		pmtuFix   = "bulk TCP stalled after the handshake; if lowering MTU makes it drain, lower the interface MTU"
	)

	tests := []struct {
		name string
		// results overrides the all-pass baseline; every other probe passes.
		results map[diagnostic.ProbeID]diagnostic.ProbeResult
		want    string
	}{
		{
			name: "egress alone fails",
			results: map[diagnostic.ProbeID]diagnostic.ProbeResult{
				diagnostic.ProbeInternet: fail(egressFix),
			},
			want: "✗ The target works but direct internet egress is blocked (proxy-only or filtered network?).\n" +
				"  Fix: " + egressFix + "\n" +
				"  Next: press p for ping the host (ping)",
		},
		{
			name: "egress fails and every rung below it fails too",
			results: map[diagnostic.ProbeID]diagnostic.ProbeResult{
				diagnostic.ProbeInternet:  fail(egressFix),
				diagnostic.ProbeProxy:     fail(proxyFix),
				diagnostic.ProbeTargetTCP: fail(tcpFix),
				diagnostic.ProbeTLS:       fail(tlsFix),
				diagnostic.ProbeHTTP:      fail(httpFix),
				diagnostic.ProbeHTTPS:     fail(httpsFix),
			},
			want: "✗ example.com resolves but neither it nor the general internet is reachable: local egress problem.\n" +
				"  Fix: " + egressFix + "\n" +
				"  Next: press p for ping the host (ping)",
		},
		{
			name: "resolver fails and the target rungs fail behind it",
			results: map[diagnostic.ProbeID]diagnostic.ProbeResult{
				diagnostic.ProbeDNS:       fail(dnsFix),
				diagnostic.ProbeTargetTCP: fail(tcpFix),
				diagnostic.ProbeTLS:       fail(tlsFix),
				diagnostic.ProbeHTTP:      fail(httpFix),
				diagnostic.ProbeHTTPS:     fail(httpsFix),
			},
			want: "✗ Cannot resolve example.com: DNS failure. (The general internet is reachable.)\n" +
				"  Fix: " + dnsFix + "\n" +
				"  Next: press d for DNS lookup (dig)",
		},
		{
			name: "protocol rows stall behind a path MTU warning",
			results: map[diagnostic.ProbeID]diagnostic.ProbeResult{
				diagnostic.ProbePMTU:  {Status: diagnostic.StatusWarn, Fix: pmtuFix},
				diagnostic.ProbeTLS:   fail(tlsFix),
				diagnostic.ProbeHTTP:  fail(httpFix),
				diagnostic.ProbeHTTPS: fail(httpsFix),
			},
			want: "✗ TCP reaches example.com:443 but the TLS handshake fails: bad/expired cert, clock skew, or MITM proxy.\n" +
				"  Fix: " + tlsFix + "\n" +
				"  Next: press c for web check (curl)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tgt := mustTarget(t, "example.com:443")
			m := newModel(tgt, false)
			// One fixed GOOS table keeps the "Next:" label identical wherever
			// the suite runs; the per-GOOS tables themselves are pinned by
			// TestToolsForDefinitions.
			m.tools = toolsFor(tgt, "linux", toolBind{})
			for _, p := range m.probes {
				r, ok := tt.results[p.ID]
				if !ok {
					r = diagnostic.ProbeResult{Status: diagnostic.StatusPass}
				}
				r.ID = p.ID
				m.results[p.ID] = r
			}
			if got := m.banner(); got != tt.want {
				t.Errorf("banner() =\n%s\n\nwant\n%s", got, tt.want)
			}
		})
	}
}
