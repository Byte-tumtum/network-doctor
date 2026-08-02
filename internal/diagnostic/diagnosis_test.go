// Table tests for Diagnose verdicts: generic and targeted runs, proxy-only
// networks, and the in-progress placeholder.

package diagnostic

import (
	"net"
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
			if v, _ := Diagnose(nil, order, res); !strings.Contains(v, c.want) {
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
			if v, _ := Diagnose(nil, order, res); !strings.Contains(v, c.want) {
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
	downgradeEgress(res)
	if v, _ := Diagnose(tg, order, res); !strings.Contains(v, "proxy-only network") {
		t.Errorf("got %q, want a proxy-only verdict", v)
	}
	res[ProbeInternet] = ProbeResult{Status: StatusWarn}
	res[ProbeTargetTCP] = ProbeResult{Status: StatusPass}
	if v, _ := Diagnose(tg, order, res); !strings.Contains(v, "direct internet egress is degraded") {
		t.Errorf("got %q, want a degraded direct-egress verdict", v)
	}
}

func TestDiagnoseIncomplete(t *testing.T) {
	order := []ProbeID{ProbeIface, ProbeInternet, ProbeDNS}
	res := map[ProbeID]ProbeResult{ProbeIface: {Status: StatusPass}}
	if v, _ := Diagnose(nil, order, res); !strings.Contains(v, "Running") {
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
	if v, _ := Diagnose(tg, order, res); !strings.Contains(v, "unreachable") {
		t.Errorf("got %q, want 'unreachable'", v)
	}

	// Everything passes → Diagnose owns the shared all-clear verdict.
	for _, id := range order {
		res[id] = ProbeResult{Status: StatusPass}
	}
	if v, _ := Diagnose(tg, order, res); !strings.Contains(v, "github.com:443 looks healthy") {
		t.Errorf("got %q, want target healthy verdict", v)
	}

	// A raw egress failure must never fall through to the all-clear verdict.
	res[ProbeInternet] = ProbeResult{Status: StatusFail}
	if v, _ := Diagnose(tg, order, res); !strings.Contains(v, "direct internet egress is blocked") {
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
		if v, _ := Diagnose(tg, order, res); !strings.Contains(v, "some checks are degraded") {
			t.Errorf("%s warning: got %q, want degraded verdict", warning, v)
		}
	}
}

// The service/network split is the whole point of the machine-readable
// verdict: the same target failure classifies differently depending on
// whether anything else proved the path usable.
func TestVerdict(t *testing.T) {
	tg := mustTarget(t, "github.com")
	targetOrder := []ProbeID{ProbeIface, ProbeInternet, ProbeProxy, ProbeDNS, ProbeTargetTCP, ProbePMTU, ProbeTLS, ProbeHTTP, ProbeHTTPS}
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
		// A confirmed black hole reclassifies the rung it broke: the service is
		// fine, the path can't carry a full-size packet.
		{"tls broken by a black hole", tg, targetOrder, map[ProbeID]ProbeResult{
			ProbePMTU: {Status: StatusWarn},
			ProbeTLS:  {Status: StatusFail},
		}, VerdictNetwork},
		// On its own it's only a warning — nothing the caller asked for broke,
		// and a stalled write has innocent explanations.
		{"black hole evidence alone", tg, targetOrder, map[ProbeID]ProbeResult{
			ProbePMTU: {Status: StatusWarn},
		}, VerdictDegraded},
		{"target fine, egress blocked", tg, targetOrder, map[ProbeID]ProbeResult{
			ProbeInternet: {Status: StatusFail},
		}, VerdictDegraded},
		{"slow but working", tg, targetOrder, map[ProbeID]ProbeResult{
			ProbeTargetTCP: {Status: StatusWarn},
		}, VerdictDegraded},
		// The target and direct egress both work; only the configured proxy
		// check failed. Apps that honor HTTP(S)_PROXY will still fail, so
		// this can't be a clean pass.
		{"target fine, configured proxy broken", tg, targetOrder, map[ProbeID]ProbeResult{
			ProbeProxy: {Status: StatusFail},
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
		// Direct internet and DNS both work; only the configured proxy check
		// failed. The summary already warns proxy-aware apps will fail, so
		// the verdict can't say ok.
		{"generic direct fine, configured proxy broken", nil, genericOrder, map[ProbeID]ProbeResult{
			ProbeProxy: {Status: StatusFail},
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
			if summary, got := Diagnose(c.target, c.order, res); got != c.want {
				t.Errorf("Verdict = %q, want %q (summary: %s)", got, c.want, summary)
			}
		})
	}

	// An unfinished run must not claim health.
	if _, got := Diagnose(tg, targetOrder, map[ProbeID]ProbeResult{ProbeIface: {Status: StatusPass}}); got != VerdictIncomplete {
		t.Errorf("Verdict on partial results = %q, want %q", got, VerdictIncomplete)
	}
}

func TestReconcileDNS(t *testing.T) {
	ip1, ip2 := net.ParseIP("192.0.2.1"), net.ParseIP("192.0.2.2")
	t.Run("public resolver works", func(t *testing.T) {
		res := map[ProbeID]ProbeResult{
			ProbeDNS:       {Status: StatusFail, Detail: "SERVFAIL"},
			ProbeDNSPublic: {Status: StatusPass, Addrs: []net.IP{ip1}},
		}
		reconcileDNS(res)
		if !strings.Contains(res[ProbeDNS].Detail, "public DNS resolves it") || res[ProbeDNSPublic].Status != StatusPass {
			t.Fatalf("results = %+v, want public success to explain system failure", res)
		}
	})
	t.Run("both say name is absent", func(t *testing.T) {
		res := map[ProbeID]ProbeResult{
			ProbeDNS:       {Status: StatusFail, DNSNotFound: true, Detail: "not found"},
			ProbeDNSPublic: {Status: StatusPass, DNSNotFound: true},
		}
		reconcileDNS(res)
		if !strings.Contains(res[ProbeDNS].Detail, "agrees") || res[ProbeDNSPublic].Status != StatusPass {
			t.Fatalf("results = %+v, want corroborated not-found", res)
		}
	})
	t.Run("different answers warn", func(t *testing.T) {
		res := map[ProbeID]ProbeResult{
			ProbeDNS:       {Status: StatusPass, Addrs: []net.IP{ip1}},
			ProbeDNSPublic: {Status: StatusPass, Addrs: []net.IP{ip2}},
		}
		reconcileDNS(res)
		if res[ProbeDNSPublic].Status != StatusWarn || !strings.Contains(res[ProbeDNSPublic].Detail, "split DNS") {
			t.Fatalf("public result = %+v, want explanatory warning", res[ProbeDNSPublic])
		}
	})
	t.Run("same answers in different order pass", func(t *testing.T) {
		res := map[ProbeID]ProbeResult{
			ProbeDNS:       {Status: StatusPass, Addrs: []net.IP{ip1, ip2}},
			ProbeDNSPublic: {Status: StatusPass, Addrs: []net.IP{ip2, ip1}},
		}
		reconcileDNS(res)
		if res[ProbeDNSPublic].Status != StatusPass {
			t.Fatalf("public result = %+v, want matching set to pass", res[ProbeDNSPublic])
		}
	})
	t.Run("unavailable public resolver is neutral", func(t *testing.T) {
		res := map[ProbeID]ProbeResult{
			ProbeDNS:       {Status: StatusPass, Addrs: []net.IP{ip1}},
			ProbeDNSPublic: {Status: StatusNA},
		}
		reconcileDNS(res)
		order := []ProbeID{ProbeDNS, ProbeDNSPublic}
		if _, verdict := Diagnose(nil, order, res); verdict != VerdictOK {
			t.Fatalf("verdict = %q, want public N/A to be neutral", verdict)
		}
	})
}

func TestDiagnoseSecondOpinionDNS(t *testing.T) {
	tg := mustTarget(t, "example.com")
	order := []ProbeID{ProbeIface, ProbeInternet, ProbeDNS, ProbeDNSPublic}
	base := map[ProbeID]ProbeResult{
		ProbeIface:    {Status: StatusPass},
		ProbeInternet: {Status: StatusPass},
	}
	for _, tc := range []struct {
		name    string
		system  ProbeResult
		public  ProbeResult
		want    string
		verdict string
	}{
		{
			"system resolver broken",
			ProbeResult{Status: StatusFail},
			ProbeResult{Status: StatusPass, Addrs: []net.IP{net.ParseIP("192.0.2.1")}},
			"public DNS can",
			VerdictDNS,
		},
		{
			"name does not exist",
			ProbeResult{Status: StatusFail, DNSNotFound: true},
			ProbeResult{Status: StatusPass, DNSNotFound: true},
			"no A/AAAA records according to either",
			VerdictDNS,
		},
		{
			"split DNS warning",
			ProbeResult{Status: StatusPass, Addrs: []net.IP{net.ParseIP("192.0.2.1")}},
			ProbeResult{Status: StatusPass, Addrs: []net.IP{net.ParseIP("192.0.2.2")}},
			"system DNS and public DNS disagree",
			VerdictDegraded,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := make(map[ProbeID]ProbeResult, len(base)+2)
			for id, r := range base {
				res[id] = r
			}
			res[ProbeDNS], res[ProbeDNSPublic] = tc.system, tc.public
			reconcileDNS(res)
			summary, verdict := Diagnose(tg, order, res)
			if verdict != tc.verdict || !strings.Contains(summary, tc.want) {
				t.Fatalf("Diagnose = (%q, %q), want %q and %q", summary, verdict, tc.want, tc.verdict)
			}
		})
	}
}
