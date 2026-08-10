package simulation

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	quic "github.com/quic-go/quic-go"
)

const (
	quicProbeService = "internet-quic"
	quicTrustSubdir  = "quic-roots"
)

type quicServer struct {
	cancel    context.CancelFunc
	listeners []*quic.Listener
	packets   []*net.UDPConn
	caPath    string
	wg        sync.WaitGroup
	once      sync.Once
}

func startQUICService(ctx context.Context, svc Service, addresses []string, trustDir string) (*quicServer, error) {
	material, err := generateTLSMaterial(ctx, *svc.Certificate, time.Now())
	if err != nil {
		return nil, err
	}
	rootDir := filepath.Join(trustDir, quicTrustSubdir)
	if err := os.MkdirAll(rootDir, 0o700); err != nil {
		return nil, err
	}
	caPath := filepath.Join(rootDir, svc.Name+"-ca.pem")
	if err := writePublicCA(caPath, material.caPEM); err != nil {
		return nil, err
	}
	serveCtx, cancel := context.WithCancel(context.Background())
	server := &quicServer{cancel: cancel, caPath: caPath}
	for _, address := range bindAddresses(addresses) {
		network := "udp"
		if ip := net.ParseIP(address); ip != nil {
			if ip.To4() != nil {
				network = "udp4"
			} else {
				network = "udp6"
			}
		}
		packet, err := net.ListenUDP(network, &net.UDPAddr{IP: net.ParseIP(address), Port: svc.Port})
		if err != nil {
			_ = server.Close()
			return nil, err
		}
		listener, err := quic.Listen(packet, &tls.Config{Certificates: []tls.Certificate{material.certificate}, NextProtos: []string{"h3"}}, nil)
		if err != nil {
			_ = packet.Close()
			_ = server.Close()
			return nil, err
		}
		server.packets = append(server.packets, packet)
		server.listeners = append(server.listeners, listener)
		server.wg.Add(1)
		go server.serve(serveCtx, listener)
	}
	return server, nil
}

func (s *quicServer) serve(ctx context.Context, listener *quic.Listener) {
	defer s.wg.Done()
	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			return
		}
		_ = conn.CloseWithError(0, "fixture complete")
	}
}

func (s *quicServer) Close() error {
	var closeErr error
	s.once.Do(func() {
		s.cancel()
		for _, listener := range s.listeners {
			if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				closeErr = errors.Join(closeErr, err)
			}
		}
		for _, packet := range s.packets {
			if err := packet.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				closeErr = errors.Join(closeErr, err)
			}
		}
		s.wg.Wait()
		if s.caPath != "" {
			if err := os.Remove(s.caPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				closeErr = errors.Join(closeErr, err)
			}
			_ = os.Remove(filepath.Dir(s.caPath))
		}
	})
	return closeErr
}

func quicTrustAnchorPath(work, service string) string {
	return filepath.Join(work, quicTrustSubdir, service+"-ca.pem")
}
