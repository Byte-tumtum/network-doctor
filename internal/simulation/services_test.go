package simulation

import (
	"encoding/binary"
	"net/netip"
	"strings"
	"testing"
)

// dnsQuery builds a wire-format query, the way a stub resolver would.
func dnsQuery(name string, qtype uint16) []byte {
	msg := make([]byte, dnsHeaderLen)
	binary.BigEndian.PutUint16(msg[0:2], 0xbeef)
	binary.BigEndian.PutUint16(msg[2:4], dnsFlagRD)
	binary.BigEndian.PutUint16(msg[4:6], 1)
	for _, label := range strings.Split(name, ".") {
		msg = append(msg, byte(len(label)))
		msg = append(msg, label...)
	}
	msg = append(msg, 0)
	msg = binary.BigEndian.AppendUint16(msg, qtype)
	return binary.BigEndian.AppendUint16(msg, dnsClassIN)
}

func testZone(t *testing.T) map[string][]netip.Addr {
	t.Helper()
	zone, err := parseZone(map[string]string{
		"example.test": "10.77.0.20",
		"v6.test":      "2001:db8::1",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return zone
}

func rcode(msg []byte) uint16  { return binary.BigEndian.Uint16(msg[2:4]) & 0xf }
func answers(msg []byte) int   { return int(binary.BigEndian.Uint16(msg[6:8])) }
func msgID(msg []byte) uint16  { return binary.BigEndian.Uint16(msg[0:2]) }
func isReply(msg []byte) bool  { return binary.BigEndian.Uint16(msg[2:4])&dnsFlagResponse != 0 }
func questions(msg []byte) int { return int(binary.BigEndian.Uint16(msg[4:6])) }

func TestDNSReplyAnswersFromZone(t *testing.T) {
	reply := dnsReply(dnsQuery("example.test", dnsTypeA), testZone(t))
	if reply == nil {
		t.Fatal("no reply")
	}
	if !isReply(reply) || msgID(reply) != 0xbeef {
		t.Errorf("reply header is wrong: qr=%t id=%#x", isReply(reply), msgID(reply))
	}
	if rcode(reply) != dnsRcodeSuccess || answers(reply) != 1 || questions(reply) != 1 {
		t.Fatalf("rcode=%d answers=%d questions=%d", rcode(reply), answers(reply), questions(reply))
	}
	// The record's rdata is the last 4 bytes of an A answer.
	got, ok := netip.AddrFromSlice(reply[len(reply)-4:])
	if !ok || got.String() != "10.77.0.20" {
		t.Errorf("rdata = %v", reply[len(reply)-4:])
	}
}

func TestDNSReplyCases(t *testing.T) {
	zone := testZone(t)
	tests := []struct {
		name        string
		query       []byte
		wantRcode   uint16
		wantAnswers int
	}{
		{"known name", dnsQuery("example.test", dnsTypeA), dnsRcodeSuccess, 1},
		{"case insensitive", dnsQuery("EXAMPLE.TEST", dnsTypeA), dnsRcodeSuccess, 1},
		{"unknown name", dnsQuery("nope.test", dnsTypeA), dnsRcodeNXDomain, 0},
		// NODATA, not NXDOMAIN: the name exists, this family does not. A client
		// that asks for A and AAAA at once must not conclude the name is gone.
		{"known name, wrong family", dnsQuery("example.test", dnsTypeAAAA), dnsRcodeSuccess, 0},
		{"v6 name", dnsQuery("v6.test", dnsTypeAAAA), dnsRcodeSuccess, 1},
		{"unsupported type", dnsQuery("example.test", 15), dnsRcodeSuccess, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reply := dnsReply(tc.query, zone)
			if reply == nil {
				t.Fatal("no reply")
			}
			if rcode(reply) != tc.wantRcode || answers(reply) != tc.wantAnswers {
				t.Errorf("rcode=%d answers=%d, want %d/%d", rcode(reply), answers(reply), tc.wantRcode, tc.wantAnswers)
			}
		})
	}
}

func TestDNSReplyDualStackName(t *testing.T) {
	zone, err := parseZone(nil, []DNSRecord{
		{Name: "dual.test", Address: "10.77.0.20"},
		{Name: "dual.test", Address: "fd77::20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, qtype := range []uint16{dnsTypeA, dnsTypeAAAA} {
		reply := dnsReply(dnsQuery("dual.test", qtype), zone)
		if reply == nil || rcode(reply) != dnsRcodeSuccess || answers(reply) != 1 {
			t.Errorf("qtype %d reply = %v", qtype, reply)
		}
		_, observed, result, ok := dnsObservation(dnsQuery("dual.test", qtype), zone)
		if !ok || observed != qtype || result != "ANSWER" {
			t.Errorf("qtype %d observation = %d %q %t", qtype, observed, result, ok)
		}
	}
}

func TestDNSReplyRejectsJunk(t *testing.T) {
	zone := testZone(t)
	// An answer, not a query: replying would make two of these servers talk to
	// each other forever.
	reply := dnsQuery("example.test", dnsTypeA)
	binary.BigEndian.PutUint16(reply[2:4], dnsFlagResponse)
	if got := dnsReply(reply, zone); got != nil {
		t.Error("replied to a reply")
	}
	if got := dnsReply([]byte{1, 2, 3}, zone); got != nil {
		t.Error("replied to a runt")
	}
	// A truncated name must not walk off the end of the buffer.
	truncated := dnsQuery("example.test", dnsTypeA)
	if got := dnsReply(truncated[:len(truncated)-6], zone); got == nil || rcode(got) != dnsRcodeFormErr {
		t.Errorf("truncated query: %v", got)
	}
	// A label length that runs past the message is the classic parser bug.
	overrun := append(dnsQuery("example.test", dnsTypeA)[:dnsHeaderLen], 0x40)
	if got := dnsReply(overrun, zone); got == nil || rcode(got) != dnsRcodeFormErr {
		t.Errorf("overrunning label: %v", got)
	}
}

func TestParseZoneRejectsBadAddress(t *testing.T) {
	if _, err := parseZone(map[string]string{"a.test": "not-an-ip"}, nil); err == nil {
		t.Error("want an error")
	}
}

func TestBindAddressesFallsBackToWildcard(t *testing.T) {
	if got := bindAddresses(nil); len(got) != 1 || got[0] != "" {
		t.Errorf("bindAddresses(nil) = %q", got)
	}
	if got := bindAddresses([]string{"10.0.0.1", "1.1.1.1"}); len(got) != 2 {
		t.Errorf("bindAddresses = %q", got)
	}
}

func TestDNSScheduleIsPerFamilyAndBounded(t *testing.T) {
	s := newDNSState(&DNSFault{
		A:    []string{DNSOutcomeAnswer, DNSOutcomeSERVFAIL},
		AAAA: []string{DNSOutcomeSERVFAIL},
	})
	for _, tc := range []struct {
		qtype   uint16
		seq     int
		outcome string
	}{
		{dnsTypeA, 1, DNSOutcomeAnswer},
		{dnsTypeAAAA, 1, DNSOutcomeSERVFAIL},
		{dnsTypeA, 2, DNSOutcomeSERVFAIL},
		{dnsTypeAAAA, 2, DNSOutcomeAnswer}, // exhausted schedules fail open
		{dnsTypeA, 3, DNSOutcomeAnswer},
	} {
		seq, outcome, _ := s.next("target.test", tc.qtype)
		if seq != tc.seq || outcome != tc.outcome {
			t.Errorf("next(%d) = %d/%s, want %d/%s", tc.qtype, seq, outcome, tc.seq, tc.outcome)
		}
	}
}

// A scenario writes a schedule for the name it is making a point about. Queries
// for anything else — netdoc's captive-portal host, a public-DNS comparison,
// whatever a future probe adds — must not spend that name's budget, because how
// many of those a run sends is not fixed.
func TestDNSScheduleIsPerName(t *testing.T) {
	s := newDNSState(&DNSFault{A: []string{DNSOutcomeSERVFAIL, DNSOutcomeSERVFAIL}})
	if seq, outcome, _ := s.next("target.test", dnsTypeA); seq != 1 || outcome != DNSOutcomeSERVFAIL {
		t.Fatalf("first target query = %d/%s", seq, outcome)
	}
	for i := 0; i < 5; i++ {
		if seq, outcome, _ := s.next("connectivitycheck.gstatic.com", dnsTypeA); seq != i+1 || outcome == "" {
			t.Fatalf("unrelated query %d = %d/%s", i, seq, outcome)
		}
	}
	if seq, outcome, _ := s.next("target.test", dnsTypeA); seq != 2 || outcome != DNSOutcomeSERVFAIL {
		t.Errorf("second target query = %d/%s, want 2/%s — unrelated names ate the schedule",
			seq, outcome, DNSOutcomeSERVFAIL)
	}
	if seq, outcome, _ := s.next("target.test", dnsTypeA); seq != 3 || outcome != DNSOutcomeAnswer {
		t.Errorf("third target query = %d/%s, want 3/%s", seq, outcome, DNSOutcomeAnswer)
	}
}

// The counter key is a DNS name, so it carries DNS name equality: case is not
// significant and the root dot is optional. Otherwise one stub resolver's
// trailing dot would hand a name a second, untouched schedule.
func TestDNSScheduleNormalizesNames(t *testing.T) {
	s := newDNSState(&DNSFault{A: []string{DNSOutcomeSERVFAIL, DNSOutcomeSERVFAIL, DNSOutcomeAnswer}})
	for i, name := range []string{"target.test", "TARGET.test.", "Target.Test"} {
		seq, outcome, _ := s.next(name, dnsTypeA)
		want := DNSOutcomeSERVFAIL
		if i == 2 {
			want = DNSOutcomeAnswer
		}
		if seq != i+1 || outcome != want {
			t.Errorf("next(%q) = %d/%s, want %d/%s", name, seq, outcome, i+1, want)
		}
	}
}

func TestDNSErrorReplySERVFAIL(t *testing.T) {
	query := dnsQuery("example.test", dnsTypeA)
	reply := dnsErrorReply(query, dnsRcodeServFail)
	if reply == nil || rcode(reply) != dnsRcodeServFail || questions(reply) != 1 || answers(reply) != 0 {
		t.Fatalf("SERVFAIL reply = %v", reply)
	}
	if got := dnsErrorReply([]byte{1, 2}, dnsRcodeServFail); got != nil {
		t.Errorf("runt query produced %v", got)
	}
}
