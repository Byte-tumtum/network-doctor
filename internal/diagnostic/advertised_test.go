package diagnostic

import (
	"maps"
	"strings"
	"testing"
)

// Captured shape of `dns-sd -t 3 -Z <type> local.` — the banner, the bare
// service-type PTR, then an SRV/TXT pair per instance. Not a capture from a
// real Mac: nobody here has one, so this fixture is transcribed from Apple's
// output format and the macOS side is untested against a live responder.
const dnssdZoneFixture = `; To view record data in zone file format, use 'dns-sd -Z <service type>'

Browsing for _googlecast._tcp.local
;
; _googlecast._tcp
_googlecast._tcp                                        PTR     Living\032Room\032TV._googlecast._tcp
Living\032Room\032TV._googlecast._tcp                   SRV     0 0 8009 4d2f9c11.local. ; Replace with unicast FQDN of target host
Living\032Room\032TV._googlecast._tcp                   TXT     "id=4d2f9c11" "md=Chromecast" "fn=Living Room TV"
pi-nas\032-\032SSH._ssh._tcp                            SRV     0 0 22 pi-nas.local. ; Replace with unicast FQDN of target host
HP\032Envy\0326100e\032series._ipp._tcp                 SRV     0 0 631 printer.local. ; Replace with unicast FQDN of target host
HP\032Envy\0326100e\032series._ipp._tcp                 TXT     "rp=ipp/print" "ty=HP Envy 6100e series"
Matt\226\128\153s\032MacBook\032Pro._device-info._tcp    SRV     0 0 0 macbook.local. ; Replace with unicast FQDN of target host
Matt\226\128\153s\032MacBook\032Pro._device-info._tcp    TXT     "model=MacBookPro18,3"
_orphan._tcp                                            TXT     "fn=No instance here"
Never\032Resolved._airplay._tcp                         TXT     "fn=No SRV, no address"
`

func TestParseDNSSDZone(t *testing.T) {
	entries := parseDNSSDZone([]byte(dnssdZoneFixture))

	// Keyed by SRV target: on darwin that host is what gets resolved back to an
	// address, and an instance without one can't be placed on the map at all.
	best := bestNames{}
	for _, entry := range entries {
		host := strings.TrimSuffix(strings.TrimSuffix(entry.host, "."), ".local")
		name, score := nameCandidate(host, entry.instance, entry.svc, entry.txt)
		best.put(entry.host, name, score)
	}

	want := map[string]string{
		"4d2f9c11.local.": "Living Room TV",
		"pi-nas.local.":   "pi-nas",
		"printer.local.":  "HP Envy 6100e series",
		"macbook.local.":  "Matt’s MacBook Pro",
	}
	if got := best.plain(); !maps.Equal(got, want) {
		t.Fatalf("parseDNSSDZone names = %v, want %v", got, want)
	}
}

func TestParseDNSSDZoneOverlongTXT(t *testing.T) {
	zone := "giant._http._tcp SRV 0 0 80 giant.local.\n" +
		`giant._http._tcp TXT "fn=` + strings.Repeat("a", 70<<10) + `"` + "\n" +
		"tv._googlecast._tcp SRV 0 0 8009 tv.local.\n" +
		`tv._googlecast._tcp TXT "fn=Living Room TV"` + "\n"

	best := bestNames{}
	for _, entry := range parseDNSSDZone([]byte(zone)) {
		name, score := nameCandidate("", entry.instance, entry.svc, entry.txt)
		best.put(entry.host, name, score)
	}

	// The 70K name is dropped for length, taking its host with it — and, the
	// point of the test, the entry behind it still lands.
	want := map[string]string{"tv.local.": "Living Room TV"}
	if got := best.plain(); !maps.Equal(got, want) {
		t.Fatalf("parseDNSSDZone names = %v, want %v", got, want)
	}
}
