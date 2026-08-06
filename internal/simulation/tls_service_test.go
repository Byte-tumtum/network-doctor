package simulation

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var tlsTestNow = time.Date(2030, 6, 1, 12, 0, 0, 0, time.UTC)

func testTLSConfig(mode string, names ...string) TLSCertificate {
	return TLSCertificate{Mode: mode, DNSNames: names}
}

func testTLSMaterial(t *testing.T, mode string, names ...string) tlsMaterial {
	t.Helper()
	material, err := generateTLSMaterial(context.Background(), testTLSConfig(mode, names...), tlsTestNow)
	if err != nil {
		t.Fatal(err)
	}
	return material
}

func rootsFromMaterial(t *testing.T, material tlsMaterial) *x509.CertPool {
	t.Helper()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(material.caPEM) {
		t.Fatal("generated CA PEM did not parse")
	}
	return roots
}

func TestTLSCertificateGeneration(t *testing.T) {
	valid := testTLSMaterial(t, TLSCertificateValid, "secure-target.test")
	if !valid.notBefore.Before(tlsTestNow) || !valid.notAfter.After(tlsTestNow) {
		t.Fatalf("valid window = %s..%s, evaluation %s", valid.notBefore, valid.notAfter, tlsTestNow)
	}
	if err := valid.certificate.Leaf.VerifyHostname("secure-target.test"); err != nil {
		t.Fatalf("valid SAN: %v", err)
	}
	if _, err := valid.certificate.Leaf.Verify(x509.VerifyOptions{
		DNSName: "secure-target.test", Roots: rootsFromMaterial(t, valid), CurrentTime: tlsTestNow,
	}); err != nil {
		t.Fatalf("valid chain: %v", err)
	}

	expired := testTLSMaterial(t, TLSCertificateExpired, "secure-target.test")
	if !expired.notAfter.Before(tlsTestNow) {
		t.Fatalf("expired NotAfter = %s", expired.notAfter)
	}
	if _, err := expired.certificate.Leaf.Verify(x509.VerifyOptions{
		DNSName: "secure-target.test", Roots: rootsFromMaterial(t, expired), CurrentTime: tlsTestNow,
	}); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired verification = %v", err)
	}

	mismatch := testTLSMaterial(t, TLSCertificateHostnameMismatch, "other-target.test")
	if err := mismatch.certificate.Leaf.VerifyHostname("secure-target.test"); err == nil {
		t.Fatal("mismatched SAN verified")
	}
}

func TestTLSGenerationCreatesCAAndHonorsCancellation(t *testing.T) {
	material := testTLSMaterial(t, TLSCertificateValid, "secure-target.test")
	block, _ := pem.Decode(material.caPEM)
	if block == nil {
		t.Fatal("generated CA PEM did not decode")
	}
	root, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !root.IsCA || root.SerialNumber.Int64() != 1 || material.certificate.Leaf.SerialNumber.Int64() != 100 {
		t.Fatalf("root/leaf metadata: root CA=%t serial=%s leaf serial=%s", root.IsCA, root.SerialNumber, material.certificate.Leaf.SerialNumber)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := generateTLSMaterial(ctx, testTLSConfig(TLSCertificateValid, "secure-target.test"), tlsTestNow); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled generation = %v", err)
	}
}

func startPipeTLSServer(t *testing.T, material tlsMaterial, mode string) (*tlsServer, *pipeListener, *evidenceRecorder, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "evidence.jsonl")
	recorder, err := openEvidenceRecorder(path, "target")
	if err != nil {
		t.Fatal(err)
	}
	listener := newPipeListener()
	server := startTLSServer(listener, "tls-target", mode, material, "", recorder)
	t.Cleanup(func() {
		_ = server.Close()
		_ = recorder.Close()
	})
	return server, listener, recorder, path
}

func pipeTLSClient(t *testing.T, listener *pipeListener, cfg *tls.Config) error {
	t.Helper()
	serverSide, clientSide := net.Pipe()
	listener.ch <- serverSide
	client := tls.Client(clientSide, cfg)
	err := client.Handshake()
	_ = client.Close()
	return err
}

func TestTLSServerHandshakeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mode       string
		names      []string
		serverName string
		wantOK     bool
		wantResult string
	}{
		{"valid", TLSCertificateValid, []string{"secure-target.test"}, "secure-target.test", true, "passed"},
		{"expired", TLSCertificateExpired, []string{"secure-target.test"}, "secure-target.test", false, "client_rejected_certificate"},
		{"hostname mismatch", TLSCertificateHostnameMismatch, []string{"other-target.test"}, "secure-target.test", false, "client_rejected_certificate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			material := testTLSMaterial(t, tc.mode, tc.names...)
			_, listener, recorder, path := startPipeTLSServer(t, material, tc.mode)
			err := pipeTLSClient(t, listener, &tls.Config{
				ServerName: tc.serverName, RootCAs: rootsFromMaterial(t, material), Time: func() time.Time { return tlsTestNow }, MinVersion: tls.VersionTLS12,
			})
			if (err == nil) != tc.wantOK {
				t.Fatalf("handshake error = %v, want success %t", err, tc.wantOK)
			}
			evidence, found := waitForTLSEvidence(t, recorder, path, func(item TLSEvidence) bool {
				return item.RequestedServer == tc.serverName && item.CertificatePresented && item.Result == tc.wantResult
			})
			if !found {
				t.Errorf("TLS evidence = %+v", evidence.TLS)
			}
		})
	}
}

func waitForTLSEvidence(t *testing.T, recorder *evidenceRecorder, path string, match func(TLSEvidence) bool) (Evidence, bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		recorder.mu.Lock()
		evidence, err := readEvidence([]string{path})
		recorder.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range evidence.TLS {
			if match(item) {
				return evidence, true
			}
		}
		if time.Now().After(deadline) {
			return evidence, false
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTLSServerConcurrentConnectionsAndShutdown(t *testing.T) {
	material := testTLSMaterial(t, TLSCertificateValid, "secure-target.test")
	server, listener, _, path := startPipeTLSServer(t, material, TLSCertificateValid)
	const clients = 12
	var wg sync.WaitGroup
	errs := make(chan error, clients)
	for range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			serverSide, clientSide := net.Pipe()
			listener.ch <- serverSide
			client := tls.Client(clientSide, &tls.Config{
				ServerName: "secure-target.test", RootCAs: rootsFromMaterial(t, material), Time: func() time.Time { return tlsTestNow },
			})
			errs <- client.Handshake()
			_ = client.Close()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	evidence, err := readEvidence([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, item := range evidence.TLS {
		if item.Result == "passed" {
			count += item.Count
		}
	}
	if count != clients {
		t.Errorf("passed handshakes = %d, want %d; evidence %+v", count, clients, evidence.TLS)
	}
}

func TestTLSServerCancellationClosesSlowAndTruncatedClients(t *testing.T) {
	for name, input := range map[string][]byte{"silent": nil, "truncated": {0x16, 0x03}} {
		t.Run(name, func(t *testing.T) {
			material := testTLSMaterial(t, TLSCertificateValid, "secure-target.test")
			server, listener, _, _ := startPipeTLSServer(t, material, TLSCertificateValid)
			serverSide, client := net.Pipe()
			listener.ch <- serverSide
			if len(input) > 0 {
				if _, err := client.Write(input); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan error, 1)
			go func() { done <- server.Close() }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("TLS server shutdown blocked on partial ClientHello")
			}
			_ = client.Close()
		})
	}
}

func TestTLSServicePartialStartupRemovesTrustAnchor(t *testing.T) {
	dir := t.TempDir()
	svc := Service{Name: "tls-target", Type: ServiceTLS, Port: 9443,
		Certificate: &TLSCertificate{Mode: TLSCertificateValid, DNSNames: []string{"secure-target.test"}}}
	_, err := startTLSServiceWith(context.Background(), svc, dir, nil, func(string, string) (net.Listener, error) {
		return nil, errors.New("bind failed")
	})
	if err == nil {
		t.Fatal("startup unexpectedly succeeded")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "tls-target-ca.pem")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("trust anchor survived failed startup: %v", statErr)
	}
}

func TestTLSEvidenceContainsNoPrivateKeyMaterial(t *testing.T) {
	material := testTLSMaterial(t, TLSCertificateValid, "secure-target.test")
	evidence := aggregateEvidence([]evidenceEvent{{
		Kind: ServiceTLS, Node: "target", Service: "tls-target", CertificateMode: TLSCertificateValid,
		RequestedServer: "secure-target.test", CertificateDNS: material.dnsNames,
		NotBefore: material.notBefore, NotAfter: material.notAfter, CertificatePresented: true, Result: "passed",
	}})
	blob, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "PRIVATE KEY") || strings.Contains(string(blob), "BEGIN EC") {
		t.Fatalf("private key leaked into evidence: %s", blob)
	}
}
