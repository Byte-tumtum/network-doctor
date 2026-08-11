package simulation

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDoHInvalidResponseModeReturnsGarbage(t *testing.T) {
	query := dnsQuery("example.test", dnsTypeA)
	req := httptest.NewRequest(http.MethodPost, encryptedDNSPath, bytes.NewReader(query))
	rec := httptest.NewRecorder()
	serveDoH(rec, req, testZone(t), DoHResponseInvalid)
	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != encryptedDNSMediaType || string(body) != "not dns" {
		t.Fatalf("invalid DoH response = %d %q %q", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
}

func TestEncryptedDNSRejectsUnknownDoHResponseMode(t *testing.T) {
	node := Node{Name: "resolver", Services: []Service{{Name: encryptedDNSProbeService, Type: ServiceEncryptedDNS,
		Port: 443, Zone: map[string]string{"example.test": "192.0.2.1"}, DoHResponse: "surprise",
		Certificate: &TLSCertificate{Mode: TLSCertificateValid, DNSNames: []string{"cloudflare-dns.com"}}}}}
	if err := node.validateServices(map[string]bool{}); err == nil {
		t.Fatal("accepted unknown DoH response mode")
	}
}
