// Table tests for Diagnose verdicts: generic and targeted runs, proxy-only
// networks, and the in-progress placeholder.

package diagnostic

import (
	"strings"
	"testing"
)

func TestDiagnoseGeneric(t *testing.T) {
	order := []ProbeID{ProbeIface, ProbeInternet, ProbeDNS}
	cases := []struct {
		name          string
		internet, dns Status
		want          string
	}{
		{"online", StatusPass, StatusPass, "Online"},
		{"dns down", StatusPass, StatusFail, "DNS resolution is failing"},
		{"no egress", StatusFail, StatusPass, "no direct TCP egress"},
		{"offline", StatusFail, StatusFail, "Offline"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := map[ProbeID]ProbeResult{
				ProbeIface:    {Status: StatusPass},
				ProbeInternet: {Status: c.internet},
				ProbeDNS:      {Status: c.dns},
			}
			if v := Diagnose(nil, order, res); !strings.Contains(v, c.want) {
				t.Errorf("got %q, want substring %q", v, c.want)
			}
		})
	}
}

// Direct and proxied egress are diagnosed separately: a proxy-only network
// reads as online-via-proxy, and a dead configured proxy is called out even
// when direct connectivity works.
func TestDiagnoseProxy(t *testing.T) {
	order := []ProbeID{ProbeIface, ProbeInternet, ProbeProxy, ProbeDNS}
	cases := []struct {
		name                 string
		internet, proxy, dns Status
		downgraded           bool
		want                 string
	}{
		{"proxy-only network", StatusWarn, StatusWarn, StatusPass, true, "Online via the environment proxy"},
		{"degraded direct with proxy", StatusWarn, StatusPass, StatusPass, false, "Online but degraded"},
		{"proxy failed, direct fine", StatusPass, StatusFail, StatusPass, false, "proxy check failed"},
		{"no proxy configured", StatusPass, StatusNA, StatusPass, false, "Online — direct TCP egress"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := map[ProbeID]ProbeResult{
				ProbeIface:    {Status: StatusPass},
				ProbeInternet: {Status: c.internet, downgraded: c.downgraded},
				ProbeProxy:    {Status: c.proxy},
				ProbeDNS:      {Status: c.dns},
			}
			if v := Diagnose(nil, order, res); !strings.Contains(v, c.want) {
				t.Errorf("got %q, want substring %q", v, c.want)
			}
		})
	}
}

func TestDiagnoseTargetProxyOnly(t *testing.T) {
	tg := mustTarget(t, "host:9999")
	order := []ProbeID{ProbeIface, ProbeInternet, ProbeProxy, ProbeDNS, ProbeTargetTCP}
	res := map[ProbeID]ProbeResult{
		ProbeIface: {Status: StatusPass}, ProbeInternet: {Status: StatusFail},
		ProbeProxy: {Status: StatusPass}, ProbeDNS: {Status: StatusPass},
		ProbeTargetTCP: {Status: StatusFail},
	}
	DowngradeEgress(res)
	if v := Diagnose(tg, order, res); !strings.Contains(v, "proxy-only network") {
		t.Errorf("got %q, want a proxy-only verdict", v)
	}
	res[ProbeInternet] = ProbeResult{Status: StatusWarn}
	res[ProbeTargetTCP] = ProbeResult{Status: StatusPass}
	if v := Diagnose(tg, order, res); !strings.Contains(v, "direct internet egress is degraded") {
		t.Errorf("got %q, want a degraded direct-egress verdict", v)
	}
}

func TestDiagnoseIncomplete(t *testing.T) {
	order := []ProbeID{ProbeIface, ProbeInternet, ProbeDNS}
	res := map[ProbeID]ProbeResult{ProbeIface: {Status: StatusPass}}
	if v := Diagnose(nil, order, res); !strings.Contains(v, "Running") {
		t.Errorf("incomplete should report running, got %q", v)
	}
}

func TestDiagnoseTarget(t *testing.T) {
	tg := mustTarget(t, "github.com")
	order := []ProbeID{ProbeIface, ProbeInternet, ProbeDNS, ProbeTargetTCP, ProbeTLS, ProbeHTTP, ProbeHTTPS}

	// DNS + internet OK, Target TCP fails → remote port/firewall verdict.
	res := map[ProbeID]ProbeResult{
		ProbeIface: {Status: StatusPass}, ProbeInternet: {Status: StatusPass},
		ProbeDNS: {Status: StatusPass}, ProbeTargetTCP: {Status: StatusFail},
		ProbeTLS: {Status: StatusSkip}, ProbeHTTP: {Status: StatusPass}, ProbeHTTPS: {Status: StatusSkip},
	}
	if v := Diagnose(tg, order, res); !strings.Contains(v, "unreachable") {
		t.Errorf("got %q, want 'unreachable'", v)
	}

	// Everything passes → Diagnose owns the shared all-clear verdict.
	for _, id := range order {
		res[id] = ProbeResult{Status: StatusPass}
	}
	if v := Diagnose(tg, order, res); !strings.Contains(v, "github.com:443 looks healthy") {
		t.Errorf("got %q, want target healthy verdict", v)
	}

	// A raw egress failure must never fall through to the all-clear verdict.
	res[ProbeInternet] = ProbeResult{Status: StatusFail}
	if v := Diagnose(tg, order, res); !strings.Contains(v, "direct internet egress is blocked") {
		t.Errorf("got %q, want blocked-egress verdict", v)
	}
}

func TestDiagnoseTargetWarnings(t *testing.T) {
	tg := mustTarget(t, "github.com")
	order := []ProbeID{ProbeIface, ProbeInternet, ProbeProxy, ProbeDNS, ProbeTargetTCP, ProbeTLS, ProbeHTTP, ProbeHTTPS}
	for _, warning := range []ProbeID{ProbeProxy, ProbeTargetTCP} {
		res := make(map[ProbeID]ProbeResult, len(order))
		for _, id := range order {
			res[id] = ProbeResult{Status: StatusPass}
		}
		res[warning] = ProbeResult{Status: StatusWarn}
		if v := Diagnose(tg, order, res); !strings.Contains(v, "some checks are degraded") {
			t.Errorf("%s warning: got %q, want degraded verdict", warning, v)
		}
	}
}

// The service/network split is the whole point of the machine-readable
// verdict: the same target failure classifies differently depending on
// whether anything else proved the path usable.
func TestVerdict(t *testing.T) {
	tg := mustTarget(t, "github.com")
	targetOrder := []ProbeID{ProbeIface, ProbeInternet, ProbeProxy, ProbeDNS, ProbeTargetTCP, ProbeTLS, ProbeHTTP, ProbeHTTPS}
	genericOrder := []ProbeID{ProbeIface, ProbeInternet, ProbeProxy, ProbeDNS}

	cases := []struct {
		name   string
		target *Target
		order  []ProbeID
		res    map[ProbeID]ProbeResult
		want   string
	}{
		{"all clear", tg, targetOrder, nil, VerdictOK},
		{"link down", tg, targetOrder, map[ProbeID]ProbeResult{
			ProbeIface: {Status: StatusFail},
		}, VerdictNetwork},
		{"dns failure", tg, targetOrder, map[ProbeID]ProbeResult{
			ProbeDNS: {Status: StatusFail},
		}, VerdictDNS},
		{"port closed, internet fine", tg, targetOrder, map[ProbeID]ProbeResult{
			ProbeTargetTCP: {Status: StatusFail},
		}, VerdictService},
		{"target unreachable, no egress", tg, targetOrder, map[ProbeID]ProbeResult{
			ProbeInternet:  {Status: StatusFail},
			ProbeTargetTCP: {Status: StatusFail},
		}, VerdictNetwork},
		{"tls broken", tg, targetOrder, map[ProbeID]ProbeResult{
			ProbeTLS: {Status: StatusFail},
		}, VerdictService},
		{"http broken", tg, targetOrder, map[ProbeID]ProbeResult{
			ProbeHTTPS: {Status: StatusFail},
		}, VerdictService},
		{"target fine, egress blocked", tg, targetOrder, map[ProbeID]ProbeResult{
			ProbeInternet: {Status: StatusFail},
		}, VerdictDegraded},
		{"slow but working", tg, targetOrder, map[ProbeID]ProbeResult{
			ProbeTargetTCP: {Status: StatusWarn},
		}, VerdictDegraded},
		{"generic online", nil, genericOrder, nil, VerdictOK},
		{"generic dns down", nil, genericOrder, map[ProbeID]ProbeResult{
			ProbeDNS: {Status: StatusFail},
		}, VerdictDNS},
		{"generic offline", nil, genericOrder, map[ProbeID]ProbeResult{
			ProbeInternet: {Status: StatusFail},
			ProbeDNS:      {Status: StatusFail},
		}, VerdictNetwork},
		{"generic no egress", nil, genericOrder, map[ProbeID]ProbeResult{
			ProbeInternet: {Status: StatusFail},
			ProbeProxy:    {Status: StatusNA},
		}, VerdictNetwork},
		{"generic proxy-only", nil, genericOrder, map[ProbeID]ProbeResult{
			ProbeInternet: {Status: StatusWarn, downgraded: true},
			ProbeProxy:    {Status: StatusPass},
		}, VerdictDegraded},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := make(map[ProbeID]ProbeResult, len(c.order))
			for _, id := range c.order {
				res[id] = ProbeResult{Status: StatusPass}
			}
			for id, r := range c.res {
				res[id] = r
			}
			if got := Verdict(c.target, c.order, res); got != c.want {
				t.Errorf("Verdict = %q, want %q (summary: %s)", got, c.want, Diagnose(c.target, c.order, res))
			}
		})
	}

	// An unfinished run must not claim health.
	if got := Verdict(tg, targetOrder, map[ProbeID]ProbeResult{ProbeIface: {Status: StatusPass}}); got != VerdictIncomplete {
		t.Errorf("Verdict on partial results = %q, want %q", got, VerdictIncomplete)
	}
}
