//go:build integration

// Opt-in (-tags integration) test that points the local-device service check
// at real loopback sockets.

package diagnostic

// Run with:
//
//	go test -tags integration ./internal/diagnostic
//
// Offline-safe: loopback only, and the ports come from the kernel rather than
// from the shipped list, so it needs no privilege and collides with nothing.

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestDiscoverServicesAgainstLoopback(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	open := listener.Addr().(*net.TCPAddr).Port

	// A port nothing is on: bound, then released, so the kernel refuses it.
	spare, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closed := spare.Addr().(*net.TCPAddr).Port
	if err := spare.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	services := []LocalService{{Port: open, Name: "LISTENING"}, {Port: closed, Name: "CLOSED"}}
	scan := defaultOps.discoverServices(context.Background(), "127.0.0.1", services, 2*time.Second)
	if len(scan.Open) != 1 || scan.Open[0].Port != open {
		t.Fatalf("open = %+v, want only the listening port %d", scan.Open, open)
	}
	if scan.Refused != 1 || scan.Silent != 0 {
		t.Errorf("refused/silent = %d/%d, want 1/0: loopback refuses a closed port", scan.Refused, scan.Silent)
	}
}
