// Cross-GOOS smoke: ssid runs without incident on the host OS and every GOOS
// has fix hints.

package diagnostic

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Availability smoke tests: the per-OS wrappers really run on the CI matrix.
// No WLAN assumptions — assert only that each returns without panic within
// its deadline and the output shape is sane. Cancellation/kill correctness is
// covered deterministically by the ui package's re-exec tests.

func TestSSIDSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if got := ssid(ctx, "no-such-interface-0"); got != "" {
		t.Errorf("ssid on a nonexistent interface = %q, want empty", got)
	}
}

func TestFixHintsPerGOOS(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows", "plan9"} {
		if ifaceFix(goos) == "" || dnsFix(goos) == "" {
			t.Errorf("empty fix hint for %s", goos)
		}
	}
	if ifaceFix("darwin") == ifaceFix("windows") {
		t.Error("darwin and windows iface hints should differ")
	}
}

func TestTLSFix(t *testing.T) {
	leaf := &x509.Certificate{
		DNSNames:  []string{"example.com", "www.example.com"},
		NotBefore: time.Date(2023, 1, 2, 0, 0, 0, 0, time.UTC),
		NotAfter:  time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
	}
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"hostname", x509.HostnameError{Certificate: leaf, Host: "wrong.test"}, "cert is for example.com, www.example.com, not wrong.test"},
		{"expired", x509.CertificateInvalidError{Cert: leaf, Reason: x509.Expired}, "cert is only valid 2023-01-02 → 2024-01-02"},
		{"unknown CA", x509.UnknownAuthorityError{Cert: leaf}, "unknown CA"},
		{"not TLS", tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}, "not with TLS"},
		{"wrapped", fmt.Errorf("tls: %w", x509.UnknownAuthorityError{Cert: leaf}), "unknown CA"},
		{"other", errors.New("connection reset by peer"), "TLS broken"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tlsFix(tc.err); !strings.Contains(got, tc.want) {
				t.Fatalf("tlsFix(%v) = %q, want it to contain %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestCertNamesTruncatesAndFallsBack(t *testing.T) {
	many := &x509.Certificate{DNSNames: []string{"a", "b", "c", "d"}}
	if got := certNames(many); got != "a, b, c, …" {
		t.Fatalf("certNames(4 SANs) = %q", got)
	}
	cn := &x509.Certificate{}
	cn.Subject.CommonName = "legacy.test"
	if got := certNames(cn); got != "legacy.test" {
		t.Fatalf("certNames(CN only) = %q", got)
	}
	if got := certNames(nil); got != "another name" {
		t.Fatalf("certNames(nil) = %q", got)
	}
}
