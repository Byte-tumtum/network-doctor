// ParseTarget grammar: the accepted forms and the rejects.

package diagnostic

import (
	"net"
	"strings"
	"testing"

	"github.com/heymaikol/network-doctor/internal/textsafe"
)

func TestParseTarget(t *testing.T) {
	cases := []struct {
		in      string
		host    string
		port    int
		proto   Proto
		literal bool
	}{
		{"github.com", "github.com", 443, ProtoTLSHTTP, false},
		{"example.com.", "example.com.", 443, ProtoTLSHTTP, false},
		{"github.com:22", "github.com", 22, ProtoSSH, false},
		{"https://github.com", "github.com", 443, ProtoTLSHTTP, false},
		{"http://example.com", "example.com", 80, ProtoHTTP, false},
		{"https://host:80", "host", 80, ProtoTLSHTTP, false}, // scheme selects proto
		{"http://host:443", "host", 443, ProtoHTTP, false},   // scheme selects proto
		{"ssh://host:2222", "host", 2222, ProtoSSH, false},
		{"smtp://host:2525", "host", 2525, ProtoSMTP, false},
		{"ssh://host", "host", 22, ProtoSSH, false},
		{"smtp://host", "host", 25, ProtoSMTP, false},
		{"1.1.1.1", "1.1.1.1", 443, ProtoTLSHTTP, true},
		{"1.1.1.1:25", "1.1.1.1", 25, ProtoSMTP, true},
		{"mail.example.com:587", "mail.example.com", 587, ProtoSMTP, false},
		{"https://github.com/owner/repo", "github.com", 443, ProtoTLSHTTP, false},
		{"host:8443", "host", 8443, ProtoTLSHTTP, false},
		{"::1", "::1", 443, ProtoTLSHTTP, true},
		{"fe80::1", "fe80::1", 443, ProtoTLSHTTP, true},
		{"[::1]", "::1", 443, ProtoTLSHTTP, true},
		{"[2001:db8::1]:22", "2001:db8::1", 22, ProtoSSH, true},
		{"https://[2001:db8::1]:8443/path", "2001:db8::1", 8443, ProtoTLSHTTP, true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			tg, err := ParseTarget(c.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tg.Host != c.host {
				t.Errorf("host = %q, want %q", tg.Host, c.host)
			}
			if tg.Port != c.port {
				t.Errorf("port = %d, want %d", tg.Port, c.port)
			}
			if tg.Proto != c.proto {
				t.Errorf("proto = %d, want %d", tg.Proto, c.proto)
			}
			if (tg.IP != nil) != c.literal {
				t.Errorf("literal = %v, want %v", tg.IP != nil, c.literal)
			}
		})
	}
}

func TestParseTargetErrors(t *testing.T) {
	bad := []string{"", "host:0", "host:99999", "ftp://host", "bad_host!",
		"[::1", "[::1]x", "[1.2.3.4]:80", "[hostname]:80", "[]:80", "[fe80::1%eth0]", "a:b:c",
		"https://user@example.com", "https://host:not-a-port", "host:65536", "host:-1",
		"host:9999999999999999999999999999999999999999", "host:\x0080"}
	for _, in := range bad {
		if tg, err := ParseTarget(in); err == nil {
			t.Errorf("ParseTarget(%q) = %+v, want error", in, tg)
		}
	}
}

func TestParseTargetPortExplicit(t *testing.T) {
	for _, c := range []struct {
		in       string
		explicit bool
	}{
		{"example.com", false},
		{"https://example.com", false},
		{"example.com:1", true},
		{"https://example.com:65535", true},
	} {
		tg, err := ParseTarget(c.in)
		if err != nil {
			t.Fatalf("ParseTarget(%q): %v", c.in, err)
		}
		if tg.PortExplicit != c.explicit {
			t.Errorf("ParseTarget(%q).PortExplicit = %v, want %v", c.in, tg.PortExplicit, c.explicit)
		}
	}
}

// A rejected target's error text goes straight to a terminal, whether stderr,
// the restart prompt or the SSH form, so it must survive Clean unchanged. The
// wrapped errors are the risk: net.AddrError echoes the host without quoting
// it, which is how a bidi override used to reach the screen.
func TestParseTargetErrorsAreTerminalSafe(t *testing.T) {
	rlo := string(rune(0x202e))
	for _, in := range []string{
		rlo + ":1:2",            // net.AddrError, "too many colons"
		"ssh://" + rlo + ":1:2", // same, behind a scheme
		rlo + "host",            // hostname allowlist reject
		"[" + rlo + "]:80",      // bracket reject
		string(rune(0x1b)) + ":1:2",
		string(rune(0x200b)) + ".example.com",
	} {
		_, err := ParseTarget(in)
		if err == nil {
			t.Fatalf("ParseTarget(%q) = nil error, want reject", in)
		}
		if got := err.Error(); got != textsafe.Clean(got) {
			t.Errorf("ParseTarget(%q) error %q carries unsanitized bytes", in, got)
		}
	}
}

func TestParseTargetCanonicalRaw(t *testing.T) {
	tg, err := ParseTarget("HTTPS://example.com:8443/ignored?query#fragment\x1b[31m")
	if err != nil {
		t.Fatal(err)
	}
	if tg.Raw != "https://example.com:8443" {
		t.Fatalf("Raw = %q, want validated endpoint only", tg.Raw)
	}
}

func FuzzParseTarget(f *testing.F) {
	seeds := []string{
		// Ordinary host, address, port, and URL forms.
		"example.com", "www.example.com", "example.com.", "192.0.2.1",
		"example.com:443", "192.0.2.1:80", "[2001:db8::1]", "[2001:db8::1]:443",
		"host:1", "host:65535", "HTTPS://example.com:8443/path?query#fragment",
		"ssh://example.com:8022/path",

		// IPv6 and colon ambiguity, including malformed bracket structures.
		"2001:db8::1", "::1", "::", "::::", "2001:db8::1:80", "example.com:80:90",
		"2001:db8::1]:80", "[2001:db8::1:80", "[[2001:db8::1]]", "[example.com]:80",
		":80", "host:", ":", "[", "]", "[]", "[::1]]:80",

		// Port boundaries and hostile numeric spellings.
		"host:0", "host:65536", "host:-1", "host:+80", "host: 80", "host:\t80",
		"host:8o", "host:0x50", "host:9999999999999999999999999999999999999999",
		"host:" + strings.Repeat("9", 256),

		// Empty, whitespace, controls, punctuation, and bounded long inputs.
		"", " ", "\t\r\n", " example.com ", "\x00", "\n", "\r", "\t", "\x1f",
		"exam\x00ple.com", "host:\n80", "https://example.com/path\x1b[31m", "...", ":::[]",
		strings.Repeat("a", 300) + ".example", strings.Repeat("[]:.\x00", 256),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		target, err := ParseTarget(input)
		if err != nil {
			if target != nil {
				t.Fatalf("ParseTarget(%q) returned target %+v with error %v", input, target, err)
			}
			if got := err.Error(); got != textsafe.Clean(got) {
				t.Fatalf("ParseTarget(%q) returned terminal-unsafe error %q", input, got)
			}
			return
		}
		if target == nil {
			t.Fatalf("ParseTarget(%q) succeeded with a nil target", input)
		}
		if target.Host == "" || target.Raw == "" {
			t.Fatalf("ParseTarget(%q) = %+v, want non-empty Host and Raw", input, target)
		}
		if target.Port < 1 || target.Port > 65535 {
			t.Fatalf("ParseTarget(%q).Port = %d, want 1..65535", input, target.Port)
		}
		if target.Proto < 0 || target.Proto >= Proto(len(protoNames)) {
			t.Fatalf("ParseTarget(%q).Proto = %d, want a defined protocol", input, target.Proto)
		}
		if target.Host != textsafe.Clean(target.Host) || target.Raw != textsafe.Clean(target.Raw) {
			t.Fatalf("ParseTarget(%q) returned terminal-unsafe target %+v", input, target)
		}

		ip := net.ParseIP(target.Host)
		if (ip == nil) != (target.IP == nil) || ip != nil && !ip.Equal(target.IP) {
			t.Fatalf("ParseTarget(%q) Host/IP disagree: Host %q, IP %v", input, target.Host, target.IP)
		}

		again, err := ParseTarget(target.Raw)
		if err != nil {
			t.Fatalf("ParseTarget(%q) produced Raw %q that cannot be parsed: %v", input, target.Raw, err)
		}
		sameIP := target.IP == nil && again.IP == nil || target.IP != nil && again.IP != nil && target.IP.Equal(again.IP)
		if again.Raw != target.Raw || again.Host != target.Host || again.Port != target.Port ||
			again.Proto != target.Proto || again.PortExplicit != target.PortExplicit || !sameIP {
			t.Fatalf("ParseTarget(%q) is not stable through Raw: first %+v, again %+v", input, target, again)
		}
	})
}

func TestProtoString(t *testing.T) {
	cases := []struct {
		p    Proto
		want string
	}{
		{ProtoNone, "none"},
		{ProtoTLSHTTP, "tls+http"},
		{ProtoHTTP, "http"},
		{ProtoSSH, "ssh"},
		{ProtoSMTP, "smtp"},
		{Proto(-1), "none"},
		{Proto(99), "none"}, // out-of-range collapses to none, not a panic
	}
	for _, c := range cases {
		if got := c.p.String(); got != c.want {
			t.Errorf("Proto(%d).String() = %q, want %q", c.p, got, c.want)
		}
	}
}
