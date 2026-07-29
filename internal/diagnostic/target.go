package diagnostic

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Proto selects which protocol-specific probe rows append to the target path.
type Proto int

const (
	ProtoNone Proto = iota // stop at Target TCP — no protocol-specific check
	ProtoTLSHTTP
	ProtoHTTP
	ProtoSSH
	ProtoSMTP
)

func (p Proto) String() string {
	switch p {
	case ProtoTLSHTTP:
		return "tls+http"
	case ProtoHTTP:
		return "http"
	case ProtoSSH:
		return "ssh"
	case ProtoSMTP:
		return "smtp"
	}
	return "none"
}

// Target is the parsed, validated destination. Two independent axes: the
// endpoint Port (explicit > scheme default > 443) and the Proto of the
// protocol rows (explicit scheme wins; else inferred from the effective port).
type Target struct {
	Raw          string // validated endpoint spelling, echoed back in the restart prompt
	Host         string
	IP           net.IP // non-nil iff the target is an IP literal
	Port         int
	Proto        Proto
	PortExplicit bool
}

// TargetForms documents the grammar ParseTarget accepts. --help and the
// restart prompt render it verbatim; no trailing newline — the seams around
// the block belong to the callers.
const TargetForms = `  example.com            hostname (default port 443)
  example.com:8022       hostname with port (protocol inferred from the port)
  ssh://example.com:8022 URL (scheme sets protocol and default port; path ignored)
  192.0.2.1, 2001:db8::1 IP literal
  [2001:db8::1]:443      IP literal with port (IPv6 needs the brackets)
  (nothing)              no target — runs the generic checks`

// hostnameRe is a strict RFC-1123-ish hostname allowlist (labels of
// alphanumerics + internal hyphens, dot-separated). Everything else is rejected
// so nothing user-supplied is ever fed to a probe or (later) a command.
var hostnameRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)

// ParseTarget parses a CLI target: <host> | <host>:<port> | <ipv6> |
// [<ipv6>][:<port>] | <scheme>://<host>[:port][/path], where scheme is HTTP,
// HTTPS, SSH, or SMTP. Returns a typed Target
// or an error (caller exits 2 on error).
func ParseTarget(raw string) (*Target, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, errors.New("empty target")
	}
	t := &Target{}
	parseable := s
	if !strings.Contains(s, "://") {
		parseable = "//" + s
	}
	u, err := url.Parse(parseable)
	if err != nil {
		return nil, fmt.Errorf("invalid target %q: %w", s, err)
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "", "http", "https", "ssh", "smtp":
	default:
		return nil, fmt.Errorf("unsupported scheme %q (only http/https/ssh/smtp)", scheme)
	}
	if u.User != nil {
		return nil, errors.New("userinfo is not allowed in target")
	}
	if u.Host == "" {
		return nil, errors.New("missing host")
	}

	host := u.Host
	if net.ParseIP(host) == nil {
		if host == "["+u.Hostname()+"]" {
			host = u.Hostname()
		} else if strings.Contains(host, ":") {
			var port string
			host, port, err = net.SplitHostPort(host)
			if err != nil {
				return nil, fmt.Errorf("invalid target %q: %w", s, err)
			}
			t.Port, err = parsePort(port)
			if err != nil {
				return nil, err
			}
			t.PortExplicit = true
		}
	}
	if host == "" {
		return nil, errors.New("missing host")
	}

	if ip := net.ParseIP(host); ip != nil {
		t.IP = ip
		if v4 := ip.To4(); v4 != nil {
			t.IP = v4
		}
		t.Host = host
	} else {
		name := strings.TrimSuffix(host, ".")
		if len(name) > 253 || !hostnameRe.MatchString(name) {
			return nil, fmt.Errorf("invalid hostname %q", host)
		}
		t.Host = host
	}

	// Endpoint port: explicit > scheme default > 443.
	if !t.PortExplicit {
		switch scheme {
		case "http":
			t.Port = 80
		case "ssh":
			t.Port = 22
		case "smtp":
			t.Port = 25
		default: // https or bare host
			t.Port = 443
		}
	}

	// Protocol rows: explicit scheme wins; else infer from the effective port.
	switch scheme {
	case "https":
		t.Proto = ProtoTLSHTTP
	case "http":
		t.Proto = ProtoHTTP
	case "ssh":
		t.Proto = ProtoSSH
	case "smtp":
		t.Proto = ProtoSMTP
	default:
		switch t.Port {
		case 443, 8443: // 8443: where HTTPS admin panels go to feel special
			t.Proto = ProtoTLSHTTP
		case 80:
			t.Proto = ProtoHTTP
		case 22:
			t.Proto = ProtoSSH
		case 25, 587:
			t.Proto = ProtoSMTP
		default:
			t.Proto = ProtoNone
		}
	}
	t.Raw = u.Host
	if scheme != "" {
		t.Raw = scheme + "://" + u.Host
	}
	return t, nil
}

func parsePort(s string) (int, error) {
	port, err := strconv.Atoi(s)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	return port, nil
}
