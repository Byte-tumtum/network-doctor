package simulation

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	tlsHandshakeTimeout = 5 * time.Second
	tlsConnectionLimit  = 10 * time.Second
	tlsMaxRequestBytes  = 64 << 10
)

type tlsMaterial struct {
	certificate tls.Certificate
	caPEM       []byte
	dnsNames    []string
	notBefore   time.Time
	notAfter    time.Time
}

// generateTLSMaterial creates a private CA and leaf entirely in memory. Only
// caPEM, which is public certificate material, is eligible to be written out.
func generateTLSMaterial(ctx context.Context, cfg TLSCertificate, now time.Time) (tlsMaterial, error) {
	if err := ctx.Err(); err != nil {
		return tlsMaterial{}, err
	}
	now = now.UTC().Truncate(time.Second)
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tlsMaterial{}, fmt.Errorf("generate CA key: %w", err)
	}
	ca := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "netdoc simulator root"},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              now.AddDate(20, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return tlsMaterial{}, fmt.Errorf("create CA certificate: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return tlsMaterial{}, err
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tlsMaterial{}, fmt.Errorf("generate leaf key: %w", err)
	}
	notBefore, notAfter := now.Add(-24*time.Hour), now.AddDate(1, 0, 0)
	if cfg.Mode == TLSCertificateExpired {
		notBefore, notAfter = now.AddDate(-2, 0, 0), now.Add(-24*time.Hour)
	}
	dnsNames := append([]string(nil), cfg.DNSNames...)
	sort.Strings(dnsNames)
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(100),
		Subject:      pkix.Name{CommonName: dnsNames[0]},
		DNSNames:     dnsNames,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		return tlsMaterial{}, fmt.Errorf("create leaf certificate: %w", err)
	}
	parsedLeaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		return tlsMaterial{}, fmt.Errorf("parse generated leaf certificate: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return tlsMaterial{}, err
	}
	return tlsMaterial{
		certificate: tls.Certificate{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey, Leaf: parsedLeaf},
		caPEM:       pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		dnsNames:    dnsNames,
		notBefore:   notBefore,
		notAfter:    notAfter,
	}, nil
}

type tlsServer struct {
	listener net.Listener
	service  string
	mode     string
	material tlsMaterial
	recorder *evidenceRecorder
	caPath   string

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	wg     sync.WaitGroup
	once   sync.Once
}

type listenTLSFunc func(network, address string) (net.Listener, error)

func startTLSService(ctx context.Context, svc Service, trustDir string, recorder *evidenceRecorder) (*tlsServer, error) {
	return startTLSServiceWith(ctx, svc, trustDir, recorder, net.Listen)
}

func startTLSServiceWith(ctx context.Context, svc Service, trustDir string, recorder *evidenceRecorder, listen listenTLSFunc) (*tlsServer, error) {
	material, err := generateTLSMaterial(ctx, *svc.Certificate, time.Now())
	if err != nil {
		return nil, err
	}
	caPath := filepath.Join(trustDir, svc.Name+"-ca.pem")
	if err := writePublicCA(caPath, material.caPEM); err != nil {
		return nil, err
	}
	listener, err := listen("tcp", fmt.Sprintf(":%d", svc.Port))
	if err != nil {
		_ = os.Remove(caPath)
		return nil, err
	}
	return startTLSServer(listener, svc.Name, svc.Certificate.Mode, material, caPath, recorder), nil
}

func writePublicCA(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create TLS trust anchor: %w", err)
	}
	if _, err = f.Write(data); err == nil {
		err = f.Close()
	} else {
		_ = f.Close()
	}
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("write TLS trust anchor: %w", err)
	}
	return nil
}

func startTLSServer(listener net.Listener, service, mode string, material tlsMaterial, caPath string, recorder *evidenceRecorder) *tlsServer {
	ctx, cancel := context.WithCancel(context.Background())
	s := &tlsServer{
		listener: listener, service: service, mode: mode, material: material, recorder: recorder, caPath: caPath,
		ctx: ctx, cancel: cancel, conns: make(map[net.Conn]struct{}),
	}
	s.wg.Add(1)
	go s.serve()
	return s
}

func (s *tlsServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.track(conn, true)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.track(conn, false)
			defer conn.Close()
			s.handle(conn)
		}()
	}
}

func (s *tlsServer) handle(raw net.Conn) {
	accepted := time.Now()
	_ = raw.SetDeadline(accepted.Add(tlsHandshakeTimeout))
	requestedServer := ""
	presented := false
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	config.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		requestedServer = hello.ServerName
		presented = true
		return &s.material.certificate, nil
	}
	conn := tls.Server(raw, config)
	handshakeCtx, cancel := context.WithTimeout(s.ctx, tlsHandshakeTimeout)
	err := conn.HandshakeContext(handshakeCtx)
	cancel()
	result := tlsHandshakeResult(err, presented)
	if err := s.recorder.record(evidenceEvent{
		Kind: ServiceTLS, Service: s.service, Result: result,
		CertificateMode: s.mode, RequestedServer: requestedServer,
		CertificateDNS: append([]string(nil), s.material.dnsNames...),
		NotBefore:      s.material.notBefore, NotAfter: s.material.notAfter,
		CertificatePresented: presented,
	}); err != nil {
		return
	}
	if err != nil {
		return
	}

	_ = conn.SetDeadline(accepted.Add(tlsConnectionLimit))
	request, err := http.ReadRequest(bufio.NewReader(io.LimitReader(conn, tlsMaxRequestBytes+1)))
	if err != nil {
		return // The TLS-only probe closes immediately after its successful handshake.
	}
	if request.Body != nil {
		_ = request.Body.Close()
	}
	_, _ = io.WriteString(conn, "HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n")
}

func tlsHandshakeResult(err error, presented bool) string {
	switch {
	case err == nil:
		return "passed"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, net.ErrClosed):
		return "client_closed"
	case timeoutErrorForService(err):
		return "timeout"
	case presented:
		return "client_rejected_certificate"
	default:
		return "handshake_failure"
	}
}

func timeoutErrorForService(err error) bool {
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout()
}

func (s *tlsServer) track(conn net.Conn, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if add {
		s.conns[conn] = struct{}{}
	} else {
		delete(s.conns, conn)
	}
}

func (s *tlsServer) Close() error {
	var closeErr error
	s.once.Do(func() {
		s.cancel()
		closeErr = s.listener.Close()
		s.mu.Lock()
		for conn := range s.conns {
			_ = conn.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
		if s.caPath != "" {
			if err := os.Remove(s.caPath); err != nil && !errors.Is(err, os.ErrNotExist) && closeErr == nil {
				closeErr = err
			}
		}
	})
	if errors.Is(closeErr, net.ErrClosed) {
		return nil
	}
	return closeErr
}
